package handler

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"errors"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
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
	Users        *staff.Store
}

func (h DashboardPageHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h DashboardPageHandler) configured() bool {
	return strings.TrimSpace(h.Password) != "" && !staffsession.WeakSecret(h.Secret)
}

func dashboardUnavailable(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Dashboard not configured", http.StatusServiceUnavailable)
}

func (h DashboardPageHandler) Page(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		dashboardUnavailable(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.HTML)
}

func (h DashboardPageHandler) LoginForm(w http.ResponseWriter, r *http.Request) {
	h.writeLogin(w, http.StatusOK, "")
}

func (h DashboardPageHandler) LoginPost(w http.ResponseWriter, r *http.Request) {
	if !h.configured() {
		w.Header().Set("Cache-Control", "no-store")
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

	if h.Users == nil {
		dashboardUnavailable(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writeLogin(w, http.StatusBadRequest, "Неверный логин или пароль")
		return
	}
	login := r.FormValue("login")
	password := r.FormValue("password")
	principal, err := h.Users.Authenticate(r.Context(), login, password, ip, h.now())
	if err != nil {
		if h.Logger != nil {
			h.Logger.Info("dashboard login failed", "ip", ip, "login", strings.TrimSpace(login))
		}
		if errors.Is(err, staff.ErrLocked) {
			h.writeLogin(w, http.StatusTooManyRequests, "Слишком много попыток, попробуйте через 15 минут")
			return
		}
		if errors.Is(err, staff.ErrNoBranches) {
			h.writeLogin(w, http.StatusForbidden, "нет филиалов — обратитесь в ИТ")
			return
		}
		if errors.Is(err, staff.ErrJournal) {
			h.writeLogin(w, http.StatusServiceUnavailable, "Вход временно недоступен")
			return
		}
		h.writeLogin(w, http.StatusUnauthorized, "Неверный логин или пароль")
		return
	}
	h.setStaffCookie(w, principal.ID, principal.SessionVersion)
	if principal.MustChangePassword {
		http.Redirect(w, r, "/dashboard/password", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h DashboardPageHandler) PasswordForm(w http.ResponseWriter, r *http.Request) {
	h.writePassword(w, http.StatusOK, "")
}

func (h DashboardPageHandler) PasswordPost(w http.ResponseWriter, r *http.Request) {
	p, ok := staff.PrincipalFrom(r.Context())
	if !ok || h.Users == nil {
		http.Redirect(w, r, "/dashboard/login", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writePassword(w, http.StatusOK, "Не удалось сменить пароль")
		return
	}
	next := r.FormValue("new_password")
	if next != r.FormValue("new_password_again") {
		h.writePassword(w, http.StatusOK, "Новый пароль и повтор не совпадают")
		return
	}
	err := h.Users.ChangePassword(r.Context(), p.ID, r.FormValue("current_password"), next)
	if err != nil {
		if errors.Is(err, staff.ErrShortPassword) {
			h.writePassword(w, http.StatusOK, "Пароль не короче 10 символов")
			return
		}
		if errors.Is(err, staff.ErrStale) {
			h.writePassword(w, http.StatusConflict, "Пароль уже изменён — войдите заново")
			return
		}
		h.writePassword(w, http.StatusOK, "Неверный текущий пароль")
		return
	}
	fresh, err := h.Users.ByID(r.Context(), p.ID)
	if err != nil {
		h.writePassword(w, http.StatusOK, "Не удалось сменить пароль")
		return
	}
	h.setStaffCookie(w, fresh.ID, fresh.SessionVersion)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h DashboardPageHandler) setStaffCookie(w http.ResponseWriter, userID int64, version int) {
	ttl := h.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	exp := h.now().Add(ttl)
	cookieVal, err := staffsession.Sign(h.Secret, userID, version, exp)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     staffsession.CookieName,
		Value:    cookieVal,
		Path:     "/dashboard",
		Expires:  exp,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.CookieSecure,
	})
}

func (h DashboardPageHandler) Logout(w http.ResponseWriter, r *http.Request) {
	p, ok := staff.PrincipalFrom(r.Context())
	if !ok || h.Users == nil {
		h.logoutFailed(w, r, 0, errors.New("logout store unavailable"))
		return
	}
	if err := h.Users.EndSession(r.Context(), p.ID); err != nil {
		h.logoutFailed(w, r, p.ID, err)
		return
	}
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

func (h DashboardPageHandler) logoutFailed(w http.ResponseWriter, r *http.Request, staffID int64, err error) {
	if h.Logger != nil {
		h.Logger.Error("dashboard logout failed", "staff_id", staffID, "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	if logoutWantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   "Не удалось выйти, повторите",
			"code":    "LOGOUT_FAILED",
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="ru"><head><meta charset="utf-8"><title>Выход</title></head><body><p>Не удалось выйти, повторите</p></body></html>`))
}

func logoutWantsJSON(r *http.Request) bool {
	if r.Header.Get("X-Dashboard-Request") != "" {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (h DashboardPageHandler) writeLogin(w http.ResponseWriter, status int, errMsg string) {
	if !h.configured() {
		dashboardUnavailable(w)
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
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
	b.WriteString(`<label for="login">Логин</label>`)
	b.WriteString(`<input id="login" name="login" autocomplete="username" required autofocus>`)
	b.WriteString(`<label for="password">Пароль</label>`)
	b.WriteString(`<input id="password" name="password" type="password" autocomplete="current-password" required>`)
	b.WriteString(`<button type="submit">Войти</button></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}

func (h DashboardPageHandler) writePassword(w http.ResponseWriter, status int, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="ru"><head><meta charset="utf-8"><title>Смена пароля</title>`)
	b.WriteString(`<style>body{font-family:system-ui,sans-serif;background:#EEF1EE;color:#0F1E1C;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0}`)
	b.WriteString(`form{background:#fff;padding:28px 32px;border-radius:10px;border:1px solid #D5DBD7;min-width:280px}`)
	b.WriteString(`h1{font-size:18px;margin:0 0 16px}label{display:block;font-size:13px;margin:10px 0 6px}`)
	b.WriteString(`input{width:100%;box-sizing:border-box;padding:8px 10px;font-size:15px;border:1px solid #D5DBD7;border-radius:6px}`)
	b.WriteString(`button{margin-top:14px;width:100%;padding:10px;background:#0E4F45;color:#fff;border:0;border-radius:6px;font-size:14px;cursor:pointer}`)
	b.WriteString(`.err{color:#B3261E;font-size:13px;margin:0 0 12px}</style></head><body>`)
	b.WriteString(`<form method="post" action="/dashboard/password"><h1>Смените пароль</h1>`)
	if errMsg != "" {
		b.WriteString(`<p class="err">`)
		b.WriteString(html.EscapeString(errMsg))
		b.WriteString(`</p>`)
	}
	b.WriteString(`<label for="current_password">Текущий пароль</label>`)
	b.WriteString(`<input id="current_password" name="current_password" type="password" autocomplete="current-password" required>`)
	b.WriteString(`<label for="new_password">Новый пароль</label>`)
	b.WriteString(`<input id="new_password" name="new_password" type="password" autocomplete="new-password" required minlength="10">`)
	b.WriteString(`<label for="new_password_again">Новый пароль ещё раз</label>`)
	b.WriteString(`<input id="new_password_again" name="new_password_again" type="password" autocomplete="new-password" required minlength="10">`)
	b.WriteString(`<button type="submit">Сохранить</button></form></body></html>`)
	_, _ = w.Write([]byte(b.String()))
}
