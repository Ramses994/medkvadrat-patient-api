package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type DoctorService interface {
	Doctors(ctx context.Context) ([]repo.Doctor, error)
}

type DoctorHandler struct {
	Svc    DoctorService
	Logger *slog.Logger
}

type doctorDTO struct {
	DoctorID    int    `json:"doctor_id"`
	FullName    string `json:"full_name"`
	Specialty   string `json:"specialty"`
	IsAvailable bool   `json:"is_available"`
}

func (h DoctorHandler) List(w http.ResponseWriter, r *http.Request) {
	doctors, err := h.Svc.Doctors(r.Context())
	if err != nil {
		h.Logger.Error("doctors list failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	out := make([]doctorDTO, 0, len(doctors))
	for _, d := range doctors {
		out = append(out, doctorDTO{
			DoctorID:    d.DoctorID,
			FullName:    d.FullName,
			Specialty:   d.Specialty,
			IsAvailable: true,
		})
	}
	response.OK(w, out)
}
