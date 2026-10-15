package service

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/health"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type Services struct {
	MSSQL      *sql.DB
	MSSQLWrite *sql.DB
	SQLite     *sql.DB
	Repos      repo.Repos
	Logger     *slog.Logger

	DefaultModelsID int
	Config          config.Config
	WriteGuard      medialogwrite.Guard
	WriteLog        *medialogwrite.LogRepo
}

func New(mssql, mssqlWrite, sqlite *sql.DB, repos repo.Repos, logger *slog.Logger, defaultModelsID int, cfg config.Config) *Services {
	rwOK := mssqlWrite != nil
	guard := medialogwrite.NewGuard(
		cfg.MedialogWriteEnabled,
		rwOK,
		cfg.MedialogWriteAllowPatients,
		cfg.MedialogWriteAllowPlSubj,
	)
	return &Services{
		MSSQL: mssql, MSSQLWrite: mssqlWrite, SQLite: sqlite,
		Repos: repos, Logger: logger, DefaultModelsID: defaultModelsID, Config: cfg,
		WriteGuard: guard, WriteLog: medialogwrite.NewLogRepo(sqlite),
	}
}

func (s *Services) WriteGuardDeps() medialogwrite.HTTPDeps {
	return medialogwrite.HTTPDeps{Guard: s.WriteGuard, Lookup: medialogwrite.DBLookup{DB: s.MSSQL}, Log: s.WriteLog}
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
	db := s.MSSQLWrite
	if db == nil {
		return 0, false, medialogwrite.ErrWriteNotConfigured
	}
	res, err := repo.BookMe(ctx, db, s.Repos.Planning, s.Repos.Motconsu, planningID, patientID, s.DefaultModelsID, now)
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
	db := s.MSSQLWrite
	if db == nil {
		return medialogwrite.ErrWriteNotConfigured
	}
	return repo.CancelMe(ctx, db, motconsuID, patientID, time.Duration(minH)*time.Hour, now)
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
	db := s.MSSQLWrite
	if db == nil {
		return repo.BookResult{}, medialogwrite.ErrWriteNotConfigured
	}
	return repo.Book(ctx, db, s.Repos.Planning, s.Repos.Motconsu, in, s.DefaultModelsID)
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

func (s *Services) HealthSnapshot(ctx context.Context) health.Snapshot {
	out := health.Snapshot{API: "ok", Medialog: "ok"}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.MSSQL.PingContext(pingCtx); err != nil {
		out.Medialog = "unavailable"
	}
	return out
}

func (s *Services) MaxModifyDate(ctx context.Context) (time.Time, error) {
	return s.Repos.Motconsu.MaxModifyDate(ctx)
}

func (s *Services) PollAfter(ctx context.Context, last time.Time) ([]repo.PollRow, error) {
	return s.Repos.Motconsu.PollAfter(ctx, last)
}

func (s *Services) RemindersDue(ctx context.Context, from, to time.Time, patientIDs []int64) ([]repo.DueReminder, error) {
	return repo.DueReminders(ctx, s.MSSQL, from, to, patientIDs)
}

func ParseDefaultModelsID(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
