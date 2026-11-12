package handler

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/consents"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func consentDB(t *testing.T) *consents.Store {
	t.Helper()
	sqlDB, err := db.OpenSQLite(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.Migrate(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	s := consents.New(sqlDB)
	s.SetClock(func() time.Time { return time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC) })
	return s
}

func TestBotConsents_RejectsBadFields(t *testing.T) {
	s := consentDB(t)
	h := BotConsentsHandler{Store: s}
	bodies := []string{
		`{"patient_id":0,"max_user_id":1,"kind":"service","text_version":"service-v1","action":"accept"}`,
		`{"patient_id":1,"max_user_id":0,"kind":"service","text_version":"service-v1","action":"accept"}`,
		`{"patient_id":1,"max_user_id":1,"kind":"marketing","text_version":"service-v1","action":"accept"}`,
		`{"patient_id":1,"max_user_id":1,"kind":"service","text_version":"Service-v1","action":"accept"}`,
		`{"patient_id":1,"max_user_id":1,"kind":"service","text_version":"service-v1","action":"maybe"}`,
	}
	for _, body := range bodies {
		w := httptest.NewRecorder()
		h.Post(w, httptest.NewRequest(http.MethodPost, "/api/internal/bot-consents", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d %s", body, w.Code, w.Body.String())
		}
	}
	var n int
	n, err := s.RowCount(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
}

func TestRevokeWithoutRows_IsRevokedInGetAndTheThreeFeeds(t *testing.T) {
	s := consentDB(t)
	h := BotConsentsHandler{Store: s}
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.Post(w, httptest.NewRequest(http.MethodPost, "/api/internal/bot-consents", strings.NewReader(body)))
		return w
	}
	body := `{"patient_id":20,"max_user_id":7,"kind":"service","text_version":"service-v1","action":"revoke"}`
	if w := post(body); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"revoked"`) {
		t.Fatalf("revoke %d %s", w.Code, w.Body.String())
	}
	if n, err := s.RowCount(context.Background()); err != nil || n != 1 {
		t.Fatalf("rows=%d err=%v", n, err)
	}
	if w := post(body); w.Code != http.StatusOK {
		t.Fatalf("repeat %d %s", w.Code, w.Body.String())
	}
	if n, err := s.RowCount(context.Background()); err != nil || n != 1 {
		t.Fatalf("repeat rows=%d err=%v", n, err)
	}
	gw := httptest.NewRecorder()
	h.Get(gw, httptest.NewRequest(http.MethodGet, "/api/internal/bot-consents?patient_id=20", nil))
	if gw.Code != http.StatusOK || !strings.Contains(gw.Body.String(), `"state":"revoked"`) {
		t.Fatalf("get %d %s", gw.Code, gw.Body.String())
	}

	loc := time.FixedZone("MSK", 3*3600)
	row := repo.DueReminder{
		PlanningID: 20, PatientID: 20, DoctorName: "Иванов Иван", Service: "Прием",
		BranchID: 3, DateConsultation: time.Date(2026, 6, 26, 19, 30, 0, 0, loc),
	}
	feeds := []struct {
		name string
		call func(*httptest.ResponseRecorder)
	}{
		{"due", func(w *httptest.ResponseRecorder) {
			RemindersHandler{Svc: reminderPage{rows: []repo.DueReminder{row}}, Consents: s}.Due(w, httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-26T00:00:00&to=2026-06-27T00:00:00", nil))
		}},
		{"new", func(w *httptest.ResponseRecorder) {
			NotificationsHandler{Svc: bookingPage{rows: []repo.NewBooking{{DueReminder: row}}}, Consents: s}.NewBookings(w, httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", strings.NewReader(`{"patient_ids":[20]}`)))
		}},
		{"one", func(w *httptest.ResponseRecorder) {
			req := httptest.NewRequest(http.MethodGet, "/api/notifications/booking/20", nil)
			req.SetPathValue("planning_id", "20")
			NotificationsHandler{Svc: bookingPage{one: repo.BookingNotice{DueReminder: row}}, Consents: s}.BookingByID(w, req)
		}},
	}
	for _, feed := range feeds {
		w := httptest.NewRecorder()
		feed.call(w)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"bot_consent_service":"revoked"`) {
			t.Fatalf("%s %d %s", feed.name, w.Code, w.Body.String())
		}
	}
	if w := post(`{"patient_id":20,"max_user_id":7,"kind":"service","text_version":"service-v1","action":"accept"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"active"`) {
		t.Fatalf("accept %d %s", w.Code, w.Body.String())
	}
}

