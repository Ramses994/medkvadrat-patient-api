package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

var loadMis = repo.ListWaitlistMis

var misNow = time.Now

type pageEnrichment struct {
	live    map[int64]repo.WaitlistPlanningLive
	futures []repo.WaitlistFutureVisit
}

type waitTagged struct {
	item confirmations.DashboardItem
	when string
}

var loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time) (pageEnrichment, error) {
	live, futures, err := repo.WaitlistPageEnrich(ctx, db, planningIDs, patientIDs, exams, now)
	if err != nil {
		return pageEnrichment{}, err
	}
	return pageEnrichment{live: live, futures: futures}, nil
}

type misCacheEntry struct {
	at   time.Time
	rows []repo.WaitlistMisRow
}

var misRegistry = struct {
	mu     sync.Mutex
	cache  map[string]misCacheEntry
	flight singleflight.Group
}{cache: map[string]misCacheEntry{}}

func resetMisCache() {
	misRegistry.mu.Lock()
	misRegistry.cache = map[string]misCacheEntry{}
	misRegistry.mu.Unlock()
}

var ErrBranchForbidden = staff.ErrBranchForbidden

func (s *Services) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	wl := confirmations.NewRepo(s.SQLite)
	page, per := waitlistPage(filter)
	need := page * per

	var maxRows []confirmations.WaitlistRow
	maxTotal := 0
	var err error
	if filter.Source != "registry" {
		maxFilter := confirmations.WaitlistListFilter{
			BranchIDs: filter.BranchIDs, BranchID: filter.BranchID, Status: filter.Status, OpenOnly: filter.OpenOnly,
		}
		maxTotal, err = wl.CountWaitlist(ctx, maxFilter)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
		maxRows, err = wl.ListWaitlistHead(ctx, maxFilter, need)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
	}

	var misRows []repo.WaitlistMisRow
	if s.MSSQL != nil && filter.Source != "max" {
		back, ahead := s.Config.WaitlistMisDaysBack, s.Config.WaitlistMisDaysAhead
		if back == 0 {
			back = 7
		}
		if ahead == 0 {
			ahead = 60
		}
		misRows, err = s.cachedMis(ctx, back, ahead, s.Config.WaitlistExamIDs)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
	}
	regHead, regTotal, work, err := s.registryHead(ctx, wl, filter, misRows, need)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}

	all := make([]waitTagged, 0, len(maxRows)+len(regHead))
	for _, row := range maxRows {
		all = append(all, waitTagged{when: row.CreatedAt, item: itemFromWaitlist(row)})
	}
	all = append(all, regHead...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].when == all[j].when {
			return all[i].item.ID > all[j].item.ID
		}
		return all[i].when > all[j].when
	})
	total := maxTotal + regTotal
	start := (page - 1) * per
	if start > len(all) {
		start = len(all)
	}
	end := start + per
	if end > len(all) {
		end = len(all)
	}
	pageItems := make([]confirmations.DashboardItem, 0, end-start)
	for _, t := range all[start:end] {
		pageItems = append(pageItems, t.item)
	}
	pageItems, err = s.decorateWaitlist(ctx, pageItems)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	openTotal, err := s.openTotal(ctx, wl, filter, misRows, work)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	return confirmations.DashboardPage{Items: pageItems, Total: total, OpenTotal: openTotal, Page: page, PerPage: per}, nil
}

func waitlistPage(filter confirmations.WaitlistListFilter) (int, int) {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	per := filter.PerPage
	if per < 1 {
		per = 50
	}
	if per > 100 {
		per = 100
	}
	return page, per
}

func itemFromWaitlist(row confirmations.WaitlistRow) confirmations.DashboardItem {
	return confirmations.DashboardItem{
		ID: row.ID, PlanningID: row.PlanningID, PatientID: row.PatientID,
		Reason: row.Reason, Status: row.Status, Outcome: row.Outcome,
		CreatedAt: row.CreatedAt, CreatedAtMSK: row.CreatedAtMSK, BranchID: row.BranchID,
		Source: "max", UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt, ApptStart: row.ApptStart,
		Version: row.Version,
	}
}

