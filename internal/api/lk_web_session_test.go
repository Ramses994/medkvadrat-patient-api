package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/otp"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

const (
	webHost   = "cabinet.test:8083"
	webOrigin = "http://cabinet.test:8083"
	webCode   = "246813"
)

type webEnv struct {
	h    http.Handler
	lite *sql.DB
	cfg  config.Config
}

func newWebEnv(t *testing.T) webEnv {
	t.Helper()
	lite, err := db.OpenSQLite(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lite.Close() })
	if err := store.Migrate(context.Background(), lite); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		APIToken: "s2s-token",
		Auth: config.AuthConfig{
			Mode: "prod", CookieSecure: true,
			WebRefreshTTL: 12 * time.Hour, WebIdleMin: 20 * time.Minute,
			WebOrigins: []string{webOrigin},
		},
		OTP: config.OTPConfig{HMACSecret: "otp-secret", TTLSeconds: 300},
		JWT: config.JWTConfig{Secret: "jwt-secret", Issuer: "test", AccessTTLMin: 15, RefreshTTLDays: 30},
	}
	svc := service.New(nil, nil, lite, repo.Repos{}, slog.New(slog.DiscardHandler), 0, cfg)
	return webEnv{h: NewRouter(cfg, svc, slog.New(slog.DiscardHandler)), lite: lite, cfg: cfg}
}

// otpRequest stores a code request for one mailbox, as OTPRequest does after the Medialog search.
func (e webEnv) otpRequest(t *testing.T, ids ...int64) string {
	t.Helper()
	items := make([]otp.CandidatesItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, otp.CandidatesItem{PatientID: id, FullName: "Profile", MaskedEmail: "a***@example.com"})
	}
	hash := otp.HashCodeHMAC(webCode, e.cfg.OTP.HMACSecret)
	groups, _ := json.Marshal([]otp.AddressGroup{{CodeHash: hash, Candidates: items}})
	hashes, _ := json.Marshal([]string{hash})
	rid, err := auth.NewJTI()
	if err != nil {
		t.Fatal(err)
	}
	if err := otp.NewRepo(e.lite).Create(context.Background(), otp.RequestRow{
		RequestID:      rid,
		Phone:          "79990000000",
		CodeHash:       string(hashes),
		CandidatesJSON: string(groups),
		ExpiresAt:      time.Now().Add(5 * time.Minute),
		Format:         otp.FormatAddresses,
		IssuedMode:     "prod",
	}); err != nil {
		t.Fatal(err)
	}
	return rid
}

type webReq struct {
	method, path, body string
	web                bool
	origin             string
	cookie             string
}

func (e webEnv) do(r webReq) *httptest.ResponseRecorder {
	req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	req.Host = webHost
	if r.web {
		req.Header.Set("X-LK-Client", "web")
	}
	if r.origin != "" {
		req.Header.Set("Origin", r.origin)
	}
	if r.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "lk_refresh", Value: r.cookie})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

func refreshCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "lk_refresh" {
			if found != nil {
				t.Fatal("two lk_refresh cookies")
			}
			found = c
		}
	}
	return found
}

func dataOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return env.Data
}

func webLogin(t *testing.T, e webEnv) (*httptest.ResponseRecorder, *http.Cookie) {
	t.Helper()
	rid := e.otpRequest(t, 11)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", web: true, origin: webOrigin,
		body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	if w.Code != http.StatusOK {
		t.Fatalf("verify %d %s", w.Code, w.Body.String())
	}
	c := refreshCookie(t, w)
	if c == nil || c.Value == "" {
		t.Fatal("no lk_refresh cookie")
	}
	return w, c
}

