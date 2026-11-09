package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

var resolveNotes = map[string]bool{
	"checked_in_client":  true,
	"registry_completed": true,
	"other":              true,
}

// Sweep moves crashed in-flight rows to unknown and reconciles unknown rows by reading only.
// StartMaintenance retires leftover in-flight rows, then reconciles unknown rows until ctx ends.
func (e *BookingEngine) StartMaintenance(ctx context.Context) {
	if e == nil || e.Ops == nil || e.Ops.DB == nil {
		return
	}
	e.recMu.Lock()
	if e.lastReconcile == nil {
		e.lastReconcile = map[string]time.Time{}
	}
	e.recMu.Unlock()
	e.retireAllInFlight(context.WithoutCancel(ctx))
	go e.maintain(ctx)
}

func (e *BookingEngine) retireAllInFlight(ctx context.Context) {
	rows, err := e.Ops.ListStates(ctx, []string{writeops.StateInFlight})
	if err != nil {
		e.journalErr("startup", "", err)
		return
	}
	now := e.now()
	for _, op := range rows {
		e.markUnknown(ctx, op, "startup", now)
	}
}

func (e *BookingEngine) maintain(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.safeTick(ctx)
		}
	}
}

func (e *BookingEngine) safeTick(ctx context.Context) {
	defer func() {
		if recover() != nil {
			e.log().Error("lk write maintenance panic")
		}
	}()
	e.probeJournal(ctx)
	e.expireStale(ctx)
	e.reconcileDue(ctx)
	e.warnUnresolved(ctx, e.now())
}

func (e *BookingEngine) probeJournal(ctx context.Context) {
	if e.Ops == nil || !e.journalDown.Load() {
		return
	}
	now := e.now()
	e.recMu.Lock()
	if !e.lastProbe.IsZero() && now.Sub(e.lastProbe) < 30*time.Second {
		e.recMu.Unlock()
		return
	}
	e.lastProbe = now
	e.recMu.Unlock()
	if err := e.Ops.ProbeHealth(ctx, now); err != nil {
		e.journalErr("probe", "", err)
		return
	}
	e.journalDown.Store(false)
}

func (e *BookingEngine) expireStale(ctx context.Context) {
	now := e.now()
	stale, err := e.Ops.StaleInFlight(ctx, now.Add(-2*time.Minute))
	if err != nil {
		e.journalErr("expire", "", err)
		return
	}
	for _, op := range stale {
		e.markUnknown(ctx, op, "expire", now)
	}
}

func (e *BookingEngine) markUnknown(ctx context.Context, op writeops.Op, step string, now time.Time) {
	if e.isLive(op.OpID) {
		return
	}
	code := "BOOKING_UNKNOWN"
	if op.Action == writeops.ActionCancel {
		code = "CANCEL_UNKNOWN"
	}
	_, err := e.Ops.MarkUnknown(ctx, op.OpID, code, now.UTC().Format(time.RFC3339))
	if err != nil {
		e.noteWrite(err)
		e.journalErr(step, op.OpID, err)
		return
	}
	e.noteWrite(nil)
}

func (e *BookingEngine) reconcileDue(ctx context.Context) {
	now := e.now()
	rows, err := e.Ops.ListStates(ctx, []string{writeops.StateUnknown})
	if err != nil {
		e.journalErr("reconcile", "", err)
		return
	}
	for _, op := range rows {
		if e.isLive(op.OpID) {
			continue
		}
		if !dueReconcile(op, now) {
			continue
		}
		e.recMu.Lock()
		last := e.lastReconcile[op.OpID]
		if !last.IsZero() && now.Sub(last) < time.Minute {
			e.recMu.Unlock()
			continue
		}
		if e.lastReconcile == nil {
			e.lastReconcile = map[string]time.Time{}
		}
		e.lastReconcile[op.OpID] = now
		e.recMu.Unlock()
		e.reconcile(ctx, op, now)
	}
}