func (s *Services) registryHead(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, misRows []repo.WaitlistMisRow, need int) ([]waitTagged, int, map[int64]confirmations.MisWork, error) {
	if filter.Source == "max" {
		return nil, 0, map[int64]confirmations.MisWork{}, nil
	}
	ids := make([]int64, 0, len(misRows))
	for _, row := range misRows {
		if branchVisible(filter, row.BranchID) {
			ids = append(ids, row.PlanningID)
		}
	}
	work, err := wl.MisWorkMap(ctx, ids)
	if err != nil {
		return nil, 0, nil, err
	}
	matched := make([]waitTagged, 0)
	for _, row := range misRows {
		if !branchVisible(filter, row.BranchID) {
			continue
		}
		st := confirmations.WaitlistStatusNew
		var outcome, by, at string
		version := 1
		if w, ok := work[row.PlanningID]; ok {
			st = w.Status
			outcome, by, at = w.Outcome, w.UpdatedBy, w.UpdatedAt
			if w.Version > 0 {
				version = w.Version
			}
		}
		if !statusVisible(filter, st) || !sourceVisible(filter, "registry") {
			continue
		}
		matched = append(matched, waitTagged{
			when: row.CreatedAt,
			item: confirmations.DashboardItem{
				ID: row.PlanningID, PlanningID: row.PlanningID, PatientID: row.PatientID,
				Status: st, Outcome: outcome, BranchID: row.BranchID, Source: "registry",
				PatientName: row.PatientName, PatientPhone: row.Phone, PatientBirth: row.BirthDate,
				OrigDateStart: row.DateStart, OrigDoctor: row.DoctorName, OrigService: row.Comment,
				Comment: row.Comment, CreatorName: row.CreatorName,
				CreatedAt: row.CreatedAt, CreatedAtMSK: row.CreatedAt,
				MedialogState: "запись действует", UpdatedBy: by, UpdatedAt: at, Version: version,
			},
		})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].when == matched[j].when {
			return matched[i].item.ID > matched[j].item.ID
		}
		return matched[i].when > matched[j].when
	})
	total := len(matched)
	if len(matched) > need {
		matched = matched[:need]
	}
	return matched, total, work, nil
}

func (s *Services) openTotal(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, misRows []repo.WaitlistMisRow, work map[int64]confirmations.MisWork) (int, error) {
	n := 0
	if filter.Source != "registry" {
		openFilter := confirmations.WaitlistListFilter{BranchIDs: filter.BranchIDs, BranchID: filter.BranchID, OpenOnly: true}
		counted, err := wl.CountWaitlist(ctx, openFilter)
		if err != nil {
			return 0, err
		}
		n += counted
	}
	if filter.Source == "max" {
		return n, nil
	}
	for _, row := range misRows {
		if !branchVisible(filter, row.BranchID) {
			continue
		}
		st := confirmations.WaitlistStatusNew
		if w, ok := work[row.PlanningID]; ok {
			st = w.Status
		}
		if isOpenStatus(st) {
			n++
		}
	}
	return n, nil
}

func misCacheKey(now time.Time, back, ahead int, examIDs []int64) string {
	from, to := repo.WaitlistMisWindow(now, back, ahead)
	parts := make([]string, len(examIDs))
	for i, id := range examIDs {
		parts[i] = fmt.Sprint(id)
	}
	return from + "|" + to + "|" + strings.Join(parts, ",")
}

