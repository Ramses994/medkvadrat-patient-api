package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestAuditLK1aConfirmationOnlyWhenVersionMatches(t *testing.T) {
	dateStart := "2026-11-06 10:00:00"
	subj := int64(1119)
	exam := int64(10000688)
	current := repo.ApptVersion(dateStart, subj, exam)
	confirmed := "confirmed"
	match := repo.PlanningVisit{
		PlanningID: 1, StartsAt: "2026-11-06 10:00", StartsAtFull: dateStart,
		PlSubjID: subj, PlExamID: exam,
	}

	for _, tc := range []struct {
		name    string
		version any
		visit   repo.PlanningVisit
		want    *string
	}{
		{
			name: "rescheduled", version: current,
			visit: repo.PlanningVisit{PlanningID: 1, StartsAtFull: "2026-11-07 15:00:00", PlSubjID: subj, PlExamID: exam},
		},
		{
			name: "exam_changed", version: current,
			visit: repo.PlanningVisit{PlanningID: 1, StartsAtFull: dateStart, PlSubjID: subj, PlExamID: exam + 1},
		},
		{name: "legacy_null_version", version: nil, visit: match},
		{name: "matching", version: current, visit: match, want: &confirmed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := store.Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			_, err = db.ExecContext(ctx, `INSERT INTO appointment_confirmations
				(planning_id, patient_id, status, source, updated_at, appt_start, appt_version)
				VALUES (1, 1, 'confirmed', 'max', '2026-11-05T10:00:00Z', '2026-11-06 10:00:00', ?)`, tc.version)
			if err != nil {
				t.Fatal(err)
			}
			s := &Services{SQLite: db}
			got, err := s.attachConfirmations(ctx, []repo.PlanningVisit{tc.visit})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if got[0].Confirmation != nil {
					t.Fatalf("confirmation applied: %q", *got[0].Confirmation)
				}
				return
			}
			if got[0].Confirmation == nil || *got[0].Confirmation != *tc.want {
				t.Fatalf("confirmation = %v, want %s", got[0].Confirmation, *tc.want)
			}
		})
	}
}

func TestAuditLK1aPastWithinCurrentMinute(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 30, 15, 0, time.FixedZone("MSK", 3*3600))
	got := repo.PlanningVisitState(0, 0, "2026-11-05 12:30:00", now, false)
	if got != "not_marked" {
		t.Fatalf("past appointment classified as %q", got)
	}
	earlier := time.Date(2026, 11, 5, 12, 29, 59, 0, time.FixedZone("MSK", 3*3600))
	if got := repo.PlanningVisitState(0, 0, "2026-11-05 12:30:00", earlier, false); got != "scheduled" {
		t.Fatalf("upcoming appointment classified as %q", got)
	}
}
