package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

func TestOTPMail_QueueFullOnlyLogs(t *testing.T) {
	s, _, _ := otpService(t, nil)
	var out strings.Builder
	s.Logger = slog.New(slog.NewTextHandler(&out, nil))
	release := make(chan struct{})
	sendOTPMail = func(context.Context, *Services, string, string) error {
		<-release
		return nil
	}
	for i := 0; i < otpMailQueueSize+otpMailWorkers+1; i++ {
		s.enqueueOTPMail(otpMailJob{requestID: "req", email: "a@example.com", code: "123456", expiresAt: time.Now().Add(time.Hour)})
	}
	close(release)
	waitOTPMail(s)
	log := out.String()
	if !strings.Contains(log, "code=queue_full") || !strings.Contains(log, "request_id=req") || strings.Contains(log, "@") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestOTPMail_StopDrainsQueue(t *testing.T) {
	s, _, _ := otpService(t, nil)
	var sentN atomic.Int32
	sendOTPMail = func(context.Context, *Services, string, string) error {
		time.Sleep(20 * time.Millisecond)
		sentN.Add(1)
		return nil
	}
	for i := 0; i < 6; i++ {
		s.enqueueOTPMail(otpMailJob{requestID: "req", email: "a@example.com", code: "123456", expiresAt: time.Now().Add(time.Hour)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.StopOTPMail(ctx); err != nil || sentN.Load() != 6 {
		t.Fatalf("err=%v sent=%d", err, sentN.Load())
	}
}

func TestOTPMail_StopGivesUpAtShutdownTimeout(t *testing.T) {
	s, _, _ := otpService(t, nil)
	release := make(chan struct{})
	sendOTPMail = func(context.Context, *Services, string, string) error {
		<-release
		return nil
	}
	s.enqueueOTPMail(otpMailJob{requestID: "req", email: "a@example.com", code: "123456", expiresAt: time.Now().Add(time.Hour)})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.StopOTPMail(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	close(release)
}

func TestOTPMail_ExpiredRequestIsNotSent(t *testing.T) {
	s, _, _ := otpService(t, nil)
	var out strings.Builder
	s.Logger = slog.New(slog.NewTextHandler(&out, nil))
	release := make(chan struct{})
	var mu sync.Mutex
	var sent []string
	sendOTPMail = func(_ context.Context, _ *Services, _ string, code string) error {
		<-release
		mu.Lock()
		sent = append(sent, code)
		mu.Unlock()
		return nil
	}
	start := time.Now()
	var offset atomic.Int64
	otpMailNow = func() time.Time { return start.Add(time.Duration(offset.Load())) }
	t.Cleanup(func() { otpMailNow = time.Now })

	// Both workers wait on the first two letters; the third waits in the queue past its expiry.
	s.enqueueOTPMail(otpMailJob{requestID: "busy-1", email: "a@example.com", code: "111111", expiresAt: start.Add(time.Minute)})
	s.enqueueOTPMail(otpMailJob{requestID: "busy-2", email: "b@example.com", code: "222222", expiresAt: start.Add(time.Minute)})
	s.enqueueOTPMail(otpMailJob{requestID: "late", email: "c@example.com", code: "333333", expiresAt: start.Add(time.Second)})
	offset.Store(int64(2 * time.Second))
	close(release)
	waitOTPMail(s)

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || strings.Contains(strings.Join(sent, ","), "333333") {
		t.Fatalf("sent=%v", sent)
	}
	log := out.String()
	if !strings.Contains(log, "otp email skipped") || !strings.Contains(log, "reason=expired") || !strings.Contains(log, "request_id=late") {
		t.Fatalf("log:\n%s", log)
	}
	if strings.Contains(log, "@") || strings.Contains(log, "333333") {
		t.Fatalf("address or code in log:\n%s", log)
	}
}

func TestOTPMail_RequestExpiresAtCodeTTL(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	sentN := 0
	sendOTPMail = func(context.Context, *Services, string, string) error {
		sentN++
		return nil
	}
	otpMailNow = func() time.Time { return time.Now().Add(301 * time.Second) }
	t.Cleanup(func() { otpMailNow = time.Now })
	if _, err := requestOTP(s); err != nil {
		t.Fatal(err)
	}
	if sentN != 0 {
		t.Fatalf("letter sent after the code TTL: %d", sentN)
	}
}

func TestOTP_SessionEndsWhenModeChanges(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	pilotMode(s)
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.OTPVerify(context.Background(), res.RequestID, testPilotCode)
	if err != nil || tok.Access == "" || tok.Refresh == "" {
		t.Fatalf("login err=%v", err)
	}

	if code := patientStatus(s, "prod", tok.Access); code != http.StatusUnauthorized {
		t.Fatalf("access in prod: %d", code)
	}
	s.Config.Auth.Mode = "prod"
	if _, err := s.Refresh(context.Background(), tok.Refresh); !isCode(err, 401, "UNAUTHORIZED") {
		t.Fatalf("refresh in prod err=%v", err)
	}

	s.Config.Auth.Mode = "pilot"
	if code := patientStatus(s, "pilot", tok.Access); code != http.StatusOK {
		t.Fatalf("access in pilot: %d", code)
	}
	if _, err := s.Refresh(context.Background(), tok.Refresh); err != nil {
		t.Fatalf("refresh in pilot err=%v", err)
	}
}

func TestOTP_TokensWithoutModeAreRefused(t *testing.T) {
	s, lite, _ := otpService(t, nil)
	secret := []byte(s.Config.JWT.Secret)
	now := time.Now()
	access, err := auth.IssueAccess(secret, "test", 11, "", "", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if code := patientStatus(s, "prod", access); code != http.StatusUnauthorized {
		t.Fatalf("access without mode: %d", code)
	}
	refresh, err := auth.IssueRefresh(secret, "test", 11, "legacy", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lite.Exec(`INSERT INTO refresh_tokens(jti, patient_id, token_hash, expires_at) VALUES(?, ?, ?, ?)`,
		"legacy", 11, auth.HashToken(refresh), now.Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Refresh(context.Background(), refresh); !errors.Is(err, apperr.ErrUnauthorized) {
		t.Fatalf("refresh without mode err=%v", err)
	}
}

func patientStatus(s *Services, mode, access string) int {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	middleware.RequirePatient{JWTSecret: []byte(s.Config.JWT.Secret), Mode: mode}.Wrap(ok).ServeHTTP(w, req)
	return w.Code
}
