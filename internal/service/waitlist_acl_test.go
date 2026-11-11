package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestUpdateWaitlist_RegistrarKashirkaBlockedFromKurkino(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:svc_waitlist_acl?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(77,66,'v1','2026-11-06 10:00:00','declined','max','new','2026-11-06T06:00:00Z','2026-11-06T06:00:00Z','2026-11-06 09:00:00',3)`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db}
	reg := staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}}
	_, err = svc.UpdateWaitlist(staff.WithPrincipal(ctx, reg), id, "contacted", "no_answer", "kash")
	if !errors.Is(err, staff.ErrBranchForbidden) {
		t.Fatalf("%v", err)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM waitlist WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "new" {
		t.Fatalf("status=%s", status)
	}
	mgr := staff.Principal{ID: 3, Login: "dir", Role: staff.RoleManager, All: true, BranchIDs: staff.DashboardBranchIDs}
	row, err := svc.UpdateWaitlist(staff.WithPrincipal(ctx, mgr), id, "contacted", "no_answer", "dir")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "contacted" || row.BranchID != 3 {
		t.Fatalf("%+v", row)
	}
}
