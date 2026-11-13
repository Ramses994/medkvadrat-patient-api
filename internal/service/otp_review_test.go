package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
)

func TestOTP_OldFormatPendingIsRefused(t *testing.T) {
	s, lite, _ := otpService(t, nil)
	cands := `[{"patient_id":11,"full_name":"Profile","masked_email":"a***@example.com"}]`
	insertOldOTPRow(t, lite, "old-pending", otp.HashCodeHMAC("123456", "test-secret"), cands, false, 0)

	_, err := s.OTPVerify(context.Background(), "old-pending", "123456")
	if !isCode(err, 401, "OTP_EXPIRED") || errMessage(err) != "Код устарел, запросите новый" {
		t.Fatalf("verify err=%v", err)
	}
	if n := tokenCount(t, lite); n != 0 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_OldFormatSelectedIsRefused(t *testing.T) {
	s, lite, _ := otpService(t, nil)
	cands := `[{"patient_id":11,"full_name":"Profile"},{"patient_id":22,"full_name":"Profile"}]`
	insertOldOTPRow(t, lite, "old-selected", otp.HashCodeHMAC("123456", "test-secret"), cands, true, 11)

	for _, id := range []int64{11, 22} {
		if _, err := s.OTPSelectPatient(context.Background(), "old-selected", id); !isCode(err, 401, "OTP_EXPIRED") {
			t.Fatalf("select %d err=%v", id, err)
		}
	}
	if _, err := s.OTPVerify(context.Background(), "old-selected", "123456"); !isCode(err, 401, "OTP_EXPIRED") {
		t.Fatalf("verify err=%v", err)
	}
	if n := tokenCount(t, lite); n != 0 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_EmailCaseBeforeAtIsAnotherAddress(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "Ivan@Mail.ru"},
		{id: 22, email: "ivan@mail.ru"},
	})
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent["Ivan@Mail.ru"] == "" || sent["ivan@mail.ru"] == "" || sent["Ivan@Mail.ru"] == sent["ivan@mail.ru"] {
		t.Fatalf("mails=%d, each address needs its own code", len(sent))
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, sent["Ivan@Mail.ru"])
	if err != nil || verified.Access == "" || tokenPatient(t, lite) != 11 {
		t.Fatalf("verify err=%v", err)
	}
	if _, err := s.OTPSelectPatient(context.Background(), res.RequestID, 22); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("other address err=%v", err)
	}
	if n := tokenCount(t, lite); n != 1 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_EmailDomainCaseIsSameAddress(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "ivan@Mail.ru"},
		{id: 22, email: "ivan@mail.ru"},
	})
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("mails=%d", len(sent))
	}
	var code string
	for _, c := range sent {
		code = c
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, code)
	if err != nil || verified.Access != "" || len(verified.Candidates) != 2 || tokenCount(t, lite) != 0 {
		t.Fatalf("verify err=%v candidates=%d", err, len(verified.Candidates))
	}
}

func TestOTP_CodesInOneRequestDiffer(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "a@example.com"},
		{id: 22, email: "b@example.com"},
		{id: 33, email: "c@example.com"},
	})
	seq := []string{"111111", "111111", "222222", "111111", "222222", "333333"}
	prev := newOTPCode
	newOTPCode = func() (string, error) {
		c := seq[0]
		seq = seq[1:]
		return c, nil
	}
	t.Cleanup(func() { newOTPCode = prev })

	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if sent["a@example.com"] != "111111" || sent["b@example.com"] != "222222" || sent["c@example.com"] != "333333" {
		t.Fatal("codes in one request must differ")
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, "222222")
	if err != nil || verified.Access == "" || tokenPatient(t, lite) != 22 {
		t.Fatalf("verify err=%v", err)
	}
}

