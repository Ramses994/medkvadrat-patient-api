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
	PlanningID int64
	PatientID  int64
	Status     string
	Source     string
	ApptStart  string
	UpdatedAt  time.Time
}

type Repo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

// Upsert stores the first answer for a planning_id at a given appointment start.
// A later answer is kept only when apptStart differs from the stored one (reschedule in Medialog).
// A matching or legacy NULL start returns AlreadyAnsweredError.
func (r *Repo) Upsert(ctx context.Context, planningID, patientID int64, status, source, apptStart string) (Record, error) {
	if source == "" {
		source = defaultSource
	}
	status = NormalizeStatus(status)

	existing, err := r.get(ctx, planningID)
	if err != nil && err != sql.ErrNoRows {
		return Record{}, err
	}
	if err == nil {
		if existing.ApptStart == "" || existing.ApptStart == apptStart {
			return existing, &AlreadyAnsweredError{Record: existing}
		}
	}

	const q = `
INSERT INTO appointment_confirmations (planning_id, status, source, patient_id, appt_start, updated_at)
VALUES (?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(planning_id) DO UPDATE SET
  status = excluded.status,
  source = excluded.source,
  patient_id = excluded.patient_id,
  appt_start = excluded.appt_start,
  updated_at = excluded.updated_at
RETURNING planning_id, status, source, patient_id, appt_start, updated_at`

	var rec Record
	var start sql.NullString
	var updatedAt string
	err = r.db.QueryRowContext(ctx, q, planningID, status, source, patientID, nullIfEmpty(apptStart)).Scan(
		&rec.PlanningID, &rec.Status, &rec.Source, &rec.PatientID, &start, &updatedAt,
	)
	if err != nil {
		return Record{}, fmt.Errorf("upsert confirmation: %w", err)
	}
	if start.Valid {
		rec.ApptStart = start.String
	}
	t, err := parseSQLiteTime(updatedAt)
	if err != nil {
		return Record{}, err
	}
	rec.UpdatedAt = t
	return rec, nil
}

func (r *Repo) get(ctx context.Context, planningID int64) (Record, error) {
	const q = `
SELECT planning_id, status, source, patient_id, appt_start, updated_at
FROM appointment_confirmations WHERE planning_id = ?`
	var rec Record
	var patientID sql.NullInt64
	var start sql.NullString
	var updatedAt string
	err := r.db.QueryRowContext(ctx, q, planningID).Scan(
		&rec.PlanningID, &rec.Status, &rec.Source, &patientID, &start, &updatedAt,
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
