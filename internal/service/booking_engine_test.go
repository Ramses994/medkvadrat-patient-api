package service

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/slottoken"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
	_ "modernc.org/sqlite"
)

const engSecret = "0123456789abcdef0123456789abcdef"

type fakeClinic struct {
	mu       sync.Mutex
	starts   []string
	dayErr   error
	future   map[int64]int
	rows     map[int64]repo.CreatedRow
	owners   map[int64]repo.OwnerRow
	adm      booking.Admission
	admNext  *booking.Admission
	admCalls int
	snaps    []booking.Snap
	snapErr  error
	foundID  int64
	found    bool
	noDoctor map[int64]bool
	booked   []repo.AddVisitParams
}

func (f *fakeClinic) Pair(_ context.Context, subj, exam int64) (repo.BookingOption, bool) {
	if exam != 10000688 {
		return repo.BookingOption{}, false
	}
	opt := repo.BookingOption{PlSubjID: subj, PlExamID: exam, BranchID: 496, MedecinsID: 160, DurationMin: 30, DoctorName: "Врач", Service: "Тест", BranchCode: "K"}
	if f.noDoctor[subj] {
		opt.MedecinsID = 0
	}
	if subj == 1119 || subj == 1120 || subj == 2000 {
		return opt, true
	}
	return repo.BookingOption{}, false
}

func (f *fakeClinic) DayStarts(context.Context, int64, int64, string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dayErr != nil {
		return nil, f.dayErr
	}
	return append([]string(nil), f.starts...), nil
}

func (f *fakeClinic) FutureCount(_ context.Context, patient, _ int64, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.future[patient], nil
}

func (f *fakeClinic) DoctorBusy(_ context.Context, patient, medecins, plSubj int64, day string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.booked {
		if b.PatientID != patient || len(b.VisitStart) < 10 || b.VisitStart[:10] != day {
			continue
		}
		if medecins > 0 || b.PlSubjID == plSubj {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeClinic) ReadCreated(_ context.Context, id int64) (repo.CreatedRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[id]
	if !ok {
		return repo.CreatedRow{}, nil
	}
	return row, nil
}

func (f *fakeClinic) FindIntent(context.Context, int64, int64, int64, int64, string, string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.foundID, f.found, nil
}

func (f *fakeClinic) Owner(_ context.Context, id int64) (repo.OwnerRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.owners[id]
	if !ok {
		return repo.OwnerRow{}, nil
	}
	return row, nil
}

func (f *fakeClinic) Admission(context.Context, int64) (booking.Admission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admCalls++
	if f.admNext != nil && f.admCalls > 1 {
		return *f.admNext, nil
	}
	return f.adm, nil
}

func (f *fakeClinic) Snap(context.Context, int64, *int64, *int64) (booking.Snap, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapErr != nil {
		return booking.Snap{}, f.snapErr
	}
	if len(f.snaps) == 0 {
		return booking.Snap{ReadOK: true}, nil
	}
	s := f.snaps[0]
	if len(f.snaps) > 1 {
		f.snaps = f.snaps[1:]
	}
	return s, nil
}

type fakeProcs struct {
	mu     sync.Mutex
	next   repo.ProcResult
	queue  []repo.ProcResult
	calls  atomic.Int32
	in     atomic.Int32
	maxIn  atomic.Int32
	clinic *fakeClinic
	record bool
}

func (p *fakeProcs) AddVisit(_ context.Context, in repo.AddVisitParams) repo.ProcResult {
	cur := p.in.Add(1)
	for {
		old := p.maxIn.Load()
		if cur <= old || p.maxIn.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	p.in.Add(-1)
	p.calls.Add(1)
	res := p.take()
	if p.record && res.HasRow && res.PlanningID != nil && p.clinic != nil {
		p.clinic.mu.Lock()
		if p.clinic.rows == nil {
			p.clinic.rows = map[int64]repo.CreatedRow{}
		}
		p.clinic.rows[*res.PlanningID] = repo.CreatedRow{
			Found: true, PatientID: in.PatientID, PlSubjID: in.PlSubjID, PlExamID: in.PlExamID,
			DateStart: in.VisitStart, CreatorID: in.AuthorID,
		}
		if p.clinic.future == nil {
			p.clinic.future = map[int64]int{}
		}
		p.clinic.future[in.PatientID]++
		p.clinic.booked = append(p.clinic.booked, in)
		p.clinic.mu.Unlock()
	}
	return res
}

func (p *fakeProcs) CancelVisit(context.Context, repo.CancelParams) repo.ProcResult {
	p.calls.Add(1)
	return p.take()
}

func (p *fakeProcs) take() repo.ProcResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) > 0 {
		res := p.queue[0]
		p.queue = p.queue[1:]
		return res
	}
	return p.next
}

func testEngine(t *testing.T) (*BookingEngine, *fakeClinic, *fakeProcs, *bytes.Buffer) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	c := &fakeClinic{starts: []string{"2026-11-08T10:00:00"}, rows: map[int64]repo.CreatedRow{}, owners: map[int64]repo.OwnerRow{}}
	id := int64(42)
	p := &fakeProcs{clinic: c, record: true, next: repo.ProcResult{Sent: true, Finished: true, HasRow: true, PlanningID: &id, State: 0}}
	var buf bytes.Buffer
	e := &BookingEngine{
		Ops: writeops.New(db), Clinic: c, Procs: p, Locks: &booking.Locks{},
		Cfg: config.Config{
			SlotTokenSecret: engSecret, BookingMinHoursAhead: 2, BookingMaxDays: 14, BookingMaxFuture: 3,
			LKAuthorMedecinsID: 2058, LKAuthorMeddepID: 10002309, MedialogRWTimeout: 30 * time.Second,
			CancelMinHoursBefore: 24,
		},
		Guard:  medialogwrite.NewGuard(true, true, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, []int64{1119, 1120, 2000}),
		Log:    slog.New(slog.NewTextHandler(&buf, nil)),
		LangOK: func() bool { return true },
		Now:    func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600)) },
	}
	return e, c, p, &buf
}

