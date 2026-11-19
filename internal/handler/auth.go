package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type AuthService interface {
	OTPRequest(ctx context.Context, phone string, ip string) (OTPRequestResult, error)
	OTPVerify(ctx context.Context, requestID, code string) (OTPVerifyResult, error)
	OTPSelectPatient(ctx context.Context, requestID string, patientID int64) (Tokens, error)
	Refresh(ctx context.Context, refresh string) (Tokens, error)
	Logout(ctx context.Context, refresh string) error
	WebOTPVerify(ctx context.Context, requestID, code, previousRefresh string) (OTPVerifyResult, error)
	WebOTPSelectPatient(ctx context.Context, requestID string, patientID int64, previousRefresh string) (Tokens, error)
	WebRefresh(ctx context.Context, refresh, expectedSession string) (Tokens, error)
	// WebLogout revokes the cookie session unless expectedSession is set and differs (then ClearCookie is false).
	WebLogout(ctx context.Context, refresh, expectedSession string) (clearCookie bool, err error)
}

type AuthHandler struct {
	Svc    AuthService
	Logger *slog.Logger
	// CookieSecure sets Secure on lk_refresh (LK_COOKIE_SECURE).
	CookieSecure bool
}

const (
	// LKClientHeader with the value "web" switches verify, select-patient, refresh and logout to the cookie session.
	LKClientHeader = "X-LK-Client"
	// RefreshCookie holds the web cabinet refresh token; page code cannot read it.
	RefreshCookie     = "lk_refresh"
	refreshCookiePath = "/api/auth"
)

// IsWebClient reports whether the request comes from the web cabinet.
func IsWebClient(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(LKClientHeader)), "web")
}

// OTPRequestResult is the same for a known number, an unknown one and a card without e-mail.
type OTPRequestResult struct {
	RequestID string `json:"request_id"`
	TTL       int    `json:"ttl"`
	Message   string `json:"message"`
	Delivery  string `json:"delivery"`
	DevCode   string `json:"dev_code,omitempty"`
}

type Tokens struct {
	Access           string `json:"access"`
	Refresh          string `json:"refresh"`
	AccessExpiresIn  int    `json:"access_expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
	// Session is the opaque web session id (sid); empty for mobile.
	Session string `json:"session,omitempty"`
}

// WebTokens is what the web cabinet gets in the body: the refresh token goes to the cookie only.
type WebTokens struct {
	Access          string `json:"access"`
	AccessExpiresIn int    `json:"access_expires_in"`
	Session         string `json:"session"`
}

type Candidate struct {
	PatientID   int64  `json:"patient_id"`
	FullName    string `json:"full_name"`
	MaskedEmail string `json:"masked_email"`
}

type OTPVerifyResult struct {
	// If tokens are present -> login complete (single candidate).
	// If candidates present -> client must call select-patient.
	Access           string      `json:"access,omitempty"`
	Refresh          string      `json:"refresh,omitempty"`
	AccessExpiresIn  int         `json:"access_expires_in,omitempty"`
	RefreshExpiresIn int         `json:"refresh_expires_in,omitempty"`
	Session          string      `json:"session,omitempty"`
	Candidates       []Candidate `json:"patient_candidates,omitempty"`
}

func (h AuthHandler) OTPRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if body.Phone == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "phone обязателен")
		return
	}

	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = fwd
	}

	res, err := h.Svc.OTPRequest(r.Context(), body.Phone, ip)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	response.OK(w, res)
}

func (h AuthHandler) OTPVerify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if body.RequestID == "" || body.Code == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "request_id и code обязательны")
		return
	}
	web := IsWebClient(r)
	var res OTPVerifyResult
	var err error
	if web {
		prev := ""
		if c, cerr := r.Cookie(RefreshCookie); cerr == nil {
			prev = c.Value
		}
		res, err = h.Svc.WebOTPVerify(r.Context(), body.RequestID, body.Code, prev)
	} else {
		res, err = h.Svc.OTPVerify(r.Context(), body.RequestID, body.Code)
	}
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	if web && res.Refresh != "" {
		h.setRefreshCookie(w, res.Refresh)
		response.OK(w, WebTokens{Access: res.Access, AccessExpiresIn: res.AccessExpiresIn, Session: res.Session})
		return
	}
	response.OK(w, res)
}

func (h AuthHandler) OTPSelectPatient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
		PatientID int64  `json:"patient_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if body.RequestID == "" || body.PatientID == 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "request_id и patient_id обязательны")
		return
	}
	web := IsWebClient(r)
	var tok Tokens
	var err error
	if web {
		prev := ""
		if c, cerr := r.Cookie(RefreshCookie); cerr == nil {
			prev = c.Value
		}
		tok, err = h.Svc.WebOTPSelectPatient(r.Context(), body.RequestID, body.PatientID, prev)
	} else {
		tok, err = h.Svc.OTPSelectPatient(r.Context(), body.RequestID, body.PatientID)
	}
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	if web {
		h.writeWebTokens(w, tok)
		return
	}
	response.OK(w, tok)
}

func (h AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	if IsWebClient(r) {
		h.webRefresh(w, r)
		return
	}
	var body struct {
		Refresh string `json:"refresh"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if body.Refresh == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "refresh обязателен")
		return
	}
	tok, err := h.Svc.Refresh(r.Context(), body.Refresh)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	response.OK(w, tok)
}

// webRefresh takes the token from the cookie only; a refresh field in the body is ignored.
// Optional body.session is the session the tab expects (Ц2).
func (h AuthHandler) webRefresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(RefreshCookie)
	if err != nil || c.Value == "" {
		h.clearRefreshCookie(w)
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	var body struct {
		Session string `json:"session"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	tok, err := h.Svc.WebRefresh(r.Context(), c.Value, strings.TrimSpace(body.Session))
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			if ae.Status == http.StatusUnauthorized {
				h.clearRefreshCookie(w)
			}
			// 409 SESSION_CHANGED / REFRESH_RACE: cookie and session stay as they are.
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		h.clearRefreshCookie(w)
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	h.writeWebTokens(w, tok)
}

func (h AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if IsWebClient(r) {
		var body struct {
			Session string `json:"session"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		refresh := ""
		if c, err := r.Cookie(RefreshCookie); err == nil {
			refresh = c.Value
		}
		clear, _ := h.Svc.WebLogout(r.Context(), refresh, strings.TrimSpace(body.Session))
		if clear {
			h.clearRefreshCookie(w)
		}
		response.NoContent(w)
		return
	}
	var body struct {
		Refresh string `json:"refresh"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if body.Refresh == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "refresh обязателен")
		return
	}
	_ = h.Svc.Logout(r.Context(), body.Refresh)
	response.NoContent(w)
}

func (h AuthHandler) writeWebTokens(w http.ResponseWriter, tok Tokens) {
	h.setRefreshCookie(w, tok.Refresh)
	response.OK(w, WebTokens{Access: tok.Access, AccessExpiresIn: tok.AccessExpiresIn, Session: tok.Session})
}

// setRefreshCookie writes a session cookie (no Max-Age / Expires); browser close ends it.
func (h AuthHandler) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookie,
		Value:    token,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h AuthHandler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookie,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}
