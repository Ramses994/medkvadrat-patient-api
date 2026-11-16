package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestRouter_WaitlistAuthorSurvivesLoginReuse(t *testing.T) {
	ctx := context.Background()
	h, users, sqlite := staffDeleteRouter(t)
	mustCreateStaff(t, users, "admin", staff.RoleAdmin, "*")
	reg := mustCreateStaff(t, users, "reg", staff.RoleRegistrar, "106")
	if _, err := sqlite.ExecContext(ctx, `UPDATE staff_users SET full_name = ? WHERE id = ?`, "Старый Регистратор", reg.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := sqlite.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(1,1,'v1','2026-11-16 12:00:00','declined','max','new','2026-11-16T09:00:00Z','2026-11-16T09:00:00Z','2026-11-16 12:00:00',106)`); err != nil {
		t.Fatal(err)
	}

	regCookie := loginCookie(t, h, "reg", "password123")
	patch := dashReq(http.MethodPatch, "/dashboard/waitlist/max/1", `{"outcome":"call_back","version":1}`, regCookie)
	patch.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	h.ServeHTTP(pw, patch)
	if pw.Code != http.StatusOK {
		t.Fatalf("mark %d %s", pw.Code, pw.Body.String())
	}

	waitlist := confirmations.NewRepo(sqlite)
	old, err := waitlist.GetWaitlist(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if old.UpdatedBy != "reg" || old.StaffID != reg.ID || old.UpdatedByName != "Старый Регистратор" {
		t.Fatalf("old mark %+v", old)
	}
	if old.AuthorDisplay() != "Старый Регистратор" {
		t.Fatalf("display=%q", old.AuthorDisplay())
	}

	adminCookie := loginCookie(t, h, "admin", "password123")
	if w := deleteStaff(h, reg.ID, adminCookie); w.Code != http.StatusOK {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}

	create := dashReq(http.MethodPost, "/dashboard/staff", `{"login":"reg","full_name":"Новый Регистратор","role":"registrar","branches":"106","password":"password456"}`, adminCookie)
	create.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, create)
	if cw.Code != http.StatusOK {
		t.Fatalf("recreate %d %s", cw.Code, cw.Body.String())
	}
	var newID int64
	if err := sqlite.QueryRowContext(ctx, `SELECT id FROM staff_users WHERE login = 'reg' AND deleted_at IS NULL`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID == reg.ID {
		t.Fatal("new account reused deleted id")
	}

	still, err := waitlist.GetWaitlist(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if still.StaffID != reg.ID || still.UpdatedByName != "Старый Регистратор" || still.AuthorDisplay() != "Старый Регистратор" {
		t.Fatalf("old mark after reuse %+v", still)
	}

	if _, err := sqlite.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(2,2,'v2','2026-11-16 13:00:00','declined','max','new','2026-11-16T10:00:00Z','2026-11-16T10:00:00Z','2026-11-16 13:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	freshCookie := loginCookie(t, h, "reg", "password456")
	patch2 := dashReq(http.MethodPatch, "/dashboard/waitlist/max/2", `{"outcome":"no_answer","version":1}`, freshCookie)
	patch2.Header.Set("Content-Type", "application/json")
	p2 := httptest.NewRecorder()
	h.ServeHTTP(p2, patch2)
	if p2.Code != http.StatusOK {
		t.Fatalf("new mark %d %s", p2.Code, p2.Body.String())
	}
	neu, err := waitlist.GetWaitlist(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if neu.StaffID != newID || neu.UpdatedBy != "reg" || neu.UpdatedByName != "Новый Регистратор" || neu.AuthorDisplay() != "Новый Регистратор" {
		t.Fatalf("new mark %+v want staff_id=%d", neu, newID)
	}
	if still, err := waitlist.GetWaitlist(ctx, 1); err != nil || still.StaffID != reg.ID || still.AuthorDisplay() != "Старый Регистратор" {
		t.Fatalf("old mark changed %+v err=%v", still, err)
	}

	if _, err := sqlite.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,updated_by,created_at,updated_at,created_at_msk,branch_id)
VALUES(3,3,'v3','2026-11-16 14:00:00','declined','max','contacted','reg','2026-11-16T11:00:00Z','2026-11-16T11:00:00Z','2026-11-16 14:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	legacy, err := waitlist.GetWaitlist(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.StaffID != 0 || legacy.UpdatedByName != "" || legacy.AuthorDisplay() != "reg" {
		t.Fatalf("legacy %+v", legacy)
	}
}
