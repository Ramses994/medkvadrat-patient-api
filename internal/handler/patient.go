package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type PatientService interface {
	PatientSearch(ctx context.Context, phone string) ([]repo.PatientInfo, error)
	LabResults(ctx context.Context, patientID string, daysBack int) ([]repo.LabResult, error)
	LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error)
}

type PatientHandler struct {
	Svc    PatientService
	Logger *slog.Logger
}

type patientInfoDTO struct {
	PatientID int    `json:"patient_id"`
	FullName  string `json:"full_name"`
	Phone     string `json:"phone"`
	BirthDate string `json:"birth_date"`
}

func (h PatientHandler) Search(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Параметр phone обязателен")
		return
	}

	patients, err := h.Svc.PatientSearch(r.Context(), phone)
	if err != nil {
		h.Logger.Error("patient search failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	out := make([]patientInfoDTO, 0, len(patients))
	for _, p := range patients {
		dto := patientInfoDTO{
			PatientID: p.PatientID,
			FullName:  p.FullName,
			Phone:     p.Phone,
		}
		if p.BirthDate != nil {
			dto.BirthDate = p.BirthDate.Format("2006-01-02")
		}
		out = append(out, dto)
	}

	response.OK(w, out)
}

func (h PatientHandler) LabResults(w http.ResponseWriter, r *http.Request) {
	patientID := r.URL.Query().Get("patient_id")
	if patientID == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "patient_id обязателен")
		return
	}

	daysBack := 0
	if s := r.URL.Query().Get("days_back"); s != "" {
		n, _ := strconv.Atoi(s)
		daysBack = n
	}

	res, err := h.Svc.LabResults(r.Context(), patientID, daysBack)
	if err != nil {
		h.Logger.Error("lab results failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	out := make([]repo.LabResultDTO, 0, len(res))
	for _, x := range res {
		dto := repo.LabResultDTO{
			ResultID:    x.ResultID,
			PatdirecID:  x.PatdirecID,
			GroupName:   x.GroupName,
			Code:        x.Code,
			Name:        x.Name,
			Value:       x.Value,
			Unit:        x.Unit,
			Norms:       x.Norms,
			Method:      x.Method,
			ApprovedBy:  x.ApprovedBy,
			ReadyAt:     x.ReadyAt.Format("2006-01-02 15:04"),
			TestComment: x.TestComment,
		}
		if inRange, ok := repo.CheckInRangePublic(x.Value, x.Norms); ok {
			dto.InRange = &inRange
		}
		out = append(out, dto)
	}

	response.OK(w, out)
}

func (h PatientHandler) LabPanels(w http.ResponseWriter, r *http.Request) {
	patientID := r.URL.Query().Get("patient_id")
	if patientID == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "patient_id обязателен")
		return
	}

	daysBack := 0
	if s := r.URL.Query().Get("days_back"); s != "" {
		n, _ := strconv.Atoi(s)
		daysBack = n
	}

	panels, err := h.Svc.LabPanels(r.Context(), patientID, daysBack)
	if err != nil {
		h.Logger.Error("lab panels failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	response.OK(w, panels)
}
