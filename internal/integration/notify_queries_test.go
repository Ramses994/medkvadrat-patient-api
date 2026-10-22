//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func TestNotifyQueries_ReadOnlyLive(t *testing.T) {
	if os.Getenv("INTEGRATION_NOTIFY_QUERIES") != "1" {
		t.Skip("owner-only: INTEGRATION_NOTIFY_QUERIES=1 with svc_patient_api_ro")
	}
	assertNotifySQLReadOnly(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	mssql, err := db.OpenMSSQL(cfg.MSSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer mssql.Close()

	ctx := context.Background()
	now := time.Now()
	ids := []int64{1, 1587578}
	wait := cfg.WaitlistExamIDs

	if _, err := repo.DueReminders(ctx, mssql, now, now.Add(72*time.Hour), ids, wait); err != nil {
		t.Fatalf("DueReminders: %v", err)
	}
	cur := &repo.BookingCursor{
		CreatedAt:  now.Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05.000"),
		PlanningID: 0,
	}
	page, err := repo.NewBookings(ctx, mssql, now, ids, cur, 10, wait)
	if err != nil {
		t.Fatalf("NewBookings: %v", err)
	}
	if len(page.Rows) > 0 {
		if _, err := repo.BookingByPlanningID(ctx, mssql, page.Rows[0].PlanningID, wait); err != nil {
			t.Fatalf("BookingByPlanningID: %v", err)
		}
	}
	if _, err := repo.DashboardPlanningRows(ctx, mssql, now, wait); err != nil {
		t.Fatalf("DashboardPlanningRows: %v", err)
	}
}

func assertNotifySQLReadOnly(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "repo")
	files := []string{"reminders.go", "new_bookings.go", "dashboard_schedule.go", "planning_owner.go"}
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if repo.SQLLooksMutating(src) {
			t.Errorf("%s contains mutating SQL", name)
		}
		if strings.Contains(strings.ToUpper(src), "INSERT") {
			t.Errorf("%s INSERT", name)
		}
	}
}
