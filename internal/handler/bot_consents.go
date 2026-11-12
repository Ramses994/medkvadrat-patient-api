package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/consents"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

var consentVersion = regexp.MustCompile(`^[a-z]+-v[0-9]+$`)

// BotConsentsHandler serves consent state to the bot.
// The router wraps these routes with the bearer token and the clinic network.
type BotConsentsHandler struct {
	Store  *consents.Store
	Notify func(ctx context.Context, patientID int64) (*int, error)
	Logger *slog.Logger
}

type consentBody struct {
	PatientID        int64  `json:"patient_id"`
	MaxUserID        int64  `json:"max_user_id"`
	Kind             string `json:"kind"`
	TextVersion      string `json:"text_version"`
	Action           string `json:"action"`
	ExpectedRevision *int64 `json:"expected_revision"`
}

func (h BotConsentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	patientID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("patient_id")), 10, 64)
	if err != nil || patientID <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "patient_id")
		return
	}
	h.write(w, r, patientID, func() (consents.KindState, consents.KindState, error) {
		return h.Store.Current(r.Context(), patientID)
	})
}

func (h BotConsentsHandler) Post(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректное тело запроса")
		return
	}
	var req consentBody
	if err := json.Unmarshal(body, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}
	if req.PatientID <= 0 || req.MaxUserID <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "patient_id и max_user_id обязательны")
		return
	}
	if req.Kind != consents.KindService && req.Kind != consents.KindAds {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "kind: service или ads")
		return
	}
	if !consentVersion.MatchString(req.TextVersion) {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "text_version")
		return
	}
	if req.Action != "accept" && req.Action != "revoke" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "action: accept или revoke")
		return
	}
	if req.Action == "accept" && req.ExpectedRevision == nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "expected_revision")
		return
	}
	h.write(w, r, req.PatientID, func() (consents.KindState, consents.KindState, error) {
		return h.Store.Apply(r.Context(), consents.Input{
			PatientID: req.PatientID, MaxUserID: req.MaxUserID,
			Kind: req.Kind, TextVersion: req.TextVersion, Action: req.Action,
			ExpectedRevision: req.ExpectedRevision,
		})
	})
}

func (h BotConsentsHandler) write(w http.ResponseWriter, r *http.Request, patientID int64, load func() (consents.KindState, consents.KindState, error)) {
	if h.Store == nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	service, ads, err := load()
	if errors.Is(err, consents.ErrConsentChanged) {
		response.Error(w, http.StatusConflict, "CONSENT_CHANGED", "Согласие изменилось")
		return
	}
	if errors.Is(err, consents.ErrExpectedRevision) {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "expected_revision")
		return
	}
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("bot consent", "err", err)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	var notify *int
	if h.Notify != nil {
		notify, err = h.Notify(r.Context(), patientID)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Error("messenger notify", "err", err)
			}
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
			return
		}
	}
	response.OK(w, consents.Snapshot{MessengerNotify: notify, Service: service, Ads: ads})
}