func tok(t *testing.T, patient, subj int64, start string, now time.Time) string {
	t.Helper()
	s, err := slottoken.Issue(engSecret, slottoken.Payload{PatientID: patient, PlSubjID: subj, PlExamID: 10000688, Start: start, DurationMin: 30, BranchID: 496}, now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBook_ResultMatrixAndNoRawText(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	samples := []struct {
		name string
		res  repo.ProcResult
		code string
	}{}
	id := int64(7)
	add := func(name, result string, state int, planning *int64, code string) {
		samples = append(samples, struct {
			name string
			res  repo.ProcResult
			code string
		}{name, repo.ProcResult{Sent: true, Finished: true, HasRow: true, PlanningID: planning, Result: result, State: state}, code})
	}
	for _, code := range []string{"001", "002", "003", "004", "005", "006", "007", "008", "009", "010", "011", "012", "013", "014", "015", "016", "019", "020", "021"} {
		text := booking.Template(code)
		text = replaceN(text, "1119", "10000688")
		want := "BOOKING_ERROR"
		switch code {
		case "009", "012":
			want = "SLOT_TAKEN"
		case "013":
			want = "SLOT_LOCKED"
		case "010":
			want = "PATIENT_TIME_CONFLICT"
		case "001", "011", "014", "015", "016":
			want = "SLOT_UNAVAILABLE"
		case "007", "008", "021":
			want = "LIMIT_MIS"
		}
		add(code, text, 0, &id, want)
	}
	add("017", replaceN(booking.Template("017"), "1119", "10000688"), 0, &id, "BOOKING_ERROR")
	add("017suffix", replaceN(booking.Template("017"), "1119", "10000688")+" - для веб-записи", 0, &id, "BOOKING_ERROR")
	add("022race", replaceOnce(booking.Template("022"), "%1", "Cannot insert the value NULL into column 'PLANNING_ID', table 'x.PLANNING_USER_EXT'"), 0, nil, "SLOT_TAKEN")
	add("022meddep", replaceOnce(booking.Template("022"), "%1", "Cannot INSERT PLANNING because MEDDEP does not exist"), 0, nil, "BOOKING_ERROR")
	add("022nest", replaceOnce(booking.Template("022"), "%1", "Maximum stored procedure, function, trigger, or view nesting level exceeded (limit 32)"), 0, nil, "BOOKING_ERROR")
	add("022ru", replaceOnce(booking.Template("022"), "%1", `Не удается вставить значение NULL в столбец PLANNING_ID таблицы PLANNING_USER_EXT`), 0, nil, "BOOKING_ERROR")
	add("state2", booking.Template("009"), 2, &id, "BOOKING_REJECTED")
	add("state2empty", "", 2, &id, "BOOKING_REJECTED")
	add("unmapped", "неизвестный текст процедуры", 0, &id, "BOOKING_REJECTED")
	add("noid", "", 0, nil, "BOOKING_UNKNOWN")
	for _, tc := range samples {
		t.Run(tc.name, func(t *testing.T) {
			e, _, p, buf := testEngine(t)
			p.record = false
			p.next = tc.res
			out := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "11111111-1111-1111-1111-111111111111")
			if out.Code != tc.code {
				t.Fatalf("code=%s want %s status=%d", out.Code, tc.code, out.Status)
			}
			raw := tc.res.Result
			if raw != "" && (bytes.Contains(buf.Bytes(), []byte(raw)) || (out.Message != "" && bytes.Contains([]byte(out.Message), []byte(raw)))) {
				t.Fatalf("raw result leaked log=%s msg=%s", buf.String(), out.Message)
			}
			dump, err := e.Ops.DumpText(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if raw != "" && bytes.Contains([]byte(dump), []byte(raw)) {
				t.Fatal("raw result stored in journal")
			}
		})
	}
}

func replaceN(tmpl, a, b string) string {
	out := tmpl
	out = replaceOnce(out, "%1", a)
	out = replaceOnce(out, "%2", b)
	return out
}

func replaceOnce(s, old, neu string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + neu + s[i+len(old):]
		}
	}
	return s
}