func (s *Services) cachedMis(ctx context.Context, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
	now := misNow()
	key := misCacheKey(now, back, ahead, examIDs)
	misRegistry.mu.Lock()
	if e, ok := misRegistry.cache[key]; ok && time.Since(e.at) < 30*time.Second {
		rows := e.rows
		misRegistry.mu.Unlock()
		return rows, nil
	}
	misRegistry.mu.Unlock()
	v, err, _ := misRegistry.flight.Do(key, func() (any, error) {
		misRegistry.mu.Lock()
		if e, ok := misRegistry.cache[key]; ok && time.Since(e.at) < 30*time.Second {
			rows := e.rows
			misRegistry.mu.Unlock()
			return rows, nil
		}
		misRegistry.mu.Unlock()
		rows, err := loadMis(ctx, s.MSSQL, now, back, ahead, examIDs)
		if err != nil {
			return nil, err
		}
		misRegistry.mu.Lock()
		misRegistry.cache[key] = misCacheEntry{at: time.Now(), rows: rows}
		misRegistry.mu.Unlock()
		return rows, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]repo.WaitlistMisRow), nil
}

func (s *Services) decorateWaitlist(ctx context.Context, items []confirmations.DashboardItem) ([]confirmations.DashboardItem, error) {
	restricted, scope := staffScope(ctx)
	if s.MSSQL == nil || len(items) == 0 {
		for i := range items {
			if items[i].Source != "registry" && items[i].MedialogState == "" {
				items[i].MedialogState = "запись не найдена в Медиалоге"
			}
		}
		return items, nil
	}
	planningIDs := make([]int64, 0)
	patientIDs := make([]int64, 0)
	seen := map[int64]bool{}
	for _, it := range items {
		if it.PatientID != 0 && !seen[it.PatientID] {
			seen[it.PatientID] = true
			patientIDs = append(patientIDs, it.PatientID)
		}
		if it.Source != "registry" {
			planningIDs = append(planningIDs, it.PlanningID)
		}
	}
	now := misNow().In(confirmations.MoscowLocation())
	page, err := loadPageEnrich(ctx, s.MSSQL, planningIDs, patientIDs, s.Config.WaitlistExamIDs, now)
	if err != nil {
		return nil, err
	}
	live, futures := page.live, page.futures
	kept := items[:0]
	for i := range items {
		it := items[i]
		if it.Source == "registry" {
			if fut := pickFuture(it.CreatedAt, futures, it.PatientID, true); fut != nil {
				applyFuture(&it, fut, restricted, scope)
			}
			kept = append(kept, it)
			continue
		}
		orig, ok := live[it.PlanningID]
		br := currentBranchID(it.BranchID, orig.BranchID, ok)
		if br != it.BranchID && s.SQLite != nil {
			if err := confirmations.NewRepo(s.SQLite).SetWaitlistBranch(ctx, it.ID, br); err != nil {
				return nil, err
			}
			it.BranchID = br
		}
		if restricted && !branchIn(scope, br) {
			continue
		}
		if ok {
			it.PatientName = orig.PatientName
			it.PatientPhone = orig.Phone
			it.PatientBirth = orig.BirthDate
			it.OrigDateStart = orig.DateStart
			it.OrigDoctor = orig.DoctorName
			it.OrigService = orig.Service
			it.MedialogState = repo.MedialogStateLabel(orig, it.ApptStart)
			it.BranchID = br
		} else {
			it.MedialogState = "запись не найдена в Медиалоге"
		}
		if fut := repo.PickNewerFuture(it.CreatedAtMSK, futures, it.PatientID); fut != nil {
			applyFuture(&it, fut, restricted, scope)
		}
		kept = append(kept, it)
	}
	if kept == nil {
		kept = []confirmations.DashboardItem{}
	}
	return kept, nil
}

func staffScope(ctx context.Context) (bool, []int) {
	p, ok := staff.PrincipalFrom(ctx)
	if !ok || p.All {
		return false, nil
	}
	return true, p.BranchIDs
}

func branchIn(ids []int, branch int) bool {
	for _, id := range ids {
		if id == branch {
			return true
		}
	}
	return false
}

