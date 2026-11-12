package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestOTP_CodeOpensOnlyItsAddress(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "a@example.com"},
		{id: 22, email: "b@example.com"},
		{id: 33, email: ""},
	})
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if sent["a@example.com"] == "" || sent["b@example.com"] == "" || sent["a@example.com"] == sent["b@example.com"] {
		t.Fatal("each address needs its own code")
	}
	if _, ok := sent[""]; ok {
		t.Fatal("empty address was mailed")
	}

	verified, err := s.OTPVerify(context.Background(), res.RequestID, sent["a@example.com"])
	if err != nil {
		t.Fatal(err)
	}
	if verified.Access == "" || tokenPatient(t, lite) != 11 {
		t.Fatalf("token patient=%d access empty=%v", tokenPatient(t, lite), verified.Access == "")
	}
	if _, err := s.OTPSelectPatient(context.Background(), res.RequestID, 22); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("other profile err=%v", err)
	}
	if _, err := s.OTPSelectPatient(context.Background(), res.RequestID, 11); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("second select err=%v", err)
	}
	if n := tokenCount(t, lite); n != 1 || tokenPatient(t, lite) != 11 {
		t.Fatalf("tokens=%d patient=%d", n, tokenPatient(t, lite))
	}
}

func TestOTP_SharedMailboxSelectsOnce(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "family@example.com"},
		{id: 22, email: "family@example.com"},
	})
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("mails=%d", len(sent))
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, sent["family@example.com"])
	if err != nil {
		t.Fatal(err)
	}
	if verified.Access != "" || len(verified.Candidates) != 2 {
		t.Fatalf("access empty=%v candidates=%d", verified.Access == "", len(verified.Candidates))
	}
	ids := map[int64]bool{}
	for _, c := range verified.Candidates {
		ids[c.PatientID] = true
	}
	if !ids[11] || !ids[22] {
		t.Fatalf("candidates=%v", ids)
	}
	if tokenCount(t, lite) != 0 {
		t.Fatal("token issued before the choice")
	}
	tok, err := s.OTPSelectPatient(context.Background(), res.RequestID, 22)
	if err != nil || tok.Access == "" || tokenPatient(t, lite) != 22 {
		t.Fatalf("select err=%v patient=%d", err, tokenPatient(t, lite))
	}
	if _, err := s.OTPSelectPatient(context.Background(), res.RequestID, 11); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("second select err=%v", err)
	}
	if tokenCount(t, lite) != 1 {
		t.Fatalf("tokens=%d", tokenCount(t, lite))
	}
}

func TestOTP_PilotWhitelistKeepsTheFixedCode(t *testing.T) {
	s, _, sent := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	s.Config.Auth.Mode = "pilot"
	s.Config.Auth.PilotWhitelist = []string{"79990000000"}
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 || res.DevCode != "000000" {
		t.Fatalf("sent=%d code set=%v", len(sent), res.DevCode == "000000")
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, "000000")
	if err != nil || verified.Access == "" {
		t.Fatalf("verify err=%v", err)
	}
}

type otpRow struct {
	id    int64
	email string
}

func otpService(t *testing.T, rows []otpRow) (*Services, *sql.DB, map[string]string) {
	t.Helper()
	mssql, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
		func(_, _ string) error { return nil },
	)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mssql.Close() })
	rr := sqlmock.NewRows([]string{"PATIENTS_ID", "FULL_NAME", "EMAIL", "NOT_SEND_MAILING_SMS", "SEND_AUTO_EMAIL"})
	for _, row := range rows {
		rr.AddRow(row.id, "Profile", row.email, 0, 0)
	}
	mock.ExpectQuery("PATIENTS").WillReturnRows(rr)

	lite, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lite.Close() })
	if err := store.Migrate(context.Background(), lite); err != nil {
		t.Fatal(err)
	}
	sent := map[string]string{}
	prev := sendOTPMail
	sendOTPMail = func(_ context.Context, _ *Services, email, code string) error {
		sent[email] = code
		return nil
	}
	t.Cleanup(func() { sendOTPMail = prev })
	s := &Services{
		MSSQL:  mssql,
		SQLite: lite,
		Repos:  repo.New(mssql),
		Logger: slog.New(slog.DiscardHandler),
		Config: config.Config{
			Auth: config.AuthConfig{Mode: "prod"},
			OTP:  config.OTPConfig{HMACSecret: "test-secret", TTLSeconds: 300},
			JWT:  config.JWTConfig{Secret: "jwt-secret", Issuer: "test", AccessTTLMin: 15, RefreshTTLDays: 30},
		},
	}
	return s, lite, sent
}

func tokenPatient(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var pid int64
	if err := db.QueryRow(`SELECT patient_id FROM refresh_tokens`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}

func tokenCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refresh_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func isCode(err error, status int, code string) bool {
	var ae *apperr.AppError
	return errors.As(err, &ae) && ae.Status == status && ae.Code == code
}
