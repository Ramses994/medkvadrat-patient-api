package handler

import (
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

const (
	dashboardLoginRateScope  = "dashboard_login"
	dashboardLoginRateLimit  = 10
	dashboardLoginRateWindow = 15 * time.Minute
)

// DashboardPageHandler serves the staff dashboard HTML and login/logout.
type DashboardPageHandler struct {
	HTML         []byte
	Password     string
	Secret       []byte
	SessionTTL   time.Duration
	CookieSecure bool
	RateLimit    *ratelimit.Store
	Logger       *slog.Logger
	Now          func() time.Time
}

func (h DashboardPageHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h DashboardPageHandler) configured() bool {
	return strings.TrimSpace(h.Password) != "" && len(h.Secret) > 0
}

func (h DashboardPageHandler) Page(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		http.Error(w, "Dashboard not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.HTML)
}

func (h DashboardPageHandler) LoginForm(w http.ResponseWriter, r *http.Request) {
	h.writeLogin(w, "")
}

func (h DashboardPageHandler) LoginPost(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		http.Error(w, "Dashboard not configured", http.StatusServiceUnavailable)
		return
	}

	ip := staffsession.ClientIP(r.RemoteAddr)
	if h.RateLimit != nil {
		ok, err := h.RateLimit.Allow(r.Context(), dashboardLoginRateScope, ip, dashboardLoginRateWindow, dashboardLoginRateLimit, h.now())
		if err != nil {
			h.Logger.Error("dashboard login rate limit failed", "err", err, "ip", ip)
		} else if !ok {
			response.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "Слишком много попыток, попробуйте позже")
			return
		}
	}

	if err := r.ParseForm(); err != nil {
		h.writeLogin(w, "Неверный пароль")
		return
	}
	password := r.FormValue("password")
	if !staffsession.PasswordMatch(h.Password, password) {
		h.Logger.Info("dashboard login failed", "ip", ip)
		h.writeLogin(w, "Неверный пароль")
		return
	}

	ttl := h.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	exp := h.now().Add(ttl)
	http.SetCookie(w, &http.Cookie{
		Name:     staffsession.CookieName,
		Value:    staffsession.Sign(h.Secret, exp),
		Path:     "/dashboard",
		Expires:  exp,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.CookieSecure,
	})
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h DashboardPageHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     staffsession.CookieName,
		Value:    "",
		Path:     "/dashboard",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.CookieSecure,
	})
	http.Redirect(w, r, "/dashboard/login", http.StatusFound)
}

func (h DashboardPageHandler) writeLogin(w http.ResponseWriter, errMsg string) {
	if !h.configured() {
		http.Error(w, "Dashboard not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>Вход — дашборд МедКвадрат</title>`)
	b.WriteString(`<style>body{font-family:system-ui,sans-serif;background:#EEF1EE;color:#0F1E1C;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}`)
	b.WriteString(`form{background:#fff;padding:28px 32px;border-radius:10px;border:1px solid #D5DBD7;min-width:280px}`)
	b.WriteString(`h1{font-size:18px;margin:0 0 16px}label{display:block;font-size:13px;margin-bottom:6px}`)
	b.WriteString(`input{width:100%;box-sizing:border-box;padding:8px 10px;font-size:15px;border:1px solid #D5DBD7;border-radius:6px}`)
	b.WriteString(`button{margin-top:14px;width:100%;padding:10px;background:#0E4F45;color:#fff;border:0;border-radius:6px;font-size:14px;cursor:pointer}`)
	b.WriteString(`.err{color:#B3261E;font-size:13px;margin:0 0 12px}</style></head><body>`)
	b.WriteString(`<form method="post" action="/dashboard/login"><h1>Дашборд расписания</h1>`)
	if errMsg != "" {
		b.WriteString(`<p class="err">`)
		b.WriteString(html.EscapeString(errMsg))
		b.WriteString(`</p>`)
	}
	b.WriteString(`<label for="password">Пароль</label>`)
	b.WriteString(`<input id="password" name="password" type="password" autocomplete="current-password" required autofocus>`)
	b.WriteString(`<button type="submit">Войти</button></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}