func TestOTP_AmbiguousCodeIsRefused(t *testing.T) {
	s, lite, _ := otpService(t, nil)
	hash := otp.HashCodeHMAC("123456", "test-secret")
	groups := []otp.AddressGroup{
		{CodeHash: hash, Candidates: []otp.CandidatesItem{{PatientID: 11, FullName: "Profile"}}},
		{CodeHash: hash, Candidates: []otp.CandidatesItem{{PatientID: 22, FullName: "Profile"}}},
	}
	cands, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	err = otp.NewRepo(lite).Create(context.Background(), otp.RequestRow{
		RequestID:      "twin",
		Phone:          "79990000000",
		CodeHash:       `["` + hash + `","` + hash + `"]`,
		CandidatesJSON: string(cands),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
		Format:         otp.FormatAddresses,
		IssuedMode:     "prod",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.OTPVerify(context.Background(), "twin", "123456"); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("verify err=%v", err)
	}
	row, err := otp.NewRepo(lite).Get(context.Background(), "twin")
	if err != nil {
		t.Fatal(err)
	}
	if row.Attempts != 1 || row.VerifiedAt.Valid || tokenCount(t, lite) != 0 {
		t.Fatalf("attempts=%d verified=%v tokens=%d", row.Attempts, row.VerifiedAt.Valid, tokenCount(t, lite))
	}
}

func TestOTP_PilotOutsideWhitelistIsRefused(t *testing.T) {
	s, lite, sent, mock := otpServiceMock(t, nil)
	pilotMode(s)
	s.Config.Auth.PilotWhitelist = []string{"79991111111"}

	res, err := requestOTP(s)
	if !isCode(err, 403, "PILOT_ONLY") || errMessage(err) != "Личный кабинет пока недоступен" {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("medialog queried: %v", err)
	}
	if len(sent) != 0 || res.DevCode != "" || res.RequestID != "" || otpRowCount(t, lite) != 0 {
		t.Fatalf("mails=%d code in response=%v rows=%d", len(sent), res.DevCode != "", otpRowCount(t, lite))
	}
}

func TestOTP_PilotSeveralAddressesGetNoCode(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "a@example.com"},
		{id: 22, email: "b@example.com"},
	})
	pilotMode(s)

	res, err := requestOTP(s)
	if err != nil || res.RequestID == "" || res.DevCode != "" || len(sent) != 0 {
		t.Fatalf("err=%v mails=%d code in response=%v", err, len(sent), res.DevCode != "")
	}
	if _, err := s.OTPVerify(context.Background(), res.RequestID, testPilotCode); !isCode(err, 401, "OTP_INVALID") {
		t.Fatalf("verify err=%v", err)
	}
	if n := tokenCount(t, lite); n != 0 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_ProdHidesTheCode(t *testing.T) {
	s, _, sent := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if res.DevCode != "" || sent["a@example.com"] == "" {
		t.Fatalf("code in response=%v mailed=%v", res.DevCode != "", sent["a@example.com"] != "")
	}
}

func TestOTP_DevShowsTheCode(t *testing.T) {
	s, _, sent := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	s.Config.Auth.Mode = "dev"
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if res.DevCode == "" || res.DevCode != sent["a@example.com"] {
		t.Fatal("dev mode should return the code")
	}
}

func insertOldOTPRow(t *testing.T, db *sql.DB, id, hash, cands string, verified bool, selected int64) {
	t.Helper()
	var ver, sel any
	if verified {
		ver = time.Now().UTC()
	}
	if selected != 0 {
		sel = selected
	}
	_, err := db.Exec(`
INSERT INTO otp_requests(request_id, phone, code_hash, candidates, attempts, expires_at, verified_at, selected_patient)
VALUES(?, ?, ?, ?, 0, ?, ?, ?)`,
		id, "79990000000", hash, cands, time.Now().Add(5*time.Minute).UTC(), ver, sel)
	if err != nil {
		t.Fatal(err)
	}
}

func otpRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM otp_requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func errMessage(err error) string {
	var ae *apperr.AppError
	if errors.As(err, &ae) {
		return ae.Message
	}
	return ""
}
