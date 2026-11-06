package confirmations

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const defaultSource = "max"

// Record is the persisted confirmation overlay in gateway.db.
type Record struct {
	PlanningID  int64
	PatientID   int64
	Status      string
	Source      string
	ApptStart   string
	ApptVersion string
	UpdatedAt   time.Time
}

type Repo struct {
	db  *sql.DB
	Now func() time.Time
	// AfterWaitlistRead is a test hook so concurrent updates can both observe the same status.
	AfterWaitlistRead func()
}

func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Upsert stores the first answer for a planning_id atomically.
// A later row is written only when appt_version differs (reschedule / line / exam change).
// declined/reschedule winners also insert waitlist in the same SQLite transaction.
func (r *Repo) Upsert(ctx context.Context, planningID, patientID int64, status, source, apptStart, apptVersion string, branchID int) (Record, error) {
	if source == "" {
		source = defaultSource
	}
	status = NormalizeStatus(status)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("begin confirmation tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
INSERT INTO appointment_confirmations (planning_id, status, source, patient_id, appt_version, appt_start, updated_at)
VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(planning_id) DO UPDATE SET
  status = excluded.status,
  source = excluded.source,
  patient_id = excluded.patient_id,
  appt_version = excluded.appt_version,
  appt_start = excluded.appt_start,
  updated_at = excluded.updated_at
WHERE appointment_confirmations.appt_version IS NULL
  OR appointment_confirmations.appt_version <> excluded.appt_version
RETURNING planning_id, status, source, patient_id, appt_start, appt_version, updated_at`

	var rec Record
	var start, ver sql.NullString
	var updatedAt string
	err = tx.QueryRowContext(ctx, q, planningID, status, source, patientID, nullIfEmpty(apptVersion), nullIfEmpty(apptStart)).Scan(
		&rec.PlanningID, &rec.Status, &rec.Source, &rec.PatientID, &start, &ver, &updatedAt,
	)
	if err == sql.ErrNoRows {
		if rbErr := tx.Rollback(); rbErr != nil {
			return Record{}, rbErr
		}
		existing, getErr := r.get(ctx, planningID)
		if getErr != nil {
			return Record{}, getErr
		}
		return existing, &AlreadyAnsweredError{Record: existing}
	}
	if err != nil {
		return Record{}, fmt.Errorf("upsert confirmation: %w", err)
	}
	if start.Valid {
		rec.ApptStart = start.String
	}
	if ver.Valid {
		rec.ApptVersion = ver.String
	}
	t, err := parseSQLiteTime(updatedAt)
	if err != nil {
		return Record{}, err
	}
	rec.UpdatedAt = t

	if reason, ok := waitlistReason(status); ok {
		now := r.now()
		wl := WaitlistRow{
			PlanningID:   planningID,
			PatientID:    patientID,
			ApptVersion:  apptVersion,
			ApptStart:    apptStart,
			Reason:       reason,
			Source:       source,
			Status:       WaitlistStatusNew,
			CreatedAt:    FormatWaitlistUTC(now),
			UpdatedAt:    FormatWaitlistUTC(now),
			CreatedAtMSK: FormatWaitlistMSK(now),
			BranchID:     branchID,
		}
		if err := insertWaitlistTx(ctx, tx, wl); err != nil {
			return Record{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("commit confirmation: %w", err)
	}
	return rec, nil
}

func (r *Repo) get(ctx context.Context, planningID int64) (Record, error) {
	const q = `
SELECT planning_id, status, source, patient_id, appt_start, appt_version, updated_at
FROM appointment_confirmations WHERE planning_id = ?`
	var rec Record
	var patientID sql.NullInt64
	var start, ver sql.NullString
	var updatedAt string
	err := r.db.QueryRowContext(ctx, q, planningID).Scan(
		&rec.PlanningID, &rec.Status, &rec.Source, &patientID, &start, &ver, &updatedAt,
	)
	if err != nil {
		return Record{}, err
	}
	if patientID.Valid {
		rec.PatientID = patientID.Int64
	}
	if start.Valid {
		rec.ApptStart = start.String
	}
	if ver.Valid {
		rec.ApptVersion = ver.String
	}
	t, err := parseSQLiteTime(updatedAt)
	if err != nil {
		return Record{}, err
	}
	rec.UpdatedAt = t
	return rec, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse updated_at %q", s)
}
