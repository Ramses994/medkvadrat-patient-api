package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

const (
	rowFound   = 1
	rowMissing = 2
)

// dashboardRowCap is the most SQLite rows one dashboard read will hold.
// More than this is a logged 503, not a truncated page.
var dashboardRowCap = 5000

// RowState is one appointment in the current snapshot generation.
type RowState struct {
	Kind       int
	Branch     int
	Saved      int
	WaitlistID int64
	PlanningID int64
	Live       repo.WaitlistPlanningLive
	Future     *repo.WaitlistFutureVisit
	Registry   *repo.WaitlistMisRow
}

type rowKey struct {
	source string
	id     int64
}

type branchWrite struct {
	ID     int64
	Branch int
}

// Snapshot is the one Medialog generation shared by every dashboard tab.
type Snapshot struct {
	gen     uint64
	takenAt time.Time
	down    bool
	rows    map[rowKey]RowState
	mis     []repo.WaitlistMisRow
	page    pageEnrichment
	writes  []branchWrite
}

var snapStore = struct {
	mu     sync.Mutex
	cur    *Snapshot
	gen    uint64
	flight singleflight.Group
}{}

var storeWaitlistBranch = func(ctx context.Context, wl *confirmations.Repo, id int64, branch int) error {
	return wl.SetWaitlistBranch(ctx, id, branch)
}

var beforePersistBranches = func() {}

func resetSnapStore() {
	snapStore.mu.Lock()
	snapStore.cur = nil
	snapStore.gen = 0
	snapStore.mu.Unlock()
	beforePersistBranches = func() {}
	storeWaitlistBranch = func(ctx context.Context, wl *confirmations.Repo, id int64, branch int) error {
		return wl.SetWaitlistBranch(ctx, id, branch)
	}
}

func resetMisCache() { resetSnapStore() }

func resetEnrichCache() { resetSnapStore() }

func (s *Services) warn() *slog.Logger {
	if s != nil && s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Services) prepareSnapshot(ctx context.Context, wl *confirmations.Repo) *Snapshot {
	startSnapshotSweep()
	if s == nil || s.MSSQL == nil || wl == nil {
		return &Snapshot{rows: map[rowKey]RowState{}}
	}
	if cur := currentSnap(misNow()); cur != nil {
		return cur
	}
	v, _, _ := snapStore.flight.Do("dashboard", func() (any, error) {
		now := misNow()
		if cur := currentSnap(now); cur != nil {
			return cur, nil
		}
		built, err := s.readSnapshot(ctx, wl)
		if err != nil {
			snapStore.mu.Lock()
			snapStore.cur = nil
			snapStore.mu.Unlock()
			return &Snapshot{down: true, rows: map[rowKey]RowState{}}, nil
		}
		snapStore.mu.Lock()
		built.gen = snapStore.gen + 1
		built.takenAt = misNow()
		snapStore.gen = built.gen
		snapStore.cur = built
		snapStore.mu.Unlock()
		s.persistBranches(ctx, wl, built)
		return built, nil
	})
	snap, _ := v.(*Snapshot)
	if snap == nil {
		return &Snapshot{down: true, rows: map[rowKey]RowState{}}
	}
	return snap
}

func currentSnap(now time.Time) *Snapshot {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	if snapStore.cur == nil || now.Sub(snapStore.cur.takenAt) >= dashboardCacheTTL {
		return nil
	}
	return snapStore.cur
}

