package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Dashboard branch FM_ORG_IDs (fixed display order: Каширка, Куркино, Куркино 2).
var DashboardBranchOrder = []int{106, 3, 496}

// DashboardPlanningRow is one appointment from Medialog for the dashboard.
type DashboardPlanningRow struct {
	PlanningID  int64
	PatientName string
	DoctorName  string
	BranchID    int
	BranchCode  string
	Time        string // HH:MM wall-clock
	Status      int
	IsWaitlist  bool
}

func dashboardScheduleSQL(waitlistIDs []int64) (string, []any) {
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS PATIENT_NAME,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
` + examSQL + `,
  o.FM_ORG_ID AS BRANCH_ID,
  ISNULL(o.CODE,'') AS BRANCH_CODE,
  CONVERT(varchar(5), DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS), 108) AS TIME_HHMM,
  ISNULL(p.STATUS,-1) AS STATUS
FROM PLANNING p
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
WHERE p.PATIENTS_ID IS NOT NULL
  AND ISNULL(p.CANCELLED,0) = 0
  AND ps.FM_INTORG_ID IN (106, 3, 496)
  AND p.DATE_CONS = CAST(@date AS date)
ORDER BY o.FM_ORG_ID, TIME_HHMM`
	return q, examArgs
}

// DashboardPlanningRows loads appointments for one calendar day across three branches.
func DashboardPlanningRows(ctx context.Context, db *sql.DB, date time.Time, waitlistIDs []int64) ([]DashboardPlanningRow, error) {
	dateBound := date.Format("2006-01-02")
	q, examArgs := dashboardScheduleSQL(waitlistIDs)
	args := append([]any{sql.Named("date", dateBound)}, examArgs...)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query dashboard schedule: %w", err)
	}
	defer rows.Close()

	var out []DashboardPlanningRow
	for rows.Next() {
		var r DashboardPlanningRow
		var timeRaw, webName, examName string
		var isWaitlist int
		if err := rows.Scan(
			&r.PlanningID,
			new(sql.NullInt64), // PATIENTS_ID — not exposed in dashboard DTO
			&r.PatientName,
			&r.DoctorName,
			&webName,
			&examName,
			&isWaitlist,
			&r.BranchID,
			&r.BranchCode,
			&timeRaw,
			&r.Status,
		); err != nil {
			return nil, fmt.Errorf("scan dashboard schedule: %w", err)
		}
		_ = webName
		_ = examName
		r.IsWaitlist = isWaitlist != 0
		r.Time = normalizeHHMM(timeRaw)
		r.DoctorName = trimDoctorName(r.DoctorName)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows dashboard schedule: %w", err)
	}
	return out, nil
}

func normalizeHHMM(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}

// DashboardAppointment is one slot in the dashboard response (before JSON DTO).
type DashboardAppointment struct {
	PlanningID   int64
	PatientName  string
	DoctorName   string
	Time         string
	Status       int
	Confirmation *string
	IsWaitlist   bool
}

// DashboardBranchSchedule groups appointments for a branch.
type DashboardBranchSchedule struct {
	BranchID     int
	BranchCode   string
	Name         string
	Appointments []DashboardAppointment
}

// DashboardSchedule is the full day schedule for three branches.
type DashboardSchedule struct {
	Date     string
	Branches []DashboardBranchSchedule
}

// BuildDashboardSchedule groups rows by branch in fixed order and merges confirmation overlay.
func BuildDashboardSchedule(date time.Time, rows []DashboardPlanningRow, confirmations map[int64]string) DashboardSchedule {
	byBranch := make(map[int][]DashboardPlanningRow)
	for _, row := range rows {
		byBranch[row.BranchID] = append(byBranch[row.BranchID], row)
	}

	branches := make([]DashboardBranchSchedule, 0, len(DashboardBranchOrder))
	for _, branchID := range DashboardBranchOrder {
		b := DashboardBranchSchedule{
			BranchID:     branchID,
			Appointments: []DashboardAppointment{},
		}
		if br, ok := BranchByID(branchID); ok {
			b.Name = br.Name
		}
		for _, row := range byBranch[branchID] {
			if b.BranchCode == "" {
				b.BranchCode = row.BranchCode
			}
			appt := DashboardAppointment{
				PlanningID:  row.PlanningID,
				PatientName: strings.TrimSpace(row.PatientName),
				DoctorName:  row.DoctorName,
				Time:        row.Time,
				Status:      row.Status,
				IsWaitlist:  row.IsWaitlist,
			}
			if st, ok := confirmations[row.PlanningID]; ok {
				s := st
				appt.Confirmation = &s
			}
			b.Appointments = append(b.Appointments, appt)
		}
		branches = append(branches, b)
	}

	return DashboardSchedule{
		Date:     date.Format("2006-01-02"),
		Branches: branches,
	}
}
