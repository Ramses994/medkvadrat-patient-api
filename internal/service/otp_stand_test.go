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
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
)

func TestOTP_PilotRequestExpiresAfterSwitchToProd(t *testing.T) {
	s, lite, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	pilotMode(s)
	res, err := requestOTP(s)
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
	res, err := requestOTP(s)
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
	if _, err := requestOTP(s); !isCode(err, 500, "INTERNAL") {
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
	type otpCase struct {
		name string
		rows []otpRow
	}
	cases := []otpCase{
		{"known", []otpRow{{id: 11, email: "a@example.com"}}},
		{"unknown", []otpRow{}},
		{"no email", []otpRow{{id: 11, email: ""}}},
	}
	outcomes := []string{"sent", "slow", "failed"}
	var first string
	for _, outcome := range outcomes {
		for _, c := range cases {
			t.Run(outcome+"/"+c.name, func(t *testing.T) {
				s, lite, _ := otpService(t, c.rows)
				var finished atomic.Bool
				sendOTPMail = func(context.Context, *Services, string, string) error {
					defer finished.Store(true)
					switch outcome {
					case "slow":
						time.Sleep(2 * time.Second)
					case "failed":
						return fmt.Errorf("smtp rcpt: %w", &textproto.Error{Code: 550, Msg: "rejected"})
					}
					return nil
				}

				start := time.Now()
				status, body, id := postOTPRequest(t, s)
				elapsed := time.Since(start)
				if status != http.StatusOK {
					t.Fatalf("status=%d", status)
				}
				if outcome == "slow" && (elapsed > time.Second || finished.Load()) {
					t.Fatalf("response waited for the letter: %v", elapsed)
				}
				var env struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal([]byte(body), &env); err != nil {
					t.Fatal(err)
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
				if first == "" {
					first = body
				}
				if body != first {
					t.Fatalf("body differs:\n%s\n%s", first, body)
				}

				if c.name != "known" {
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
	}
}

func TestOTP_PilotWhitelistAnswersAlike(t *testing.T) {
	cases := map[string][]otpRow{
		"one address":       {{id: 11, email: "a@example.com"}},
		"no card":           {},
		"several addresses": {{id: 11, email: "a@example.com"}, {id: 22, email: "b@example.com"}},
	}
	bodies := map[string]string{}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			s, lite, sent := otpService(t, rows)
			pilotMode(s)
			status, body, id := postOTPRequest(t, s)
			if status != http.StatusOK {
				t.Fatalf("status=%d", status)
			}
			waitOTPMail(s)
			if len(sent) != 0 {
				t.Fatalf("mails=%d", len(sent))
			}
			bodies[name] = body

			_, err := s.OTPVerify(context.Background(), id, testPilotCode)
			if name == "one address" {
				if err != nil || tokenCount(t, lite) != 1 {
					t.Fatalf("verify err=%v", err)
				}
				return
			}
			if !isCode(err, 401, "OTP_INVALID") || tokenCount(t, lite) != 0 {
				t.Fatalf("verify err=%v tokens=%d", err, tokenCount(t, lite))
			}
		})
	}
	if bodies["one address"] == "" || bodies["one address"] != bodies["no card"] || bodies["one address"] != bodies["several addresses"] {
		t.Fatalf("bodies differ:\n%s\n%s\n%s", bodies["one address"], bodies["no card"], bodies["several addresses"])
	}
}

// postOTPRequest calls the HTTP handler and returns the body with request_id replaced.
func postOTPRequest(t *testing.T, s *Services) (int, string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(`{"phone":"79990000000"}`))
	handler.AuthHandler{Svc: s, Logger: s.Logger}.OTPRequest(w, req)
	var env struct {
		Data struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.RequestID == "" {
		t.Fatalf("status=%d request_id missing: %v", w.Code, err)
	}
	return w.Code, strings.Replace(w.Body.String(), env.Data.RequestID, "REQUEST_ID", 1), env.Data.RequestID
}

func TestOTP_SMTPErrorLogHasNoAddress(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "patient@private.example"}})
	var out strings.Builder
	s.Logger = slog.New(slog.NewTextHandler(&out, nil))
	sendOTPMail = func(_ context.Context, _ *Services, email, _ string) error {
		return fmt.Errorf("smtp rcpt: %w", &textproto.Error{Code: 550, Msg: "5.1.1 <" + email + "> mailbox unavailable"})
	}

	if _, err := requestOTP(s); err != nil {
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
	res, err := requestOTP(s)
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
