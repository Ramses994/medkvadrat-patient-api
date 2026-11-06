package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

var requestUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type BookingAPI interface {
	BookSlot(ctx context.Context, patientID int64, token, requestID string) booking.WriteHTTP
	CancelBooking(ctx context.Context, patientID, planningID int64, requestID string) booking.WriteHTTP
	ListWriteOps(ctx context.Context, states []string) ([]writeops.Op, error)
	ResolveWriteOp(ctx context.Context, in booking.ResolveInput) booking.WriteHTTP
}

type BookingHandler struct {
	API   BookingAPI
	Actor string
	LAN   middleware.AllowCIDRs
}

func (h BookingHandler) Book(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SlotToken string `json:"slot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SlotToken) == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Нужен slot_token")
		return
	}
	writeOut(w, h.API.BookSlot(r.Context(), pid, body.SlotToken, key))
}

func (h BookingHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("planning_id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "planning_id должен быть числом")
		return
	}
	writeOut(w, h.API.CancelBooking(r.Context(), pid, id, key))
}

func (h BookingHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.lan(r) {
		response.Error(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("state"))
	var states []string
	if raw != "" {
		states = strings.Split(raw, ",")
	}
	rows, err := h.API.ListWriteOps(r.Context(), states)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "state: unknown или partial")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, op := range rows {
		item := map[string]any{
			"op_id": op.OpID, "patient_id": op.PatientID, "action": op.Action,
			"state": op.State, "code": op.Code, "started_at": op.StartedAt,
		}
		if op.PlanningID.Valid {
			item["planning_id"] = op.PlanningID.Int64
		}
		out = append(out, item)
	}
	response.OK(w, out)
}

func (h BookingHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	if !h.lan(r) {
		response.Error(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
		return
	}
	var body struct {
		State      string `json:"state"`
		PlanningID *int64 `json:"planning_id"`
		Note       string `json:"resolve_note_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	writeOut(w, h.API.ResolveWriteOp(r.Context(), booking.ResolveInput{
		OpID: r.PathValue("op_id"), State: body.State, PlanningID: body.PlanningID, Note: body.Note, Actor: h.Actor,
	}))
}

func (h BookingHandler) lan(r *http.Request) bool {
	if len(h.LAN.Nets) == 0 {
		return false
	}
	return h.LAN.Allowed(staffsession.ClientIP(r.RemoteAddr))
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !requestUUID.MatchString(key) {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Нужен заголовок Idempotency-Key (UUID)")
		return "", false
	}
	return key, true
}

func writeOut(w http.ResponseWriter, out booking.WriteHTTP) {
	if out.Status == 0 {
		response.Error(w, http.StatusInternalServerError, "INTERNAL", booking.MsgRegistry)
		return
	}
	if out.Body != nil && out.Code == "" && (out.Status == http.StatusOK || out.Status == http.StatusCreated) {
		response.JSON(w, out.Status, out.Body)
		return
	}
	if len(out.Slots) > 0 {
		slots := make([]map[string]any, 0, len(out.Slots))
		for _, s := range out.Slots {
			slots = append(slots, map[string]any{"start": s.Start, "duration_min": s.DurationMin})
		}
		response.ErrorWithData(w, out.Status, out.Code, out.Message, map[string]any{"slots": slots})
		return
	}
	response.Error(w, out.Status, out.Code, out.Message)
}