func (s *Services) readSnapshot(ctx context.Context, wl *confirmations.Repo) (*Snapshot, error) {
	openRows, err := wl.ListOpenBranchRows(ctx)
	if err != nil {
		return nil, err
	}
	back, ahead := s.misWindow()
	now := misNow()
	mis, err := loadMis(ctx, s.MSSQL, now, back, ahead, s.Config.WaitlistExamIDs)
	if err != nil {
		return nil, err
	}
	answered := map[int64]string{}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(openRows)+len(mis))
	for _, row := range openRows {
		if row.CreatedAtMSK != "" {
			answered[row.PlanningID] = row.CreatedAtMSK
		}
		if row.PlanningID == 0 || seen[row.PlanningID] {
			continue
		}
		seen[row.PlanningID] = true
		ids = append(ids, row.PlanningID)
	}
	for _, row := range mis {
		if row.PlanningID == 0 || seen[row.PlanningID] {
			continue
		}
		seen[row.PlanningID] = true
		ids = append(ids, row.PlanningID)
	}
	page, err := loadPageEnrich(ctx, s.MSSQL, ids, nil, s.Config.WaitlistExamIDs, now.In(confirmations.MoscowLocation()), answered)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{rows: map[rowKey]RowState{}, mis: mis, page: page}
	for _, row := range openRows {
		st := RowState{Kind: rowMissing, Saved: row.BranchID, WaitlistID: row.ID, PlanningID: row.PlanningID}
		if live, ok := page.live[row.PlanningID]; ok {
			st.Kind = rowFound
			st.Branch = live.BranchID
			st.Live = live
			if fut, ok := page.byOrigin[row.PlanningID]; ok {
				cp := fut
				st.Future = &cp
			}
			if live.BranchID != 0 && live.BranchID != row.BranchID {
				snap.writes = append(snap.writes, branchWrite{ID: row.ID, Branch: live.BranchID})
			}
		}
		snap.rows[rowKey{source: "max", id: row.ID}] = st
	}
	for i := range mis {
		live, ok := page.live[mis[i].PlanningID]
		if !ok {
			continue
		}
		reg := mis[i]
		st := RowState{Kind: rowFound, Branch: live.BranchID, PlanningID: mis[i].PlanningID, Live: live, Registry: &reg}
		if fut, ok := page.byOrigin[mis[i].PlanningID]; ok {
			cp := fut
			st.Future = &cp
		}
		snap.rows[rowKey{source: "registry", id: mis[i].PlanningID}] = st
	}
	return snap, nil
}

func (s *Services) persistBranches(ctx context.Context, wl *confirmations.Repo, snap *Snapshot) {
	if snap == nil || wl == nil || len(snap.writes) == 0 {
		return
	}
	beforePersistBranches()
	snapStore.mu.Lock()
	current := snapStore.gen
	snapStore.mu.Unlock()
	if current != snap.gen {
		return
	}
	for _, w := range snap.writes {
		if w.Branch == 0 {
			continue
		}
		if err := storeWaitlistBranch(ctx, wl, w.ID, w.Branch); err != nil {
			s.warn().Warn("waitlist branch reserve failed", "waitlist_id", w.ID, "branch_id", w.Branch, "err", err)
		}
	}
}

func (snap *Snapshot) lookup(source string, id int64) *RowState {
	if snap == nil || snap.rows == nil {
		return nil
	}
	st, ok := snap.rows[rowKey{source: source, id: id}]
	if !ok {
		return nil
	}
	return &st
}

func (snap *Snapshot) banner() string {
	if snap == nil || !snap.down {
		return ""
	}
	return "Медиалог недоступен"
}

func sweepDashboardCaches(now time.Time) {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	if snapStore.cur != nil && now.Sub(snapStore.cur.takenAt) >= dashboardCacheTTL {
		snapStore.cur = nil
	}
}

func enrichHoldsName(name string) bool {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	if snapStore.cur == nil {
		return false
	}
	for _, live := range snapStore.cur.page.live {
		if live.PatientName == name {
			return true
		}
	}
	return false
}

func misCacheHoldsName(name string) bool {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	if snapStore.cur == nil {
		return false
	}
	for _, row := range snapStore.cur.mis {
		if row.PatientName == name {
			return true
		}
	}
	return false
}

