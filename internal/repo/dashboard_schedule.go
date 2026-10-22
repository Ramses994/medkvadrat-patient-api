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
	ApptVersion string
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
  CAST(ISNULL(p.STATUS,-1) AS int) AS STATUS,
  ISNULL(p.PL_SUBJ_ID, 0) AS PL_SUBJ_ID,
  ISNULL(p.PL_EXAM_ID, 0) AS PL_EXAM_ID,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START_FULL
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
		var timeRaw, dateStart, webName, examName string
		var isWaitlist, status bitInt
		var plSubj, plExam int64
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
			&status,
			&plSubj,
			&plExam,
			&dateStart,
		); err != nil {
			return nil, fmt.Errorf("scan dashboard schedule: %w", err)
		}
		_ = webName
		_ = examName
		r.IsWaitlist = isWaitlist != 0
		r.Status = int(status)
		r.Time = normalizeHHMM(timeRaw)
		r.DoctorName = trimDoctorName(r.DoctorName)
		r.ApptVersion = ApptVersion(dateStart, plSubj, plExam)
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
	PlanningID       int64
	PatientName      string
	DoctorName       string
	Time             string
	Status           int
	Confirmation     *string
	ConfirmationHint string
	IsWaitlist       bool
	OnWaitlist       bool
}

type ConfirmationOverlay struct {
	Status  string
	Version string
}

const ConfirmationUnknownVersionHint = "ответ получен ранее (время могло измениться)"

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
func BuildDashboardSchedule(date time.Time, rows []DashboardPlanningRow, confirmations map[int64]ConfirmationOverlay) DashboardSchedule {
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
			if o, ok := confirmations[row.PlanningID]; ok && o.Status != "" {
				if o.Version == "" || row.ApptVersion == "" || o.Version != row.ApptVersion {
					appt.ConfirmationHint = ConfirmationUnknownVersionHint
				} else {
					s := o.Status
					appt.Confirmation = &s
				}
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

// DashboardConfirmationCounts counts confirmation statuses, skipping waitlist rows.
func DashboardConfirmationCounts(branches []DashboardBranchSchedule) map[string]int {
	counts := map[string]int{"confirmed": 0, "declined": 0, "reschedule": 0, "pending": 0}
	for _, b := range branches {
		for _, a := range b.Appointments {
			if a.IsWaitlist || (a.Confirmation == nil && a.ConfirmationHint != "") {
				continue
			}
			key := "pending"
			if a.Confirmation != nil && *a.Confirmation != "" {
				key = *a.Confirmation
			}
			counts[key]++
		}
	}
	return counts
}