func dueReconcile(op writeops.Op, now time.Time) bool {
	if !op.FinishedAt.Valid || op.FinishedAt.String == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339, op.FinishedAt.String)
	if err != nil {
		return false
	}
	return now.Sub(at) >= 2*time.Second
}

func (e *BookingEngine) Sweep(ctx context.Context) {
	now := e.now()
	stale, err := e.Ops.StaleInFlight(ctx, now.Add(-2*time.Minute))
	if err != nil {
		e.journalErr("expire", "", err)
		return
	}
	for _, op := range stale {
		e.markUnknown(ctx, op, "expire", now)
	}
	rows, err := e.Ops.ListStates(ctx, []string{writeops.StateUnknown})
	if err != nil {
		e.journalErr("reconcile", "", err)
		return
	}
	for _, op := range rows {
		if e.isLive(op.OpID) {
			continue
		}
		e.reconcile(ctx, op, now)
	}
	e.warnUnresolved(ctx, now)
}

func (e *BookingEngine) reconcile(ctx context.Context, op writeops.Op, now time.Time) {
	if e.isLive(op.OpID) {
		return
	}
	if op.Action == writeops.ActionBook {
		var intent struct {
			PlSubjID   int64  `json:"pl_subj_id"`
			PlExamID   int64  `json:"pl_exam_id"`
			VisitStart string `json:"visit_start"`
		}
		if json.Unmarshal([]byte(op.IntentJSON), &intent) != nil {
			return
		}
		since := mskMinus(op.StartedAtMSK, time.Minute)
		id, ok, err := e.Clinic.FindIntent(ctx, op.PatientID, intent.PlSubjID, intent.PlExamID, e.Cfg.LKAuthorMedecinsID, intent.VisitStart, since)
		if err != nil || !ok {
			return
		}
		if err := e.transitionNoted(ctx, op.OpID, writeops.StateUnknown, writeops.Patch{
			State: writeops.StateOK, PlanningID: sql.NullInt64{Int64: id, Valid: true},
			ContextJSON: op.ContextJSON, FinishedAt: now.UTC().Format(time.RFC3339),
		}); err != nil {
			return
		}
		return
	}
	var cc cancelContext
	if json.Unmarshal([]byte(op.ContextJSON), &cc) != nil || cc.Variant == "" {
		return
	}
	var intent struct {
		PlanningID int64 `json:"planning_id"`
	}
	if json.Unmarshal([]byte(op.IntentJSON), &intent) != nil {
		return
	}
	planningID := intent.PlanningID
	after, err := e.Clinic.Snap(ctx, planningID, cc.DirAnswID, cc.PatdirecID)
	if err != nil || !after.ReadOK {
		return
	}
	pred := booking.Eval(planningID, cc.Variant, cc.Before, after)
	if pred.Full {
		cc.After = &after
		if err := e.transitionNoted(ctx, op.OpID, writeops.StateUnknown, writeops.Patch{
			State: writeops.StateOK, Code: "", PlanningID: op.PlanningID,
			ContextJSON: mustJSON(cc), FinishedAt: now.UTC().Format(time.RFC3339),
		}); err != nil {
			return
		}
		return
	}
	started, err := time.Parse(time.RFC3339, op.StartedAt)
	if err != nil || now.Sub(started) < 10*time.Minute || pred.Unchanged {
		return
	}
	state, code := booking.CancelOutcome(true, pred, true)
	if state != booking.StatePartial {
		return
	}
	cc.After = &after
	_ = e.transitionNoted(ctx, op.OpID, writeops.StateUnknown, writeops.Patch{
		State: writeops.StatePartial, Code: code, PlanningID: op.PlanningID,
		ContextJSON: mustJSON(cc), FinishedAt: now.UTC().Format(time.RFC3339),
	})
}