func TestAdsRefusalWithoutConsent_ExportsRevokedWithEmptyDate(t *testing.T) {
	s := consentDB(t)
	if _, _, err := s.Apply(context.Background(), consents.Input{PatientID: 13, MaxUserID: 1, Kind: consents.KindAds, TextVersion: "ads-v1", Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	h := AdsExportHandler{Store: s, Names: &scriptNames{names: map[int64]string{}}}
	w := httptest.NewRecorder()
	h.AdsCSV(w, withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/consents/ads.csv", nil), staff.Principal{ID: 2, Login: "boss", Role: staff.RoleManager}))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "13,,отозвано,,12.11.2026") || strings.Contains(body, "service-v1") {
		t.Fatalf("%d %s", w.Code, body)
	}
}

func TestBotConsentService_OneSQLiteReadPerResponse(t *testing.T) {
	s := consentDB(t)
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, consents.Input{PatientID: 10, MaxUserID: 1, Kind: consents.KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, consents.Input{PatientID: 12, MaxUserID: 1, Kind: consents.KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, consents.Input{PatientID: 12, MaxUserID: 1, Kind: consents.KindService, TextVersion: "service-v1", Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	loc := time.FixedZone("MSK", 3*3600)
	row := func(patient int64) repo.DueReminder {
		return repo.DueReminder{
			PlanningID: patient, PatientID: patient, DoctorName: "Иванов Иван", Service: "Прием",
			BranchID: 3, DateConsultation: time.Date(2026, 6, 26, 19, 30, 0, 0, loc),
		}
	}
	reminders := RemindersHandler{Svc: reminderPage{rows: []repo.DueReminder{row(10), row(11), row(12)}}, Consents: s}
	bookings := NotificationsHandler{Svc: bookingPage{rows: []repo.NewBooking{{DueReminder: row(10)}, {DueReminder: row(12)}}}, Consents: s}
	one := NotificationsHandler{Svc: bookingPage{one: repo.BookingNotice{DueReminder: row(12)}}, Consents: s}

	before := s.Reads.Load()
	w := httptest.NewRecorder()
	reminders.Due(w, httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-26T00:00:00&to=2026-06-27T00:00:00", nil))
	if w.Code != http.StatusOK || s.Reads.Load()-before != 1 {
		t.Fatalf("reminders %d queries=%d %s", w.Code, s.Reads.Load()-before, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"patient_id":10`) || !strings.Contains(w.Body.String(), `"bot_consent_service":"active"`) || !strings.Contains(w.Body.String(), `"bot_consent_service":"none"`) || !strings.Contains(w.Body.String(), `"bot_consent_service":"revoked"`) {
		t.Fatalf("body %s", w.Body.String())
	}

	before = s.Reads.Load()
	w = httptest.NewRecorder()
	bookings.NewBookings(w, httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", strings.NewReader(`{"patient_ids":[10,12]}`)))
	if w.Code != http.StatusOK || s.Reads.Load()-before != 1 {
		t.Fatalf("bookings %d queries=%d %s", w.Code, s.Reads.Load()-before, w.Body.String())
	}

	before = s.Reads.Load()
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/booking/12", nil)
	req.SetPathValue("planning_id", "12")
	one.BookingByID(w, req)
	if w.Code != http.StatusOK || s.Reads.Load()-before != 1 || !strings.Contains(w.Body.String(), `"bot_consent_service":"revoked"`) {
		t.Fatalf("booking %d queries=%d %s", w.Code, s.Reads.Load()-before, w.Body.String())
	}
}

type reminderPage struct{ rows []repo.DueReminder }

func (p reminderPage) RemindersDue(context.Context, time.Time, time.Time, []int64) ([]repo.DueReminder, error) {
	return p.rows, nil
}

type bookingPage struct {
	rows []repo.NewBooking
	one  repo.BookingNotice
}

func (p bookingPage) NewBookings(context.Context, time.Time, []int64, *repo.BookingCursor, int) (repo.NewBookingsPage, error) {
	return repo.NewBookingsPage{Rows: p.rows}, nil
}

func (p bookingPage) BookingByPlanningID(context.Context, int64) (repo.BookingNotice, error) {
	return p.one, nil
}

type scriptNames struct {
	called bool
	names  map[int64]string
}

func (s *scriptNames) Names(context.Context, []int64) (map[int64]string, error) {
	s.called = true
	return s.names, nil
}

func TestAdsExport_RegistrarForbiddenAndServiceOmitted(t *testing.T) {
	s := consentDB(t)
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, consents.Input{PatientID: 10, MaxUserID: 1, Kind: consents.KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, consents.Input{PatientID: 11, MaxUserID: 1, Kind: consents.KindAds, TextVersion: "ads-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	names := &scriptNames{names: map[int64]string{11: "Иванов Иван Иванович"}}
	var log bytes.Buffer
	h := AdsExportHandler{
		Store: s, Names: names,
		Logger: slog.New(slog.NewTextHandler(&log, nil)),
		Now:    func() time.Time { return time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC) },
	}
	reg := withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/consents/ads.csv", nil), staff.Principal{ID: 3, Login: "reg", Role: staff.RoleRegistrar})
	rw := httptest.NewRecorder()
	h.AdsCSV(rw, reg)
	if rw.Code != http.StatusForbidden || names.called {
		t.Fatalf("registrar %d called=%v %s", rw.Code, names.called, rw.Body.String())
	}

	mgr := withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/consents/ads.csv", nil), staff.Principal{ID: 2, Login: "boss", Role: staff.RoleManager})
	mw := httptest.NewRecorder()
	h.AdsCSV(mw, mgr)
	body := mw.Body.String()
	if mw.Code != http.StatusOK || !strings.Contains(body, "Иванов Иван Иванович") || !strings.Contains(body, "действует") || strings.Contains(body, "service-v1") || strings.Contains(body, ",10,") {
		t.Fatalf("export %d %s", mw.Code, body)
	}
	if !strings.Contains(body, "11") || strings.Contains(log.String(), "Иванов") || !strings.Contains(log.String(), "actor_login=boss") || !strings.Contains(log.String(), "rows=1") {
		t.Fatalf("log %s", log.String())
	}
}
