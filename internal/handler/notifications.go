package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type NotificationsService interface {
	NewBookings(ctx context.Context, now time.Time, patientIDs []int64, cursor *repo.BookingCursor, limit int) (repo.NewBookingsPage, error)
	BookingByPlanningID(ctx context.Context, planningID int64) (repo.BookingNotice, error)
}

type NotificationsHandler struct {
	Svc    NotificationsService
	Logger *slog.Logger
	Now    func() time.Time
}

type newBookingsRequest struct {
	PatientIDs []int64            `json:"patient_ids"`
	Cursor     *repo.BookingCursor `json:"cursor"`
	Limit      int                `json:"limit"`
}

type newBookingDTO struct {
	PlanningID       int64  `json:"planning_id"`
	PatientID        int64  `json:"patient_id"`
	PatientPhone     string `json:"patient_phone"`
	PatientName      string `json:"patient_name"`
	DoctorName       string `json:"doctor_name"`
	Service          string `json:"service"`
	IsWaitlist       bool   `json:"is_waitlist"`
	MessengerNotify  *int   `json:"messenger_notify"`
	NoSMSNotify      bool   `json:"no_sms_notify"`
	ApptVersion      string `json:"appt_version"`
	BranchID         int    `json:"branch_id"`
	BranchCode       string `json:"branch_code"`
	DateConsultation string `json:"date_consultation"`
	Status           int    `json:"status"`
	CreatedAt        string `json:"created_at"`
}

type newBookingsPageDTO struct {
	Items      []newBookingDTO     `json:"items"`
	NextCursor *repo.BookingCursor `json:"next_cursor"`
	HasMore    bool                `json:"has_more"`
}

func (h NotificationsHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().In(moscowLocation())
}

func (h NotificationsHandler) NewBookings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректное тело запроса")
		return
	}
	var req newBookingsRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
			return
		}
	}
	ids, err := repo.RequirePatientIDList(req.PatientIDs, 500)
	if err != nil {
		msg := "patient_ids: ожидается список положительных целых"
		if err.Error() == "too many patient_ids" {
			msg = "patient_ids: не больше 500"
		}
		response.Error(w, http.StatusBadRequest, "VALIDATION", msg)
		return
	}
	page, err := h.Svc.NewBookings(r.Context(), h.now(), ids, req.Cursor, req.Limit)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("new bookings failed", "err", err)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	items := make([]newBookingDTO, 0, len(page.Rows))
	for _, row := range page.Rows {
		items = append(items, newBookingDTO{
			PlanningID:       row.PlanningID,
			PatientID:        row.PatientID,
			PatientPhone:     row.PatientPhone,
			PatientName:      row.PatientName,
			DoctorName:       displayDoctorName(row.DoctorName),
			Service:          row.Service,
			IsWaitlist:       row.IsWaitlist,
			MessengerNotify:  row.MessengerNotify,
			NoSMSNotify:      row.NoSMSNotify,
			ApptVersion:      row.ApptVersion,
			BranchID:         row.BranchID,
			BranchCode:       row.BranchCode,
			DateConsultation: row.DateConsultation.Format(reminderOutLayout),
			Status:           row.Status,
			CreatedAt:        row.CreatedAt,
		})
	}
	response.OK(w, newBookingsPageDTO{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}

type bookingNoticeDTO struct {
	newBookingDTO
	Cancelled bool `json:"cancelled"`
}

func (h NotificationsHandler) BookingByID(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("planning_id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "planning_id")
		return
	}
	row, err := h.Svc.BookingByPlanningID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		response.Error(w, http.StatusNotFound, "NOT_FOUND", "Запись не найдена")
		return
	}
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("booking by id failed", "err", err)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	response.OK(w, bookingNoticeDTO{
		newBookingDTO: newBookingDTO{
			PlanningID:       row.PlanningID,
			PatientID:        row.PatientID,
			PatientPhone:     row.PatientPhone,
			PatientName:      row.PatientName,
			DoctorName:       displayDoctorName(row.DoctorName),
			Service:          row.Service,
			IsWaitlist:       row.IsWaitlist,
			MessengerNotify:  row.MessengerNotify,
			NoSMSNotify:      row.NoSMSNotify,
			ApptVersion:      row.ApptVersion,
			BranchID:         row.BranchID,
			BranchCode:       row.BranchCode,
			DateConsultation: row.DateConsultation.Format(reminderOutLayout),
			Status:           row.Status,
			CreatedAt:        row.CreatedAt,
		},
		Cancelled: row.Cancelled,
	})
}
