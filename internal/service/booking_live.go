package service

import (
	"context"
	"database/sql"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

func (s *Services) engine() *BookingEngine {
	s.engOnce.Do(func() {
		s.eng = s.newEngine()
		s.engAtomic.Store(s.eng)
	})
	return s.eng
}

func (s *Services) newEngine() *BookingEngine {
	return &BookingEngine{
		Ops:        writeops.New(s.SQLite),
		Clinic:     liveClinic{s},
		Procs:      liveProcs{db: s.MSSQLWrite},
		Locks:      &booking.Locks{},
		Cfg:        s.Config,
		Guard:      s.WriteGuard,
		Log:        s.Logger,
		LangOK:     func() bool { return s.rwLang.Load() },
		Invalidate: func(id int64) { s.InvalidateSlotCache(id) },
	}
}

func (s *Services) BookSlot(ctx context.Context, patientID int64, token, requestID string) WriteHTTP {
	return s.engine().Book(ctx, patientID, token, requestID)
}

func (s *Services) CancelBooking(ctx context.Context, patientID, planningID int64, requestID string) WriteHTTP {
	return s.engine().Cancel(ctx, patientID, planningID, requestID)
}

func (s *Services) ListWriteOps(ctx context.Context, states []string) ([]writeops.Op, error) {
	return s.engine().ListOpen(ctx, states)
}

func (s *Services) ResolveWriteOp(ctx context.Context, in ResolveInput) WriteHTTP {
	return s.engine().Resolve(ctx, in)
}

func (s *Services) SweepWriteOps(ctx context.Context) { s.engine().Sweep(ctx) }

// StartWriteMaintenance is the process task main starts before it accepts booking requests.
func (s *Services) StartWriteMaintenance(ctx context.Context) {
	if s == nil || s.SQLite == nil {
		return
	}
	s.engine().StartMaintenance(ctx)
}

func (s *Services) RefreshRWLanguage(ctx context.Context) {
	if s.MSSQLWrite == nil {
		s.rwLang.Store(false)
		return
	}
	lang, err := repo.RWLanguage(ctx, s.MSSQLWrite)
	if err != nil || !booking.LanguageOK(lang) {
		s.rwLang.Store(false)
		if err != nil {
			s.log().Error("lk rw language")
		} else {
			s.log().Error("lk rw language", "language", lang)
		}
		return
	}
	s.rwLang.Store(true)
}

type liveClinic struct{ s *Services }

func (c liveClinic) Pair(ctx context.Context, subj, exam int64) (repo.BookingOption, bool) {
	return c.s.activePair(ctx, subj, exam)
}

func (c liveClinic) DayStarts(ctx context.Context, subj, exam int64, day string) ([]string, error) {
	return repo.LoadDaySlots(ctx, c.s.MSSQL, subj, exam, day)
}

func (c liveClinic) FutureCount(ctx context.Context, patient, author int64, nowMSK string) (int, error) {
	return repo.FutureLKCount(ctx, c.s.MSSQL, patient, author, nowMSK)
}

func (c liveClinic) DoctorBusy(ctx context.Context, patient, medecins, plSubj int64, day string) (bool, error) {
	return repo.DoctorDayBusy(ctx, c.s.MSSQL, patient, medecins, plSubj, day)
}

func (c liveClinic) ReadCreated(ctx context.Context, id int64) (repo.CreatedRow, error) {
	return repo.ReadCreated(ctx, c.s.MSSQL, id)
}

func (c liveClinic) FindIntent(ctx context.Context, patient, subj, exam, author int64, start, since string) (int64, bool, error) {
	return repo.FindReconcile(ctx, c.s.MSSQL, patient, subj, exam, author, start, since)
}

func (c liveClinic) Owner(ctx context.Context, id int64) (repo.OwnerRow, error) {
	return repo.LoadOwner(ctx, c.s.MSSQL, id)
}

func (c liveClinic) Admission(ctx context.Context, id int64) (booking.Admission, error) {
	return repo.LoadAdmission(ctx, c.s.MSSQL, id)
}

func (c liveClinic) Snap(ctx context.Context, planning int64, answ, dir *int64) (booking.Snap, error) {
	return repo.LoadSnap(ctx, c.s.MSSQL, planning, answ, dir)
}

type liveProcs struct{ db *sql.DB }

func (p liveProcs) AddVisit(ctx context.Context, in repo.AddVisitParams) repo.ProcResult {
	return repo.ExecAddVisit(ctx, p.db, in)
}

func (p liveProcs) CancelVisit(ctx context.Context, in repo.CancelParams) repo.ProcResult {
	return repo.ExecCancelVisit(ctx, p.db, in)
}