func applyFuture(it *confirmations.DashboardItem, fut *repo.WaitlistFutureVisit, restricted bool, scope []int) {
	if restricted && !branchIn(scope, fut.BranchID) {
		it.FutureLabel = "есть запись в другом филиале"
		it.FutureDateStart = ""
		it.FutureDoctor = ""
		it.FutureService = ""
		return
	}
	it.FutureLabel = repo.FutureVisitLabel(fut)
	it.FutureDateStart = fut.DateStart
	it.FutureDoctor = fut.DoctorName
	it.FutureService = fut.Service
}

func pickFuture(created string, futures []repo.WaitlistFutureVisit, patientID int64, skipWaitlist bool) *repo.WaitlistFutureVisit {
	for i := range futures {
		f := &futures[i]
		if f.PatientID != patientID {
			continue
		}
		if skipWaitlist && f.IsWaitlist {
			continue
		}
		if confirmations.MedialogCreateIsNewer(created, f.CreatedAt) {
			cp := *f
			return &cp
		}
	}
	return nil
}

func branchVisible(f confirmations.WaitlistListFilter, branch int) bool {
	if f.BranchID != nil {
		return branch == *f.BranchID
	}
	if len(f.BranchIDs) == 0 {
		return true
	}
	for _, id := range f.BranchIDs {
		if id == branch {
			return true
		}
	}
	return false
}

func statusVisible(f confirmations.WaitlistListFilter, status string) bool {
	switch f.Status {
	case confirmations.WaitlistStatusNew, confirmations.WaitlistStatusContacted, confirmations.WaitlistStatusRebooked, confirmations.WaitlistStatusClosed:
		return status == f.Status
	}
	if f.OpenOnly {
		return isOpenStatus(status)
	}
	return true
}

func sourceVisible(f confirmations.WaitlistListFilter, source string) bool {
	switch f.Source {
	case "", "all":
		return true
	default:
		return f.Source == source
	}
}

func isOpenStatus(status string) bool {
	return status == confirmations.WaitlistStatusNew || status == confirmations.WaitlistStatusContacted
}

func currentBranchID(saved, live int, liveFound bool) int {
	if liveFound && live != 0 {
		return live
	}
	return saved
}

func (s *Services) currentMaxBranch(ctx context.Context, row confirmations.WaitlistRow) (int, error) {
	if s.MSSQL == nil {
		return row.BranchID, nil
	}
	page, err := loadPageEnrich(ctx, s.MSSQL, []int64{row.PlanningID}, nil, s.Config.WaitlistExamIDs, misNow())
	if err != nil {
		return 0, err
	}
	orig, ok := page.live[row.PlanningID]
	br := currentBranchID(row.BranchID, orig.BranchID, ok)
	if br != row.BranchID && s.SQLite != nil {
		if err := confirmations.NewRepo(s.SQLite).SetWaitlistBranch(ctx, row.ID, br); err != nil {
			return 0, err
		}
	}
	return br, nil
}

