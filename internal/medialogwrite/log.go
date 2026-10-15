package medialogwrite

import (
	"context"
	"database/sql"
	"fmt"
)

type LogRepo struct {
	db *sql.DB
}

func NewLogRepo(db *sql.DB) *LogRepo { return &LogRepo{db: db} }

type LogEntry struct {
	RequestID  string
	Route      string
	PatientID  int64
	PlanningID int64
	PlSubjID   int64
	Decision   Decision
	Result     string
	ErrorCode  string
}

func (r *LogRepo) Insert(ctx context.Context, e LogEntry) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
INSERT INTO medialog_write_log (request_id, route, patient_id, planning_id, pl_subj_id, decision, result, error_code)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullIfEmpty(e.RequestID),
		e.Route,
		e.PatientID,
		nullZero(e.PlanningID),
		nullZero(e.PlSubjID),
		string(e.Decision),
		nullIfEmpty(e.Result),
		nullIfEmpty(e.ErrorCode),
	)
	if err != nil {
		return 0, fmt.Errorf("insert write log: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (r *LogRepo) SetResult(ctx context.Context, id int64, result, errorCode string) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE medialog_write_log SET result = ?, error_code = ? WHERE id = ?`,
		result, nullIfEmpty(errorCode), id,
	)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
