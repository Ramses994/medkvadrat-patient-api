package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/slottoken"
)

type BookingCatalogService interface {
	BookingOptions(ctx context.Context) ([]repo.BookingOption, error)
	BookingSlots(ctx context.Context, subj, exam int64, from, to string, now time.Time) ([]repo.BookingSlot, error)
}

type BookingCatalogHandler struct {
	Svc             BookingCatalogService
	Logger          *slog.Logger
	SlotTokenSecret string
	Now             func() time.Time
}

func (h BookingCatalogHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h BookingCatalogHandler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

type bookingOptionDTO struct {
	PlSubjID   int64  `json:"pl_subj_id"`
	PlExamID   int64  `json:"pl_exam_id"`
	BranchID   int    `json:"branch_id"`
	BranchCode string `json:"branch_code"`
	DoctorName string `json:"doctor_name"`
	Service    string `json:"service"`
}

func (h BookingCatalogHandler) Options(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.PatientIDFromContext(r.Context()); !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	rows, err := h.Svc.BookingOptions(r.Context())
	if err != nil {
		h.log().Error("booking options failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]bookingOptionDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, bookingOptionDTO{
			PlSubjID: row.PlSubjID, PlExamID: row.PlExamID,
			BranchID: row.BranchID, BranchCode: row.BranchCode,
			DoctorName: row.DoctorName, Service: row.Service,
		})
	}
	response.OK(w, out)
}

type bookingSlotsDTO struct {
	Slots []bookingSlotDTO `json:"slots"`
}

type bookingSlotDTO struct {
	Start       string `json:"start"`
	DurationMin int    `json:"duration_min"`
	SlotToken   string `json:"slot_token"`
}

func (h BookingCatalogHandler) Slots(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	if !slottoken.SecretOK(h.SlotTokenSecret) {
		h.log().Error("slot token secret not configured")
		response.Error(w, http.StatusServiceUnavailable, "SLOT_TOKEN_UNAVAILABLE", "Запись временно недоступна")
		return
	}
	subj, exam, from, to, ok := parseSlotQuery(w, r)
	if !ok {
		return
	}
	offers, err := h.Svc.BookingSlots(r.Context(), subj, exam, from, to, h.now())
	if err != nil {
		if errors.Is(err, repo.ErrBadSlotWindow) {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "Период не больше 14 дней")
			return
		}
		if errors.Is(err, repo.ErrInactivePair) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Эта запись недоступна")
			return
		}
		if errors.Is(err, slottoken.ErrSecret) {
			response.Error(w, http.StatusServiceUnavailable, "SLOT_TOKEN_UNAVAILABLE", "Запись временно недоступна")
			return
		}
		if errors.Is(err, repo.ErrSlotsUnavailable) {
			response.Error(w, http.StatusServiceUnavailable, "SLOTS_UNAVAILABLE", "Свободное время временно недоступно")
			return
		}
		h.log().Error("booking slots failed", "err", err, "pl_subj_id", subj)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := bookingSlotsDTO{Slots: make([]bookingSlotDTO, 0, len(offers))}
	now := h.now()
	for _, offer := range offers {
		tok, err := slottoken.Issue(h.SlotTokenSecret, slottoken.Payload{
			PatientID: pid, PlSubjID: offer.PlSubjID, PlExamID: offer.PlExamID,
			Start: offer.Start, DurationMin: offer.DurationMin, BranchID: offer.BranchID,
		}, now)
		if err != nil {
			h.log().Error("slot token secret not configured")
			response.Error(w, http.StatusServiceUnavailable, "SLOT_TOKEN_UNAVAILABLE", "Запись временно недоступна")
			return
		}
		out.Slots = append(out.Slots, bookingSlotDTO{Start: offer.Start, DurationMin: offer.DurationMin, SlotToken: tok})
	}
	response.OK(w, out)
}

func parseSlotQuery(w http.ResponseWriter, r *http.Request) (int64, int64, string, string, bool) {
	subj, err := strconv.ParseInt(r.URL.Query().Get("pl_subj_id"), 10, 64)
	if err != nil || subj <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Нужен pl_subj_id")
		return 0, 0, "", "", false
	}
	exam, err := strconv.ParseInt(r.URL.Query().Get("pl_exam_id"), 10, 64)
	if err != nil || exam <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Нужен pl_exam_id")
		return 0, 0, "", "", false
	}
	from := r.URL.Query().Get("date_from")
	to := r.URL.Query().Get("date_to")
	if _, err := time.Parse("2006-01-02", from); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "date_from: формат YYYY-MM-DD")
		return 0, 0, "", "", false
	}
	if _, err := time.Parse("2006-01-02", to); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "date_to: формат YYYY-MM-DD")
		return 0, 0, "", "", false
	}
	return subj, exam, from, to, true
}
