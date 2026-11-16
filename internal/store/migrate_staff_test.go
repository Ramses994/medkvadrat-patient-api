package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrate_StaffLoginUniqueOnlyAmongLiveRows(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.Exec(`
CREATE TABLE staff_users (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  login                TEXT NOT NULL UNIQUE,
  full_name            TEXT NOT NULL,
  password_hash        TEXT NOT NULL,
  role                 TEXT NOT NULL,
  branches             TEXT NOT NULL,
  active               INTEGER NOT NULL DEFAULT 1,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL,
  last_login_at        TEXT
)`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`INSERT INTO staff_users (id, login, full_name, password_hash, role, branches, created_at, updated_at, last_login_at)
		 VALUES (1, 'admin', 'Администратор', 'h1', 'admin', '*', 't', 't', 'l1')`,
		`INSERT INTO staff_users (id, login, full_name, password_hash, role, branches, created_at, updated_at)
		 VALUES (7, 'reg106', 'Регистратор', 'h7', 'registrar', '106', 't', 't')`,
		`INSERT INTO staff_users (id, login, full_name, password_hash, role, branches, created_at, updated_at)
		 VALUES (9, 'gone', 'Стёртый', 'h9', 'registrar', '106', 't', 't')`,
		`DELETE FROM staff_users WHERE id = 9`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}
	var inline, live int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'staff_users' AND name LIKE 'sqlite_autoindex_%'`).Scan(&inline); err != nil || inline != 0 {
		t.Fatalf("inline unique %d err=%v", inline, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_staff_users_login_live'`).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live index %d err=%v", live, err)
	}
	var login, hash, last string
	var version int
	if err := db.QueryRow(`SELECT login, password_hash, IFNULL(last_login_at,''), session_version FROM staff_users WHERE id = 1`).Scan(&login, &hash, &last, &version); err != nil {
		t.Fatal(err)
	}
	if login != "admin" || hash != "h1" || last != "l1" || version != 1 {
		t.Fatalf("row lost: %q %q %q %d", login, hash, last, version)
	}
	insert := `INSERT INTO staff_users (login, full_name, password_hash, role, branches, created_at, updated_at)
	 VALUES ('reg106', 'Новый', 'h', 'registrar', '106', 't', 't')`
	if _, err := db.Exec(insert); err == nil {
		t.Fatal("duplicate live login accepted")
	}
	if _, err := db.Exec(`UPDATE staff_users SET deleted_at = 't' WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(insert)
	if err != nil {
		t.Fatalf("login of a deleted row not freed: %v", err)
	}
	id, _ := res.LastInsertId()
	if id <= 9 {
		t.Fatalf("autoincrement reused id %d", id)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM staff_users WHERE login = 'reg106'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("reg106 rows %d err=%v", n, err)
	}
}