func (s *Services) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.WaitlistRow{}, err
	}
	br, err := s.currentMaxBranch(ctx, row)
	if err != nil {
		return confirmations.WaitlistRow{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok && !p.Allows(br) {
		return confirmations.WaitlistRow{}, ErrBranchForbidden
	}
	return wl.UpdateWaitlist(ctx, id, status, outcome, updatedBy, time.Now().UTC())
}

func (s *Services) UpdateRegistryWaitlist(ctx context.Context, planningID int64, status, outcome, updatedBy string) (confirmations.MisWork, error) {
	if s.MSSQL == nil {
		return confirmations.MisWork{}, confirmations.ErrWaitlistNotFound
	}
	branch, ok, err := repo.WaitlistMisBranch(ctx, s.MSSQL, planningID, s.Config.WaitlistExamIDs)
	if err != nil {
		return confirmations.MisWork{}, err
	}
	if !ok {
		return confirmations.MisWork{}, confirmations.ErrWaitlistNotFound
	}
	if p, has := staff.PrincipalFrom(ctx); has && !p.Allows(branch) {
		return confirmations.MisWork{}, ErrBranchForbidden
	}
	return confirmations.NewRepo(s.SQLite).UpdateMisWork(ctx, planningID, status, outcome, updatedBy, time.Now().UTC())
}

func (s *Services) PatchWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string) (confirmations.DashboardItem, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.DashboardItem{}, err
	}
	br, err := s.currentMaxBranch(ctx, row)
	if err != nil {
		return confirmations.DashboardItem{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok && !p.Allows(br) {
		return confirmations.DashboardItem{}, ErrBranchForbidden
	}
	applied, err := wl.ApplyWaitlist(ctx, id, version, outcome, action, updatedBy, time.Now().UTC())
	item, derr := s.fullWaitlistItem(ctx, applied)
	if derr != nil && item.ID == 0 {
		item = itemFromWaitlist(applied)
	}
	return item, err
}

func (s *Services) fullWaitlistItem(ctx context.Context, row confirmations.WaitlistRow) (confirmations.DashboardItem, error) {
	if row.ID == 0 {
		return confirmations.DashboardItem{}, nil
	}
	items, err := s.decorateWaitlist(ctx, []confirmations.DashboardItem{itemFromWaitlist(row)})
	if err != nil || len(items) == 0 {
		item := itemFromWaitlist(row)
		if item.MedialogState == "" {
			item.MedialogState = "запись не найдена в Медиалоге"
		}
		return item, err
	}
	return items[0], nil
}

func (s *Services) PatchRegistryWaitlist(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string) (confirmations.DashboardItem, error) {
	if s.MSSQL == nil {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	branch, ok, err := repo.WaitlistMisBranch(ctx, s.MSSQL, planningID, s.Config.WaitlistExamIDs)
	if err != nil {
		return confirmations.DashboardItem{}, err
	}
	if !ok {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	if p, has := staff.PrincipalFrom(ctx); has && !p.Allows(currentBranchID(0, branch, true)) {
		return confirmations.DashboardItem{}, ErrBranchForbidden
	}
	work, err := confirmations.NewRepo(s.SQLite).ApplyMisWork(ctx, planningID, version, outcome, action, updatedBy, time.Now().UTC())
	item := s.registryItem(ctx, work, branch)
	items, derr := s.decorateWaitlist(ctx, []confirmations.DashboardItem{item})
	if derr != nil || len(items) == 0 {
		return item, err
	}
	return items[0], err
}

func (s *Services) registryItem(ctx context.Context, work confirmations.MisWork, branch int) confirmations.DashboardItem {
	item := confirmations.DashboardItem{
		ID: work.PlanningID, PlanningID: work.PlanningID, Source: "registry",
		Status: work.Status, Outcome: work.Outcome, BranchID: branch,
		MedialogState: "запись действует", UpdatedBy: work.UpdatedBy, UpdatedAt: work.UpdatedAt,
		Version: work.Version,
	}
	if s.MSSQL == nil || work.PlanningID == 0 {
		return item
	}
	back, ahead := s.Config.WaitlistMisDaysBack, s.Config.WaitlistMisDaysAhead
	if back == 0 {
		back = 7
	}
	if ahead == 0 {
		ahead = 60
	}
	rows, err := s.cachedMis(ctx, back, ahead, s.Config.WaitlistExamIDs)
	if err != nil {
		return item
	}
	for _, row := range rows {
		if row.PlanningID != work.PlanningID {
			continue
		}
		item.PatientID = row.PatientID
		item.PatientName = row.PatientName
		item.PatientPhone = row.Phone
		item.PatientBirth = row.BirthDate
		item.OrigDateStart = row.DateStart
		item.OrigDoctor = row.DoctorName
		item.OrigService = row.Comment
		item.Comment = row.Comment
		item.CreatorName = row.CreatorName
		item.CreatedAt = row.CreatedAt
		item.CreatedAtMSK = row.CreatedAt
		item.BranchID = currentBranchID(row.BranchID, branch, branch != 0)
		return item
	}
	return item
}