func enrichCacheLen() int {
	snapStore.mu.Lock()
	defer snapStore.mu.Unlock()
	if snapStore.cur == nil {
		return 0
	}
	return 1
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

func actor(ctx context.Context) staff.Principal {
	if p, ok := staff.PrincipalFrom(ctx); ok {
		return p
	}
	return staff.Principal{All: true}
}

func elevated(p staff.Principal) bool {
	return p.All || p.Role == staff.RoleAdmin || p.Role == staff.RoleManager
}

func knownDashboardBranch(branch int) bool {
	return branch == 106 || branch == 3 || branch == 496
}

func seesSaved(p staff.Principal, branch int) bool {
	if elevated(p) {
		return true
	}
	return knownDashboardBranch(branch) && p.Allows(branch)
}

// access reports whether this employee may see the row and whether Medialog
// personal data from the snapshot may be shown. err is for PATCH.
func access(p staff.Principal, source string, savedBranch int, st *RowState, snapDown bool) (visible bool, medialogPII bool, err error) {
	if snapDown || st == nil || st.Kind != rowFound {
		if source == "registry" {
			return false, false, confirmations.ErrWaitlistNotFound
		}
		if !seesSaved(p, savedBranch) {
			return false, false, staff.ErrBranchForbidden
		}
		return true, false, nil
	}
	if st.Branch == 0 || !knownDashboardBranch(st.Branch) {
		if !elevated(p) {
			return false, false, confirmations.ErrWaitlistMoved
		}
		return true, true, nil
	}
	if staff.SeesBranch(p, st.Branch) {
		return true, true, nil
	}
	return false, false, confirmations.ErrWaitlistMoved
}

func (s *Services) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	wl := confirmations.NewRepo(s.SQLite)
	pageN, per := waitlistPage(filter)
	snap := s.prepareSnapshot(ctx, wl)
	openRows, err := s.dashboardRows(ctx, wl, openFilter(filter))
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	pageRows := openRows
	if filter.Source != "registry" && !filter.OpenOnly && filter.Status != confirmations.WaitlistStatusNew && filter.Status != confirmations.WaitlistStatusContacted {
		pageRows, err = s.dashboardRows(ctx, wl, filter)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
	}
	restricted, scope := staffScope(ctx)
	var tagged []waitTagged
	if filter.Source != "registry" {
		for _, row := range pageRows {
			if !statusVisible(filter, row.Status) {
				continue
			}
			it, ok := s.maxItem(ctx, row, snap, restricted, scope, filter)
			if !ok {
				continue
			}
			tagged = append(tagged, waitTagged{when: waitInstant("max", row.CreatedAt), item: it})
		}
	}
	if filter.Source != "max" && snap != nil && !snap.down {
		reg, err := s.registryItems(ctx, wl, filter, snap, restricted, scope)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
		tagged = append(tagged, reg...)
	}
	sortWait(tagged)
	total := len(tagged)
	pageItems := []confirmations.DashboardItem{}
	if start, end, ok := pageWindow(total, pageN, per); ok {
		for _, t := range tagged[start:end] {
			pageItems = append(pageItems, t.item)
		}
	}
	openTotal := 0
	if filter.Source != "registry" {
		openSeen := openBranchFilter(filter)
		for _, row := range openRows {
			if !isOpenStatus(row.Status) {
				continue
			}
			if _, ok := s.maxItem(ctx, row, snap, restricted, scope, openSeen); ok {
				openTotal++
			}
		}
	}
	if filter.Source != "max" && snap != nil && !snap.down {
		regOpen, err := s.registryItems(ctx, wl, openBranchFilter(filter), snap, restricted, scope)
		if err != nil {
			return confirmations.DashboardPage{}, err
		}
		for _, t := range regOpen {
			if isOpenStatus(t.item.Status) {
				openTotal++
			}
		}
	}
	if pageItems == nil {
		pageItems = []confirmations.DashboardItem{}
	}
	return confirmations.DashboardPage{
		Items: pageItems, Total: total, OpenTotal: openTotal,
		Page: pageN, PerPage: per, Banner: snap.banner(),
	}, nil
}

func openFilter(f confirmations.WaitlistListFilter) confirmations.WaitlistListFilter {
	f.OpenOnly = true
	f.Status = ""
	f.BranchID = nil
	f.BranchIDs = nil
	f.IncludeOtherBranches = false
	return f
}

func openBranchFilter(f confirmations.WaitlistListFilter) confirmations.WaitlistListFilter {
	f.OpenOnly = true
	f.Status = ""
	return f
}

func (s *Services) dashboardRows(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter) ([]confirmations.WaitlistRow, error) {
	if filter.Source == "registry" || wl == nil {
		return nil, nil
	}
	rows, err := wl.ListDashboardRows(ctx, filter, misNow(), dashboardRowCap)
	if err != nil {
		if errors.Is(err, confirmations.ErrWaitlistOverloaded) {
			s.warn().Error("waitlist dashboard over cap", "cap", dashboardRowCap)
		}
		return nil, err
	}
	return rows, nil
}

func (s *Services) maxItem(ctx context.Context, row confirmations.WaitlistRow, snap *Snapshot, restricted bool, scope []int, filter confirmations.WaitlistListFilter) (confirmations.DashboardItem, bool) {
	st := snap.lookup("max", row.ID)
	p := actor(ctx)
	visible, pii, _ := access(p, "max", row.BranchID, st, snap != nil && snap.down)
	if !visible {
		return confirmations.DashboardItem{}, false
	}
	it := itemFromWaitlist(row)
	if pii && st != nil && st.Branch != 0 {
		it.BranchID = st.Branch
	}
	if !branchAllowed(ctx, filter, it.BranchID) {
		return confirmations.DashboardItem{}, false
	}
	return decorateFromState(it, st, pii, restricted, scope), true
}

