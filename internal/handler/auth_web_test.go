package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAuth struct {
	calls []string
}

var fixedTokens = Tokens{Access: "acc", Refresh: "ref", AccessExpiresIn: 900, RefreshExpiresIn: 2592000}

func (f *fakeAuth) OTPRequest(context.Context, string, string) (OTPRequestResult, error) {
	return OTPRequestResult{}, nil
}
func (f *fakeAuth) OTPVerify(context.Context, string, string) (OTPVerifyResult, error) {
	f.calls = append(f.calls, "verify")
	return OTPVerifyResult{Access: "acc", Refresh: "ref", AccessExpiresIn: 900, RefreshExpiresIn: 2592000}, nil
}
func (f *fakeAuth) OTPSelectPatient(context.Context, string, int64) (Tokens, error) {
	f.calls = append(f.calls, "select")
	return fixedTokens, nil
}
func (f *fakeAuth) Refresh(_ context.Context, refresh string) (Tokens, error) {
	f.calls = append(f.calls, "refresh:"+refresh)
	return fixedTokens, nil
}
func (f *fakeAuth) Logout(_ context.Context, refresh string) error {
	f.calls = append(f.calls, "logout:"+refresh)
	return nil
}
func (f *fakeAuth) WebOTPVerify(context.Context, string, string) (OTPVerifyResult, error) {
	f.calls = append(f.calls, "web-verify")
	return OTPVerifyResult{Access: "acc", Refresh: "webref", AccessExpiresIn: 900, RefreshExpiresIn: 43200}, nil
}
func (f *fakeAuth) WebOTPSelectPatient(context.Context, string, int64) (Tokens, error) {
	f.calls = append(f.calls, "web-select")
	return Tokens{Access: "acc", Refresh: "webref", AccessExpiresIn: 900, RefreshExpiresIn: 43200}, nil
}
func (f *fakeAuth) WebRefresh(_ context.Context, refresh string) (Tokens, error) {
	f.calls = append(f.calls, "web-refresh:"+refresh)
	return Tokens{Access: "acc", Refresh: "webref", AccessExpiresIn: 900, RefreshExpiresIn: 43200}, nil
}

// TestAuth_WithoutWebHeaderBytesUnchanged pins the mobile contract: same status, headers and body bytes
// as before the web session, with or without an unrelated X-LK-Client value.
func TestAuth_WithoutWebHeaderBytesUnchanged(t *testing.T) {
	tokens := `{"success":true,"data":{"access":"acc","refresh":"ref","access_expires_in":900,"refresh_expires_in":2592000}}` + "\n"
	cases := []struct {
		name, path, body string
		serve            func(AuthHandler) http.HandlerFunc
		code             int
		want             string
		call             string
	}{
		{"verify", "/api/auth/otp/verify", `{"request_id":"r","code":"1"}`, func(h AuthHandler) http.HandlerFunc { return h.OTPVerify }, 200, tokens, "verify"},
		{"select", "/api/auth/otp/select-patient", `{"request_id":"r","patient_id":5}`, func(h AuthHandler) http.HandlerFunc { return h.OTPSelectPatient }, 200, tokens, "select"},
		{"refresh", "/api/auth/refresh", `{"refresh":"old"}`, func(h AuthHandler) http.HandlerFunc { return h.Refresh }, 200, tokens, "refresh:old"},
		{"refresh missing", "/api/auth/refresh", `{}`, func(h AuthHandler) http.HandlerFunc { return h.Refresh }, 400,
			`{"success":false,"error":"refresh обязателен","error_details":{"code":"VALIDATION","message":"refresh обязателен"}}` + "\n", ""},
		{"logout", "/api/auth/logout", `{"refresh":"old"}`, func(h AuthHandler) http.HandlerFunc { return h.Logout }, 204, "", "logout:old"},
		{"logout missing", "/api/auth/logout", `{}`, func(h AuthHandler) http.HandlerFunc { return h.Logout }, 400,
			`{"success":false,"error":"refresh обязателен","error_details":{"code":"VALIDATION","message":"refresh обязателен"}}` + "\n", ""},
	}
	for _, header := range []string{"", "mobile"} {
		for _, tc := range cases {
			f := &fakeAuth{}
			h := AuthHandler{Svc: f, CookieSecure: true}
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.AddCookie(&http.Cookie{Name: RefreshCookie, Value: "cookie-token"})
			if header != "" {
				req.Header.Set(LKClientHeader, header)
			}
			w := httptest.NewRecorder()
			tc.serve(h)(w, req)
			if w.Code != tc.code || w.Body.String() != tc.want {
				t.Fatalf("%s header=%q: %d %q", tc.name, header, w.Code, w.Body.String())
			}
			for k := range w.Header() {
				if k != "Content-Type" {
					t.Fatalf("%s header=%q: extra header %s", tc.name, header, k)
				}
			}
			if tc.code != 204 && w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("%s: content-type %q", tc.name, w.Header().Get("Content-Type"))
			}
			if got := strings.Join(f.calls, ","); got != tc.call {
				t.Fatalf("%s header=%q: calls %q", tc.name, header, got)
			}
		}
	}
}

func TestAuth_WebHeaderUsesCookie(t *testing.T) {
	f := &fakeAuth{}
	h := AuthHandler{Svc: f}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{"refresh":"body-token"}`))
	req.Header.Set(LKClientHeader, "Web")
	req.AddCookie(&http.Cookie{Name: RefreshCookie, Value: "cookie-token"})
	w := httptest.NewRecorder()
	h.Refresh(w, req)
	if w.Code != 200 || w.Body.String() != `{"success":true,"data":{"access":"acc","access_expires_in":900}}`+"\n" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if got := strings.Join(f.calls, ","); got != "web-refresh:cookie-token" {
		t.Fatalf("calls %q", got)
	}
	sc := w.Header().Get("Set-Cookie")
	for _, part := range []string{"lk_refresh=webref", "Path=/api/auth", "Max-Age=43200", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(sc, part) {
			t.Fatalf("set-cookie %q lacks %q", sc, part)
		}
	}
	if strings.Contains(sc, "Secure") {
		t.Fatalf("secure without LK_COOKIE_SECURE: %q", sc)
	}
}
