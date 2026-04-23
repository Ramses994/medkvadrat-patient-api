package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type AuthService interface {
	OTPRequest(ctx context.Context, phone string, ip string) (OTPRequestResult, error)
	OTPVerify(ctx context.Context, requestID, code string) (OTPVerifyResult, error)
	OTPSelectPatient(ctx context.Context, requestID string, patientID int64) (Tokens, error)
	Refresh(ctx context.Context, refresh string) (Tokens, error)
	Logout(ctx context.Context, refresh string) error
}

type AuthHandler struct {
	Svc    AuthService
	Logger *slog.Logger
}

type OTPRequestResult struct {
	RequestID         string      `json:"request_id"`
	TTL               int         `json:"ttl"`
	Channel           string      `json:"channel"`
	MaskedDestination interface{} `json:"masked_destination"` // string or []string
	DevCode           string      `json:"dev_code,omitempty"`
}

type Tokens struct {
	Access           string `json:"access"`
	Refresh          string `json:"refresh"`
	AccessExpiresIn  int    `json:"access_expires_in"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
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
	res, err := h.Svc.OTPVerify(r.Context(), body.RequestID, body.Code)
	if err != nil {
		var ae *apperr.AppError
		if errors.As(err, &ae) {
			response.Error(w, ae.Status, ae.Code, ae.Message)
			return
		}
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
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
	tok, err := h.Svc.OTPSelectPatient(r.Context(), body.RequestID, body.PatientID)
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

func (h AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
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

func (h AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
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