func TestBook_GuardBeforeProc(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	cases := []string{"off", "patient", "line", "rw", "meddep", "lang"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			e, _, p, _ := testEngine(t)
			switch name {
			case "off":
				e.Guard.Enabled = false
			case "patient":
				e.Guard = medialogwrite.NewGuard(true, true, []int64{9}, []int64{1119})
			case "line":
				e.Guard = medialogwrite.NewGuard(true, true, []int64{1}, []int64{1})
			case "rw":
				e.Guard.RWConfigured = false
			case "meddep":
				e.Cfg.LKAuthorMeddepID = 0
			case "lang":
				e.LangOK = func() bool { return false }
			}
			out := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "22222222-2222-2222-2222-222222222222")
			if p.calls.Load() != 0 || out.Status < 400 {
				t.Fatalf("status=%d calls=%d", out.Status, p.calls.Load())
			}
		})
	}
}

func TestBook_IdempotencyAndLimits(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	key := "33333333-3333-3333-3333-333333333333"
	token := tok(t, 1, 1119, "2026-11-08T10:00:00", now)
	var wg sync.WaitGroup
	var codes [2]int
	var ids [2]int64
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			out := e.Book(context.Background(), 1, token, key)
			codes[i] = out.Status
			if m, ok := out.Body.(map[string]any); ok {
				if v, ok := m["planning_id"].(int64); ok {
					ids[i] = v
				}
			}
		}(i)
	}
	wg.Wait()
	if p.calls.Load() != 1 {
		t.Fatalf("calls=%d", p.calls.Load())
	}
	for _, st := range codes {
		if st != http.StatusCreated && st != http.StatusConflict {
			t.Fatalf("status %d", st)
		}
	}
	c.mu.Lock()
	c.booked = append(c.booked, repo.AddVisitParams{VisitStart: "2026-11-08T10:00:00", PlSubjID: 1119})
	c.mu.Unlock()
	again := e.Book(context.Background(), 1, token, key)
	if again.Status != http.StatusCreated {
		t.Fatalf("replay after doctor-day would block: %d %s", again.Status, again.Code)
	}
	e.Now = func() time.Time { return now.Add(6 * time.Minute) }
	late := e.Book(context.Background(), 1, token, key)
	if late.Status != http.StatusCreated {
		t.Fatalf("expired token replay: %d %s", late.Status, late.Code)
	}
	other := tok(t, 1, 1119, "2026-11-09T10:00:00", now)
	bad := e.Book(context.Background(), 1, other, key)
	if bad.Status != http.StatusUnprocessableEntity || bad.Code != "IDEMPOTENCY_MISMATCH" {
		t.Fatalf("mismatch %d %s", bad.Status, bad.Code)
	}
	_ = ids
}