func TestWebSession_VerifySetsHttpOnlyCookieAndHidesRefresh(t *testing.T) {
	e := newWebEnv(t)
	w, c := webLogin(t, e)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/auth" || c.MaxAge != 0 {
		t.Fatalf("cookie attrs: httponly=%v secure=%v samesite=%v path=%q maxage=%d", c.HttpOnly, c.Secure, c.SameSite, c.Path, c.MaxAge)
	}
	if sc := w.Header().Get("Set-Cookie"); strings.Contains(sc, "Max-Age=") || strings.Contains(strings.ToLower(sc), "expires=") {
		t.Fatalf("session cookie must not set expiry: %q", sc)
	}
	data := dataOf(t, w)
	if data["access"] == "" || data["access"] == nil {
		t.Fatal("no access token in body")
	}
	if sess, _ := data["session"].(string); sess == "" {
		t.Fatal("missing opaque session id")
	}
	if _, ok := data["refresh"]; ok {
		t.Fatal("refresh in body")
	}
	if _, ok := data["refresh_expires_in"]; ok {
		t.Fatal("refresh_expires_in in body")
	}
	if strings.Contains(w.Body.String(), c.Value) {
		t.Fatal("refresh token value in body")
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control %q", got)
	}
}

func TestWebSession_AbsoluteAndIdleTTL(t *testing.T) {
	e := newWebEnv(t)
	start := time.Now()
	_, c := webLogin(t, e)
	var idleExp, sessionExp time.Time
	var client, sid string
	if err := e.lite.QueryRow(`SELECT expires_at, session_expires_at, client, sid FROM refresh_tokens`).Scan(&idleExp, &sessionExp, &client, &sid); err != nil {
		t.Fatal(err)
	}
	if client != "web" || sid == "" {
		t.Fatalf("client=%q sid=%q", client, sid)
	}
	if idle := idleExp.Sub(start); idle < 19*time.Minute || idle > 21*time.Minute {
		t.Fatalf("idle ttl=%v", idle)
	}
	if abs := sessionExp.Sub(start); abs < 12*time.Hour-time.Minute || abs > 12*time.Hour+time.Minute {
		t.Fatalf("absolute ttl=%v", abs)
	}
	if c.MaxAge != 0 {
		t.Fatalf("max-age %d", c.MaxAge)
	}
}

func TestWebSession_RefreshByCookieRotates(t *testing.T) {
	e := newWebEnv(t)
	_, old := webLogin(t, e)

	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: old.Value})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh %d %s", w.Code, w.Body.String())
	}
	fresh := refreshCookie(t, w)
	if fresh == nil || fresh.Value == "" || fresh.Value == old.Value || fresh.MaxAge != 0 || !fresh.HttpOnly {
		t.Fatalf("new cookie: %+v", fresh)
	}
	data := dataOf(t, w)
	if data["access"] == nil || data["access_expires_in"] == nil {
		t.Fatalf("body %s", w.Body.String())
	}
	if _, ok := data["refresh"]; ok {
		t.Fatal("refresh in body")
	}

	// Immediate replay is a race (Ц3): 409, session stays alive.
	again := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: old.Value})
	if again.Code != http.StatusConflict {
		t.Fatalf("old cookie race: %d %s", again.Code, again.Body.String())
	}
	if refreshCookie(t, again) != nil {
		t.Fatal("409 must not touch cookie")
	}
	alive := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh.Value})
	if alive.Code != http.StatusOK {
		t.Fatalf("session must survive race: %d", alive.Code)
	}
}

func TestWebSession_BodyRefreshIgnoredAndClientsBound(t *testing.T) {
	e := newWebEnv(t)
	_, webTok := webLogin(t, e)

	mobileRid := e.otpRequest(t, 22)
	mw := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", body: `{"request_id":"` + mobileRid + `","code":"` + webCode + `"}`})
	mobileRefresh, _ := dataOf(t, mw)["refresh"].(string)
	if mw.Code != http.StatusOK || mobileRefresh == "" {
		t.Fatalf("mobile verify %d %s", mw.Code, mw.Body.String())
	}

	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin,
		body: `{"refresh":"` + mobileRefresh + `"}`})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("web refresh by body: %d", w.Code)
	}

	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: mobileRefresh})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("mobile token in cookie: %d", w.Code)
	}

	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", body: `{"refresh":"` + webTok.Value + `"}`})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("web token in mobile body: %d", w.Code)
	}

	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", body: `{"refresh":"` + mobileRefresh + `"}`})
	if w.Code != http.StatusOK {
		t.Fatalf("mobile refresh: %d", w.Code)
	}
}

