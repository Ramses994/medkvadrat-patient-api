package repo

import (
	"testing"
	"time"
)

func TestBuildDashboardSchedule_OrderAndOverlay(t *testing.T) {
	rows := []DashboardPlanningRow{
		{PlanningID: 1, PatientName: "Иванов", DoctorName: "Доктор", BranchID: 3, BranchCode: "Куркино", Time: "10:00", Status: 0},
		{PlanningID: 2, PatientName: "Петров", DoctorName: "Врач", BranchID: 106, BranchCode: "Каширка", Time: "09:00", Status: 0},
		{PlanningID: 3, PatientName: "Сидоров", DoctorName: "Эксперт", BranchID: 496, BranchCode: "Куркино 2 (взр.)", Time: "11:30", Status: 1},
	}
	overlay := map[int64]string{1: "confirmed", 3: "declined"}

	got := BuildDashboardSchedule(time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC), rows, overlay)

	if got.Date != "2026-06-26" {
		t.Fatalf("date=%q", got.Date)
	}
	if len(got.Branches) != 3 {
		t.Fatalf("branches=%d", len(got.Branches))
	}
	wantOrder := []int{106, 3, 496}
	for i, wantID := range wantOrder {
		if got.Branches[i].BranchID != wantID {
			t.Fatalf("branch[%d] id=%d want %d", i, got.Branches[i].BranchID, wantID)
		}
	}

	// Каширка — no overlay
	if got.Branches[0].Appointments[0].Confirmation != nil {
		t.Fatal("expected null confirmation")
	}
	// Куркино — confirmed
	if got.Branches[1].Appointments[0].Confirmation == nil || *got.Branches[1].Appointments[0].Confirmation != "confirmed" {
		t.Fatalf("confirmation=%v", got.Branches[1].Appointments[0].Confirmation)
	}
	// Куркино 2 — declined + short name
	if got.Branches[2].Name != "Куркино 2" {
		t.Fatalf("name=%q", got.Branches[2].Name)
	}
	if got.Branches[2].Appointments[0].Confirmation == nil || *got.Branches[2].Appointments[0].Confirmation != "declined" {
		t.Fatalf("confirmation=%v", got.Branches[2].Appointments[0].Confirmation)
	}
}

func TestBuildDashboardSchedule_EmptyDay(t *testing.T) {
	got := BuildDashboardSchedule(time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), nil, nil)
	if len(got.Branches) != 3 {
		t.Fatalf("branches=%d", len(got.Branches))
	}
	for _, b := range got.Branches {
		if len(b.Appointments) != 0 {
			t.Fatalf("branch %d has appointments", b.BranchID)
		}
		if b.Name == "" {
			t.Fatalf("branch %d missing name", b.BranchID)
		}
	}
}

func TestNormalizeHHMM(t *testing.T) {
	if got := normalizeHHMM("19:30:00"); got != "19:30" {
		t.Fatalf("got %q", got)
	}
}