func TestBook_OpenOpAndFutureLimit(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, _, p, _ := testEngine(t)
	ctx := context.Background()
	op := e.newOp(1, writeops.ActionBook, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", writeops.BookIntent(1119, 10000688, "2026-11-08T11:00:00"), `{}`, now)
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	out := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	if out.Code != "OPERATION_IN_PROGRESS" || p.calls.Load() != 0 {
		t.Fatalf("open %d %s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	_, _ = e.Ops.Transition(ctx, mustOp(t, e, 1, op.RequestID), writeops.StateInFlight, writeops.Patch{State: writeops.StateRejected, Code: "SLOT_TAKEN", ContextJSON: `{}`})
	e2, c2, p2, _ := testEngine(t)
	c2.future = map[int64]int{1: 2}
	c2.starts = []string{"2026-11-08T10:00:00", "2026-11-09T10:00:00"}
	var wg sync.WaitGroup
	got := make(chan string, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		out := e2.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "cccccccc-cccc-cccc-cccc-cccccccccccc")
		got <- out.Code
	}()
	go func() {
		defer wg.Done()
		out := e2.Book(ctx, 1, tok(t, 1, 1120, "2026-11-09T10:00:00", now), "dddddddd-dddd-dddd-dddd-dddddddddddd")
		got <- out.Code
	}()
	wg.Wait()
	close(got)
	var lim int
	for s := range got {
		if s == "LIMIT_FUTURE" {
			lim++
		}
	}
	if lim != 1 || p2.calls.Load() != 1 {
		t.Fatalf("future limit lim=%d calls=%d", lim, p2.calls.Load())
	}
}

func mustOp(t *testing.T, e *BookingEngine, patient int64, req string) string {
	t.Helper()
	op, err := e.Ops.Find(context.Background(), patient, writeops.ActionBook, req)
	if err != nil || op == nil {
		t.Fatal(err)
	}
	return op.OpID
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestBook_LineMutex(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	c.starts = []string{"2026-11-08T10:00:00"}
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "eeeeeeee-eeee-eeee-eeee-" + leftPad(i)
			_ = e.Book(context.Background(), int64(i), tok(t, int64(i), 1119, "2026-11-08T10:00:00", now), key)
		}(i)
	}
	wg.Wait()
	if p.calls.Load() != 20 || p.maxIn.Load() != 1 {
		t.Fatalf("calls=%d max=%d", p.calls.Load(), p.maxIn.Load())
	}
}

func leftPad(i int) string {
	s := itoa(i)
	for len(s) < 12 {
		s = "0" + s
	}
	return s
}

func TestBook_CrashAndLateReconcile(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	e.AfterInFlight = func() error { return ErrStopped }
	out := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if out.Status != 0 || p.calls.Load() != 0 {
		t.Fatalf("stop before call status=%d calls=%d", out.Status, p.calls.Load())
	}
	if _, err := e.Ops.DB.Exec(`UPDATE lk_write_ops SET started_at = ?`, now.Add(-5*time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	e.Sweep(context.Background())
	op, _ := e.Ops.Find(context.Background(), 1, writeops.ActionBook, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if op == nil || op.State != writeops.StateUnknown || p.calls.Load() != 0 {
		t.Fatalf("after sweep %+v calls=%d", op, p.calls.Load())
	}
	blocked := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-09T10:00:00", now), "99999999-9999-9999-9999-999999999999")
	if blocked.Code != "OPERATION_IN_PROGRESS" {
		t.Fatalf("blocked %s", blocked.Code)
	}
	c.found = false
	e.Sweep(context.Background())
	op, _ = e.Ops.Get(context.Background(), op.OpID)
	if op.State != writeops.StateUnknown {
		t.Fatalf("still unknown, got %s", op.State)
	}
	c.found = true
	c.foundID = 77
	e.Sweep(context.Background())
	op, _ = e.Ops.Get(context.Background(), op.OpID)
	if op.State != writeops.StateOK || !op.PlanningID.Valid || op.PlanningID.Int64 != 77 || p.calls.Load() != 0 {
		t.Fatalf("reconcile %+v calls=%d", op, p.calls.Load())
	}
}

func TestBook_DoctorDayAndNoDoctorLine(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, _, _ := testEngine(t)
	c.starts = []string{"2026-11-08T10:00:00", "2026-11-09T10:00:00"}
	c.noDoctor = map[int64]bool{}
	first := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "12121212-1212-1212-1212-121212121212")
	if first.Status != http.StatusCreated {
		t.Fatalf("first %d %s", first.Status, first.Code)
	}
	second := e.Book(context.Background(), 1, tok(t, 1, 1120, "2026-11-08T10:00:00", now), "13131313-1313-1313-1313-131313131313")
	if second.Code != "LIMIT_DOCTOR_DAY" {
		t.Fatalf("same doctor day %s", second.Code)
	}
	other := e.Book(context.Background(), 1, tok(t, 1, 1120, "2026-11-09T10:00:00", now), "14141414-1414-1414-1414-141414141414")
	if other.Status != http.StatusCreated {
		t.Fatalf("other day %d %s", other.Status, other.Code)
	}
	e2, c2, _, _ := testEngine(t)
	c2.noDoctor = map[int64]bool{2000: true}
	c2.starts = []string{"2026-11-08T10:00:00"}
	ok := e2.Book(context.Background(), 2, tok(t, 2, 2000, "2026-11-08T10:00:00", now), "15151515-1515-1515-1515-151515151515")
	if ok.Status != http.StatusCreated {
		t.Fatalf("no doctor %d %s", ok.Status, ok.Code)
	}
	again := e2.Book(context.Background(), 2, tok(t, 2, 2000, "2026-11-08T10:00:00", now), "16161616-1616-1616-1616-161616161616")
	if again.Code != "LIMIT_DOCTOR_DAY" {
		t.Fatalf("same line %s", again.Code)
	}
}

func TestCancel_Outcomes(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	e.Now = func() time.Time { return now }
	start := "2026-11-08T10:00:00"
	c.owners[5] = repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: start}
	c.adm = booking.Admission{Found: true, Active: true}
	before := booking.Snap{ReadOK: true}
	after := booking.Snap{ReadOK: true, PlanningFound: true, PlanningCancelled: 1}
	c.snaps = []booking.Snap{before, after}
	p.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true, Result: ""}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "17171717-1717-1717-1717-171717171717")
	if out.Status != http.StatusOK || p.calls.Load() != 1 {
		t.Fatalf("full %d %s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	e2, c2, p2, _ := testEngine(t)
	e2.Now = func() time.Time { return now }
	c2.owners[5] = c.owners[5]
	c2.adm = c.adm
	same := booking.Snap{ReadOK: true}
	c2.snaps = []booking.Snap{same, same}
	p2.record = false
	p2.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true}
	out = e2.Cancel(context.Background(), 1, 5, "18181818-1818-1818-1818-181818181818")
	if out.Code != "CANCEL_FAILED" {
		t.Fatalf("unchanged %s", out.Code)
	}
	e3, c3, p3, _ := testEngine(t)
	e3.Now = func() time.Time { return now }
	c3.owners[5] = c.owners[5]
	c3.adm = c.adm
	part := booking.Snap{ReadOK: true, PlanningCancelled: 0, AnswFound: true, A: []int64{9}}
	c3.snaps = []booking.Snap{booking.Snap{ReadOK: true}, part}
	p3.record = false
	p3.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true}
	out = e3.Cancel(context.Background(), 1, 5, "19191919-1919-1919-1919-191919191919")
	if out.Code != "CANCEL_PARTIAL" && out.Code != "EXTERNAL_CHANGE" {
		t.Fatalf("partial %s", out.Code)
	}
	foreign := e.Cancel(context.Background(), 2, 5, "20202020-2020-2020-2020-202020202020")
	if foreign.Status != http.StatusNotFound {
		t.Fatalf("foreign %d", foreign.Status)
	}
}