func TestWebSession_LogoutClearsCookieAndRevokes(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout %d", w.Code)
	}
	cleared := refreshCookie(t, w)
	if cleared == nil || cleared.Value != "" || cleared.MaxAge >= 0 || cleared.Path != "/api/auth" {
		t.Fatalf("cookie not cleared: %+v", cleared)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("set-cookie %q", w.Header().Get("Set-Cookie"))
	}
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: %d", w.Code)
	}

	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", web: true, origin: webOrigin})
	if w.Code != http.StatusNoContent || refreshCookie(t, w) == nil {
		t.Fatalf("logout without cookie: %d", w.Code)
	}
}

func TestWebSession_SelectPatientSetsCookie(t *testing.T) {
	e := newWebEnv(t)
	rid := e.otpRequest(t, 11, 12)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", web: true, origin: webOrigin,
		body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	if w.Code != http.StatusOK || refreshCookie(t, w) != nil || !strings.Contains(w.Body.String(), "patient_candidates") {
		t.Fatalf("verify with candidates %d %s", w.Code, w.Body.String())
	}
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/select-patient", web: true, origin: webOrigin,
		body: `{"request_id":"` + rid + `","patient_id":12}`})
	if w.Code != http.StatusOK {
		t.Fatalf("select %d %s", w.Code, w.Body.String())
	}
	c := refreshCookie(t, w)
	if c == nil || !c.HttpOnly || c.MaxAge != 0 {
		t.Fatalf("cookie %+v", c)
	}
	if strings.Contains(w.Body.String(), `"refresh`) {
		t.Fatalf("refresh in body: %s", w.Body.String())
	}
}

func TestWebSession_ForeignOriginForbidden(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	rid := e.otpRequest(t, 11, 12)
	routes := []webReq{
		{method: http.MethodPost, path: "/api/auth/otp/verify", body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`},
		{method: http.MethodPost, path: "/api/auth/otp/select-patient", body: `{"request_id":"` + rid + `","patient_id":12}`},
		{method: http.MethodPost, path: "/api/auth/refresh", cookie: c.Value},
		{method: http.MethodPost, path: "/api/auth/logout", cookie: c.Value},
	}
	badOrigins := []string{
		"https://cabinet.test:8083",     // other scheme
		"http://cabinet.test:9",         // other port
		"http://evil.test",              // other host
		"http://cabinet.test.evil.test", // host suffix
		"",                              // missing Origin
	}
	for _, origin := range badOrigins {
		for _, r := range routes {
			r.web = true
			r.origin = origin
			w := e.do(r)
			if w.Code != http.StatusForbidden || refreshCookie(t, w) != nil {
				t.Fatalf("%s origin=%q: %d cookie=%v", r.path, origin, w.Code, refreshCookie(t, w) != nil)
			}
		}
	}
	// Referer alone is not enough — Origin is required.
	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
		req.Host = webHost
		req.Header.Set("X-LK-Client", "web")
		req.Header.Set("Referer", webOrigin+"/")
		if r.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "lk_refresh", Value: r.cookie})
		}
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || refreshCookie(t, w) != nil {
			t.Fatalf("%s referer-only: %d", r.path, w.Code)
		}
	}
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusOK {
		t.Fatalf("token was spent by a forbidden request: %d", w.Code)
	}
}

func TestWebSession_EmptyOriginsForbidAll(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	e.cfg.Auth.WebOrigins = nil
	svc := service.New(nil, nil, e.lite, repo.Repos{}, slog.New(slog.DiscardHandler), 0, e.cfg)
	e.h = NewRouter(e.cfg, svc, slog.New(slog.DiscardHandler))
	rid := e.otpRequest(t, 11, 12)
	routes := []webReq{
		{method: http.MethodPost, path: "/api/auth/otp/verify", body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`},
		{method: http.MethodPost, path: "/api/auth/otp/select-patient", body: `{"request_id":"` + rid + `","patient_id":12}`},
		{method: http.MethodPost, path: "/api/auth/refresh", cookie: c.Value},
		{method: http.MethodPost, path: "/api/auth/logout", cookie: c.Value},
	}
	for _, r := range routes {
		r.web = true
		r.origin = webOrigin
		w := e.do(r)
		if w.Code != http.StatusForbidden || refreshCookie(t, w) != nil {
			t.Fatalf("%s empty allowlist: %d cookie=%v", r.path, w.Code, refreshCookie(t, w) != nil)
		}
	}
}

