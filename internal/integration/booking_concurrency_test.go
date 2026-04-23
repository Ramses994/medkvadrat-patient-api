//go:build integration

package integration

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/denisenkom/go-mssqldb"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

// Concurrency: 10 different patients race for one free planning row.
// Expect: exactly 1 success; others get SLOT_TAKEN (or ALREADY_BOOKED if a patient is duplicated — we use distinct patients).
// Requires a live Medialog MSSQL; point env DB_* to it (e.g. dev) or a local
//
//	docker run: mcr.microsoft.com/mssql/server:2022-latest
//
//	go test -tags=integration ./internal/integration/...
func TestBookMe_SerializableConcurrent(t *testing.T) {
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

	var planningID int
	err = mssql.QueryRowContext(context.Background(), `
SELECT TOP 1 p.PLANNING_ID
FROM PLANNING p
WHERE p.PATIENTS_ID IS NULL
  AND p.STATUS = 0
  AND p.DATE_CONS >= CAST(GETDATE() AS date)
ORDER BY p.DATE_CONS, p.HEURE`).Scan(&planningID)
	if err != nil {
		t.Fatalf("pick planning: %v", err)
	}
	if planningID == 0 {
		t.Fatal("no free planning row found")
	}

	rows, err := mssql.QueryContext(context.Background(), `
SELECT TOP 10 PATIENTS_ID
FROM PATIENTS
ORDER BY PATIENTS_ID`)
	if err != nil {
		t.Fatalf("patients: %v", err)
	}
	var pats []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatalf("scan: %v", err)
		}
		pats = append(pats, id)
	}
	_ = rows.Close()
	if len(pats) < 10 {
		t.Fatalf("need 10 patients, got %d", len(pats))
	}

	plan := &repo.PlanningRepo{}
	mot := &repo.MotconsuRepo{}
	now := time.Now()

	var okCount int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(patientID int64) {
			defer wg.Done()
			_, err := repo.BookMe(context.Background(), mssql, plan, mot, planningID, patientID, 306, now)
			if err == nil {
				atomic.AddInt32(&okCount, 1)
				return
			}
			// any failure is fine for losers
			_ = err
		}(pats[i])
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("expected exactly 1 successful booking, got %d", okCount)
	}
}