func (e *BookingEngine) transitionNoted(ctx context.Context, opID, from string, p writeops.Patch) error {
	_, err := e.Ops.Transition(ctx, opID, from, p)
	if err != nil {
		e.noteWrite(err)
		e.journalErr("reconcile", opID, err)
		return err
	}
	e.noteWrite(nil)
	return nil
}

func (e *BookingEngine) warnUnresolved(ctx context.Context, now time.Time) {
	rows, err := e.Ops.ListStates(ctx, []string{writeops.StateUnknown, writeops.StatePartial})
	if err != nil {
		e.journalErr("warn", "", err)
		return
	}
	for _, op := range rows {
		started, err := time.Parse(time.RFC3339, op.StartedAt)
		if err != nil || now.Sub(started) < 10*time.Minute {
			continue
		}
		stamp := now.UTC().Format(time.RFC3339)
		older := now.Add(-time.Hour).UTC().Format(time.RFC3339)
		ok, err := e.Ops.MarkUnresolvedLogged(ctx, op.OpID, stamp, older)
		if err == nil && ok {
			e.log().Error("lk write op unresolved", "op_id", op.OpID, "action", op.Action)
		}
	}
}

func mskMinus(wall string, d time.Duration) string {
	at, err := time.ParseInLocation(repoSlotLayout(), wall, time.FixedZone("MSK", 3*3600))
	if err != nil {
		return wall
	}
	return at.Add(-d).Format(repoSlotLayout())
}

func repoSlotLayout() string { return "2006-01-02T15:04:05" }

func (e *BookingEngine) Resolve(ctx context.Context, in ResolveInput) WriteHTTP {
	if !resolveNotes[in.Note] {
		return e.fail(400, "VALIDATION", "Нужен resolve_note_code")
	}
	if in.State != writeops.StateManualOK && in.State != writeops.StateManualRejected {
		return e.fail(400, "VALIDATION", "Неизвестное состояние")
	}
	op, err := e.Ops.Get(ctx, in.OpID)
	if err != nil || op == nil {
		return e.fail(404, "NOT_FOUND", "Операция не найдена")
	}
	if op.State != writeops.StateUnknown && op.State != writeops.StatePartial {
		return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
	}
	from := op.State
	if in.State == writeops.StateManualOK {
		if op.Action == writeops.ActionBook {
			if in.PlanningID == nil || *in.PlanningID <= 0 {
				return e.fail(422, "VALIDATION", "Нужен planning_id")
			}
			ok, err := e.manualBookOK(ctx, op, *in.PlanningID)
			if err != nil {
				return e.readDown()
			}
			if !ok {
				return e.fail(422, "VALIDATION", "Запись не подтверждена")
			}
		} else {
			ok, err := e.manualCancelOK(ctx, op)
			if err != nil {
				return e.readDown()
			}
			if !ok {
				return e.fail(422, "VALIDATION", booking.MsgManualActive)
			}
		}
	} else if op.Action == writeops.ActionBook {
		exists, err := e.manualBookExists(ctx, op)
		if err != nil {
			return e.readDown()
		}
		if exists {
			return e.fail(422, "VALIDATION", "Запись есть, отказ не принят")
		}
	} else {
		ok, err := e.manualCancelUnchanged(ctx, op)
		if err != nil {
			return e.readDown()
		}
		if !ok {
			return e.fail(422, "VALIDATION", "Запись изменилась")
		}
	}
	pid := op.PlanningID
	if in.PlanningID != nil {
		pid = sql.NullInt64{Int64: *in.PlanningID, Valid: true}
	}
	ok, err := e.Ops.Transition(ctx, op.OpID, from, writeops.Patch{
		State: in.State, Code: op.Code, PlanningID: pid, ContextJSON: op.ContextJSON,
		FinishedAt: e.now().UTC().Format(time.RFC3339), ResolvedBy: in.Actor, ResolveNote: in.Note,
	})
	if err != nil {
		e.noteWrite(err)
		e.journalErr("resolve", op.OpID, err)
		return e.fail(503, "WRITE_LOG_UNAVAILABLE", booking.MsgRegistry)
	}
	e.noteWrite(nil)
	if !ok {
		fresh, _ := e.Ops.Get(ctx, op.OpID)
		if fresh != nil {
			return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
		}
		return e.fail(409, "OPERATION_IN_PROGRESS", booking.MsgInProgress)
	}
	return WriteHTTP{Status: 200, Body: map[string]any{"op_id": op.OpID, "state": in.State}}
}

