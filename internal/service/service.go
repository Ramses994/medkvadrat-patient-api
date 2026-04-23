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