func TestWebSession_InsecureCookieWhenConfigured(t *testing.T) {
	e := newWebEnv(t)
	e.cfg.Auth.CookieSecure = false
	svc := service.New(nil, nil, e.lite, repo.Repos{}, slog.New(slog.DiscardHandler), 0, e.cfg)
	e.h = NewRouter(e.cfg, svc, slog.New(slog.DiscardHandler))
	_, c := webLogin(t, e)
	if c.Secure || !c.HttpOnly {
		t.Fatalf("secure=%v httponly=%v", c.Secure, c.HttpOnly)
	}
}

func TestMobileSession_ResponsesUnchanged(t *testing.T) {
	e := newWebEnv(t)
	rid := e.otpRequest(t, 11)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("verify %d set-cookie=%q", w.Code, w.Header().Get("Set-Cookie"))
	}
	data := dataOf(t, w)
	refresh, _ := data["refresh"].(string)
	if refresh == "" || data["refresh_expires_in"] != float64(30*24*3600) || data["access_expires_in"] != float64(900) {
		t.Fatalf("mobile body %s", w.Body.String())
	}
	var client string
	if err := e.lite.QueryRow(`SELECT client FROM refresh_tokens`).Scan(&client); err != nil || client != "" {
		t.Fatalf("client=%q err=%v", client, err)
	}

	// A foreign Origin does not matter without the header.
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", origin: "http://evil.test", body: `{"refresh":"` + refresh + `"}`})
	if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("refresh %d", w.Code)
	}
	refresh2, _ := dataOf(t, w)["refresh"].(string)
	if refresh2 == "" {
		t.Fatalf("mobile refresh body %s", w.Body.String())
	}
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", body: `{}`})
	if w.Code != http.StatusBadRequest || w.Body.String() != `{"success":false,"error":"refresh обязателен","error_details":{"code":"VALIDATION","message":"refresh обязателен"}}`+"\n" {
		t.Fatalf("refresh without token %d %q", w.Code, w.Body.String())
	}
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", body: `{"refresh":"` + refresh2 + `"}`})
	if w.Code != http.StatusNoContent || w.Header().Get("Set-Cookie") != "" || w.Body.Len() != 0 {
		t.Fatalf("logout %d", w.Code)
	}
	w = e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", body: `{}`})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("logout without token %d", w.Code)
	}
}

