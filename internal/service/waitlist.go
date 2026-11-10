package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
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
	live     map[int64]repo.WaitlistPlanningLive
	futures  []repo.WaitlistFutureVisit
	byOrigin map[int64]repo.WaitlistFutureVisit
}

type tickSnap struct {
	ok   bool
	down bool
	page pageEnrichment
	mis  []repo.WaitlistMisRow
}

type waitTagged struct {
	item confirmations.DashboardItem
	when time.Time
}

// dashboardCacheTTL is the maximum age of one dashboard snapshot.
// Access, personal data and counters share it. See docs/lk/dashboard-snapshot.md.
const dashboardCacheTTL = 30 * time.Second

const dashboardCacheMaxKeys = 32

var loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
	live, futures, err := repo.WaitlistPageEnrich(ctx, db, planningIDs, patientIDs, exams, now, answered)
	if err != nil {
		return pageEnrichment{}, err
	}
	return pageEnrichment{live: live, futures: futures, byOrigin: futuresByOrigin(futures)}, nil
}

func futuresByOrigin(futures []repo.WaitlistFutureVisit) map[int64]repo.WaitlistFutureVisit {
	out := map[int64]repo.WaitlistFutureVisit{}
	for _, fut := range futures {
		if fut.OriginID != 0 {
			out[fut.OriginID] = fut
		}
	}
	return out
}

type storedSnap struct {
	at   time.Time
	mis  []repo.WaitlistMisRow
	page pageEnrichment
}

// snapStore is the one process-wide snapshot. Registry rows and enrichment
// share takenAt, so a failed or newer read cannot be paired with the other.
var snapStore = struct {
	mu     sync.Mutex
	m      map[string]storedSnap
	flight singleflight.Group
}{m: map[string]storedSnap{}}

func resetSnapStore() {
	snapStore.mu.Lock()
	snapStore.m = map[string]storedSnap{}
	snapStore.mu.Unlock()
}

func resetMisCache() { resetSnapStore() }

func resetEnrichCache() { resetSnapStore() }

var ErrBranchForbidden = staff.ErrBranchForbidden

func (s *Services) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	wl := confirmations.NewRepo(s.SQLite)
	page, per := waitlistPage(filter)
	applyUnknownBranch(ctx, &filter)
	snap := s.prepareSnapshot(ctx, wl, filter)
	if filter.Source == "max" {
		return s.maxOnlyPage(ctx, wl, filter, page, per, snap)
	}
	need := headLimit(page, per)

	var maxRows []confirmations.WaitlistRow
	maxTotal := 0
	var err error
	if filter.Source != "registry" {
		maxFilter := branchFilter(filter)
		maxTotal, err = wl.CountWaitlist(ctx, maxFilter)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
		maxRows, err = wl.ListWaitlistHead(ctx, maxFilter, need)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
	}

	misRows := snap.mis
	if !snap.ok {
		misRows = nil
	}
	regHead, regTotal, work, err := s.registryHead(ctx, wl, filter, misRows, need)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}

	all := make([]waitTagged, 0, len(maxRows)+len(regHead))
	for _, row := range maxRows {
		all = append(all, waitTagged{when: waitInstant("max", row.CreatedAt), item: itemFromWaitlist(row)})
	}
	all = append(all, regHead...)
	sort.SliceStable(all, waitBefore(all))
	total := maxTotal + regTotal
	pageItems := []confirmations.DashboardItem{}
	if start, end, ok := pageWindow(len(all), page, per); ok {
		for _, t := range all[start:end] {
			pageItems = append(pageItems, t.item)
		}
	}
	pageItems, err = s.decorateWaitlist(ctx, pageItems, snap)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	openTotal, err := s.openTotal(ctx, wl, filter, misRows, work)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	return confirmations.DashboardPage{Items: pageItems, Total: total, OpenTotal: openTotal, Page: page, PerPage: per, Banner: snap.banner()}, nil
}

