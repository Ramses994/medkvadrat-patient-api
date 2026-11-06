package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type CatalogService interface {
	CatalogSpecialties(ctx context.Context) ([]repo.Specialty, error)
	CatalogDepartments(ctx context.Context) ([]repo.Department, error)
	CatalogDoctors(ctx context.Context, specialtyID *int, meddepID *int) ([]repo.CatalogDoctor, error)
}

type CatalogHandler struct {
	Svc    CatalogService
	Logger *slog.Logger
}

type specialtyDTO struct {
	SpecialtyID int    `json:"specialty_id"`
	Code        string `json:"code"`
	Label       string `json:"label"`
}

func (h CatalogHandler) Specialties(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Svc.CatalogSpecialties(r.Context())
	if err != nil {
		h.Logger.Error("catalog specialties failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]specialtyDTO, 0, len(rows))
	for _, s := range rows {
		out = append(out, specialtyDTO{SpecialtyID: s.SpecialtyID, Code: s.Code, Label: s.Label})
	}
	response.OK(w, out)
}

type departmentDTO struct {
	DepartmentID int    `json:"department_id"`
	Code         string `json:"code"`
	Label        string `json:"label"`
}

func (h CatalogHandler) Departments(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Svc.CatalogDepartments(r.Context())
	if err != nil {
		h.Logger.Error("catalog departments failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]departmentDTO, 0, len(rows))
	for _, d := range rows {
		out = append(out, departmentDTO{DepartmentID: d.DepartmentID, Code: d.Code, Label: d.Label})
	}
	response.OK(w, out)
}

type catalogDoctorDTO struct {
	DoctorID    int    `json:"doctor_id"`
	FullName    string `json:"full_name"`
	SpecialtyID int    `json:"specialty_id"`
	Specialty   string `json:"specialty"`
	MeddepID    int    `json:"meddep_id"`
}

func (h CatalogHandler) Doctors(w http.ResponseWriter, r *http.Request) {
	var specID *int
	if s := r.URL.Query().Get("specialty_id"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "specialty_id должен быть числом")
			return
		}
		specID = &n
	}
	var meddepID *int
	if s := r.URL.Query().Get("meddep_id"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "meddep_id должен быть числом")
			return
		}
		meddepID = &n
	}

	rows, err := h.Svc.CatalogDoctors(r.Context(), specID, meddepID)
	if err != nil {
		h.Logger.Error("catalog doctors failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]catalogDoctorDTO, 0, len(rows))
	for _, d := range rows {
		out = append(out, catalogDoctorDTO{
			DoctorID: d.DoctorID, FullName: d.FullName, SpecialtyID: d.SpecialtyID, Specialty: d.Specialty, MeddepID: d.MeddepID,
		})
	}
	response.OK(w, out)
}