func (s *Services) registryItems(ctx context.Context, wl *confirmations.Repo, filter confirmations.WaitlistListFilter, snap *Snapshot, restricted bool, scope []int) ([]waitTagged, error) {
	if snap == nil || snap.down {
		return nil, nil
	}
	ids := make([]int64, 0)
	for _, row := range snap.mis {
		if _, ok := snap.rows[rowKey{source: "registry", id: row.PlanningID}]; ok {
			ids = append(ids, row.PlanningID)
		}
	}
	work, err := wl.MisWorkMap(ctx, ids)
	if err != nil {
		return nil, err
	}
	p := actor(ctx)
	out := make([]waitTagged, 0)
	for i := range snap.mis {
		row := snap.mis[i]
		st := snap.lookup("registry", row.PlanningID)
		visible, pii, _ := access(p, "registry", 0, st, snap.down)
		if !visible || st == nil {
			continue
		}
		w := work[row.PlanningID]
		status := confirmations.WaitlistStatusNew
		version := 1
		if w.Status != "" {
			status = w.Status
		}
		if w.Version > 0 {
			version = w.Version
		}
		if !statusVisible(filter, status) || !sourceVisible(filter, "registry") {
			continue
		}
		it := confirmations.DashboardItem{
			ID: row.PlanningID, PlanningID: row.PlanningID, PatientID: row.PatientID,
			Status: status, Outcome: w.Outcome, BranchID: row.BranchID, Source: "registry",
			Comment: row.Comment, CreatorName: row.CreatorName,
			CreatedAt: row.CreatedAt, CreatedAtMSK: row.CreatedAt,
			UpdatedBy: w.UpdatedBy, UpdatedAt: w.UpdatedAt, Version: version,
		}
		if pii && st.Branch != 0 {
			it.BranchID = st.Branch
		}
		if !branchAllowed(ctx, filter, it.BranchID) {
			continue
		}
		it = decorateFromState(it, st, pii, restricted, scope)
		out = append(out, waitTagged{when: waitInstant("registry", row.CreatedAt), item: it})
	}
	return out, nil
}

func decorateFromState(it confirmations.DashboardItem, st *RowState, pii bool, restricted bool, scope []int) confirmations.DashboardItem {
	if !pii || st == nil || st.Kind != rowFound {
		it.PatientName = ""
		it.PatientPhone = ""
		it.PatientBirth = ""
		it.OrigDateStart = ""
		it.OrigDoctor = ""
		it.OrigService = ""
		it.FutureLabel = ""
		it.FutureDateStart = ""
		it.FutureDoctor = ""
		it.FutureService = ""
		it.Enriched = false
		if it.Source != "registry" {
			it.MedialogState = "запись не найдена в Медиалоге"
		}
		return it
	}
	it.PatientName = strings.TrimSpace(st.Live.PatientName)
	it.PatientPhone = strings.TrimSpace(st.Live.Phone)
	it.PatientBirth = strings.TrimSpace(st.Live.BirthDate)
	it.OrigDateStart = st.Live.DateStart
	it.OrigDoctor = st.Live.DoctorName
	it.OrigService = st.Live.Service
	if it.Source == "registry" {
		it.MedialogState = "запись действует"
	} else {
		it.MedialogState = repo.MedialogStateLabel(st.Live, it.ApptStart)
	}
	it.Enriched = it.PatientName != "" || it.PatientPhone != ""
	if st.Future != nil {
		origin := it.CreatedAtMSK
		if it.Source == "registry" {
			origin = it.CreatedAt
		}
		if repo.VisitCreatedAfter(origin, st.Future.CreatedAt) {
			cp := *st.Future
			applyFuture(&it, &cp, restricted, scope)
		}
	}
	return it
}

func sortWait(all []waitTagged) {
	sort.SliceStable(all, waitBefore(all))
}

func branchAllowed(ctx context.Context, f confirmations.WaitlistListFilter, branch int) bool {
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
	return allowOther(ctx, f) && !knownDashboardBranch(branch)
}

func allowOther(ctx context.Context, f confirmations.WaitlistListFilter) bool {
	if f.BranchID != nil {
		return false
	}
	if f.IncludeOtherBranches {
		return true
	}
	p, ok := staff.PrincipalFrom(ctx)
	if !ok {
		return true
	}
	return elevated(p)
}

