package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
)

func TestOTP_PilotRequestExpiresAfterSwitchToProd(t *testing.T) {
	s, lite, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	pilotMode(s)
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Auth.Mode = "prod"
	if _, err := s.OTPVerify(context.Background(), res.RequestID, testPilotCode); !isCode(err, 401, "OTP_EXPIRED") {
		t.Fatalf("verify err=%v", err)
	}
	if n := tokenCount(t, lite); n != 0 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_PilotSelectExpiresAfterSwitchToProd(t *testing.T) {
	s, lite, _ := otpService(t, []otpRow{{id: 11, email: ""}, {id: 12, email: ""}})
	pilotMode(s)
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, testPilotCode)
	if err != nil || len(verified.Candidates) != 2 {
		t.Fatalf("verify err=%v candidates=%d", err, len(verified.Candidates))
	}
	s.Config.Auth.Mode = "prod"
	if _, err := s.OTPSelectPatient(context.Background(), res.RequestID, 11); !isCode(err, 401, "OTP_EXPIRED") {
		t.Fatalf("select err=%v", err)
	}
	if n := tokenCount(t, lite); n != 0 {
		t.Fatalf("tokens=%d", n)
	}
}

func TestOTP_PilotWithoutPilotCodeIssuesNothing(t *testing.T) {
	s, lite, sent, mock := otpServiceMock(t, nil)
	pilotMode(s)
	s.Config.Auth.PilotCode = "000000"
	if _, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1"); !isCode(err, 500, "INTERNAL") {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("medialog queried: %v", err)
	}
	if len(sent) != 0 || otpRowCount(t, lite) != 0 {
		t.Fatalf("mails=%d rows=%d", len(sent), otpRowCount(t, lite))
	}
}

func TestOTPRequest_ProdAnswersAlike(t *testing.T) {
	cases := map[string][]otpRow{
		"known":    {{id: 11, email: "a@example.com"}},
		"unknown":  {},
		"no email": {{id: 11, email: ""}},
	}
	bodies := map[string]string{}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			s, lite, _ := otpService(t, rows)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(`{"phone":"79990000000"}`))
			handler.AuthHandler{Svc: s, Logger: s.Logger}.OTPRequest(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d", w.Code)
			}
			var env struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			var id string
			if err := json.Unmarshal(env.Data["request_id"], &id); err != nil || id == "" {
				t.Fatalf("request_id err=%v", err)
			}
			for _, field := range []string{"masked_destination", "dev_code", "channel"} {
				if _, ok := env.Data[field]; ok {
					t.Fatalf("response has %s", field)
				}
			}
			var msg string
			_ = json.Unmarshal(env.Data["message"], &msg)
			if msg != "Если номер зарегистрирован в клинике, код отправлен на e-mail из карточки" {
				t.Fatalf("message=%q", msg)
			}
			bodies[name] = strings.Replace(w.Body.String(), id, "REQUEST_ID", 1)

			if name != "known" {
				if _, err := s.OTPVerify(context.Background(), id, "123456"); !isCode(err, 401, "OTP_INVALID") {
					t.Fatalf("verify err=%v", err)
				}
				row, err := otp.NewRepo(lite).Get(context.Background(), id)
				if err != nil || row.Attempts != 1 || tokenCount(t, lite) != 0 {
					t.Fatalf("attempts=%d tokens=%d err=%v", row.Attempts, tokenCount(t, lite), err)
				}
			}
		})
	}
	if bodies["known"] == "" || bodies["known"] != bodies["unknown"] || bodies["known"] != bodies["no email"] {
		t.Fatalf("bodies differ:\n%s\n%s\n%s", bodies["known"], bodies["unknown"], bodies["no email"])
	}
}

func TestOTP_SMTPErrorLogHasNoAddress(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "patient@private.example"}})
	var out strings.Builder
	s.Logger = slog.New(slog.NewTextHandler(&out, nil))
	sendOTPMail = func(_ context.Context, _ *Services, email, _ string) error {
		return fmt.Errorf("smtp rcpt: %w", &textproto.Error{Code: 550, Msg: "5.1.1 <" + email + "> mailbox unavailable"})
	}

	_, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if !isCode(err, 502, "EMAIL_SEND_FAILED") {
		t.Fatalf("err=%v", err)
	}
	log := out.String()
	if strings.Contains(log, "@") || strings.Contains(log, "private.example") || strings.Contains(log, "mailbox") {
		t.Fatalf("log repeats the SMTP text:\n%s", log)
	}
	for _, want := range []string{"stage=smtp_send", "code=rejected", "request_id="} {
		if !strings.Contains(log, want) {
			t.Fatalf("log has no %s:\n%s", want, log)
		}
	}
}

func TestOTP_LastAttemptIsComparedOnce(t *testing.T) {
	s, lite, _ := otpService(t, nil)
	lite.SetMaxOpenConns(1)
	hash := otp.HashCodeHMAC("123456", "test-secret")
	err := otp.NewRepo(lite).Create(context.Background(), otp.RequestRow{
		RequestID:      "last",
		Phone:          "79990000000",
		CodeHash:       `["` + hash + `"]`,
		CandidatesJSON: `[{"code_hash":"` + hash + `","candidates":[{"patient_id":11}]}]`,
		Attempts:       2,
		ExpiresAt:      time.Now().Add(5 * time.Minute),
		Format:         otp.FormatAddresses,
		IssuedMode:     "prod",
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	compared, exceeded := 0, 0
	var other []error
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.OTPVerify(context.Background(), "last", fmt.Sprintf("%06d", 900000+i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case isCode(err, 401, "OTP_INVALID"):
				compared++
			case isCode(err, 401, "OTP_ATTEMPTS_EXCEEDED"):
				exceeded++
			default:
				other = append(other, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	row, err := otp.NewRepo(lite).Get(context.Background(), "last")
	if err != nil {
		t.Fatal(err)
	}
	if compared != 1 || exceeded != 7 || len(other) != 0 || row.Attempts != 3 || tokenCount(t, lite) != 0 {
		t.Fatalf("compared=%d exceeded=%d other=%v attempts=%d", compared, exceeded, other, row.Attempts)
	}
}

func TestOTP_RightCodeSpendsAnAttempt(t *testing.T) {
	s, lite, sent := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	res, err := s.OTPRequest(context.Background(), "79990000000", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OTPVerify(context.Background(), res.RequestID, sent["a@example.com"]); err != nil {
		t.Fatal(err)
	}
	row, err := otp.NewRepo(lite).Get(context.Background(), res.RequestID)
	if err != nil || row.Attempts != 1 {
		t.Fatalf("attempts=%d err=%v", row.Attempts, err)
	}
}
