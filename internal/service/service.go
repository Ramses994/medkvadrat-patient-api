package service

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type Services struct {
	MSSQL  *sql.DB
	SQLite *sql.DB
	Repos  repo.Repos
	Logger *slog.Logger

	DefaultModelsID int
	Config          config.Config
}

func New(mssql *sql.DB, sqlite *sql.DB, repos repo.Repos, logger *slog.Logger, defaultModelsID int, cfg config.Config) *Services {
	return &Services{MSSQL: mssql, SQLite: sqlite, Repos: repos, Logger: logger, DefaultModelsID: defaultModelsID, Config: cfg}
}

func (s *Services) Doctors(ctx context.Context) ([]repo.Doctor, error) {
	return s.Repos.Doctor.List(ctx)
}

func (s *Services) CatalogSpecialties(ctx context.Context) ([]repo.Specialty, error) {
	return s.Repos.Catalog.Specialties(ctx)
}

func (s *Services) CatalogDepartments(ctx context.Context) ([]repo.Department, error) {
	return s.Repos.Catalog.Departments(ctx)
}

func (s *Services) CatalogDoctors(ctx context.Context, specialtyID *int, meddepID *int) ([]repo.CatalogDoctor, error) {
	return s.Repos.Catalog.Doctors(ctx, specialtyID, meddepID)
}

func (s *Services) CatalogSlots(ctx context.Context, doctorID int, dateFrom, dateTo time.Time) ([]repo.Slot, error) {
	return s.Repos.Catalog.Slots(ctx, doctorID, dateFrom, dateTo)
}

func (s *Services) MeProfile(ctx context.Context, patientID int64) (repo.Profile, error) {
	return s.Repos.Me.Profile(ctx, patientID)
}

func (s *Services) MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.Appointment, error) {
	return s.Repos.Me.Appointments(ctx, patientID, now, kind)
}

func (s *Services) MeBookAppointment(ctx context.Context, patientID int64, planningID int, now time.Time) (motconsuID int64, restored bool, err error) {
	res, err := repo.BookMe(ctx, s.MSSQL, s.Repos.Planning, s.Repos.Motconsu, planningID, patientID, s.DefaultModelsID, now)
	if err != nil {
		return 0, false, err
	}
	return res.MotconsuID, res.Restored, nil
}

func (s *Services) MeCancelAppointment(ctx context.Context, patientID int64, motconsuID int64, now time.Time) error {
	minH := s.Config.CancelMinHoursBefore
	if minH <= 0 {
		minH = 24
	}
	return repo.CancelMe(ctx, s.MSSQL, motconsuID, patientID, time.Duration(minH)*time.Hour, now)
}

func (s *Services) PatientSearch(ctx context.Context, phone string) ([]repo.PatientInfo, error) {
	return s.Repos.Patient.SearchByPhone(ctx, repo.CleanPhoneLast10(phone))
}

func (s *Services) ScheduleChanges(ctx context.Context, since time.Time) ([]repo.ScheduleChange, error) {
	return s.Repos.Motconsu.ChangesSince(ctx, since)
}

func (s *Services) FreeSlots(ctx context.Context, doctorID, date string) ([]repo.FreeSlot, error) {
	return s.Repos.Planning.FreeSlots(ctx, doctorID, date)
}

func (s *Services) Book(ctx context.Context, in repo.BookInput) (repo.BookResult, error) {
	return repo.Book(ctx, s.MSSQL, s.Repos.Planning, s.Repos.Motconsu, in, s.DefaultModelsID)
}

func (s *Services) LabResults(ctx context.Context, patientID string, daysBack int) ([]repo.LabResult, error) {
	return s.Repos.Patient.LabResults(ctx, patientID, daysBack)
}

func (s *Services) LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error) {
	rows, err := s.Repos.Patient.LabPanels(ctx, patientID, daysBack)
	if err != nil {
		return nil, err
	}
	return repo.BuildLabPanels(rows), nil
}

func (s *Services) Ping(ctx context.Context) error {
	return s.Repos.Health.Ping(ctx)
}

func (s *Services) MaxModifyDate(ctx context.Context) (time.Time, error) {
	return s.Repos.Motconsu.MaxModifyDate(ctx)
}

func (s *Services) PollAfter(ctx context.Context, last time.Time) ([]repo.PollRow, error) {
	return s.Repos.Motconsu.PollAfter(ctx, last)
}

func ParseDefaultModelsID(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
