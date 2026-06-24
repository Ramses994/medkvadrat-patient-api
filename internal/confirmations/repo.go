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
	UpdatedAt  time.Time
}

type Repo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

// Upsert stores confirmation with last-write-wins semantics per planning_id.
func (r *Repo) Upsert(ctx context.Context, planningID, patientID int64, status, source string) (Record, error) {
	if source == "" {
		source = defaultSource
	}
	status = NormalizeStatus(status)

	const q = `
INSERT INTO appointment_confirmations (planning_id, status, source, patient_id, updated_at)
VALUES (?, ?, ?, ?, datetime('now'))
ON CONFLICT(planning_id) DO UPDATE SET
  status = excluded.status,
  source = excluded.source,
  patient_id = excluded.patient_id,
  updated_at = excluded.updated_at
RETURNING planning_id, status, source, patient_id, updated_at`

	var rec Record
	var updatedAt string
	err := r.db.QueryRowContext(ctx, q, planningID, status, source, patientID).Scan(
		&rec.PlanningID, &rec.Status, &rec.Source, &rec.PatientID, &updatedAt,
	)
	if err != nil {
		return Record{}, fmt.Errorf("upsert confirmation: %w", err)
	}
	t, err := parseSQLiteTime(updatedAt)
	if err != nil {
		return Record{}, err
	}
	rec.UpdatedAt = t
	return rec, nil
}

func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse updated_at %q", s)
}
