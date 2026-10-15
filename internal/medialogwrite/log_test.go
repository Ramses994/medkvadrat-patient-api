package medialogwrite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func openLogTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:medialog_write_log_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestLogRepo_InsertAndUpdate(t *testing.T) {
	r := NewLogRepo(openLogTestDB(t))
	ctx := context.Background()
	id, err := r.Insert(ctx, LogEntry{
		RequestID: "req-1", Route: "POST /api/me/appointments",
		PatientID: 1, PlanningID: 2, PlSubjID: 3,
		Decision: DecisionAttempted,
	})
	if err != nil || id <= 0 {
		t.Fatalf("insert: id=%d err=%v", id, err)
	}
	if err := r.SetResult(ctx, id, "ok", ""); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := r.db.QueryRowContext(ctx, `SELECT result FROM medialog_write_log WHERE id=?`, id).Scan(&result); err != nil || result != "ok" {
		t.Fatalf("result=%q err=%v", result, err)
	}
}
