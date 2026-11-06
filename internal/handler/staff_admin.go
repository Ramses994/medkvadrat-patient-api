package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

type StaffHandler struct {
	Store  *staff.Store
	Logger *slog.Logger
}

type staffPublic struct {
	ID                 int64  `json:"id"`
	Login              string `json:"login"`
	FullName           string `json:"full_name"`
	Role               string `json:"role"`
	Branches           string `json:"branches"`
	Active             bool   `json:"active"`
	MustChangePassword bool   `json:"must_change_password"`
}

func toPublic(u staff.User) staffPublic {
	return staffPublic{
		ID: u.ID, Login: u.Login, FullName: u.FullName, Role: u.Role,
		Branches: u.Branches, Active: u.Active, MustChangePassword: u.MustChangePassword,
	}
}

func requireAdmin(w http.ResponseWriter, r *http.Request) (staff.Principal, bool) {
	p, ok := staff.PrincipalFrom(r.Context())
	if !ok || p.Role != staff.RoleAdmin {
		response.Error(w, http.StatusForbidden, "FORBIDDEN", "Только для администратора")
		return staff.Principal{}, false
	}
	return p, true
}

func (h StaffHandler) Me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := staff.PrincipalFrom(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход")
		return
	}
	type branch struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	names := map[int]string{106: "Каширка", 3: "Куркино", 496: "Куркино 2"}
	branches := make([]branch, 0)
	for _, id := range p.VisibleBranches() {
		branches = append(branches, branch{ID: id, Name: names[id]})
	}
	response.OK(w, map[string]any{
		"login": p.Login, "full_name": p.FullName, "role": p.Role,
		"branches": branches, "must_change_password": p.MustChangePassword,
	})
}

func (h StaffHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	users, err := h.Store.List(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]staffPublic, 0, len(users))
	for _, u := range users {
		out = append(out, toPublic(u))
	}
	response.OK(w, map[string]any{"users": out})
}

func (h StaffHandler) Create(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Login    string `json:"login"`
		FullName string `json:"full_name"`
		Role     string `json:"role"`
		Branches string `json:"branches"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}
	user, temp, err := h.Store.Create(r.Context(), staff.CreateUser{
		Login: body.Login, FullName: body.FullName, Role: body.Role, Branches: body.Branches, Password: body.Password,
	})
	if err != nil {
		h.writeStaffErr(w, err)
		return
	}
	if h.Logger != nil {
		h.Logger.Info("staff created", "staff_id", user.ID, "role", user.Role)
	}
	payload := map[string]any{"user": toPublic(user)}
	if temp != "" {
		payload["temporary_password"] = temp
	}
	response.OK(w, payload)
}

func (h StaffHandler) Update(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "id")
		return
	}
	var body struct {
		Role          string `json:"role"`
		Branches      string `json:"branches"`
		Active        *bool  `json:"active"`
		ResetPassword bool   `json:"reset_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}
	user, temp, err := h.Store.Update(r.Context(), actor.ID, id, staff.UpdateUser{
		Role: body.Role, Branches: body.Branches, Active: body.Active, ResetPassword: body.ResetPassword,
	})
	if err != nil {
		h.writeStaffErr(w, err)
		return
	}
	if h.Logger != nil {
		h.Logger.Info("staff updated", "staff_id", user.ID, "role", user.Role, "active", user.Active)
	}
	payload := map[string]any{"user": toPublic(user)}
	if temp != "" {
		payload["temporary_password"] = temp
	}
	response.OK(w, payload)
}

func (h StaffHandler) writeStaffErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, staff.ErrShortPassword):
		response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Пароль не короче 10 символов")
	case errors.Is(err, staff.ErrBadRole):
		response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Роль: admin, manager или registrar")
	case errors.Is(err, staff.ErrLastAdmin):
		response.Error(w, http.StatusConflict, "LAST_ADMIN", "Нельзя выключить последнего администратора")
	case errors.Is(err, staff.ErrNoBranches):
		response.Error(w, http.StatusBadRequest, "VALIDATION", "У регистратора нет филиалов")
	case errors.Is(err, staff.ErrNotFound):
		response.Error(w, http.StatusNotFound, "NOT_FOUND", "Сотрудник не найден")
	default:
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
			response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Такой логин уже есть")
			return
		}
		response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Проверьте логин, роль и филиалы")
	}
}
