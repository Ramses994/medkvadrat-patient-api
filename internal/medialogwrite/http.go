package medialogwrite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

const (
	MedialogQueryTimeout = 10 * time.Second
	writeLogFinishTimeout = 3 * time.Second
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

func (d HTTPDeps) denyWriteLogUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	if d.Logger != nil {
		d.Logger.Error("medialog write log unavailable",
			"request_id", middleware.GetRequestID(r.Context()),
			"err", err,
		)
	}
	response.Error(w, http.StatusServiceUnavailable, "WRITE_LOG_UNAVAILABLE", PatientForbiddenMessage)
}

func (d HTTPDeps) beginWriteAttempt(ctx context.Context, w http.ResponseWriter, r *http.Request, route string, patientID, planningID, plSubjID int64) (int64, bool) {
	if d.Log == nil {
		d.denyWriteLogUnavailable(w, r, errors.New("log repo nil"))
		return 0, false
	}
	id, err := d.Log.Insert(ctx, LogEntry{
		RequestID:  middleware.GetRequestID(r.Context()),
		Route:      route,
		PatientID:  patientID,
		PlanningID: planningID,
		PlSubjID:   plSubjID,
		Decision:   DecisionAttempted,
	})
	if err != nil || id <= 0 {
		if err == nil {
			err = errors.New("insert returned no id")
		}
		d.denyWriteLogUnavailable(w, r, err)
		return 0, false
	}
	return id, true
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
	qCtx, cancel := context.WithTimeout(ctx, MedialogQueryTimeout)
	defer cancel()

	plSubj, err := d.Lookup.PlSubjIDForPlanning(qCtx, planningID)
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
	return d.beginWriteAttempt(ctx, w, r, route, patientID, planningID, plSubj)
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
	qCtx, cancel := context.WithTimeout(ctx, MedialogQueryTimeout)
	defer cancel()

	planningID, err := d.Lookup.PlanningIDForMotconsu(qCtx, motconsuID, patientID)
	if err != nil {
		if err == sql.ErrNoRows {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Запись не найдена")
			return 0, false
		}
		if errors.Is(err, ErrNoPlanning) {
			response.Error(w, http.StatusConflict, "CANCEL_NOT_SUPPORTED", "Эту запись можно отменить только через регистратуру +7 (499) 288-88-14")
			return 0, false
		}
		d.logLookupError(r, "PlanningIDForMotconsu", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return 0, false
	}
	plSubj, err := d.Lookup.PlSubjIDForPlanning(qCtx, planningID)
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
	return d.beginWriteAttempt(ctx, w, r, route, patientID, planningID, plSubj)
}

func RespondFeatureDisabled(w http.ResponseWriter) {
	response.Error(w, http.StatusServiceUnavailable, "FEATURE_DISABLED", FeatureDisabledMessage)
}

func FinishWriteLog(logger *slog.Logger, requestID string, log *LogRepo, id int64, result, errCode string) {
	if log == nil || id <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeLogFinishTimeout)
	defer cancel()
	if err := log.SetResult(ctx, id, result, errCode); err != nil && logger != nil {
		logger.Error("medialog write log finish failed",
			"request_id", requestID,
			"log_id", id,
			"err", err,
		)
	}
}
