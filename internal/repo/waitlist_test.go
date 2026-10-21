package repo

import (
	"strings"
	"testing"
	"time"
)

func TestIsWaitlistExamByNameOrID(t *testing.T) {
	if !IsWaitlistExam("Лист ожидания 10 мин", 1, nil) {
		t.Fatal("NAME with ожидан must be waitlist")
	}
	if IsWaitlistExam("Прием терапевта", 1, nil) {
		t.Fatal("normal exam must not be waitlist")
	}
	if !IsWaitlistExam("Прием терапевта", 3118, nil) {
		t.Fatal("exam id 3118 must be waitlist even with a normal NAME")
	}
}

func TestExamSelectSQLWaitlistRule(t *testing.T) {
	sql, args := examSelectSQL(nil)
	if !strings.Contains(sql, `LIKE N'%ожидан%'`) || !strings.Contains(sql, "PL_EXAM_ID IN") {
		t.Fatalf("sql=%s", sql)
	}
	if len(args) != 1 {
		t.Fatalf("args=%d", len(args))
	}
}

func TestDashboardConfirmationCountsSkipWaitlist(t *testing.T) {
	rows := []DashboardPlanningRow{
		{PlanningID: 1, PatientName: "A", BranchID: 106, BranchCode: "Каширка", Time: "09:00"},
		{PlanningID: 2, PatientName: "B", BranchID: 106, BranchCode: "Каширка", Time: "09:10", IsWaitlist: true},
		{PlanningID: 3, PatientName: "C", BranchID: 106, BranchCode: "Каширка", Time: "09:20"},
	}
	overlay := map[int64]string{1: "confirmed", 2: "confirmed", 3: "declined"}
	got := BuildDashboardSchedule(time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC), rows, overlay)
	if !got.Branches[0].Appointments[1].IsWaitlist {
		t.Fatal("waitlist flag must be copied")
	}
	counts := DashboardConfirmationCounts(got.Branches)
	if counts["confirmed"] != 1 || counts["declined"] != 1 || counts["pending"] != 0 {
		t.Fatalf("counts=%v", counts)
	}
}