func TestCancel_NotSupportedWithoutCall(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	e.Now = func() time.Time { return now }
	c.owners[5] = repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: "2026-11-08T10:00:00", Cancelled: 0}
	c.adm = booking.Admission{Directions: []booking.Direction{{ID: 1, PlanningCR: intPtr(5)}}}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "21212121-2121-2121-2121-212121212121")
	if out.Code != "CANCEL_NOT_SUPPORTED" || p.calls.Load() != 0 {
		t.Fatalf("%s calls=%d", out.Code, p.calls.Load())
	}
	e2, c2, p2, _ := testEngine(t)
	e2.Now = func() time.Time { return now }
	c2.owners[5] = c.owners[5]
	c2.adm = booking.Admission{Found: true}
	bill := booking.Admission{Answers: []booking.Answer{{ID: 1, PatdirecID: 2, HasPatdirec: true, BillNull: false}}, Directions: []booking.Direction{{ID: 2, PlanningCR: intPtr(5)}}, OwnDirection: &booking.Direction{ID: 2, PlanningCR: intPtr(5)}}
	c2.admNext = &bill
	c2.snaps = []booking.Snap{{ReadOK: true}}
	p2.record = false
	p2.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true}
	out = e2.Cancel(context.Background(), 1, 5, "23232323-2323-2323-2323-232323232323")
	if out.Code != "CANCEL_NOT_SUPPORTED" || p2.calls.Load() != 0 {
		t.Fatalf("reread bill %s calls=%d", out.Code, p2.calls.Load())
	}
}

