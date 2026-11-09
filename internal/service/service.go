package service

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
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
	book            *bookingState
	eng             *BookingEngine
	engOnce         sync.Once
	engAtomic       atomic.Pointer[BookingEngine]
	rwLang          atomic.Bool
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
		book: &bookingState{active: map[string]repo.BookingOption{}, slots: map[string]slotCache{}},
	}
}

func (s *Services) WriteGuardDeps() medialogwrite.HTTPDeps {
	return medialogwrite.HTTPDeps{
		Guard:  s.WriteGuard,
		Lookup: medialogwrite.DBLookup{DB: s.MSSQL},
		Log:    s.WriteLog,
		Logger: s.Logger,
	}
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

func (s *Services) MeProfile(ctx context.Context, patientID int64) (repo.Profile, error) {
	return s.Repos.Me.Profile(ctx, patientID)
}

func (s *Services) MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.PlanningVisit, error) {
	rows, err := s.Repos.Me.PlanningAppointments(ctx, patientID, now, kind)
	if err != nil {
		return nil, err
	}
	return s.attachConfirmations(ctx, rows)
}

func (s *Services) MeVisits(ctx context.Context, patientID int64, from, to time.Time) ([]repo.PlanningVisit, error) {
	rows, err := s.Repos.Me.PlanningVisits(ctx, patientID, from, to)
	if err != nil {
		return nil, err
	}
	return s.attachConfirmations(ctx, rows)
}

func (s *Services) attachConfirmations(ctx context.Context, rows []repo.PlanningVisit) ([]repo.PlanningVisit, error) {
	if len(rows) == 0 || s.SQLite == nil {
		return rows, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PlanningID)
	}
	overlay, err := confirmations.NewRepo(s.SQLite).OverlayByPlanningIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		o, ok := overlay[rows[i].PlanningID]
		if !ok || o.Status == "" || o.Version == "" {
			continue
		}
		current := repo.ApptVersion(rows[i].StartsAtFull, rows[i].PlSubjID, rows[i].PlExamID)
		if o.Version != current {
			continue
		}
		status := o.Status
		rows[i].Confirmation = &status
	}
	return rows, nil
}

func (s *Services) PatientSearch(ctx context.Context, phone string) ([]repo.PatientInfo, error) {
	return s.Repos.Patient.SearchByPhone(ctx, repo.CleanPhoneLast10(phone))
}

func (s *Services) ScheduleChanges(ctx context.Context, since time.Time) ([]repo.ScheduleChange, error) {
	return s.Repos.Motconsu.ChangesSince(ctx, since)
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
	out := health.Snapshot{API: "ok", Medialog: "ok", WriteJournal: "ok"}
	if eng := s.engAtomic.Load(); eng != nil && eng.journalDown.Load() {
		out.WriteJournal = "degraded"
	}
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
	return repo.DueReminders(ctx, s.MSSQL, from, to, patientIDs, s.Config.WaitlistExamIDs)
}

func (s *Services) NewBookings(ctx context.Context, now time.Time, patientIDs []int64, cursor *repo.BookingCursor, limit int) (repo.NewBookingsPage, error) {
	return repo.NewBookings(ctx, s.MSSQL, now, patientIDs, cursor, limit, s.Config.WaitlistExamIDs)
}

func (s *Services) BookingByPlanningID(ctx context.Context, planningID int64) (repo.BookingNotice, error) {
	return repo.BookingByPlanningID(ctx, s.MSSQL, planningID, s.Config.WaitlistExamIDs)
}

func ParseDefaultModelsID(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