func (s *Services) maxOnlyPage(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, page, per int, snap tickSnap) (confirmations.DashboardPage, error) {
	maxFilter := branchFilter(filter)
	maxFilter.Page = page
	maxFilter.PerPage = per
	rows, total, err := wl.ListWaitlist(ctx, maxFilter)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	items := make([]confirmations.DashboardItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, itemFromWaitlist(row))
	}
	items, err = s.decorateWaitlist(ctx, items, snap)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	if items == nil {
		items = []confirmations.DashboardItem{}
	}
	openTotal, err := s.openTotal(ctx, wl, filter, nil, nil)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	return confirmations.DashboardPage{Items: items, Total: total, OpenTotal: openTotal, Page: page, PerPage: per, Banner: snap.banner()}, nil
}

func branchFilter(filter confirmations.WaitlistListFilter) confirmations.WaitlistListFilter {
	return confirmations.WaitlistListFilter{
		BranchIDs: filter.BranchIDs, BranchID: filter.BranchID, Status: filter.Status, OpenOnly: filter.OpenOnly,
		IncludeOtherBranches: filter.IncludeOtherBranches,
	}
}

func applyUnknownBranch(ctx context.Context, filter *confirmations.WaitlistListFilter) {
	if filter.BranchID != nil {
		filter.IncludeOtherBranches = false
		return
	}
	p, ok := staff.PrincipalFrom(ctx)
	if !ok {
		return
	}
	if p.All || p.Role == staff.RoleAdmin || p.Role == staff.RoleManager {
		filter.IncludeOtherBranches = true
	}
}

func headLimit(page, per int) int {
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	if per < 1 {
		per = 50
	}
	if per > 100 {
		per = 100
	}
	return page * per
}

func pageWindow(n, page, per int) (start, end int, ok bool) {
	if page < 1 || per < 1 || n == 0 {
		return 0, 0, false
	}
	off := int64(page-1) * int64(per)
	if off < 0 || off >= int64(n) {
		return 0, 0, false
	}
	start = int(off)
	end = start + per
	if end > n {
		end = n
	}
	return start, end, true
}

func waitBefore(all []waitTagged) func(i, j int) bool {
	return func(i, j int) bool {
		if !all[i].when.Equal(all[j].when) {
			return all[i].when.After(all[j].when)
		}
		if all[i].item.Source != all[j].item.Source {
			return all[i].item.Source < all[j].item.Source
		}
		return all[i].item.ID > all[j].item.ID
	}
}

