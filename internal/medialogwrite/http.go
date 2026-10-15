package medialogwrite

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

// HTTPDeps bundles Medialog write guard dependencies for HTTP handlers.
type HTTPDeps struct {
	Guard  Guard
	Lookup PlanningLookup
	Log    *LogRepo
	Logger *slog.Logger
}

func (d HTTPDeps) deny(w http.ResponseWriter, r *http.Request, route string, patientID, planningID, plSubjID int64, deny *Deny) {
	if d.Log != nil {
		_, _ = d.Log.Insert(r.Context(), LogEntry{
			RequestID:  middleware.GetRequestID(r.Context()),
			Route:      route,
			PatientID:  patientID,
			PlanningID: planningID,
			PlSubjID:   plSubjID,
			Decision:   deny.Decision,
			Result:     "error",
			ErrorCode:  deny.Code,
		})
	}
	response.Error(w, deny.HTTPStatus, deny.Code, deny.Message)
}

func (d HTTPDeps) logLookupError(r *http.Request, op string, err error) {
	if d.Logger == nil || err == nil {
		return
	}
	d.Logger.Error("medialog write guard lookup failed",
		"request_id", middleware.GetRequestID(r.Context()),
		"op", op,
		"err", err,
	)
}

func (d HTTPDeps) GuardBook(ctx context.Context, w http.ResponseWriter, r *http.Request, route string, patientID int64, planningID int64) (logID int64, ok bool) {
	if deny := d.Guard.CheckEnabled(); deny != nil {
		d.deny(w, r, route, patientID, planningID, 0, deny)
		return 0, false
	}
	if deny := d.Guard.CheckPatient(patientID); deny != nil {
		d.deny(w, r, route, patientID, planningID, 0, deny)
		return 0, false
	}
	plSubj, err := d.Lookup.PlSubjIDForPlanning(ctx, planningID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Слот не найден")
			return 0, false
		}
		d.logLookupError(r, "PlSubjIDForPlanning", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return 0, false
	}
	if deny := d.Guard.CheckPlSubj(plSubj); deny != nil {
		d.deny(w, r, route, patientID, planningID, plSubj, deny)
		return 0, false
	}
	if deny := d.Guard.CheckRWConfigured(); deny != nil {
		d.deny(w, r, route, patientID, planningID, plSubj, deny)
		return 0, false
	}
	if d.Log != nil {
		id, _ := d.Log.Insert(ctx, LogEntry{
			RequestID:  middleware.GetRequestID(r.Context()),
			Route:      route,
			PatientID:  patientID,
			PlanningID: planningID,
			PlSubjID:   plSubj,
			Decision:   DecisionAttempted,
		})
		return id, true
	}
	return 0, true
}

func (d HTTPDeps) GuardCancel(ctx context.Context, w http.ResponseWriter, r *http.Request, route string, patientID, motconsuID int64) (logID int64, ok bool) {
	if deny := d.Guard.CheckEnabled(); deny != nil {
		d.deny(w, r, route, patientID, 0, 0, deny)
		return 0, false
	}
	if deny := d.Guard.CheckPatient(patientID); deny != nil {
		d.deny(w, r, route, patientID, 0, 0, deny)
		return 0, false
	}
	planningID, err := d.Lookup.PlanningIDForMotconsu(ctx, motconsuID, patientID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Запись не найдена")
			return 0, false
		}
		d.logLookupError(r, "PlanningIDForMotconsu", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return 0, false
	}
	plSubj, err := d.Lookup.PlSubjIDForPlanning(ctx, planningID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Слот не найден")
			return 0, false
		}
		d.logLookupError(r, "PlSubjIDForPlanning", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return 0, false
	}
	if deny := d.Guard.CheckPlSubj(plSubj); deny != nil {
		d.deny(w, r, route, patientID, planningID, plSubj, deny)
		return 0, false
	}
	if deny := d.Guard.CheckRWConfigured(); deny != nil {
		d.deny(w, r, route, patientID, planningID, plSubj, deny)
		return 0, false
	}
	if d.Log != nil {
		id, _ := d.Log.Insert(ctx, LogEntry{
			RequestID:  middleware.GetRequestID(r.Context()),
			Route:      route,
			PatientID:  patientID,
			PlanningID: planningID,
			PlSubjID:   plSubj,
			Decision:   DecisionAttempted,
		})
		return id, true
	}
	return 0, true
}

func RespondFeatureDisabled(w http.ResponseWriter) {
	response.Error(w, http.StatusServiceUnavailable, "FEATURE_DISABLED", FeatureDisabledMessage)
}

func FinishWriteLog(ctx context.Context, log *LogRepo, id int64, result, errCode string) {
	if log == nil || id <= 0 {
		return
	}
	_ = log.SetResult(ctx, id, result, errCode)
}