func intPtr(v int64) *int64 { return &v }

func TestManualResolveAndRace(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, _, _ := testEngine(t)
	ctx := context.Background()
	op := e.newOp(1, writeops.ActionBook, "24242424-2424-2424-2424-242424242424", writeops.BookIntent(1119, 10000688, "2026-11-08T10:00:00"), `{"org_id":496}`, now.Add(-20*time.Minute))
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	saved, _ := e.Ops.Find(ctx, 1, writeops.ActionBook, op.RequestID)
	_, _ = e.Ops.Transition(ctx, saved.OpID, writeops.StateInFlight, writeops.Patch{State: writeops.StateUnknown, Code: "BOOKING_UNKNOWN", ContextJSON: saved.ContextJSON})
	bad := e.Resolve(ctx, ResolveInput{OpID: saved.OpID, State: writeops.StateManualOK, Note: "checked_in_client", Actor: "s2s"})
	if bad.Status != http.StatusUnprocessableEntity {
		t.Fatalf("manual without id %d", bad.Status)
	}
	pid := int64(88)
	c.rows[88] = repo.CreatedRow{Found: true, PatientID: 1, PlSubjID: 1119, PlExamID: 10000688, DateStart: "2026-11-08T10:00:00", CreatorID: 2058}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = e.Resolve(ctx, ResolveInput{OpID: saved.OpID, State: writeops.StateManualOK, PlanningID: &pid, Note: "checked_in_client", Actor: "s2s"})
	}()
	go func() {
		defer wg.Done()
		c.found = true
		c.foundID = 88
		e.Sweep(ctx)
	}()
	wg.Wait()
	got, _ := e.Ops.Get(ctx, saved.OpID)
	if got.State != writeops.StateManualOK && got.State != writeops.StateOK {
		t.Fatalf("race state %s", got.State)
	}
	if got.State == writeops.StateManualOK {
		replay := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), op.RequestID)
		if replay.Status != http.StatusCreated {
			t.Fatalf("manual replay %d", replay.Status)
		}
	}
	e.Sweep(ctx)
	got2, _ := e.Ops.Get(ctx, saved.OpID)
	if got2.State != got.State {
		t.Fatalf("background changed manual/final %s -> %s", got.State, got2.State)
	}
}

