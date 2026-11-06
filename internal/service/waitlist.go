package service

import (
	"context"
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
	var maxRows []confirmations.WaitlistRow
	var err error
	if filter.Source != "registry" {
		maxRows, err = wl.ListWaitlistMatching(ctx, confirmations.WaitlistListFilter{
			BranchIDs: filter.BranchIDs, BranchID: filter.BranchID, Status: filter.Status, OpenOnly: filter.OpenOnly,
		})
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
	misIDs := make([]int64, 0, len(misRows))
	for _, row := range misRows {
		if !branchVisible(filter, row.BranchID) {
			continue
		}
		misIDs = append(misIDs, row.PlanningID)
	}
	work, err := wl.MisWorkMap(ctx, misIDs)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}

	type tagged struct {
		item confirmations.DashboardItem
		when string
	}
	var all []tagged
	for _, row := range maxRows {
		if !branchVisible(filter, row.BranchID) {
			continue
		}
		item := confirmations.DashboardItem{
			ID: row.ID, PlanningID: row.PlanningID, PatientID: row.PatientID,
			Reason: row.Reason, Status: row.Status, Outcome: row.Outcome,
			CreatedAt: row.CreatedAt, CreatedAtMSK: row.CreatedAtMSK, BranchID: row.BranchID,
			Source: "max", UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt, ApptStart: row.ApptStart,
			Version: row.Version,
		}
		if !statusVisible(filter, row.Status) || !sourceVisible(filter, "max") {
			continue
		}
		all = append(all, tagged{item: item, when: row.CreatedAt})
	}
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
		all = append(all, tagged{
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
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].when == all[j].when {
			return all[i].item.ID > all[j].item.ID
		}
		return all[i].when > all[j].when
	})

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
	total := len(all)
	start := (page - 1) * per
	if start > total {
		start = total
	}
	end := start + per
	if end > total {
		end = total
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

func (s *Services) cachedMis(ctx context.Context, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
	parts := make([]string, len(examIDs))
	for i, id := range examIDs {
		parts[i] = fmt.Sprint(id)
	}
	key := fmt.Sprintf("%d|%d|%s", back, ahead, strings.Join(parts, ","))
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
		rows, err := loadMis(ctx, s.MSSQL, time.Now(), back, ahead, examIDs)
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
	now := time.Now().In(confirmations.MoscowLocation())
	live, err := repo.WaitlistOriginals(ctx, s.MSSQL, planningIDs, s.Config.WaitlistExamIDs)
	if err != nil {
		return nil, err
	}
	futures, err := repo.WaitlistFutures(ctx, s.MSSQL, patientIDs, now, s.Config.WaitlistExamIDs)
	if err != nil {
		return nil, err
	}
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
		if orig, ok := live[it.PlanningID]; ok {
			if restricted && orig.BranchID != 0 && !branchIn(scope, orig.BranchID) {
				continue
			}
			it.PatientName = orig.PatientName
			it.PatientPhone = orig.Phone
			it.PatientBirth = orig.BirthDate
			it.OrigDateStart = orig.DateStart
			it.OrigDoctor = orig.DoctorName
			it.OrigService = orig.Service
			it.MedialogState = repo.MedialogStateLabel(orig, it.ApptStart)
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

func (s *Services) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.WaitlistRow{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok && !p.Allows(row.BranchID) {
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

func (s *Services) PatchWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string) (confirmations.WaitlistRow, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.WaitlistRow{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok && !p.Allows(row.BranchID) {
		return confirmations.WaitlistRow{}, ErrBranchForbidden
	}
	return wl.ApplyWaitlist(ctx, id, version, outcome, action, updatedBy, time.Now().UTC())
}

func (s *Services) PatchRegistryWaitlist(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string) (confirmations.MisWork, error) {
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
	return confirmations.NewRepo(s.SQLite).ApplyMisWork(ctx, planningID, version, outcome, action, updatedBy, time.Now().UTC())
}