func TestNoStore_AuthAndMeIncludingErrors(t *testing.T) {
	e := newWebEnv(t)
	access, _ := dataOf(t, func() *httptest.ResponseRecorder { w, _ := webLogin(t, e); return w }())["access"].(string)
	cases := []struct {
		name string
		req  *http.Request
		code int
	}{
		{"auth ok", httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{"refresh":"x"}`)), http.StatusNoContent},
		{"auth validation", httptest.NewRequest(http.MethodPost, "/api/auth/otp/request", strings.NewReader(`{}`)), http.StatusBadRequest},
		{"auth bad json", httptest.NewRequest(http.MethodPost, "/api/auth/refresh", strings.NewReader(`{`)), http.StatusBadRequest},
		{"auth unknown", httptest.NewRequest(http.MethodGet, "/api/auth/nope", nil), http.StatusNotFound},
		{"me no token", httptest.NewRequest(http.MethodGet, "/api/me/profile", nil), http.StatusUnauthorized},
		{"me validation", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/me/appointments?status=bad", nil)
			r.Header.Set("Authorization", "Bearer "+access)
			return r
		}(), http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, tc.req)
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: code=%d cache-control=%q", tc.name, w.Code, w.Header().Get("Cache-Control"))
		}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if w.Header().Get("Cache-Control") == "no-store" {
		t.Fatal("no-store leaked to /api/health")
	}
}

func TestWebSession_RefreshDoesNotExtendAbsoluteTTL(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh %d", w.Code)
	}
	fresh := refreshCookie(t, w)
	past := time.Now().Add(-time.Minute).UTC()
	if _, err := e.lite.Exec(`UPDATE refresh_tokens SET session_expires_at = ?`, past); err != nil {
		t.Fatal(err)
	}
	again := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh.Value})
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("absolute ttl must end session: %d", again.Code)
	}
}

func TestWebSession_IdleTimeoutWithoutRefresh(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	past := time.Now().Add(-time.Minute).UTC()
	if _, err := e.lite.Exec(`UPDATE refresh_tokens SET expires_at = ? WHERE revoked_at IS NULL`, past); err != nil {
		t.Fatal(err)
	}
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("idle expiry: %d", w.Code)
	}
}

func TestWebSession_LogoutRevokesWholeSid(t *testing.T) {
	e := newWebEnv(t)
	_, old := webLogin(t, e)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: old.Value})
	fresh := refreshCookie(t, w)
	if w.Code != http.StatusOK || fresh == nil {
		t.Fatalf("refresh %d", w.Code)
	}
	// Logout with the rotated-away token must still kill the new one (same sid).
	out := e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", web: true, origin: webOrigin, cookie: old.Value})
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout %d", out.Code)
	}
	again := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh.Value})
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("fresh token after sid logout: %d", again.Code)
	}
}

func TestWebSession_AccessTTLCappedByAbsolute(t *testing.T) {
	e := newWebEnv(t)
	_, c := webLogin(t, e)
	end := time.Now().UTC().Add(5 * time.Minute)
	if _, err := e.lite.Exec(`UPDATE refresh_tokens SET session_expires_at = ? WHERE revoked_at IS NULL`, end); err != nil {
		t.Fatal(err)
	}
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: c.Value})
	if w.Code != http.StatusOK {
		t.Fatalf("refresh %d %s", w.Code, w.Body.String())
	}
	data := dataOf(t, w)
	expIn, _ := data["access_expires_in"].(float64)
	if expIn <= 0 || expIn > 300 {
		t.Fatalf("access_expires_in=%v want ≤300", expIn)
	}
	access, _ := data["access"].(string)
	// Six minutes after the absolute remaining window the access JWT is dead.
	expired, err := auth.IssueAccess([]byte(e.cfg.JWT.Secret), e.cfg.JWT.Issuer, 11, "", e.cfg.Auth.Mode, 5*time.Minute, time.Now().Add(-6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_ = access
	req := httptest.NewRequest(http.MethodGet, "/api/me/profile", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	pw := httptest.NewRecorder()
	e.h.ServeHTTP(pw, req)
	if pw.Code != http.StatusUnauthorized {
		t.Fatalf("expired access after absolute window: %d", pw.Code)
	}
}

func TestWebSession_LoginRevokesPreviousCookieSession(t *testing.T) {
	e := newWebEnv(t)
	wA, cA := webLogin(t, e)
	sidA, _ := dataOf(t, wA)["session"].(string)
	if sidA == "" {
		t.Fatal("no session A")
	}
	rid := e.otpRequest(t, 11)
	wB := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", web: true, origin: webOrigin,
		cookie: cA.Value, body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	if wB.Code != http.StatusOK {
		t.Fatalf("login B %d %s", wB.Code, wB.Body.String())
	}
	cB := refreshCookie(t, wB)
	sidB, _ := dataOf(t, wB)["session"].(string)
	if cB == nil || sidB == "" || sidB == sidA {
		t.Fatalf("session B cookie=%v sid=%q", cB != nil, sidB)
	}
	old := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: cA.Value})
	if old.Code != http.StatusUnauthorized && old.Code != http.StatusConflict {
		t.Fatalf("session A must be dead: %d", old.Code)
	}
	ok := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: cB.Value})
	if ok.Code != http.StatusOK {
		t.Fatalf("session B: %d", ok.Code)
	}
}

func TestWebSession_RefreshSessionMismatchConflict(t *testing.T) {
	e := newWebEnv(t)
	wA, cA := webLogin(t, e)
	sidA, _ := dataOf(t, wA)["session"].(string)
	rid := e.otpRequest(t, 11)
	wB := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", web: true, origin: webOrigin,
		cookie: cA.Value, body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	cB := refreshCookie(t, wB)
	sidB, _ := dataOf(t, wB)["session"].(string)
	if wB.Code != http.StatusOK || cB == nil || sidB == "" {
		t.Fatalf("login B %d", wB.Code)
	}
	before := cB.Value
	conflict := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin,
		cookie: cB.Value, body: `{"session":"` + sidA + `"}`})
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "SESSION_CHANGED") {
		t.Fatalf("mismatch: %d %s", conflict.Code, conflict.Body.String())
	}
	if refreshCookie(t, conflict) != nil {
		t.Fatal("409 must not set cookie")
	}
	ok := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin,
		cookie: before, body: `{"session":"` + sidB + `"}`})
	if ok.Code != http.StatusOK {
		t.Fatalf("B still works: %d", ok.Code)
	}
}

func TestWebSession_LogoutSessionMismatchLeavesCookie(t *testing.T) {
	e := newWebEnv(t)
	wA, cA := webLogin(t, e)
	sidA, _ := dataOf(t, wA)["session"].(string)
	rid := e.otpRequest(t, 11)
	wB := e.do(webReq{method: http.MethodPost, path: "/api/auth/otp/verify", web: true, origin: webOrigin,
		cookie: cA.Value, body: `{"request_id":"` + rid + `","code":"` + webCode + `"}`})
	cB := refreshCookie(t, wB)
	if wB.Code != http.StatusOK || cB == nil {
		t.Fatalf("login B %d", wB.Code)
	}
	out := e.do(webReq{method: http.MethodPost, path: "/api/auth/logout", web: true, origin: webOrigin,
		cookie: cB.Value, body: `{"session":"` + sidA + `"}`})
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout %d", out.Code)
	}
	if refreshCookie(t, out) != nil {
		t.Fatal("mismatch logout must not clear cookie")
	}
	ok := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: cB.Value})
	if ok.Code != http.StatusOK {
		t.Fatalf("B after stale logout: %d", ok.Code)
	}
}

func TestWebSession_RefreshRaceWindow(t *testing.T) {
	e := newWebEnv(t)
	_, old := webLogin(t, e)
	w := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: old.Value})
	fresh := refreshCookie(t, w)
	if w.Code != http.StatusOK || fresh == nil {
		t.Fatalf("refresh %d", w.Code)
	}
	race := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: old.Value})
	if race.Code != http.StatusConflict || !strings.Contains(race.Body.String(), "REFRESH_RACE") {
		t.Fatalf("inside window: %d %s", race.Code, race.Body.String())
	}
	alive := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh.Value})
	fresh2 := refreshCookie(t, alive)
	if alive.Code != http.StatusOK || fresh2 == nil {
		t.Fatalf("alive after race: %d", alive.Code)
	}
	// Older than 10s: backdate revoked_at on the superseded token (fresh) only, then replay it.
	past := time.Now().UTC().Add(-11 * time.Second).Format("2006-01-02 15:04:05")
	if _, err := e.lite.Exec(
		`UPDATE refresh_tokens SET revoked_at = ? WHERE token_hash = ?`,
		past, auth.HashToken(fresh.Value),
	); err != nil {
		t.Fatal(err)
	}
	kill := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh.Value})
	if kill.Code != http.StatusUnauthorized {
		t.Fatalf("outside window: %d %s", kill.Code, kill.Body.String())
	}
	dead := e.do(webReq{method: http.MethodPost, path: "/api/auth/refresh", web: true, origin: webOrigin, cookie: fresh2.Value})
	if dead.Code != http.StatusUnauthorized {
		t.Fatalf("session must die outside window: %d", dead.Code)
	}
}