func TestBook_UniqueConflictReplay(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, _, _, _ := testEngine(t)
	ctx := context.Background()
	intent := writeops.BookIntent(1119, 10000688, "2026-11-08T10:00:00")
	key := "30303030-3030-3030-3030-303030303030"
	op := e.newOp(1, writeops.ActionBook, key, intent, `{}`, now)
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	saved, err := e.Ops.Find(ctx, 1, writeops.ActionBook, key)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	pid := int64(55)
	ok, err := e.Ops.Transition(ctx, saved.OpID, writeops.StateInFlight, writeops.Patch{
		State: writeops.StateOK, PlanningID: sql.NullInt64{Int64: pid, Valid: true}, ContextJSON: `{}`,
	})
	if err != nil || !ok {
		t.Fatal(err)
	}
	_, out, owned := e.insertOrReplay(ctx, e.newOp(1, writeops.ActionBook, key, intent, `{}`, now), intent)
	if owned || out.Status != http.StatusCreated {
		t.Fatalf("conflict replay owned=%v status=%d code=%s", owned, out.Status, out.Code)
	}
	body, _ := out.Body.(map[string]any)
	if body["planning_id"] != pid {
		t.Fatalf("planning %v", body["planning_id"])
	}
}

func TestBook_CancelThenRebook(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	first := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "32323232-3232-3232-3232-323232323232")
	if first.Status != http.StatusCreated {
		t.Fatalf("book %d %s", first.Status, first.Code)
	}
	id1, _ := first.Body.(map[string]any)["planning_id"].(int64)
	c.owners[id1] = repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: "2026-11-08T10:00:00"}
	c.snaps = []booking.Snap{{ReadOK: true}, {ReadOK: true, PlanningFound: true, PlanningCancelled: 1}}
	id2 := int64(43)
	p.next.PlanningID = &id2
	cancelled := e.Cancel(ctx, 1, id1, "33333333-3333-4333-8333-333333333333")
	if cancelled.Status != http.StatusOK {
		t.Fatalf("cancel %d %s", cancelled.Status, cancelled.Code)
	}
	c.mu.Lock()
	c.booked = nil
	c.future[1] = 0
	c.mu.Unlock()
	second := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), "34343434-3434-3434-3434-343434343434")
	if second.Status != http.StatusCreated {
		t.Fatalf("rebook %d %s", second.Status, second.Code)
	}
	idNew, _ := second.Body.(map[string]any)["planning_id"].(int64)
	if idNew == id1 || idNew != 43 {
		t.Fatalf("new planning %d old %d", idNew, id1)
	}
}

