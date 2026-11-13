//go:build integration_write

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/api"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/slottoken"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

// TestLK2 books and cancels patient 1 on line 1119 in the live clinic database.
// It does not run unless LK_INTEGRATION_WRITE=1. Owner only, on D1.
func TestLK2(t *testing.T) {
	env := newLive(t)
	if len(env.slots) < 3 {
		t.Fatal("no slot after cancel threshold")
	}
	t.Run("main", func(t *testing.T) { env.main(t, env.slots[0]) })
	if t.Failed() {
		t.FailNow()
	}
	t.Run("replay", func(t *testing.T) { env.replay(t, env.slots[1]) })
	if t.Failed() {
		t.FailNow()
	}
	t.Run("two", func(t *testing.T) { env.two(t, env.slots[2]) })
	if t.Failed() {
		t.FailNow()
	}
	env.failBad(t)
}

type slotOffer struct {
	Start string
	Token string
}

type liveEnv struct {
	t       *testing.T
	cfg     config.Config
	ro      *sql.DB
	rw      *sql.DB
	sqlite  *sql.DB
	srv     *httptest.Server
	client  *http.Client
	jwt     string
	slots   []slotOffer
	ids     idList
	runDir  string
	started time.Time
	logf    *os.File
}

func newLive(t *testing.T) *liveEnv {
	t.Helper()
	if os.Getenv("LK_INTEGRATION_WRITE") != "1" {
		t.Skip("меняет боевую базу; только владелец; по D1; нужен LK_INTEGRATION_WRITE=1")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MedialogWriteEnabled || cfg.MSSQL.RWUser == "" {
		t.Fatal("need MEDIALOG_WRITE_ENABLED=true and DB_RW_USER")
	}
	if len(cfg.MedialogWriteAllowPatients) != 1 || cfg.MedialogWriteAllowPatients[0] != 1 ||
		len(cfg.MedialogWriteAllowPlSubj) != 1 || cfg.MedialogWriteAllowPlSubj[0] != 1119 {
		t.Fatal("allowlists must be exactly patient 1 and line 1119")
	}
	if !slottoken.SecretOK(cfg.SlotTokenSecret) {
		t.Fatal("SLOT_TOKEN_SECRET shorter than 32 bytes")
	}
	pair := false
	for _, p := range cfg.BookingPairs {
		if p.PlSubjID == 1119 && p.PlExamID == 10000688 {
			pair = true
		}
	}
	if !pair {
		t.Fatal("LK_BOOKING_PAIRS must include 1119:10000688")
	}
	if err := liveJournalBusy(os.Getenv("LK_LIVE_API_URL"), os.Getenv("LK_LIVE_API_TOKEN")); err != nil {
		t.Fatalf("журнал стенда: %v", err)
	}
	if cfg.JWT.Secret == "" {
		cfg.JWT.Secret = "integration-write-test-secret-32b"
	}
	runDir := filepath.Join(".", "b7-run-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fmt.Println("b7 run", runDir)
	t.Log("b7 run", runDir)
	logf, err := os.Create(filepath.Join(runDir, "handler.log"))
	if err != nil {
		t.Fatal(err)
	}
	ro, err := db.OpenMSSQL(cfg.MSSQL)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := db.OpenMSSQLRW(cfg.MSSQL)
	if err != nil {
		ro.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	lang, err := repo.RWLanguage(ctx, rw)
	if err != nil || !booking.LanguageOK(lang) {
		ro.Close()
		rw.Close()
		t.Fatalf("rw language %q", lang)
	}
	lite, err := db.OpenSQLite(filepath.Join(runDir, "gateway.db"))
	if err != nil {
		ro.Close()
		rw.Close()
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, lite); err != nil {
		ro.Close()
		rw.Close()
		lite.Close()
		t.Fatal(err)
	}
	env := &liveEnv{
		t: t, cfg: cfg, ro: ro, rw: rw, sqlite: lite, client: &http.Client{Timeout: 90 * time.Second},
		runDir: runDir, started: time.Now(), logf: logf,
	}
	t.Cleanup(func() {
		fmt.Println("b7 run", runDir)
		t.Log("b7 run", runDir)
	})
	t.Cleanup(env.close)
	if n := env.futureActive(t); n != 0 {
		env.close()
		t.Fatalf("patient 1 already has %d future active rows on line 1119", n)
	}
	var open int
	if err := lite.QueryRow(`SELECT COUNT(*) FROM lk_write_ops WHERE state IN ('in_flight','unknown','partial')`).Scan(&open); err != nil || open != 0 {
		env.close()
		t.Fatalf("open ops %d", open)
	}
	logger := slog.New(slog.NewTextHandler(logf, nil))
	svc := service.New(ro, rw, lite, repo.New(ro), logger, 306, cfg)
	svc.RefreshBookingPairs(ctx)
	svc.RefreshRWLanguage(ctx)
	env.srv = httptest.NewServer(api.NewRouter(cfg, svc, logger))
	tok, err := auth.IssueAccess([]byte(cfg.JWT.Secret), cfg.JWT.Issuer, 1, "", cfg.Auth.Mode, time.Hour, time.Now())
	if err != nil {
		env.close()
		t.Fatal(err)
	}
	env.jwt = tok
	env.slots = env.loadSlots(t)
	t.Cleanup(env.reconcileOwned)
	t.Cleanup(env.cancelAll)
	return env
}

func (e *liveEnv) close() {
	if e.srv != nil {
		e.srv.Close()
		e.srv = nil
	}
	if e.ro != nil {
		_ = e.ro.Close()
		e.ro = nil
	}
	if e.rw != nil {
		_ = e.rw.Close()
		e.rw = nil
	}
	if e.sqlite != nil {
		_ = e.sqlite.Close()
		e.sqlite = nil
	}
	if e.logf != nil {
		_ = e.logf.Close()
		e.logf = nil
	}
}

func (e *liveEnv) futureActive(t *testing.T) int {
	t.Helper()
	now := time.Now().In(time.FixedZone("MSK", 3*3600)).Format("2006-01-02T15:04:05")
	var n int
	err := e.ro.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM PLANNING
WHERE PATIENTS_ID = @pid AND PL_SUBJ_ID = @subj
  AND ISNULL(CANCELLED, 0) = 0 AND ISNULL(STATUS, 0) = 0
  AND DATE_START >= CONVERT(datetime, @now, 126)`,
		sql.Named("pid", int64(1)), sql.Named("subj", int64(1119)), sql.Named("now", now)).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *liveEnv) reconcileOwned() {
	if e.ro == nil {
		return
	}
	since := e.started.In(time.FixedZone("MSK", 3*3600)).Format("2006-01-02T15:04:05")
	rows, err := e.ro.QueryContext(context.Background(), `
SELECT PLANNING_ID FROM PLANNING
WHERE PATIENTS_ID = @pid AND PL_SUBJ_ID = @subj
  AND MEDECINS_CREATOR_ID = @author
  AND CREATE_DATE_TIME >= CONVERT(datetime, @since, 126)
  AND ISNULL(CANCELLED, 0) = 0 AND ISNULL(STATUS, 0) = 0`,
		sql.Named("pid", int64(1)), sql.Named("subj", int64(1119)),
		sql.Named("author", e.cfg.LKAuthorMedecinsID), sql.Named("since", since))
	if err != nil {
		e.t.Errorf("сверка после очистки: %v", err)
		return
	}
	defer rows.Close()
	var left []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			e.t.Errorf("сверка после очистки: %v", err)
			return
		}
		left = append(left, id)
	}
	if err := rows.Err(); err != nil {
		e.t.Errorf("сверка после очистки: %v", err)
		return
	}
	if len(left) > 0 {
		e.t.Errorf("остались действующие planning_id %v. отменить в клиенте: Расписание → линия 1119 → запись → „Пациент не придёт“", left)
	}
}

func (e *liveEnv) loadSlots(t *testing.T) []slotOffer {
	t.Helper()
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Now().In(loc)
	minH := e.cfg.CancelMinHoursBefore
	if minH <= 0 {
		minH = 24
	}
	threshold := now.Add(time.Duration(minH)*time.Hour + 2*time.Hour)
	from := time.Date(threshold.Year(), threshold.Month(), threshold.Day(), 0, 0, 0, 0, loc)
	last := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, e.cfg.BookingMaxDays)
	if from.After(last) {
		return nil
	}
	to := from.AddDate(0, 0, 13)
	if to.After(last) {
		to = last
	}
	url := fmt.Sprintf("%s/api/catalog/slots?pl_subj_id=1119&pl_exam_id=10000688&date_from=%s&date_to=%s",
		e.srv.URL, from.Format("2006-01-02"), to.Format("2006-01-02"))
	status, raw := e.call(t, http.MethodGet, url, "", nil)
	if status != http.StatusOK {
		t.Fatalf("slots status %d", status)
	}
	var data struct {
		Slots []struct {
			Start string `json:"start"`
			Token string `json:"slot_token"`
		} `json:"slots"`
	}
	decodeData(t, raw, &data)
	var out []slotOffer
	for _, s := range data.Slots {
		at, err := time.ParseInLocation("2006-01-02T15:04:05", s.Start, loc)
		if err != nil || at.Before(threshold) || s.Token == "" {
			continue
		}
		out = append(out, slotOffer{Start: s.Start, Token: s.Token})
	}
	return out
}

func (e *liveEnv) main(t *testing.T, s slotOffer) {
	id := e.book(t, s, newKey())
	row, err := repo.ReadCreated(context.Background(), e.ro, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.PatientID != 1 || row.PlSubjID != 1119 || row.PlExamID != 10000688 || row.CreatorID != e.cfg.LKAuthorMedecinsID ||
		row.DateStart != s.Start || row.Cancelled != 0 || row.Status != 0 {
		t.Fatalf("created planning_id=%d", id)
	}
	e.cancel(t, id)
	var cancelled, answ, dir int
	if err := e.ro.QueryRowContext(context.Background(), `
SELECT CAST(ISNULL(CANCELLED, 0) AS int) FROM PLANNING WHERE PLANNING_ID = @id`, sql.Named("id", id)).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if err := e.ro.QueryRowContext(context.Background(), `
SELECT TOP 1 CAST(ISNULL(ANSW_STATE, 0) AS int) FROM DIR_ANSW WHERE PLANNING_ID = @id`, sql.Named("id", id)).Scan(&answ); err != nil {
		t.Fatal(err)
	}
	if err := e.ro.QueryRowContext(context.Background(), `
SELECT TOP 1 CAST(ISNULL(DIR_STATE, 0) AS int) FROM PATDIREC WHERE PLANNING_CR_ID = @id`, sql.Named("id", id)).Scan(&dir); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || answ != 2 || dir != 3 {
		t.Fatalf("after cancel planning_id=%d cancelled=%d answ=%d dir=%d", id, cancelled, answ, dir)
	}
}

func (e *liveEnv) replay(t *testing.T, s slotOffer) {
	key := newKey()
	var wg sync.WaitGroup
	type res struct {
		status int
		id     int64
	}
	got := make([]res, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			st, id := e.bookStatus(t, s.Token, key)
			got[i] = res{status: st, id: id}
		}(i)
	}
	wg.Wait()
	var created int64
	okN := 0
	for _, g := range got {
		if g.status == http.StatusCreated {
			okN++
			created = g.id
		} else if g.status != http.StatusConflict {
			t.Fatalf("replay status %d", g.status)
		}
	}
	if okN < 1 || created == 0 {
		t.Fatal("replay created nothing")
	}
	for _, g := range got {
		if g.status == http.StatusCreated && g.id != created {
			t.Fatalf("two planning ids %d %d", g.id, created)
		}
	}
	var n int
	err := e.ro.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM PLANNING
WHERE PATIENTS_ID = @pid AND PL_SUBJ_ID = @subj AND PL_EXAM_ID = @exam
  AND DATE_START = CONVERT(datetime, @start, 126)
  AND ISNULL(CANCELLED, 0) = 0 AND ISNULL(STATUS, 0) = 0`,
		sql.Named("pid", int64(1)), sql.Named("subj", int64(1119)), sql.Named("exam", int64(10000688)),
		sql.Named("start", s.Start)).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("rows %d err %v", n, err)
	}
	e.cancel(t, created)
}

func (e *liveEnv) two(t *testing.T, s slotOffer) {
	a := e.book(t, s, newKey())
	st, code, _ := e.postBook(t, s.Token, newKey())
	if st != http.StatusConflict && !(st == http.StatusUnprocessableEntity && code == "LIMIT_DOCTOR_DAY") {
		t.Fatalf("second status %d code %s", st, code)
	}
	if code != "" && code != "SLOT_TAKEN" && code != "LIMIT_DOCTOR_DAY" && code != "OPERATION_IN_PROGRESS" {
		t.Fatalf("second code %s", code)
	}
	e.cancel(t, a)
}

func (e *liveEnv) book(t *testing.T, s slotOffer, key string) int64 {
	t.Helper()
	st, code, id := e.postBook(t, s.Token, key)
	if st != http.StatusCreated || id <= 0 {
		e.failBad(t)
		t.Fatalf("book status %d code %s planning_id=%d", st, code, id)
	}
	return id
}

func (e *liveEnv) bookStatus(t *testing.T, token, key string) (int, int64) {
	t.Helper()
	st, _, id := e.postBook(t, token, key)
	return st, id
}

func (e *liveEnv) postBook(t *testing.T, token, key string) (int, string, int64) {
	t.Helper()
	body := []byte(`{"slot_token":"` + token + `"}`)
	status, raw := e.call(t, http.MethodPost, e.srv.URL+"/api/me/appointments", key, body)
	var data struct {
		PlanningID int64 `json:"planning_id"`
	}
	code := decodeData(t, raw, &data)
	notePlanningID(&e.ids, data.PlanningID)
	return status, code, data.PlanningID
}

func (e *liveEnv) cancel(t *testing.T, id int64) {
	t.Helper()
	if err := e.cancelQuiet(id, e.ids.cancelKey(id)); err != nil {
		e.failBad(t)
		t.Fatalf("planning_id=%d %v. отменить в клиенте: Расписание → линия 1119 → запись → „Пациент не придёт“", id, err)
	}
}

func (e *liveEnv) cancelQuiet(id int64, key string) error {
	return cancelAppointment(e.client, e.srv.URL, e.jwt, id, key)
}

func (e *liveEnv) confirmedCancelled(id int64) (bool, error) {
	var cancelled sql.NullInt64
	err := e.ro.QueryRowContext(context.Background(), `
SELECT CAST(ISNULL(CANCELLED, 0) AS int) FROM PLANNING WHERE PLANNING_ID = @id`, sql.Named("id", id)).Scan(&cancelled)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return cancelled.Int64 == 1, nil
}

func (e *liveEnv) cancelAll() {
	cancelEach(e.t, e.ids.snapshot(), e.ids.cancelKey, e.cancelQuiet, e.confirmedCancelled)
}

func (e *liveEnv) failBad(t *testing.T) {
	t.Helper()
	rows, err := e.sqlite.Query(`SELECT op_id, IFNULL(planning_id, 0) FROM lk_write_ops WHERE state IN ('unknown','partial')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		var id int64
		if err := rows.Scan(&op, &id); err != nil {
			t.Fatal(err)
		}
		t.Errorf("op_id=%s planning_id=%d", op, id)
	}
}

func (e *liveEnv) call(t *testing.T, method, url, key string, body []byte) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.jwt)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

func decodeData(t *testing.T, raw []byte, dest any) string {
	t.Helper()
	var env struct {
		Data         json.RawMessage `json:"data"`
		ErrorDetails struct {
			Code string `json:"code"`
		} `json:"error_details"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("bad json")
	}
	if len(env.Data) > 0 && string(env.Data) != "null" && dest != nil {
		_ = json.Unmarshal(env.Data, dest)
	}
	return env.ErrorDetails.Code
}

func newKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
