package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/slottoken"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

// ErrStopped marks a crash between journal insert and the stored result. No procedure is repeated.
var ErrStopped = errors.New("stopped before result")

type (
	WriteHTTP    = booking.WriteHTTP
	ResolveInput = booking.ResolveInput
)

type clinicFace interface {
	Pair(ctx context.Context, subj, exam int64) (repo.BookingOption, bool)
	DayStarts(ctx context.Context, subj, exam int64, day string) ([]string, error)
	FutureCount(ctx context.Context, patient, author int64, nowMSK string) (int, error)
	DoctorBusy(ctx context.Context, patient, medecins, plSubj int64, day string) (bool, error)
	ReadCreated(ctx context.Context, id int64) (repo.CreatedRow, error)
	FindIntent(ctx context.Context, patient, subj, exam, author int64, start, since string) (int64, bool, error)
	Owner(ctx context.Context, id int64) (repo.OwnerRow, error)
	Admission(ctx context.Context, id int64) (booking.Admission, error)
	Snap(ctx context.Context, planning int64, answ, dir *int64) (booking.Snap, error)
}

type procFace interface {
	AddVisit(ctx context.Context, p repo.AddVisitParams) repo.ProcResult
	CancelVisit(ctx context.Context, p repo.CancelParams) repo.ProcResult
}

// BookingEngine books and cancels through the approved procedures. One process owns the locks.
type BookingEngine struct {
	Ops            *writeops.Repo
	Clinic         clinicFace
	Procs          procFace
	Locks          *booking.Locks
	Cfg            config.Config
	Guard          medialogwrite.Guard
	Log            *slog.Logger
	Now            func() time.Time
	LangOK         func() bool
	Invalidate     func(plSubjID int64)
	AfterInFlight  func() error
	BeforeLineLock func()
	BeforeProc     func()
	AfterProc      func() error
	procCalls      atomic.Int32
	recMu          sync.Mutex
	lastReconcile  map[string]time.Time
	journalLogAt   map[string]time.Time
	journalDown    atomic.Bool
	liveMu         sync.Mutex
	live           map[string]struct{}
}

