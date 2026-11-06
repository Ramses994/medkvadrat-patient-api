package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type fakeCatalogSvc struct{}

func (fakeCatalogSvc) CatalogSpecialties(ctx context.Context) ([]repo.Specialty, error) {
	return []repo.Specialty{{SpecialtyID: 3, Code: "001", Label: "Аллергология-Иммунология"}}, nil
}
func (fakeCatalogSvc) CatalogDepartments(ctx context.Context) ([]repo.Department, error) {
	return []repo.Department{{DepartmentID: 10, Code: "DEP", Label: "Отделение"}}, nil
}
func (fakeCatalogSvc) CatalogDoctors(ctx context.Context, specialtyID *int, meddepID *int) ([]repo.CatalogDoctor, error) {
	return []repo.CatalogDoctor{{DoctorID: 7, FullName: "Иван Петров", SpecialtyID: 3, Specialty: "Аллергология-Иммунология", MeddepID: 77}}, nil
}

func TestGolden_Catalog_Specialties_OK(t *testing.T) {
	h := CatalogHandler{Svc: fakeCatalogSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/catalog/specialties", nil)
	w := httptest.NewRecorder()
	h.Specialties(w, r)
	assertGolden(t, "catalog_specialties_ok", w.Body.Bytes())
}

func TestGolden_Catalog_Departments_OK(t *testing.T) {
	h := CatalogHandler{Svc: fakeCatalogSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/catalog/departments", nil)
	w := httptest.NewRecorder()
	h.Departments(w, r)
	assertGolden(t, "catalog_departments_ok", w.Body.Bytes())
}

func TestGolden_Catalog_Doctors_OK(t *testing.T) {
	h := CatalogHandler{Svc: fakeCatalogSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/catalog/doctors?specialty_id=3&meddep_id=77", nil)
	w := httptest.NewRecorder()
	h.Doctors(w, r)
	assertGolden(t, "catalog_doctors_ok", w.Body.Bytes())
}
