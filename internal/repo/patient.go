package repo

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PatientRepo struct {
	db *sql.DB
}

func (r *PatientRepo) SearchByPhone(ctx context.Context, phoneLast10 string) ([]PatientInfo, error) {
	query := `
		SELECT PATIENTS_ID,
			ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
			ISNULL(MOBIL_TELEFON, ISNULL(TEL, ISNULL(RAB_TEL,''))) AS PHONE,
			NULL AS BIRTH_DATE
		FROM PATIENTS
		WHERE REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(MOBIL_TELEFON,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'
		   OR REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(TEL,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'
		   OR REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(RAB_TEL,''),'+',''),'-',''),' ',''),'(','') LIKE '%' + @phone + '%'`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("phone", phoneLast10))
	if err != nil {
		return nil, fmt.Errorf("query patient search: %w", err)
	}
	defer rows.Close()

	var patients []PatientInfo
	for rows.Next() {
		var p PatientInfo
		var bd sql.NullTime
		if err := rows.Scan(&p.PatientID, &p.FullName, &p.Phone, &bd); err != nil {
			return nil, fmt.Errorf("scan patient search: %w", err)
		}
		if bd.Valid {
			t := bd.Time
			p.BirthDate = &t
		}
		patients = append(patients, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows patient search: %w", err)
	}
	return patients, nil
}

func (r *PatientRepo) LabResults(ctx context.Context, patientID string, daysBack int) ([]LabResult, error) {
	query := `
		SELECT
			LAB_ANT_RESULTS_ID, ISNULL(PATDIREC_ID,0) AS PATDIREC_ID,
			ISNULL(GROUP_NAME,'')   AS GROUP_NAME,
			ISNULL(CODE,'')         AS CODE,
			ISNULL(NAME,'')         AS NAME,
			ISNULL(VALUE,'')        AS VALUE,
			ISNULL(UNIT_NAME,'')    AS UNIT_NAME,
			ISNULL(NORMS,'')        AS NORMS,
			ISNULL(METHOD_NAME,'')  AS METHOD_NAME,
			ISNULL(APPROVING_DOCTOR,'') AS APPROVING_DOCTOR,
			KRN_CREATE_DATE,
			ISNULL(CAST(TEST_COMMENT AS varchar(4000)),'') AS TEST_COMMENT
		FROM LAB_ANT_RESULTS
		WHERE PATIENTS_ID = @patID
		  AND (@days = 0 OR KRN_CREATE_DATE >= DATEADD(day, -@days, GETDATE()))
		ORDER BY KRN_CREATE_DATE DESC, PATDIREC_ID DESC, GROUP_NAME, NAME`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("patID", patientID), sql.Named("days", daysBack))
	if err != nil {
		return nil, fmt.Errorf("query lab results: %w", err)
	}
	defer rows.Close()

	var results []LabResult
	for rows.Next() {
		var x LabResult
		if err := rows.Scan(
			&x.ResultID, &x.PatdirecID, &x.GroupName, &x.Code, &x.Name,
			&x.Value, &x.Unit, &x.Norms, &x.Method, &x.ApprovedBy,
			&x.ReadyAt, &x.TestComment,
		); err != nil {
			return nil, fmt.Errorf("scan lab results: %w", err)
		}
		results = append(results, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows lab results: %w", err)
	}
	return results, nil
}

func (r *PatientRepo) LabPanels(ctx context.Context, patientID string, daysBack int) ([]LabPanelRow, error) {
	query := `
		SELECT
			r.LAB_ANT_RESULTS_ID,
			ISNULL(r.PATDIREC_ID,0) AS PATDIREC_ID,
			ISNULL(LEFT(CAST(p.DESCRIPTION AS varchar(500)), 250),'') AS PANEL_NAME,
			p.CREATE_DATE_TIME AS ORDERED_AT,
			ISNULL(r.GROUP_NAME,'')   AS GROUP_NAME,
			ISNULL(r.CODE,'')         AS CODE,
			ISNULL(r.NAME,'')         AS NAME,
			ISNULL(r.VALUE,'')        AS VALUE,
			ISNULL(r.UNIT_NAME,'')    AS UNIT_NAME,
			ISNULL(r.NORMS,'')        AS NORMS,
			ISNULL(r.METHOD_NAME,'')  AS METHOD_NAME,
			ISNULL(r.APPROVING_DOCTOR,'') AS APPROVING_DOCTOR,
			r.KRN_CREATE_DATE,
			ISNULL(CAST(r.TEST_COMMENT AS varchar(4000)),'') AS TEST_COMMENT
		FROM LAB_ANT_RESULTS r
		LEFT JOIN PATDIREC p ON p.PATDIREC_ID = r.PATDIREC_ID
		WHERE r.PATIENTS_ID = @patID
		  AND (@days = 0 OR r.KRN_CREATE_DATE >= DATEADD(day, -@days, GETDATE()))
		ORDER BY r.PATDIREC_ID DESC, r.GROUP_NAME, r.NAME`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("patID", patientID), sql.Named("days", daysBack))
	if err != nil {
		return nil, fmt.Errorf("query lab panels: %w", err)
	}
	defer rows.Close()

	var out []LabPanelRow
	for rows.Next() {
		var row LabPanelRow
		var orderedAt sql.NullTime
		if err := rows.Scan(
			&row.ResultID, &row.PatdirecID, &row.PanelName, &orderedAt,
			&row.GroupName, &row.Code, &row.Name, &row.Value, &row.Unit, &row.Norms,
			&row.Method, &row.ApprovedBy, &row.ReadyAt, &row.TestComment,
		); err != nil {
			return nil, fmt.Errorf("scan lab panels: %w", err)
		}
		if orderedAt.Valid {
			t := orderedAt.Time
			row.OrderedAt = &t
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows lab panels: %w", err)
	}
	return out, nil
}