func (e *BookingEngine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *BookingEngine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

func (e *BookingEngine) fail(status int, code, msg string) WriteHTTP {
	if msg == "" {
		msg = booking.Message(code)
	}
	if status == 0 {
		status = booking.StatusFor(code)
	}
	return WriteHTTP{Status: status, Code: code, Message: msg}
}

type cancelContext struct {
	OrgID      int           `json:"org_id"`
	Variant    string        `json:"variant"`
	DirAnswID  *int64        `json:"dir_answ_id"`
	PatdirecID *int64        `json:"patdirec_id"`
	Before     booking.Snap  `json:"before"`
	After      *booking.Snap `json:"after,omitempty"`
}

func (e *BookingEngine) Book(ctx context.Context, patientID int64, token, requestID string) WriteHTTP {
	if e.Ops == nil || e.Ops.DB == nil {
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	if !slottoken.SecretOK(e.Cfg.SlotTokenSecret) {
		e.log().Error("slot token secret not configured")
		return e.fail(503, "SLOT_TOKEN_UNAVAILABLE", "Запись временно недоступна")
	}
	now := e.now()
	claim, err := slottoken.Parse(e.Cfg.SlotTokenSecret, token, now, patientID, false)
	if err != nil {
		return e.tokenErr(err)
	}
	intent := writeops.BookIntent(claim.PlSubjID, claim.PlExamID, claim.Start)
	if out, done := e.lookup(ctx, patientID, writeops.ActionBook, requestID, intent); done {
		return out
	}
	if out := e.refuseJournal(); out.Status != 0 {
		return out
	}
	if _, err := slottoken.Parse(e.Cfg.SlotTokenSecret, token, now, patientID, true); err != nil {
		return e.tokenErr(err)
	}
	if out := e.preflight(patientID, claim.PlSubjID); out.Status != 0 {
		return out
	}
	opt, ok := e.Clinic.Pair(ctx, claim.PlSubjID, claim.PlExamID)
	if !ok {
		return e.fail(404, "NOT_FOUND", "Эта запись недоступна")
	}
	unlockP := e.Locks.Patient(patientID)
	defer unlockP()
	if out, done := e.lookup(ctx, patientID, writeops.ActionBook, requestID, intent); done {
		return out
	}
	if open, err := e.Ops.HasOpen(ctx, patientID); err != nil {
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	} else if open {
		return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
	}
	msk := moscowWall(now)
	n, err := e.Clinic.FutureCount(ctx, patientID, e.Cfg.LKAuthorMedecinsID, msk)
	if err != nil {
		return e.fail(500, "INTERNAL", booking.MsgRegistry)
	}
	if n >= e.Cfg.BookingMaxFuture {
		return e.fail(422, "LIMIT_FUTURE", booking.MsgLimitFuture)
	}
	day := claim.Start
	if len(day) >= 10 {
		day = day[:10]
	}
	busy, err := e.Clinic.DoctorBusy(ctx, patientID, opt.MedecinsID, claim.PlSubjID, day)
	if err != nil {
		return e.fail(500, "INTERNAL", booking.MsgRegistry)
	}
	if busy {
		return e.fail(422, "LIMIT_DOCTOR_DAY", booking.MsgDoctorDay)
	}
	unlockL := e.Locks.Line(claim.PlSubjID)
	defer unlockL()
	free, slotErr := e.slotFree(ctx, claim.PlSubjID, claim.PlExamID, claim.Start, now)
	if slotErr != nil {
		return e.fail(503, "SLOTS_UNAVAILABLE", "Свободное время временно недоступно")
	}
	if !free {
		out := e.fail(409, "SLOT_TAKEN", booking.MsgSlotTaken)
		out.Slots = e.freshOffers(ctx, opt, claim.Start, now)
		return out
	}
	if out := e.refuseJournal(); out.Status != 0 {
		return out
	}
	opID := writeops.NewID()
	e.beginLive(opID)
	defer e.endLive(opID)
	op := e.newOp(patientID, writeops.ActionBook, requestID, intent, `{}`, now)
	op.OpID = opID
	owned, out, ok := e.insertOrReplay(ctx, op, intent)
	if !ok {
		return out
	}
	if e.AfterInFlight != nil {
		if err := e.AfterInFlight(); err != nil {
			return WriteHTTP{}
		}
	}
	if alt := e.saveContext(ctx, owned, mustJSON(map[string]any{"org_id": opt.BranchID}), sql.NullInt64{}); alt.Status != 0 {
		return alt
	}
	if e.BeforeProc != nil {
		e.BeforeProc()
	}
	callCtx, cancel := context.WithTimeout(ctx, e.Cfg.MedialogRWTimeout)
	defer cancel()
	res := e.Procs.AddVisit(callCtx, repo.AddVisitParams{
		PatientID: patientID, PlSubjID: claim.PlSubjID, PlExamID: claim.PlExamID,
		VisitStart: claim.Start, OrgID: opt.BranchID, MeddepID: e.Cfg.LKAuthorMeddepID, AuthorID: e.Cfg.LKAuthorMedecinsID,
	})
	e.procCalls.Add(1)
	if e.AfterProc != nil {
		if err := e.AfterProc(); err != nil {
			return WriteHTTP{}
		}
	}
	outcome := bookOutcome(res)
	if outcome.LogProcError {
		e.log().Error("lk booking proc error", "op_id", owned.OpID)
	}
	if outcome.LogUnmapped {
		e.log().Error("lk booking unmapped result", "op_id", owned.OpID, "result_len", outcome.ResultLen)
	}
	var planningID sql.NullInt64
	if outcome.State == booking.StateCandidate {
		row, err := e.Clinic.ReadCreated(ctx, *res.PlanningID)
		if err != nil || !createdMatches(row, patientID, claim.PlSubjID, claim.PlExamID, e.Cfg.LKAuthorMedecinsID, claim.Start) {
			e.log().Error("lk booking confirm mismatch", "op_id", owned.OpID)
			outcome = booking.Outcome{Code: "BOOKING_UNKNOWN", HTTPStatus: 503, State: booking.StateUnknown}
		} else {
			planningID = sql.NullInt64{Int64: *res.PlanningID, Valid: true}
			if alt := e.finish(ctx, owned, booking.StateOK, "", planningID, owned.ContextJSON, now); alt.Status != 0 {
				return alt
			}
			if e.Invalidate != nil {
				e.Invalidate(claim.PlSubjID)
			}
			return WriteHTTP{Status: 201, Body: bookBody(*res.PlanningID, row, opt, claim.Start)}
		}
	}
	if res.PlanningID != nil && outcome.State != booking.StateOK {
		planningID = sql.NullInt64{Int64: *res.PlanningID, Valid: true}
	}
	if alt := e.finish(ctx, owned, outcome.State, outcome.Code, planningID, owned.ContextJSON, now); alt.Status != 0 {
		return alt
	}
	out = e.fail(outcome.HTTPStatus, outcome.Code, "")
	if outcome.Code == "SLOT_TAKEN" || outcome.Code == "SLOT_UNAVAILABLE" {
		out.Slots = e.freshOffers(ctx, opt, claim.Start, now)
	}
	return out
}

func bookBody(planningID int64, row repo.CreatedRow, opt repo.BookingOption, start string) map[string]any {
	body := map[string]any{
		"planning_id": planningID,
		"start":       start,
		"doctor_name": opt.DoctorName,
		"service":     opt.Service,
		"branch_id":   opt.BranchID,
		"branch_code": opt.BranchCode,
	}
	if row.NotAccepted == 1 {
		body["awaiting_confirmation"] = true
	}
	return body
}

func createdMatches(row repo.CreatedRow, patient, subj, exam, author int64, start string) bool {
	return row.Found && row.PatientID == patient && row.PlSubjID == subj && row.PlExamID == exam &&
		row.CreatorID == author && row.DateStart == start && row.Cancelled == 0 && row.Status == 0
}

func bookOutcome(res repo.ProcResult) booking.Outcome {
	if res.Precondition || !res.Sent {
		return booking.Outcome{Code: "WRITE_UNAVAILABLE", HTTPStatus: 503, State: booking.StateRejected}
	}
	if !res.Finished || !res.HasRow {
		return booking.Outcome{Code: "BOOKING_UNKNOWN", HTTPStatus: 503, State: booking.StateUnknown}
	}
	return booking.Classify(res.PlanningID, res.Result, res.State)
}

func (e *BookingEngine) slotFree(ctx context.Context, subj, exam int64, start string, now time.Time) (bool, error) {
	day := start
	if len(day) >= 10 {
		day = day[:10]
	}
	starts, err := e.Clinic.DayStarts(ctx, subj, exam, day)
	if err != nil {
		return false, err
	}
	for _, s := range starts {
		if s == start && repo.KeepSlot(s, now, e.Cfg.BookingMinHoursAhead, e.Cfg.BookingMaxDays) {
			return true, nil
		}
	}
	return false, nil
}

func (e *BookingEngine) freshOffers(ctx context.Context, opt repo.BookingOption, start string, now time.Time) []booking.SlotRef {
	day := start
	if len(day) >= 10 {
		day = day[:10]
	}
	starts, err := e.Clinic.DayStarts(ctx, opt.PlSubjID, opt.PlExamID, day)
	if err != nil {
		return nil
	}
	var out []booking.SlotRef
	for _, s := range starts {
		if !repo.KeepSlot(s, now, e.Cfg.BookingMinHoursAhead, e.Cfg.BookingMaxDays) {
			continue
		}
		out = append(out, booking.SlotRef{Start: s, DurationMin: opt.DurationMin})
	}
	return out
}

func (e *BookingEngine) Cancel(ctx context.Context, patientID, planningID int64, requestID string) WriteHTTP {
	if e.Ops == nil || e.Ops.DB == nil {
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	owner, err := e.Clinic.Owner(ctx, planningID)
	if err != nil {
		return e.fail(500, "INTERNAL", booking.MsgCancelSupport)
	}
	if !owner.Found || owner.PatientID != patientID {
		return e.fail(404, "NOT_FOUND", "Запись не найдена")
	}
	intent := writeops.CancelIntent(planningID)
	if out, done := e.lookup(ctx, patientID, writeops.ActionCancel, requestID, intent); done {
		return out
	}
	if out := e.refuseJournal(); out.Status != 0 {
		return out
	}
	if out := e.preflight(patientID, owner.PlSubjID); out.Status != 0 {
		return out
	}
	now := e.now()
	unlockP := e.Locks.Patient(patientID)
	defer unlockP()
	if out, done := e.lookup(ctx, patientID, writeops.ActionCancel, requestID, intent); done {
		return out
	}
	if open, err := e.Ops.HasOpen(ctx, patientID); err != nil {
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	} else if open {
		return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
	}
	fresh, err := e.Clinic.Owner(ctx, planningID)
	if err != nil || !fresh.Found || fresh.PatientID != patientID {
		return e.fail(404, "NOT_FOUND", "Запись не найдена")
	}
	if fresh.PlSubjID != owner.PlSubjID || e.Guard.CheckPlSubj(fresh.PlSubjID) != nil {
		return e.fail(409, "CANCEL_NOT_SUPPORTED", booking.MsgCancelSupport)
	}
	if e.BeforeLineLock != nil {
		e.BeforeLineLock()
	}
	unlockL := e.Locks.Line(fresh.PlSubjID)
	defer unlockL()
	again, err := e.Clinic.Owner(ctx, planningID)
	if err != nil || !again.Found || again.PatientID != patientID || again.PlSubjID != fresh.PlSubjID || e.Guard.CheckPlSubj(again.PlSubjID) != nil || again.Cancelled != 0 || again.Status != 0 {
		return e.fail(409, "CANCEL_NOT_SUPPORTED", booking.MsgCancelSupport)
	}
	if out, ok := e.cancelGate(ctx, planningID, again, now); !ok {
		return out
	}
	v1, s1, out, ok := e.loadCancelSnap(ctx, planningID)
	if !ok {
		return out
	}
	v2, s2, out, ok := e.loadCancelSnap(ctx, planningID)
	if !ok {
		return out
	}
	if v1.variant != v2.variant {
		return e.fail(409, "CANCEL_NOT_SUPPORTED", booking.MsgCancelSupport)
	}
	if code, allow := booking.SnapAllows(s1, s2); !allow {
		msg := booking.MsgCancelSupport
		if code == "ALREADY_CANCELLED" {
			msg = booking.MsgAlreadyCancel
		}
		return e.fail(409, code, msg)
	}
	if out := e.refuseJournal(); out.Status != 0 {
		return out
	}
	cc := cancelContext{OrgID: again.OrgID, Variant: v2.variant, DirAnswID: v2.answ, PatdirecID: v2.dir, Before: s2}
	opID := writeops.NewID()
	e.beginLive(opID)
	defer e.endLive(opID)
	op := e.newOp(patientID, writeops.ActionCancel, requestID, intent, `{}`, now)
	op.OpID = opID
	op.PlanningID = sql.NullInt64{Int64: planningID, Valid: true}
	owned, out, ok := e.insertOrReplay(ctx, op, intent)
	if !ok {
		return out
	}
	if e.AfterInFlight != nil {
		if err := e.AfterInFlight(); err != nil {
			return WriteHTTP{}
		}
	}
	if alt := e.saveContext(ctx, owned, mustJSON(cc), op.PlanningID); alt.Status != 0 {
		return alt
	}
	if e.BeforeProc != nil {
		e.BeforeProc()
	}
	callCtx, cancel := context.WithTimeout(ctx, e.Cfg.MedialogRWTimeout)
	defer cancel()
	res := e.Procs.CancelVisit(callCtx, repo.CancelParams{
		PatientID: patientID, PlanningID: planningID, OrgID: again.OrgID,
		MeddepID: e.Cfg.LKAuthorMeddepID, AuthorID: e.Cfg.LKAuthorMedecinsID,
	})
	e.procCalls.Add(1)
	if e.AfterProc != nil {
		if err := e.AfterProc(); err != nil {
			return WriteHTTP{}
		}
	}
	if res.Precondition || !res.Sent {
		if alt := e.finish(ctx, owned, booking.StateRejected, "WRITE_UNAVAILABLE", op.PlanningID, owned.ContextJSON, now); alt.Status != 0 {
			return alt
		}
		return e.fail(503, "WRITE_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	return e.finishCancel(ctx, owned, cc, planningID, again.PlSubjID, res.Finished, now)
}

func (e *BookingEngine) finishCancel(ctx context.Context, op *writeops.Op, cc cancelContext, planningID, plSubj int64, callFinished bool, now time.Time) WriteHTTP {
	after, err := e.Clinic.Snap(ctx, planningID, cc.DirAnswID, cc.PatdirecID)
	if err != nil || !after.ReadOK {
		if alt := e.finish(ctx, op, booking.StateUnknown, "CANCEL_UNKNOWN", sql.NullInt64{Int64: planningID, Valid: true}, op.ContextJSON, now); alt.Status != 0 {
			return alt
		}
		return e.fail(503, "CANCEL_UNKNOWN", booking.MsgCancelUnknown)
	}
	cc.After = &after
	pred := booking.Eval(planningID, cc.Variant, cc.Before, after)
	state, code := booking.CancelOutcome(callFinished, pred, true)
	if alt := e.finish(ctx, op, state, code, sql.NullInt64{Int64: planningID, Valid: true}, mustJSON(cc), now); alt.Status != 0 {
		return alt
	}
	if state == booking.StateOK {
		if e.Invalidate != nil {
			e.Invalidate(plSubj)
		}
		return WriteHTTP{Status: 200, Body: map[string]any{"planning_id": planningID}}
	}
	if pred.External && state == booking.StatePartial {
		e.log().Error("lk cancel external change", "op_id", op.OpID, "planning_id", planningID)
	}
	if state == booking.StatePartial {
		e.log().Error("lk cancel partial", "op_id", op.OpID, "planning_id", planningID)
		return e.fail(503, "CANCEL_PARTIAL", booking.MsgCancelPartial)
	}
	if state == booking.StateRejected {
		return e.fail(503, "CANCEL_FAILED", booking.MsgCancelFailed)
	}
	return e.fail(503, "CANCEL_UNKNOWN", booking.MsgCancelUnknown)
}

func (e *BookingEngine) cancelGate(ctx context.Context, planningID int64, owner repo.OwnerRow, now time.Time) (WriteHTTP, bool) {
	if owner.Cancelled != 0 || owner.Status != 0 {
		return e.fail(409, "ALREADY_CANCELLED", booking.MsgAlreadyCancel), false
	}
	at, err := time.ParseInLocation(repo.SlotStartLayout, owner.DateStart, time.FixedZone("MSK", 3*3600))
	if err != nil {
		return e.fail(409, "CANCEL_NOT_SUPPORTED", booking.MsgCancelSupport), false
	}
	minH := e.Cfg.CancelMinHoursBefore
	if minH <= 0 {
		minH = 24
	}
	if at.Before(now.In(at.Location()).Add(time.Duration(minH) * time.Hour)) {
		return e.fail(422, "CANCEL_TOO_LATE", booking.Message("CANCEL_TOO_LATE")), false
	}
	return WriteHTTP{}, true
}

func savedIDs(variant string, adm booking.Admission) (answ, dir *int64) {
	if variant != "one" || len(adm.Answers) != 1 {
		return nil, nil
	}
	a := adm.Answers[0].ID
	answ = &a
	if adm.OwnDirection != nil {
		d := adm.OwnDirection.ID
		dir = &d
	}
	return answ, dir
}

func (e *BookingEngine) preflight(patientID, plSubj int64) WriteHTTP {
	if deny := e.Guard.CheckEnabled(); deny != nil {
		return e.fail(deny.HTTPStatus, deny.Code, deny.Message)
	}
	if deny := e.Guard.CheckPatient(patientID); deny != nil {
		return e.fail(deny.HTTPStatus, deny.Code, deny.Message)
	}
	if deny := e.Guard.CheckPlSubj(plSubj); deny != nil {
		return e.fail(deny.HTTPStatus, deny.Code, deny.Message)
	}
	if deny := e.Guard.CheckRWConfigured(); deny != nil {
		return e.fail(deny.HTTPStatus, deny.Code, deny.Message)
	}
	if e.LangOK == nil || !e.LangOK() || e.Cfg.LKAuthorMeddepID <= 0 || e.Cfg.LKAuthorMedecinsID <= 0 {
		return e.fail(403, "WRITE_DISABLED", medialogwrite.PatientForbiddenMessage)
	}
	return WriteHTTP{}
}

func (e *BookingEngine) tokenErr(err error) WriteHTTP {
	switch {
	case errors.Is(err, slottoken.ErrSecret):
		e.log().Error("slot token secret not configured")
		return e.fail(503, "SLOT_TOKEN_UNAVAILABLE", "Запись временно недоступна")
	case errors.Is(err, slottoken.ErrExpired):
		return e.fail(401, "SLOT_TOKEN", "Слот устарел. Обновите время и выберите снова")
	default:
		return e.fail(401, "SLOT_TOKEN", "Некорректный слот")
	}
}

func (e *BookingEngine) lookup(ctx context.Context, patientID int64, action, requestID, intent string) (WriteHTTP, bool) {
	ex, err := e.Ops.Find(ctx, patientID, action, requestID)
	if err != nil {
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage), true
	}
	if ex == nil {
		return WriteHTTP{}, false
	}
	return e.replay(ex, intent), true
}

func (e *BookingEngine) replay(op *writeops.Op, intent string) WriteHTTP {
	if op.IntentJSON != intent {
		return e.fail(422, "IDEMPOTENCY_MISMATCH", "Ключ уже использован для другой операции")
	}
	switch op.State {
	case writeops.StateOK, writeops.StateManualOK:
		pid := int64(0)
		if op.PlanningID.Valid {
			pid = op.PlanningID.Int64
		}
		if op.Action == writeops.ActionCancel {
			return WriteHTTP{Status: 200, Body: map[string]any{"planning_id": pid}}
		}
		return WriteHTTP{Status: 201, Body: map[string]any{"planning_id": pid}}
	case writeops.StateRejected:
		return e.fail(booking.StatusFor(op.Code), op.Code, booking.Message(op.Code))
	case writeops.StateManualRejected:
		return e.fail(409, "OPERATION_NOT_DONE", booking.MsgNotDone)
	default:
		return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
	}
}

func (e *BookingEngine) newOp(patient int64, action, requestID, intent, context string, now time.Time) writeops.Op {
	return writeops.Op{
		PatientID: patient, Action: action, RequestID: requestID, IntentJSON: intent, ContextJSON: context,
		StartedAt: now.UTC().Format(time.RFC3339), StartedAtMSK: moscowWall(now),
	}
}

func (e *BookingEngine) insertOrReplay(ctx context.Context, op writeops.Op, intent string) (*writeops.Op, WriteHTTP, bool) {
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		if !errors.Is(err, writeops.ErrConflict) {
			e.noteWrite(err)
			e.journalErr("insert", op.OpID, err)
			return nil, e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage), false
		}
		ex, ferr := e.Ops.Find(ctx, op.PatientID, op.Action, op.RequestID)
		if ferr != nil || ex == nil {
			return nil, e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage), false
		}
		return nil, e.replay(ex, intent), false
	}
	e.noteWrite(nil)
	saved, err := e.Ops.Find(ctx, op.PatientID, op.Action, op.RequestID)
	if err != nil || saved == nil {
		return nil, e.fail(503, "WRITE_LOG_UNAVAILABLE", medialogwrite.PatientForbiddenMessage), false
	}
	return saved, WriteHTTP{}, true
}

// finish writes the outcome. An empty status means this call won the CAS and the caller answers from that outcome.
// A lost CAS is answered from the row that is actually stored.
func (e *BookingEngine) finish(ctx context.Context, op *writeops.Op, state, code string, planning sql.NullInt64, contextJSON string, now time.Time) WriteHTTP {
	finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ok, err := e.Ops.Transition(finCtx, op.OpID, writeops.StateInFlight, writeops.Patch{
		State: state, Code: code, PlanningID: planning, ContextJSON: contextJSON, FinishedAt: now.UTC().Format(time.RFC3339),
	})
	if err != nil {
		e.noteWrite(err)
		e.journalErr("finalize", op.OpID, err)
		e.log().Error("lk write op finalize failed", "op_id", op.OpID, "action", op.Action, "code", code)
		return e.fail(503, unknownCode(op.Action), booking.Message(unknownCode(op.Action)))
	}
	e.noteWrite(nil)
	if ok {
		return WriteHTTP{}
	}
	fresh, err := e.Ops.Get(finCtx, op.OpID)
	if err != nil || fresh == nil {
		e.log().Error("lk write op finalize failed", "op_id", op.OpID, "action", op.Action, "code", code)
		return e.fail(503, unknownCode(op.Action), booking.Message(unknownCode(op.Action)))
	}
	return e.answerStored(fresh)
}

// saveContext is the last journal write before EXEC. Anything other than one updated row stops the call.
func (e *BookingEngine) saveContext(ctx context.Context, op *writeops.Op, contextJSON string, planning sql.NullInt64) WriteHTTP {
	finCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ok, err := e.Ops.SaveContext(finCtx, op.OpID, contextJSON, planning)
	if err != nil {
		e.noteWrite(err)
		e.journalErr("context", op.OpID, err)
		return e.fail(503, "WRITE_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	e.noteWrite(nil)
	if ok {
		op.ContextJSON = contextJSON
		return WriteHTTP{}
	}
	fresh, err := e.Ops.Get(finCtx, op.OpID)
	if err != nil || fresh == nil {
		e.log().Error("lk write op finalize failed", "op_id", op.OpID, "action", op.Action, "code", "WRITE_UNAVAILABLE")
		return e.fail(503, unknownCode(op.Action), booking.Message(unknownCode(op.Action)))
	}
	if fresh.State == writeops.StateInFlight {
		return e.fail(503, "WRITE_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	return e.answerStored(fresh)
}

type cancelSnap struct {
	variant string
	answ    *int64
	dir     *int64
}

func (e *BookingEngine) loadCancelSnap(ctx context.Context, planningID int64) (cancelSnap, booking.Snap, WriteHTTP, bool) {
	adm, err := e.Clinic.Admission(ctx, planningID)
	if err != nil {
		return cancelSnap{}, booking.Snap{}, e.fail(500, "INTERNAL", booking.MsgCancelSupport), false
	}
	variant, ok := booking.Admit(planningID, adm)
	if !ok {
		return cancelSnap{}, booking.Snap{}, e.fail(409, "CANCEL_NOT_SUPPORTED", booking.MsgCancelSupport), false
	}
	answ, dir := savedIDs(variant, adm)
	snap, err := e.Clinic.Snap(ctx, planningID, answ, dir)
	if err != nil || !snap.ReadOK {
		return cancelSnap{}, booking.Snap{}, e.fail(503, "CANCEL_UNKNOWN", booking.MsgCancelUnknown), false
	}
	return cancelSnap{variant: variant, answ: answ, dir: dir}, snap, WriteHTTP{}, true
}

func (e *BookingEngine) refuseJournal() WriteHTTP {
	if e.journalDown.Load() {
		return e.fail(503, "WRITE_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
	}
	return WriteHTTP{}
}

func (e *BookingEngine) noteWrite(err error) {
	e.journalDown.Store(err != nil)
}

func (e *BookingEngine) journalErr(step, opID string, err error) {
	if err == nil {
		return
	}
	now := e.now()
	e.recMu.Lock()
	if e.journalLogAt == nil {
		e.journalLogAt = map[string]time.Time{}
	}
	if last := e.journalLogAt[step]; !last.IsZero() && now.Sub(last) < time.Minute {
		e.recMu.Unlock()
		return
	}
	e.journalLogAt[step] = now
	e.recMu.Unlock()
	if opID != "" {
		e.log().Error("lk write journal error", "op_id", opID, "step", step, "err", err.Error())
		return
	}
	e.log().Error("lk write journal error", "step", step, "err", err.Error())
}

func (e *BookingEngine) beginLive(id string) {
	if id == "" {
		return
	}
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	if e.live == nil {
		e.live = map[string]struct{}{}
	}
	e.live[id] = struct{}{}
}

func (e *BookingEngine) endLive(id string) {
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	delete(e.live, id)
}

func (e *BookingEngine) isLive(id string) bool {
	e.liveMu.Lock()
	defer e.liveMu.Unlock()
	_, ok := e.live[id]
	return ok
}

func (e *BookingEngine) answerStored(op *writeops.Op) WriteHTTP {
	switch op.State {
	case writeops.StateUnknown, writeops.StatePartial, writeops.StateInFlight:
		return e.fail(503, unknownCode(op.Action), booking.Message(unknownCode(op.Action)))
	default:
		return e.replay(op, op.IntentJSON)
	}
}

func unknownCode(action string) string {
	if action == writeops.ActionCancel {
		return "CANCEL_UNKNOWN"
	}
	return "BOOKING_UNKNOWN"
}

func moscowWall(t time.Time) string {
	return t.In(time.FixedZone("MSK", 3*3600)).Format(repo.SlotStartLayout)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ProcCount is how many procedure batches this engine has sent. Tests use it.
func (e *BookingEngine) ProcCount() int { return int(e.procCalls.Load()) }
