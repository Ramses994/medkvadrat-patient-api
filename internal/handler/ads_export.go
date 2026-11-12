package handler

import (
	"context"
	"database/sql"
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/consents"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

type medialogNames struct{ db *sql.DB }

func (m medialogNames) Names(ctx context.Context, ids []int64) (map[int64]string, error) {
	return repo.PatientNamesByID(ctx, m.db, ids)
}

// NamesFromMedialog reads patient names for the ads export.
func NamesFromMedialog(db *sql.DB) PatientNamer { return medialogNames{db: db} }

// PatientNamer reads names for the ads export. It must not be used for logs.
type PatientNamer interface {
	Names(ctx context.Context, ids []int64) (map[int64]string, error)
}

// AdsExportHandler writes the ads-consent registry. Service consents are omitted.
type AdsExportHandler struct {
	Store  *consents.Store
	Names  PatientNamer
	Logger *slog.Logger
	Now    func() time.Time
}

func (h AdsExportHandler) AdsCSV(w http.ResponseWriter, r *http.Request) {
	p, ok := staff.PrincipalFrom(r.Context())
	if !ok || (p.Role != staff.RoleAdmin && p.Role != staff.RoleManager) {
		response.Error(w, http.StatusForbidden, "FORBIDDEN", "Недостаточно прав")
		return
	}
	if h.Store == nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	rows, err := h.Store.AdsRows(r.Context())
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("ads export", "err", err)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.PatientID
	}
	names := map[int64]string{}
	if h.Names != nil && len(ids) > 0 {
		names, err = h.Names.Names(r.Context(), ids)
		if err != nil {
			if h.Logger != nil {
				h.Logger.Error("ads export names", "err", err)
			}
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
			return
		}
	}
	when := time.Now().UTC()
	if h.Now != nil {
		when = h.Now().UTC()
	}
	if h.Logger != nil {
		h.Logger.Info("ads consents exported",
			"actor_id", p.ID,
			"actor_login", p.Login,
			"rows", len(rows),
			"at", when.Format(time.RFC3339),
		)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ads-consents.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"patient_id", "ФИО", "состояние", "дата согласия", "дата отзыва"})
	for _, row := range rows {
		state := "отозвано"
		if row.RevokedAt == "" {
			state = "действует"
		}
		_ = cw.Write([]string{
			strconv.FormatInt(row.PatientID, 10),
			names[row.PatientID],
			state,
			moscowDate(row.AcceptedAt),
			moscowDate(row.RevokedAt),
		})
	}
	cw.Flush()
}

func moscowDate(rfc string) string {
	if rfc == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return ""
	}
	return t.In(moscowLocation()).Format("02.01.2006")
}
