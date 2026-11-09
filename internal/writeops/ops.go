package writeops

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ActionBook   = "book"
	ActionCancel = "cancel"

	StateInFlight       = "in_flight"
	StateOK             = "ok"
	StateRejected       = "rejected"
	StateUnknown        = "unknown"
	StatePartial        = "partial"
	StateManualOK       = "manual_ok"
	StateManualRejected = "manual_rejected"
)

var ErrConflict = errors.New("write op conflict")

type Op struct {
	OpID               string
	PatientID          int64
	Action             string
	RequestID          string
	IntentJSON         string
	ContextJSON        string
	State              string
	Code               string
	PlanningID         sql.NullInt64
	StartedAt          string
	StartedAtMSK       string
	FinishedAt         sql.NullString
	ResolvedBy         sql.NullString
	ResolveNote        sql.NullString
	UnresolvedLoggedAt sql.NullString
}

type Patch struct {
	State       string
	Code        string
	PlanningID  sql.NullInt64
	ContextJSON string
	FinishedAt  string
	ResolvedBy  string
	ResolveNote string
}

type Repo struct {
	DB *sql.DB
	// RejectWrites is a test seam. A non-nil error skips the SQLite write.
	RejectWrites func(query string) error
}

func New(db *sql.DB) *Repo { return &Repo{DB: db} }

type bookIntent struct {
	PlSubjID   int64  `json:"pl_subj_id"`
	PlExamID   int64  `json:"pl_exam_id"`
	VisitStart string `json:"visit_start"`
}

type cancelIntent struct {
	PlanningID int64 `json:"planning_id"`
}

func BookIntent(subj, exam int64, start string) string {
	b, _ := json.Marshal(bookIntent{PlSubjID: subj, PlExamID: exam, VisitStart: start})
	return string(b)
}

func CancelIntent(planningID int64) string {
	b, _ := json.Marshal(cancelIntent{PlanningID: planningID})
	return string(b)
}

func SameIntent(a, b string) bool { return a == b }

func (r *Repo) Find(ctx context.Context, patientID int64, action, requestID string) (*Op, error) {
	if r == nil || r.DB == nil {
		return nil, errors.New("write journal unavailable")
	}
	row := r.DB.QueryRowContext(ctx, `
SELECT op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id,
       started_at, started_at_msk, finished_at, resolved_by, resolve_note_code, unresolved_logged_at
FROM lk_write_ops
WHERE patient_id = ? AND action = ? AND request_id = ?`, patientID, action, requestID)
	op, err := scanOp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
}

