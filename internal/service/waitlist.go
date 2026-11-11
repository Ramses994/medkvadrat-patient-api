package service

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

var loadMis = repo.ListWaitlistMis

// misNowPtr is the dashboard clock. Tests replace it with setMisNow so the
// background snapshot sweep does not race a plain assignment.
var misNowPtr atomic.Pointer[func() time.Time]

func init() {
	fn := time.Now
	misNowPtr.Store(&fn)
}

func misNow() time.Time {
	fn := misNowPtr.Load()
	if fn == nil || *fn == nil {
		return time.Now()
	}
	return (*fn)()
}

type pageEnrichment struct {
	live     map[int64]repo.WaitlistPlanningLive
	futures  []repo.WaitlistFutureVisit
	byOrigin map[int64]repo.WaitlistFutureVisit
}

type waitTagged struct {
	item confirmations.DashboardItem
	when time.Time
}

// dashboardCacheTTL is the maximum age of one dashboard snapshot.
// Access, personal data and counters share it. See docs/lk/dashboard-snapshot.md.
const dashboardCacheTTL = 30 * time.Second

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
