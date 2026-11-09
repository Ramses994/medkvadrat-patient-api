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
	live    map[int64]repo.WaitlistPlanningLive
	futures []repo.WaitlistFutureVisit
}

type waitTagged struct {
	item confirmations.DashboardItem
	when time.Time
}

// dashboardCacheTTL is the maximum age of the registry window and of page
// enrichment. A moved row may stay on the previous branch until the next
// branch sync, which uses the same limit. See docs/lk/dashboard-snapshot.md.
const dashboardCacheTTL = 30 * time.Second

const dashboardCacheMaxKeys = 32

var loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
	live, futures, err := repo.WaitlistPageEnrich(ctx, db, planningIDs, patientIDs, exams, now, answered)
	if err != nil {
		return pageEnrichment{}, err
	}
	return pageEnrichment{live: live, futures: futures}, nil
}

var loadMaxBranches = repo.WaitlistBranches

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

type enrichEntry struct {
	at   time.Time
	page pageEnrichment
}

var enrichCache = struct {
	mu     sync.Mutex
	m      map[string]enrichEntry
	flight singleflight.Group
}{m: map[string]enrichEntry{}}

func resetEnrichCache() {
	enrichCache.mu.Lock()
	enrichCache.m = map[string]enrichEntry{}
	enrichCache.mu.Unlock()
}

var branchSync = struct {
	mu     sync.Mutex
	at     time.Time
	key    string
	flight singleflight.Group
}{}

func resetBranchSync() {
	branchSync.mu.Lock()
	branchSync.at = time.Time{}
	branchSync.key = ""
	branchSync.mu.Unlock()
}

var ErrBranchForbidden = staff.ErrBranchForbidden