func (e *BookingEngine) readDown() WriteHTTP {
	return e.fail(503, "WRITE_UNAVAILABLE", medialogwrite.PatientForbiddenMessage)
}

func (e *BookingEngine) manualBookOK(ctx context.Context, op *writeops.Op, planningID int64) (bool, error) {
	var intent struct {
		PlSubjID   int64  `json:"pl_subj_id"`
		PlExamID   int64  `json:"pl_exam_id"`
		VisitStart string `json:"visit_start"`
	}
	if json.Unmarshal([]byte(op.IntentJSON), &intent) != nil {
		return false, nil
	}
	row, err := e.Clinic.ReadCreated(ctx, planningID)
	if err != nil {
		return false, err
	}
	return createdMatches(row, op.PatientID, intent.PlSubjID, intent.PlExamID, e.Cfg.LKAuthorMedecinsID, intent.VisitStart), nil
}

func (e *BookingEngine) manualBookExists(ctx context.Context, op *writeops.Op) (bool, error) {
	var intent struct {
		PlSubjID   int64  `json:"pl_subj_id"`
		PlExamID   int64  `json:"pl_exam_id"`
		VisitStart string `json:"visit_start"`
	}
	if json.Unmarshal([]byte(op.IntentJSON), &intent) != nil {
		return true, nil
	}
	_, ok, err := e.Clinic.FindIntent(ctx, op.PatientID, intent.PlSubjID, intent.PlExamID, e.Cfg.LKAuthorMedecinsID, intent.VisitStart, "2000-01-01T00:00:00")
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (e *BookingEngine) manualCancelOK(ctx context.Context, op *writeops.Op) (bool, error) {
	pred, ok, err := e.manualCancelPred(ctx, op)
	if err != nil {
		return false, err
	}
	return ok && pred.Full, nil
}

func (e *BookingEngine) manualCancelUnchanged(ctx context.Context, op *writeops.Op) (bool, error) {
	pred, ok, err := e.manualCancelPred(ctx, op)
	if err != nil {
		return false, err
	}
	return ok && pred.Unchanged, nil
}

func (e *BookingEngine) manualCancelPred(ctx context.Context, op *writeops.Op) (booking.Predicates, bool, error) {
	var cc cancelContext
	var intent struct {
		PlanningID int64 `json:"planning_id"`
	}
	if json.Unmarshal([]byte(op.ContextJSON), &cc) != nil || json.Unmarshal([]byte(op.IntentJSON), &intent) != nil {
		return booking.Predicates{}, false, nil
	}
	after, err := e.Clinic.Snap(ctx, intent.PlanningID, cc.DirAnswID, cc.PatdirecID)
	if err != nil {
		return booking.Predicates{}, false, err
	}
	if !after.ReadOK {
		return booking.Predicates{}, false, nil
	}
	return booking.Eval(intent.PlanningID, cc.Variant, cc.Before, after), true, nil
}

func (e *BookingEngine) ListOpen(ctx context.Context, states []string) ([]writeops.Op, error) {
	allowed := map[string]bool{writeops.StateInFlight: true, writeops.StateUnknown: true, writeops.StatePartial: true}
	for _, s := range states {
		if !allowed[s] {
			return nil, errBadState
		}
	}
	if len(states) == 0 {
		states = []string{writeops.StateUnknown, writeops.StatePartial}
	}
	return e.Ops.ListStates(ctx, states)
}

var errBadState = errState{}

type errState struct{}

func (errState) Error() string { return "bad state" }