func (s *Services) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.WaitlistRow{}, err
	}
	if p, ok := staff.PrincipalFrom(ctx); ok {
		snap := s.prepareSnapshot(ctx, wl)
		_, _, aerr := access(p, "max", row.BranchID, snap.lookup("max", row.ID), snap.down)
		if aerr != nil {
			if aerr == confirmations.ErrWaitlistMoved {
				return confirmations.WaitlistRow{}, staff.ErrBranchForbidden
			}
			return confirmations.WaitlistRow{}, aerr
		}
	}
	return wl.UpdateWaitlist(ctx, id, status, outcome, updatedBy, time.Now().UTC())
}

func (s *Services) UpdateRegistryWaitlist(ctx context.Context, planningID int64, status, outcome, updatedBy string) (confirmations.MisWork, error) {
	if s.MSSQL == nil {
		return confirmations.MisWork{}, confirmations.ErrWaitlistNotFound
	}
	wl := confirmations.NewRepo(s.SQLite)
	snap := s.prepareSnapshot(ctx, wl)
	if p, ok := staff.PrincipalFrom(ctx); ok {
		_, _, aerr := access(p, "registry", 0, snap.lookup("registry", planningID), snap.down)
		if aerr != nil {
			if aerr == confirmations.ErrWaitlistMoved {
				return confirmations.MisWork{}, staff.ErrBranchForbidden
			}
			return confirmations.MisWork{}, aerr
		}
	} else if snap.down || snap.lookup("registry", planningID) == nil {
		return confirmations.MisWork{}, confirmations.ErrWaitlistNotFound
	}
	return wl.UpdateMisWork(ctx, planningID, status, outcome, updatedBy, time.Now().UTC())
}

func (s *Services) PatchWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string) (confirmations.DashboardItem, error) {
	wl := confirmations.NewRepo(s.SQLite)
	row, err := wl.GetWaitlist(ctx, id)
	if err != nil {
		return confirmations.DashboardItem{}, err
	}
	snap := s.prepareSnapshot(ctx, wl)
	if p, ok := staff.PrincipalFrom(ctx); ok {
		_, _, aerr := access(p, "max", row.BranchID, snap.lookup("max", row.ID), snap.down)
		if aerr != nil {
			return confirmations.DashboardItem{}, aerr
		}
	}
	applied, err := wl.ApplyWaitlist(ctx, id, version, outcome, action, updatedBy, time.Now().UTC())
	return s.fullWaitlistItem(ctx, applied, snap), err
}

func (s *Services) fullWaitlistItem(ctx context.Context, row confirmations.WaitlistRow, snap *Snapshot) confirmations.DashboardItem {
	if row.ID == 0 {
		return confirmations.DashboardItem{}
	}
	restricted, scope := staffScope(ctx)
	it, ok := s.maxItem(ctx, row, snap, restricted, scope, confirmations.WaitlistListFilter{})
	if !ok {
		it = itemFromWaitlist(row)
		it.Enriched = false
	}
	return it
}

func (s *Services) PatchRegistryWaitlist(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string) (confirmations.DashboardItem, error) {
	if s.MSSQL == nil {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	wl := confirmations.NewRepo(s.SQLite)
	snap := s.prepareSnapshot(ctx, wl)
	if p, ok := staff.PrincipalFrom(ctx); ok {
		_, _, aerr := access(p, "registry", 0, snap.lookup("registry", planningID), snap.down)
		if aerr != nil {
			return confirmations.DashboardItem{}, aerr
		}
	} else if snap.down || snap.lookup("registry", planningID) == nil {
		return confirmations.DashboardItem{}, confirmations.ErrWaitlistNotFound
	}
	work, err := wl.ApplyMisWork(ctx, planningID, version, outcome, action, updatedBy, time.Now().UTC())
	restricted, scope := staffScope(ctx)
	items, rerr := s.registryItems(ctx, wl, confirmations.WaitlistListFilter{}, snap, restricted, scope)
	if rerr != nil {
		return confirmations.DashboardItem{}, rerr
	}
	for _, t := range items {
		if t.item.PlanningID == planningID {
			t.item.Status = work.Status
			t.item.Outcome = work.Outcome
			t.item.UpdatedBy = work.UpdatedBy
			t.item.UpdatedAt = work.UpdatedAt
			t.item.Version = work.Version
			return t.item, err
		}
	}
	item := confirmations.DashboardItem{
		ID: work.PlanningID, PlanningID: work.PlanningID, Source: "registry",
		Status: work.Status, Outcome: work.Outcome, UpdatedBy: work.UpdatedBy, UpdatedAt: work.UpdatedAt,
		Version: work.Version, Enriched: false,
	}
	return item, err
}
