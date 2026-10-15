package medialogwrite

import (
	"context"
	"database/sql"
	"net/http"
)

const (
	PatientForbiddenMessage = "Запись через приложение пока недоступна. Позвоните в регистратуру: +7 (499) 288-88-14"
	FeatureDisabledMessage  = "Онлайн-запись готовится. Позвоните в регистратуру: +7 (499) 288-88-14"
)

// Guard enforces Medialog write allowlists (D1).
type Guard struct {
	Enabled       bool
	RWConfigured  bool
	AllowPatients map[int64]struct{}
	AllowPlSubj   map[int64]struct{}
}

func NewGuard(enabled, rwConfigured bool, patients, plSubj []int64) Guard {
	return Guard{
		Enabled:       enabled,
		RWConfigured:  rwConfigured,
		AllowPatients: int64Set(patients),
		AllowPlSubj:   int64Set(plSubj),
	}
}

func int64Set(ids []int64) map[int64]struct{} {
	if len(ids) == 0 {
		return map[int64]struct{}{}
	}
	m := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

type Decision string

const (
	DecisionDeniedDisabled Decision = "denied_disabled"
	DecisionDeniedPatient  Decision = "denied_patient"
	DecisionDeniedPlSubj   Decision = "denied_pl_subj"
	DecisionAttempted      Decision = "attempted"
)

type Deny struct {
	HTTPStatus int
	Code       string
	Message    string
	Decision   Decision
}

func (g Guard) CheckRWConfigured() *Deny {
	if !g.RWConfigured {
		return &Deny{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       "WRITE_NOT_CONFIGURED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedDisabled,
		}
	}
	return nil
}

func (g Guard) CheckEnabled() *Deny {
	if !g.Enabled {
		return &Deny{
			HTTPStatus: http.StatusForbidden,
			Code:       "WRITE_DISABLED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedDisabled,
		}
	}
	return nil
}

func (g Guard) CheckPatient(patientID int64) *Deny {
	if len(g.AllowPatients) == 0 {
		return &Deny{
			HTTPStatus: http.StatusForbidden,
			Code:       "WRITE_NOT_ALLOWED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedPatient,
		}
	}
	if _, ok := g.AllowPatients[patientID]; !ok {
		return &Deny{
			HTTPStatus: http.StatusForbidden,
			Code:       "WRITE_NOT_ALLOWED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedPatient,
		}
	}
	return nil
}

func (g Guard) CheckPlSubj(plSubjID int64) *Deny {
	if len(g.AllowPlSubj) == 0 {
		return &Deny{
			HTTPStatus: http.StatusForbidden,
			Code:       "WRITE_NOT_ALLOWED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedPlSubj,
		}
	}
	if _, ok := g.AllowPlSubj[plSubjID]; !ok {
		return &Deny{
			HTTPStatus: http.StatusForbidden,
			Code:       "WRITE_NOT_ALLOWED",
			Message:    PatientForbiddenMessage,
			Decision:   DecisionDeniedPlSubj,
		}
	}
	return nil
}

// PlSubjIDForPlanning reads schedule line id (read-only).
func PlSubjIDForPlanning(ctx context.Context, db *sql.DB, planningID int64) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx, `
SELECT PL_SUBJ_ID FROM PLANNING WHERE PLANNING_ID = @id`,
		sql.Named("id", planningID),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// PlanningIDForMotconsu returns planning_id for a patient's appointment (read-only).
func PlanningIDForMotconsu(ctx context.Context, db *sql.DB, motconsuID, patientID int64) (int64, error) {
	var planningID sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT PLANNING_ID FROM MOTCONSU WHERE MOTCONSU_ID = @mid AND PATIENTS_ID = @pid`,
		sql.Named("mid", motconsuID),
		sql.Named("pid", patientID),
	).Scan(&planningID)
	if err != nil {
		return 0, err
	}
	if !planningID.Valid || planningID.Int64 <= 0 {
		return 0, ErrNoPlanning
	}
	return planningID.Int64, nil
}