func TestCancel_TimeoutThenLate(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	e.Now = func() time.Time { return now }
	c.owners[5] = repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: "2026-11-08T10:00:00"}
	same := booking.Snap{ReadOK: true}
	c.snaps = []booking.Snap{same, same}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: false}
	out := e.Cancel(context.Background(), 1, 5, "35353535-3535-3535-3535-353535353535")
	if out.Code != "CANCEL_UNKNOWN" || p.calls.Load() != 1 {
		t.Fatalf("timeout %s calls=%d", out.Code, p.calls.Load())
	}
	c.snaps = []booking.Snap{{ReadOK: true, PlanningFound: true, PlanningCancelled: 1}}
	e.Now = func() time.Time { return now.Add(3 * time.Minute) }
	e.Sweep(context.Background())
	op, _ := e.Ops.Find(context.Background(), 1, writeops.ActionCancel, "35353535-3535-3535-3535-353535353535")
	if op == nil || op.State != writeops.StateOK || p.calls.Load() != 1 {
		t.Fatalf("late full %+v calls=%d", op, p.calls.Load())
	}

	e2, c2, p2, _ := testEngine(t)
	e2.Now = func() time.Time { return now }
	c2.owners[5] = c.owners[5]
	part := booking.Snap{ReadOK: true, AnswFound: true, A: []int64{9}}
	c2.snaps = []booking.Snap{{ReadOK: true}, part}
	p2.record = false
	p2.next = repo.ProcResult{Sent: true, Finished: false}
	out = e2.Cancel(context.Background(), 1, 5, "36363636-3636-3636-3636-363636363636")
	if out.Code != "CANCEL_UNKNOWN" {
		t.Fatalf("timeout partial %s", out.Code)
	}
	c2.snaps = []booking.Snap{part}
	e2.Now = func() time.Time { return now.Add(11 * time.Minute) }
	e2.Sweep(context.Background())
	op, _ = e2.Ops.Find(context.Background(), 1, writeops.ActionCancel, "36363636-3636-3636-3636-363636363636")
	if op == nil || op.State != writeops.StatePartial || op.Code != "EXTERNAL_CHANGE" || p2.calls.Load() != 1 {
		t.Fatalf("promoted %+v calls=%d", op, p2.calls.Load())
	}
}

func TestManualRejected_Unblocks(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	key := "37373737-3737-3737-3737-373737373737"
	op := e.newOp(1, writeops.ActionBook, key, writeops.BookIntent(1119, 10000688, "2026-11-08T10:00:00"), `{}`, now.Add(-20*time.Minute))
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	saved, err := e.Ops.Find(ctx, 1, writeops.ActionBook, key)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	if _, err := e.Ops.Transition(ctx, saved.OpID, writeops.StateInFlight, writeops.Patch{State: writeops.StateUnknown, Code: "BOOKING_UNKNOWN", ContextJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	c.found = false
	out := e.Resolve(ctx, ResolveInput{OpID: saved.OpID, State: writeops.StateManualRejected, Note: "other", Actor: "s2s"})
	if out.Status != http.StatusOK {
		t.Fatalf("resolve %d %s", out.Status, out.Code)
	}
	replay := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), key)
	if replay.Status != http.StatusConflict || replay.Code != "OPERATION_NOT_DONE" || p.calls.Load() != 0 {
		t.Fatalf("replay %d %s calls=%d", replay.Status, replay.Code, p.calls.Load())
	}
	c.starts = []string{"2026-11-08T10:00:00", "2026-11-09T10:00:00"}
	fresh := e.Book(ctx, 1, tok(t, 1, 1119, "2026-11-09T10:00:00", now), "38383838-3838-3838-3838-383838383838")
	if fresh.Status != http.StatusCreated || p.calls.Load() != 1 {
		t.Fatalf("fresh %d %s calls=%d", fresh.Status, fresh.Code, p.calls.Load())
	}
}