func (r *Repo) Get(ctx context.Context, opID string) (*Op, error) {
	row := r.DB.QueryRowContext(ctx, `
SELECT op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id,
       started_at, started_at_msk, finished_at, resolved_by, resolve_note_code, unresolved_logged_at
FROM lk_write_ops WHERE op_id = ?`, opID)
	op, err := scanOp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOp(row scanner) (*Op, error) {
	var op Op
	err := row.Scan(&op.OpID, &op.PatientID, &op.Action, &op.RequestID, &op.IntentJSON, &op.ContextJSON,
		&op.State, &op.Code, &op.PlanningID, &op.StartedAt, &op.StartedAtMSK, &op.FinishedAt,
		&op.ResolvedBy, &op.ResolveNote, &op.UnresolvedLoggedAt)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (r *Repo) InsertInFlight(ctx context.Context, op Op) error {
	if op.OpID == "" {
		op.OpID = newID()
	}
	_, err := r.exec(ctx, `
INSERT INTO lk_write_ops (
  op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id, started_at, started_at_msk
) VALUES (?, ?, ?, ?, ?, ?, 'in_flight', '', ?, ?, ?)`,
		op.OpID, op.PatientID, op.Action, op.RequestID, op.IntentJSON, op.ContextJSON, op.PlanningID, op.StartedAt, op.StartedAtMSK)
	if err != nil && isConflict(err) {
		return ErrConflict
	}
	return err
}

func isConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

// MarkUnknown moves in_flight to unknown. It does not write context_json or planning_id.
func (r *Repo) MarkUnknown(ctx context.Context, opID, code, finishedAt string) (bool, error) {
	res, err := r.exec(ctx, `
UPDATE lk_write_ops SET state = ?, code = ?, finished_at = ?
WHERE op_id = ? AND state = ?`,
		StateUnknown, code, nullStr(finishedAt), opID, StateInFlight)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ProbeHealth writes one health row. A successful write means the journal file accepts writes.
func (r *Repo) ProbeHealth(ctx context.Context, at time.Time) error {
	_, err := r.exec(ctx, `INSERT OR REPLACE INTO lk_health (id, checked_at) VALUES (1, ?)`, at.UTC().Format(time.RFC3339Nano))
	return err
}

func (r *Repo) Transition(ctx context.Context, opID, from string, p Patch) (bool, error) {
	res, err := r.exec(ctx, `
UPDATE lk_write_ops
SET state = ?, code = ?, planning_id = ?, context_json = ?, finished_at = ?, resolved_by = ?, resolve_note_code = ?
WHERE op_id = ? AND state = ?`,
		p.State, p.Code, p.PlanningID, p.ContextJSON, nullStr(p.FinishedAt), nullStr(p.ResolvedBy), nullStr(p.ResolveNote),
		opID, from)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// NewID is the journal key. The handler registers it before the row becomes in_flight.
func NewID() string { return newID() }

// SaveContext stores the snapshot that the procedure will use.
// RowsAffected is 1 only while the row is still in_flight and the context is empty.
func (r *Repo) SaveContext(ctx context.Context, opID, contextJSON string, planning sql.NullInt64) (bool, error) {
	res, err := r.exec(ctx, `
UPDATE lk_write_ops SET context_json = ?, planning_id = ?
WHERE op_id = ? AND state = 'in_flight' AND context_json = '{}'`,
		contextJSON, planning, opID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (r *Repo) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if r != nil && r.RejectWrites != nil {
		if err := r.RejectWrites(query); err != nil {
			return nil, err
		}
	}
	return r.DB.ExecContext(ctx, query, args...)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (r *Repo) HasOpen(ctx context.Context, patientID int64) (bool, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `
SELECT COUNT(*) FROM lk_write_ops
WHERE patient_id = ? AND state IN ('in_flight', 'unknown', 'partial')`, patientID).Scan(&n)
	return n > 0, err
}

func (r *Repo) ListStates(ctx context.Context, states []string) ([]Op, error) {
	if len(states) == 0 {
		return nil, nil
	}
	q := `
SELECT op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id,
       started_at, started_at_msk, finished_at, resolved_by, resolve_note_code, unresolved_logged_at
FROM lk_write_ops WHERE state IN (` + placeholders(len(states)) + `) ORDER BY started_at`
	args := make([]any, len(states))
	for i, s := range states {
		args[i] = s
	}
	rows, err := r.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Op
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *op)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func (r *Repo) StaleInFlight(ctx context.Context, olderThan time.Time) ([]Op, error) {
	rows, err := r.DB.QueryContext(ctx, `
SELECT op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id,
       started_at, started_at_msk, finished_at, resolved_by, resolve_note_code, unresolved_logged_at
FROM lk_write_ops WHERE state = 'in_flight' AND started_at < ?`, olderThan.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Op
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *op)
	}
	return out, rows.Err()
}

func (r *Repo) MarkUnresolvedLogged(ctx context.Context, opID, at, olderThan string) (bool, error) {
	res, err := r.exec(ctx, `
UPDATE lk_write_ops SET unresolved_logged_at = ?
WHERE op_id = ? AND state IN ('unknown', 'partial')
  AND (unresolved_logged_at IS NULL OR unresolved_logged_at = '' OR unresolved_logged_at < ?)`, at, opID, olderThan)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DumpText returns every stored column for leak checks. It is not a patient-facing API.
func (r *Repo) DumpText(ctx context.Context) (string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT op_id, patient_id, action, request_id, intent_json, context_json, state, code, planning_id, started_at, started_at_msk, finished_at, resolved_by, resolve_note_code FROM lk_write_ops`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		cols := make([]sql.NullString, 14)
		dest := make([]any, len(cols))
		for i := range cols {
			dest[i] = &cols[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return "", err
		}
		for _, c := range cols {
			b.WriteString(c.String)
			b.WriteByte('\n')
		}
	}
	return b.String(), rows.Err()
}
