package repo

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestOldSchema_Migrate_DashboardGray_ThenNewAnswer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE appointment_confirmations (
  planning_id INTEGER PRIMARY KEY,
  status TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'max',
  patient_id INTEGER,
  updated_at DATETIME NOT NULL
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO appointment_confirmations (planning_id, status, source, patient_id, updated_at)
VALUES (1, 'confirmed', 'max', 2, datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	overlay, err := confirmations.NewRepo(db).OverlayByPlanningIDs(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if overlay[1].Version != "" {
		t.Fatalf("old row version=%q", overlay[1].Version)
	}
	sched := BuildDashboardSchedule(time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC), []DashboardPlanningRow{
		{PlanningID: 1, PatientName: "A", BranchID: 106, BranchCode: "Каширка", Time: "09:00", ApptVersion: "ver-new"},
	}, map[int64]ConfirmationOverlay{1: {Status: overlay[1].Status, Version: overlay[1].Version}})
	appt := sched.Branches[0].Appointments[0]
	if appt.Confirmation != nil || appt.ConfirmationHint != ConfirmationUnknownVersionHint {
		t.Fatalf("dashboard %+v", appt)
	}
	counts := DashboardConfirmationCounts(sched.Branches)
	if counts["confirmed"] != 0 {
		t.Fatalf("counts=%v", counts)
	}
	rec, err := confirmations.NewRepo(db).Upsert(ctx, 1, 2, "declined", "max", "2026-06-26 19:30:00", "ver-new")
	if err != nil {
		t.Fatalf("new answer after migration: %v", err)
	}
	if rec.Status != "declined" || rec.ApptVersion != "ver-new" {
		t.Fatalf("%+v", rec)
	}
}