func CleanPhoneLast10(phone string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(clean) > 10 {
		clean = clean[len(clean)-10:]
	}
	return clean
}

// Group panels exactly like legacy handler: keep sort by readyAtRaw desc.
type LabPanel struct {
	PatdirecID    int            `json:"patdirec_id"`
	PanelName     string         `json:"panel_name"`
	OrderedAt     string         `json:"ordered_at,omitempty"`
	ReadyAt       string         `json:"ready_at"`
	readyAtRaw    time.Time      `json:"-"`
	TestsCount    int            `json:"tests_count"`
	HasOutOfRange bool           `json:"has_out_of_range"`
	Tests         []LabResultDTO `json:"tests"`
}

type LabResultDTO struct {
	ResultID    int    `json:"result_id"`
	PatdirecID  int    `json:"patdirec_id"`
	GroupName   string `json:"group_name,omitempty"`
	Code        string `json:"code,omitempty"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
	Norms       string `json:"norms,omitempty"`
	InRange     *bool  `json:"in_range,omitempty"`
	Method      string `json:"method,omitempty"`
	ApprovedBy  string `json:"approved_by,omitempty"`
	ReadyAt     string `json:"ready_at"`
	TestComment string `json:"test_comment,omitempty"`
}

func BuildLabPanels(rows []LabPanelRow) []LabPanel {
	panelMap := map[int]*LabPanel{}
	panelOrder := []int{}

	for _, r := range rows {
		test := LabResultDTO{
			ResultID:    r.ResultID,
			PatdirecID:  r.PatdirecID,
			GroupName:   r.GroupName,
			Code:        r.Code,
			Name:        r.Name,
			Value:       r.Value,
			Unit:        r.Unit,
			Norms:       r.Norms,
			Method:      r.Method,
			ApprovedBy:  r.ApprovedBy,
			ReadyAt:     r.ReadyAt.Format("2006-01-02 15:04"),
			TestComment: r.TestComment,
		}
		if inRange, ok := CheckInRangePublic(r.Value, r.Norms); ok {
			test.InRange = &inRange
		}

		panel, exists := panelMap[r.PatdirecID]
		if !exists {
			panel = &LabPanel{
				PatdirecID: r.PatdirecID,
				PanelName:  r.PanelName,
				ReadyAt:    r.ReadyAt.Format("2006-01-02 15:04"),
				readyAtRaw: r.ReadyAt,
			}
			if r.OrderedAt != nil {
				panel.OrderedAt = r.OrderedAt.Format("2006-01-02 15:04")
			}
			panelMap[r.PatdirecID] = panel
			panelOrder = append(panelOrder, r.PatdirecID)
		}

		panel.Tests = append(panel.Tests, test)
		panel.TestsCount++

		if r.ReadyAt.After(panel.readyAtRaw) {
			panel.readyAtRaw = r.ReadyAt
			panel.ReadyAt = test.ReadyAt
		}
		if test.InRange != nil && !*test.InRange {
			panel.HasOutOfRange = true
		}
	}

	panels := make([]LabPanel, 0, len(panelOrder))
	for _, pid := range panelOrder {
		panels = append(panels, *panelMap[pid])
	}
	sort.Slice(panels, func(i, j int) bool {
		return panels[i].readyAtRaw.After(panels[j].readyAtRaw)
	})
	return panels
}

func CheckInRangePublic(valueStr, normsStr string) (bool, bool) {
	if valueStr == "" || normsStr == "" {
		return false, false
	}
	v, err := strconvParseFloatComma(valueStr)
	if err != nil {
		return false, false
	}
	parts := strings.Split(strings.ReplaceAll(normsStr, ",", "."), "-")
	if len(parts) != 2 {
		return false, false
	}
	lo, err1 := strconvParseFloatComma(parts[0])
	hi, err2 := strconvParseFloatComma(parts[1])
	if err1 != nil || err2 != nil {
		return false, false
	}
	return v >= lo && v <= hi, true
}

func strconvParseFloatComma(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
}