func waitInstant(source, raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	loc := time.UTC
	if source == "registry" {
		loc = confirmations.MoscowLocation()
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
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
			when: waitInstant("registry", row.CreatedAt),
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
	sort.SliceStable(matched, waitBefore(matched))
	total := len(matched)
	if len(matched) > need {
		matched = matched[:need]
	}
	return matched, total, work, nil
}

func (s *Services) openTotal(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, misRows []repo.WaitlistMisRow, work map[int64]confirmations.MisWork) (int, error) {
	n := 0
	if filter.Source != "registry" {
		openFilter := confirmations.WaitlistListFilter{
			BranchIDs: filter.BranchIDs, BranchID: filter.BranchID, OpenOnly: true,
			IncludeOtherBranches: filter.IncludeOtherBranches,
		}
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

func sweepDashboardCaches(now time.Time) {
	snapStore.mu.Lock()
	evictSnapLocked(now)
	snapStore.mu.Unlock()
}

func evictSnapLocked(now time.Time) {
	for k, e := range snapStore.m {
		if now.Sub(e.at) >= dashboardCacheTTL {
			delete(snapStore.m, k)
		}
	}
}

func trimSnapLocked() {
	for len(snapStore.m) > dashboardCacheMaxKeys {
		var oldest string
		var oldestAt time.Time
		for k, e := range snapStore.m {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = k, e.at
			}
		}
		delete(snapStore.m, oldest)
	}
}

func enrichHoldsName(name string) bool {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	for _, e := range snapStore.m {
		for _, live := range e.page.live {
			if live.PatientName == name {
				return true
			}
		}
	}
	return false
}

func misCacheHoldsName(name string) bool {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	for _, e := range snapStore.m {
		for _, row := range e.mis {
			if row.PatientName == name {
				return true
			}
		}
	}
	return false
}

func (s *Services) misWindow() (int, int) {
	back, ahead := s.Config.WaitlistMisDaysBack, s.Config.WaitlistMisDaysAhead
	if back == 0 {
		back = 7
	}
	if ahead == 0 {
		ahead = 60
	}
	return back, ahead
}

func (snap tickSnap) banner() string {
	if !snap.down {
		return ""
	}
	return "Медиалог недоступен"
}

func (s *Services) decorateWaitlist(ctx context.Context, items []confirmations.DashboardItem, snap tickSnap) ([]confirmations.DashboardItem, error) {
	restricted, scope := staffScope(ctx)
	if len(items) == 0 {
		return items, nil
	}
	if s.MSSQL == nil || !snap.ok {
		for i := range items {
			items[i].Enriched = false
			if items[i].Source != "registry" && items[i].MedialogState == "" && s.MSSQL == nil {
				items[i].MedialogState = "запись не найдена в Медиалоге"
			}
		}
		return items, nil
	}
	live := snap.page.live
	kept := items[:0]
	for i := range items {
		it := items[i]
		origin := it.CreatedAtMSK
		if it.Source == "registry" {
			origin = it.CreatedAt
		}
		if fut, ok := snap.page.byOrigin[it.PlanningID]; ok && repo.VisitCreatedAfter(origin, fut.CreatedAt) {
			cp := fut
			applyFuture(&it, &cp, restricted, scope)
		}
		if it.Source == "registry" {
			if orig, ok := live[it.PlanningID]; ok {
				if orig.PatientName != "" || orig.Phone != "" {
					it.PatientName = orig.PatientName
					it.PatientPhone = orig.Phone
					it.PatientBirth = orig.BirthDate
				}
				if orig.BranchID != 0 {
					it.BranchID = orig.BranchID
				}
			}
			it.Enriched = it.PatientName != "" || it.PatientPhone != ""
			kept = append(kept, it)
			continue
		}
		if orig, ok := live[it.PlanningID]; ok {
			it.Enriched = orig.PatientName != "" || orig.Phone != ""
			it.PatientName = orig.PatientName
			it.PatientPhone = orig.Phone
			it.PatientBirth = orig.BirthDate
			it.OrigDateStart = orig.DateStart
			it.OrigDoctor = orig.DoctorName
			it.OrigService = orig.Service
			it.MedialogState = repo.MedialogStateLabel(orig, it.ApptStart)
			if orig.BranchID != 0 {
				it.BranchID = orig.BranchID
			}
		} else {
			it.Enriched = false
			it.MedialogState = "запись не найдена в Медиалоге"
		}
		kept = append(kept, it)
	}
	if kept == nil {
		kept = []confirmations.DashboardItem{}
	}
	return kept, nil
}

func snapshotKey(source string, planningIDs []int64, answered map[int64]string, now time.Time, back, ahead int, exams []int64) string {
	day := now.In(confirmations.MoscowLocation()).Format("2006-01-02")
	key := source + "|" + day + "|" + intsKey(planningIDs) + "|" + answeredKey(answered)
	if source != "max" {
		key += "|" + misCacheKey(now, back, ahead, exams)
	}
	return key
}

func answeredKey(answered map[int64]string) string {
	if len(answered) == 0 {
		return ""
	}
	ids := make([]int64, 0, len(answered))
	for id := range answered {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10) + "=" + answered[id]
	}
	return strings.Join(parts, ",")
}

func intsKey(ids []int64) string {
	cp := append([]int64(nil), ids...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	parts := make([]string, len(cp))
	for i, id := range cp {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

func snapLookup(key string, now time.Time) (storedSnap, bool) {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	evictSnapLocked(now)
	e, ok := snapStore.m[key]
	if !ok || now.Sub(e.at) >= dashboardCacheTTL {
		return storedSnap{}, false
	}
	return e, true
}

func (s *Services) prepareSnapshot(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter) tickSnap {
	startSnapshotSweep()
	if s == nil || s.MSSQL == nil || wl == nil {
		return tickSnap{ok: true}
	}
	rows, err := wl.ListOpenBranchRows(ctx)
	if err != nil {
		sweepDashboardCaches(misNow())
		return tickSnap{down: true}
	}
	answered := map[int64]string{}
	var ids []int64
	if filter.Source != "registry" {
		ids = make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.PlanningID)
			if row.CreatedAtMSK != "" {
				answered[row.PlanningID] = row.CreatedAtMSK
			}
		}
	}
	back, ahead := s.misWindow()
	key := snapshotKey(filter.Source, ids, answered, misNow(), back, ahead, s.Config.WaitlistExamIDs)
	if e, ok := snapLookup(key, misNow()); ok {
		return s.materializeSnap(ctx, wl, filter, rows, e)
	}
	v, err, _ := snapStore.flight.Do(key, func() (any, error) {
		now := misNow()
		if e, ok := snapLookup(key, now); ok {
			return e, nil
		}
		enrichIDs := append([]int64(nil), ids...)
		var mis []repo.WaitlistMisRow
		if filter.Source != "max" {
			loaded, err := loadMis(ctx, s.MSSQL, now, back, ahead, s.Config.WaitlistExamIDs)
			if err != nil {
				return storedSnap{}, err
			}
			mis = loaded
			seen := map[int64]bool{}
			for _, id := range enrichIDs {
				seen[id] = true
			}
			for _, row := range mis {
				if row.PlanningID == 0 || seen[row.PlanningID] {
					continue
				}
				seen[row.PlanningID] = true
				enrichIDs = append(enrichIDs, row.PlanningID)
			}
		}
		page, err := loadPageEnrich(ctx, s.MSSQL, enrichIDs, nil, s.Config.WaitlistExamIDs, now.In(confirmations.MoscowLocation()), answered)
		if err != nil {
			return storedSnap{}, err
		}
		for i := range mis {
			if orig, ok := page.live[mis[i].PlanningID]; ok && orig.BranchID != 0 {
				mis[i].BranchID = orig.BranchID
			}
		}
		entry := storedSnap{at: misNow(), mis: mis, page: page}
		snapStore.mu.Lock()
		evictSnapLocked(misNow())
		snapStore.m[key] = entry
		trimSnapLocked()
		snapStore.mu.Unlock()
		return entry, nil
	})
	if err != nil {
		sweepDashboardCaches(misNow())
		return tickSnap{down: true}
	}
	return s.materializeSnap(ctx, wl, filter, rows, v.(storedSnap))
}

func (s *Services) materializeSnap(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, rows []confirmations.WaitlistRow, e storedSnap) tickSnap {
	if filter.Source != "registry" {
		s.applyLiveBranches(ctx, wl, rows, e.page.live)
	}
	return tickSnap{ok: true, page: e.page, mis: e.mis}
}

func (s *Services) applyLiveBranches(ctx context.Context, wl *confirmations.Repo, rows []confirmations.WaitlistRow, live map[int64]repo.WaitlistPlanningLive) {
	for _, row := range rows {
		orig, ok := live[row.PlanningID]
		if !ok || orig.BranchID == 0 || orig.BranchID == row.BranchID {
			continue
		}
		_ = wl.SetWaitlistBranch(ctx, row.ID, orig.BranchID)
	}
}

var snapshotSweep sync.Once

func startSnapshotSweep() {
	snapshotSweep.Do(func() {
		go func() {
			tick := time.NewTicker(dashboardCacheTTL)
			defer tick.Stop()
			for range tick.C {
				sweepDashboardCaches(misNow())
			}
		}()
	})
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
	if f.IncludeOtherBranches && branch != 106 && branch != 3 && branch != 496 {
		return true
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
	snap := s.prepareSnapshot(ctx, confirmations.NewRepo(s.SQLite), confirmations.WaitlistListFilter{Source: "max"})
	if snap.down {
		return row.BranchID, nil
	}
	orig, ok := snap.page.live[row.PlanningID]
	br := currentBranchID(row.BranchID, orig.BranchID, ok && orig.BranchID != 0)
	if ok && orig.BranchID != 0 && br != row.BranchID && s.SQLite != nil {
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
	if p, ok := staff.PrincipalFrom(ctx); ok && !staff.SeesBranch(p, br) {
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
	snap := s.prepareSnapshot(ctx, wl, confirmations.WaitlistListFilter{Source: "max"})
	if p, ok := staff.PrincipalFrom(ctx); ok {
		if err := accessPatch(p, "max", row.BranchID, row.PlanningID, snap); err != nil {
			return confirmations.DashboardItem{}, err
		}
	}
	if snap.ok {
		if orig, ok := snap.page.live[row.PlanningID]; ok && orig.BranchID != 0 && orig.BranchID != row.BranchID {
			_ = wl.SetWaitlistBranch(ctx, row.ID, orig.BranchID)
		}
	}
	applied, err := wl.ApplyWaitlist(ctx, id, version, outcome, action, updatedBy, time.Now().UTC())
	item := s.fullWaitlistItem(ctx, applied, snap)
	return item, err
}

func accessPatch(p staff.Principal, source string, storedBranch int, planningID int64, snap tickSnap) error {
	if !snap.ok {
		if source == "registry" {
			return confirmations.ErrWaitlistNotFound
		}
		if !staff.SeesBranch(p, storedBranch) {
			return staff.ErrBranchForbidden
		}
		return nil
	}
	if orig, found := snap.page.live[planningID]; found && orig.BranchID != 0 {
		if !staff.SeesBranch(p, orig.BranchID) {
			return confirmations.ErrWaitlistMoved
		}
		return nil
	}
	if source == "registry" {
		for _, row := range snap.mis {
			if row.PlanningID != planningID || row.BranchID == 0 {
				continue
			}
			if !staff.SeesBranch(p, row.BranchID) {
				return confirmations.ErrWaitlistMoved
			}
			return nil
		}
		return confirmations.ErrWaitlistNotFound
	}
	if !staff.SeesBranch(p, storedBranch) {
		return staff.ErrBranchForbidden
	}
	return nil
}

func (s *Services) fullWaitlistItem(ctx context.Context, row confirmations.WaitlistRow, snap tickSnap) confirmations.DashboardItem {
	if row.ID == 0 {
		return confirmations.DashboardItem{}
	}
	items, err := s.decorateWaitlist(ctx, []confirmations.DashboardItem{itemFromWaitlist(row)}, snap)
	if err != nil || len(items) == 0 {
		item := itemFromWaitlist(row)
		item.Enriched = false
		return item
	}
	return items[0]
}

func (s *Services) PatchRegistryWaitlist(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string) (confirmations.DashboardItem, error) {
	if s.MSSQL == nil {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	wl := confirmations.NewRepo(s.SQLite)
	snap := s.prepareSnapshot(ctx, wl, confirmations.WaitlistListFilter{Source: "registry"})
	branch := 0
	for _, row := range snap.mis {
		if row.PlanningID == planningID {
			branch = row.BranchID
			break
		}
	}
	if orig, ok := snap.page.live[planningID]; ok && orig.BranchID != 0 {
		branch = orig.BranchID
	}
	if p, has := staff.PrincipalFrom(ctx); has {
		if err := accessPatch(p, "registry", branch, planningID, snap); err != nil {
			return confirmations.DashboardItem{}, err
		}
	} else if !snap.ok || branch == 0 {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	work, err := wl.ApplyMisWork(ctx, planningID, version, outcome, action, updatedBy, time.Now().UTC())
	item := registryItemFromSnap(work, branch, snap.mis)
	items, derr := s.decorateWaitlist(ctx, []confirmations.DashboardItem{item}, snap)
	if derr != nil || len(items) == 0 {
		item.Enriched = false
		return item, err
	}
	return items[0], err
}

func registryItemFromSnap(work confirmations.MisWork, branch int, rows []repo.WaitlistMisRow) confirmations.DashboardItem {
	item := confirmations.DashboardItem{
		ID: work.PlanningID, PlanningID: work.PlanningID, Source: "registry",
		Status: work.Status, Outcome: work.Outcome, BranchID: branch,
		MedialogState: "запись действует", UpdatedBy: work.UpdatedBy, UpdatedAt: work.UpdatedAt,
		Version: work.Version,
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
