package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrate_AddsWriteOpsToExistingDB(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE appointment_confirmations (
  planning_id INTEGER PRIMARY KEY,
  status TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'max',
  patient_id INTEGER,
  updated_at DATETIME NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO lk_write_ops (
  op_id, patient_id, action, request_id, intent_json, context_json, state, started_at, started_at_msk
) VALUES ('op-existing', 1, 'book', 'req', '{}', '{}', 'in_flight', 't', 't')`); err != nil {
		t.Fatal(err)
	}
	var startCol, versionCol int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('appointment_confirmations') WHERE name = 'appt_start'`).Scan(&startCol); err != nil || startCol != 1 {
		t.Fatalf("appt_start %d err=%v", startCol, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('appointment_confirmations') WHERE name = 'appt_version'`).Scan(&versionCol); err != nil || versionCol != 1 {
		t.Fatalf("appt_version %d err=%v", versionCol, err)
	}
	var waitlist int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'waitlist'`).Scan(&waitlist); err != nil || waitlist != 1 {
		t.Fatalf("waitlist %d err=%v", waitlist, err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
}