func (s *Services) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	wl := confirmations.NewRepo(s.SQLite)
	page, per := waitlistPage(filter)
	applyUnknownBranch(ctx, &filter)
	if filter.Source != "registry" {
		s.syncOpenMaxBranches(ctx, wl)
	}
	if filter.Source == "max" {
		return s.maxOnlyPage(ctx, wl, filter, page, per)
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

	var misRows []repo.WaitlistMisRow
	if s.MSSQL != nil && filter.Source != "max" {
		back, ahead := s.misWindow()
		misRows, err = s.cachedMis(ctx, back, ahead, s.Config.WaitlistExamIDs)
		if err != nil {
			misRows = nil
			err = nil
		}
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

func (s *Services) maxOnlyPage(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, page, per int) (confirmations.DashboardPage, error) {
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
	items, err = s.decorateWaitlist(ctx, items)
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
	return confirmations.DashboardPage{Items: items, Total: total, OpenTotal: openTotal, Page: page, PerPage: per}, nil
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

func (s *Services) cachedMis(ctx context.Context, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
	now := misNow()
	key := misCacheKey(now, back, ahead, examIDs)
	misRegistry.mu.Lock()
	evictMisLocked(now)
	if e, ok := misRegistry.cache[key]; ok && now.Sub(e.at) < dashboardCacheTTL {
		rows := e.rows
		misRegistry.mu.Unlock()
		return rows, nil
	}
	misRegistry.mu.Unlock()
	v, err, _ := misRegistry.flight.Do(key, func() (any, error) {
		now := misNow()
		misRegistry.mu.Lock()
		evictMisLocked(now)
		if e, ok := misRegistry.cache[key]; ok && now.Sub(e.at) < dashboardCacheTTL {
			rows := e.rows
			misRegistry.mu.Unlock()
			return rows, nil
		}
		misRegistry.mu.Unlock()
		rows, err := loadMis(ctx, s.MSSQL, now, back, ahead, examIDs)
		if err != nil {
			misRegistry.mu.Lock()
			evictMisLocked(misNow())
			misRegistry.mu.Unlock()
			return nil, err
		}
		misRegistry.mu.Lock()
		evictMisLocked(now)
		misRegistry.cache[key] = misCacheEntry{at: now, rows: rows}
		misRegistry.mu.Unlock()
		return rows, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]repo.WaitlistMisRow), nil
}

func evictMisLocked(now time.Time) {
	for k, e := range misRegistry.cache {
		if now.Sub(e.at) >= dashboardCacheTTL {
			delete(misRegistry.cache, k)
		}
	}
}

func sweepDashboardCaches(now time.Time) {
	misRegistry.mu.Lock()
	evictMisLocked(now)
	misRegistry.mu.Unlock()
	enrichCache.mu.Lock()
	evictEnrichLocked(now)
	enrichCache.mu.Unlock()
}

func enrichHoldsName(name string) bool {
	enrichCache.mu.Lock()
	defer enrichCache.mu.Unlock()
	for _, e := range enrichCache.m {
		for _, live := range e.page.live {
			if live.PatientName == name {
				return true
			}
		}
	}
	return false
}

func misCacheHoldsName(name string) bool {
	misRegistry.mu.Lock()
	defer misRegistry.mu.Unlock()
	for _, e := range misRegistry.cache {
		for _, row := range e.rows {
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

func (s *Services) decorateWaitlist(ctx context.Context, items []confirmations.DashboardItem) ([]confirmations.DashboardItem, error) {
	restricted, scope := staffScope(ctx)
	if s.MSSQL == nil || len(items) == 0 {
		for i := range items {
			items[i].Enriched = false
			if items[i].Source != "registry" && items[i].MedialogState == "" {
				items[i].MedialogState = "запись не найдена в Медиалоге"
			}
		}
		return items, nil
	}
	planningIDs := make([]int64, 0)
	patientIDs := make([]int64, 0)
	answered := map[int64]string{}
	seen := map[int64]bool{}
	for _, it := range items {
		if it.PatientID != 0 && !seen[it.PatientID] {
			seen[it.PatientID] = true
			patientIDs = append(patientIDs, it.PatientID)
		}
		if it.Source != "registry" {
			planningIDs = append(planningIDs, it.PlanningID)
			if it.CreatedAtMSK != "" {
				answered[it.PlanningID] = it.CreatedAtMSK
			}
		}
	}
	now := misNow().In(confirmations.MoscowLocation())
	page, err := s.pageEnrich(ctx, planningIDs, patientIDs, answered, now)
	if err != nil {
		return nil, err
	}
	live, futures := page.live, page.futures
	kept := items[:0]
	for i := range items {
		it := items[i]
		it.Enriched = true
		if it.Source == "registry" {
			if fut := pickFuture(it.CreatedAt, futures, it.PatientID, true); fut != nil {
				applyFuture(&it, fut, restricted, scope)
			}
			kept = append(kept, it)
			continue
		}
		if orig, ok := live[it.PlanningID]; ok {
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

func (s *Services) pageEnrich(ctx context.Context, planningIDs, patientIDs []int64, answered map[int64]string, now time.Time) (pageEnrichment, error) {
	key := enrichKey(planningIDs, patientIDs, now)
	clock := misNow()
	enrichCache.mu.Lock()
	evictEnrichLocked(clock)
	if e, ok := enrichCache.m[key]; ok && clock.Sub(e.at) < dashboardCacheTTL {
		page := e.page
		enrichCache.mu.Unlock()
		return page, nil
	}
	enrichCache.mu.Unlock()
	v, err, _ := enrichCache.flight.Do(key, func() (any, error) {
		clock := misNow()
		enrichCache.mu.Lock()
		evictEnrichLocked(clock)
		if e, ok := enrichCache.m[key]; ok && clock.Sub(e.at) < dashboardCacheTTL {
			page := e.page
			enrichCache.mu.Unlock()
			return page, nil
		}
		enrichCache.mu.Unlock()
		page, err := loadPageEnrich(ctx, s.MSSQL, planningIDs, patientIDs, s.Config.WaitlistExamIDs, now, answered)
		if err != nil {
			return pageEnrichment{}, err
		}
		enrichCache.mu.Lock()
		evictEnrichLocked(misNow())
		enrichCache.m[key] = enrichEntry{at: misNow(), page: page}
		trimEnrichLocked()
		enrichCache.mu.Unlock()
		return page, nil
	})
	if err != nil {
		return pageEnrichment{}, err
	}
	return v.(pageEnrichment), nil
}

func enrichKey(planningIDs, patientIDs []int64, now time.Time) string {
	day := now.In(confirmations.MoscowLocation()).Format("2006-01-02")
	return intsKey(planningIDs) + "|" + intsKey(patientIDs) + "|" + day
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

func evictEnrichLocked(now time.Time) {
	for k, e := range enrichCache.m {
		if now.Sub(e.at) >= dashboardCacheTTL {
			delete(enrichCache.m, k)
		}
	}
}

func trimEnrichLocked() {
	for len(enrichCache.m) > dashboardCacheMaxKeys {
		var oldest string
		var oldestAt time.Time
		for k, e := range enrichCache.m {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = k, e.at
			}
		}
		delete(enrichCache.m, oldest)
	}
}

func (s *Services) syncOpenMaxBranches(ctx context.Context, wl *confirmations.Repo) {
	if s.MSSQL == nil || wl == nil {
		return
	}
	rows, err := wl.ListOpenBranchRows(ctx)
	if err != nil || len(rows) == 0 {
		return
	}
	key := intsKey(planningIDsOf(rows))
	now := misNow()
	branchSync.mu.Lock()
	fresh := branchSync.key == key && !branchSync.at.IsZero() && now.Sub(branchSync.at) < dashboardCacheTTL
	branchSync.mu.Unlock()
	if fresh {
		return
	}
	_, _, _ = branchSync.flight.Do(key, func() (any, error) {
		now := misNow()
		branchSync.mu.Lock()
		fresh := branchSync.key == key && !branchSync.at.IsZero() && now.Sub(branchSync.at) < dashboardCacheTTL
		branchSync.mu.Unlock()
		if fresh {
			return nil, nil
		}
		ids := planningIDsOf(rows)
		found, err := loadMaxBranches(ctx, s.MSSQL, ids)
		if err != nil {
			return nil, nil
		}
		for _, row := range rows {
			live, ok := found[row.PlanningID]
			if !ok || live == 0 {
				continue
			}
			if live != row.BranchID {
				_ = wl.SetWaitlistBranch(ctx, row.ID, live)
			}
		}
		branchSync.mu.Lock()
		branchSync.at = misNow()
		branchSync.key = key
		branchSync.mu.Unlock()
		return nil, nil
	})
}

func planningIDsOf(rows []confirmations.WaitlistRow) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.PlanningID
	}
	return ids
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
	answered := map[int64]string{}
	if row.CreatedAtMSK != "" {
		answered[row.PlanningID] = row.CreatedAtMSK
	}
	page, err := loadPageEnrich(ctx, s.MSSQL, []int64{row.PlanningID}, nil, s.Config.WaitlistExamIDs, misNow(), answered)
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
	br, err := s.currentMaxBranch(ctx, row)
	if err != nil {
		return confirmations.DashboardItem{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok && !staff.SeesBranch(p, br) {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistMoved
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
		item.Enriched = false
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
	back, ahead := s.misWindow()
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
