//go:build integration

package integration

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/denisenkom/go-mssqldb"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

// End-to-end DB-level smoke for Medialog write path (book + cancel).
// IMPORTANT: this test touches real Medialog tables. It best-effort cleans up.
//
// Run:
//   INTEGRATION_MSSQL=1 go test -tags=integration ./internal/integration/...
func TestBookMeAndCancelMe_RealMSSQL(t *testing.T) {
	if os.Getenv("INTEGRATION_MSSQL") == "" {
		t.Skip("set INTEGRATION_MSSQL=1 and DB_* to run")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	mssql, err := db.OpenMSSQL(cfg.MSSQL)
	if err != nil {
		t.Fatalf("mssql: %v", err)
	}
	t.Cleanup(func() { _ = mssql.Close() })

	ctx := context.Background()

	// Pick any free slot (we only need the ID; details are read from PLANNING).
	var planningID int
	if err := mssql.QueryRowContext(ctx, `
SELECT TOP 1 p.PLANNING_ID
FROM PLANNING p
WHERE p.PATIENTS_ID IS NULL
  AND p.STATUS = 0
  AND p.DATE_CONS >= CAST(GETDATE() AS date)
ORDER BY p.DATE_CONS, p.HEURE`).Scan(&planningID); err != nil {
		t.Fatalf("pick planning: %v", err)
	}
	if planningID == 0 {
		t.Fatal("no free planning row found")
	}

	// Pick a patient (any existing patient row).
	var patientID int64
	if err := mssql.QueryRowContext(ctx, `SELECT TOP 1 PATIENTS_ID FROM PATIENTS ORDER BY PATIENTS_ID`).Scan(&patientID); err != nil {
		t.Fatalf("pick patient: %v", err)
	}
	if patientID == 0 {
		t.Fatal("no patient row found")
	}

	plan := &repo.PlanningRepo{}
	mot := &repo.MotconsuRepo{}

	// Book.
	now := time.Now()
	got, err := repo.BookMe(ctx, mssql, plan, mot, planningID, patientID, 306, now)
	if err != nil {
		t.Fatalf("BookMe: %v", err)
	}
	if got.MotconsuID == 0 {
		t.Fatalf("BookMe: motconsu_id=0")
	}

	// Verify DB side effects.
	var motPlanning sql.NullInt64
	var recStatus string
	if err := mssql.QueryRowContext(ctx, `
SELECT PLANNING_ID, ISNULL(REC_STATUS,'')
FROM MOTCONSU
WHERE MOTCONSU_ID=@id`, sql.Named("id", got.MotconsuID)).Scan(&motPlanning, &recStatus); err != nil {
		t.Fatalf("select motconsu: %v", err)
	}
	if !motPlanning.Valid || int(motPlanning.Int64) != planningID {
		t.Fatalf("motconsu planning mismatch: got %v, want %d", motPlanning, planningID)
	}
	if recStatus != "W" {
		t.Fatalf("motconsu status: got %q, want %q", recStatus, "W")
	}
	var planningPat sql.NullInt64
	if err := mssql.QueryRowContext(ctx, `SELECT PATIENTS_ID FROM PLANNING WHERE PLANNING_ID=@id`, sql.Named("id", planningID)).Scan(&planningPat); err != nil {
		t.Fatalf("select planning: %v", err)
	}
	if !planningPat.Valid || planningPat.Int64 != patientID {
		t.Fatalf("planning patients_id: got %v, want %d", planningPat, patientID)
	}

	// Cancel. We bypass 24h rule by passing 0 in repo function.
	if err := repo.CancelMe(ctx, mssql, got.MotconsuID, patientID, 0, now); err != nil {
		t.Fatalf("CancelMe: %v", err)
	}
	if err := mssql.QueryRowContext(ctx, `
SELECT ISNULL(REC_STATUS,'')
FROM MOTCONSU WHERE MOTCONSU_ID=@id`, sql.Named("id", got.MotconsuID)).Scan(&recStatus); err != nil {
		t.Fatalf("select motconsu status after cancel: %v", err)
	}
	if recStatus != "D" {
		t.Fatalf("motconsu status after cancel: got %q, want %q", recStatus, "D")
	}

	// Best-effort cleanup for dev DB idempotency.
	_, _ = mssql.ExecContext(ctx, `
UPDATE PLANNING SET PATIENTS_ID=NULL, NOM=NULL, PRENOM=NULL
WHERE PLANNING_ID=@id AND PATIENTS_ID=@pat`, sql.Named("id", planningID), sql.Named("pat", patientID))
}

