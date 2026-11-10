package staff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin     = "admin"
	RoleManager   = "manager"
	RoleRegistrar = "registrar"

	BcryptCost     = 12
	MinPasswordLen = 10
	LockFailures   = 5
	LockWindow     = 15 * time.Minute
)

var (
	ErrNotFound        = errors.New("staff not found")
	ErrInactive        = errors.New("staff inactive")
	ErrBadRole         = errors.New("staff role")
	ErrBadCredentials  = errors.New("bad credentials")
	ErrLocked          = errors.New("login locked")
	ErrShortPassword   = errors.New("password too short")
	ErrLastAdmin       = errors.New("last admin")
	ErrStale           = errors.New("staff stale")
	ErrBranchForbidden = errors.New("BRANCH_FORBIDDEN")
	ErrNoBranches      = errors.New("no branches")
	ErrJournal         = errors.New("login journal")
)

// Dashboard branch ids in display order: Каширка, Куркино, Куркино 2.
var DashboardBranchIDs = []int{106, 3, 496}

type User struct {
	ID                 int64
	Login              string
	FullName           string
	Role               string
	Branches           string
	Active             bool
	MustChangePassword bool
	CreatedAt          string
	UpdatedAt          string
	LastLoginAt        string
	SessionVersion     int
}

type Principal struct {
	ID                 int64
	Login              string
	FullName           string
	Role               string
	All                bool
	BranchIDs          []int
	MustChangePassword bool
	SessionVersion     int
}

func (p Principal) Allows(branch int) bool {
	if !knownBranch(branch) {
		return false
	}
	if p.All {
		return true
	}
	for _, id := range p.BranchIDs {
		if id == branch {
			return true
		}
	}
	return false
}

func SeesBranch(p Principal, branch int) bool {
	if knownBranch(branch) {
		return p.Allows(branch)
	}
	return p.All || p.Role == RoleAdmin || p.Role == RoleManager
}

func (p Principal) VisibleBranches() []int {
	if p.All {
		return append([]int(nil), DashboardBranchIDs...)
	}
	out := make([]int, 0, len(p.BranchIDs))
	for _, id := range DashboardBranchIDs {
		if p.Allows(id) {
			out = append(out, id)
		}
	}
	return out
}

type Store struct {
	db      *sql.DB
	Compare func(hash, password []byte) error
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func ValidRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleAdmin, RoleManager, RoleRegistrar:
		return true
	default:
		return false
	}
}

func knownBranch(id int) bool {
	for _, b := range DashboardBranchIDs {
		if b == id {
			return true
		}
	}
	return false
}

// ParseBranches accepts "*" (all branches) or a CSV of 106, 3, 496.
// An empty list is an error. Only admin and manager may keep "*".
func ParseBranches(raw string) (all bool, ids []int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return true, append([]int(nil), DashboardBranchIDs...), nil
	}
	if raw == "" {
		return false, nil, errors.New("branches empty")
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var id int
		if _, scanErr := fmt.Sscan(part, &id); scanErr != nil || !knownBranch(id) {
			return false, nil, fmt.Errorf("branch %q", part)
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return false, nil, errors.New("branches empty")
	}
	return false, ids, nil
}

func principalFrom(u User) (Principal, error) {
	if !u.Active {
		return Principal{}, ErrInactive
	}
	if !ValidRole(u.Role) {
		return Principal{}, ErrBadRole
	}
	all, ids, err := ParseBranches(u.Branches)
	if u.Role == RoleAdmin || u.Role == RoleManager {
		all = true
		ids = append([]int(nil), DashboardBranchIDs...)
	} else if err != nil || all || len(ids) == 0 {
		return Principal{}, ErrNoBranches
	}
	ver := u.SessionVersion
	if ver < 1 {
		ver = 1
	}
	return Principal{
		ID: u.ID, Login: u.Login, FullName: u.FullName, Role: u.Role,
		All: all, BranchIDs: ids, MustChangePassword: u.MustChangePassword,
		SessionVersion: ver,
	}, nil
}

func (s *Store) ByID(ctx context.Context, id int64) (Principal, error) {
	u, err := s.userByID(ctx, id)
	if err != nil {
		return Principal{}, err
	}
	return principalFrom(u)
}

func (s *Store) userByID(ctx context.Context, id int64) (User, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, login, full_name, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,''), IFNULL(session_version,1)
FROM staff_users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) userByLogin(ctx context.Context, login string) (User, string, error) {
	var u User
	var hash string
	var active, must int
	err := s.db.QueryRowContext(ctx, `
SELECT id, login, full_name, password_hash, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,''), IFNULL(session_version,1)
FROM staff_users WHERE login = ?`, login).Scan(
		&u.ID, &u.Login, &u.FullName, &hash, &u.Role, &u.Branches, &active, &must,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &u.SessionVersion,
	)
	if err == sql.ErrNoRows {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", err
	}
	u.Active = active != 0
	u.MustChangePassword = must != 0
	return u, hash, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (User, error) {
	var u User
	var active, must int
	err := row.Scan(
		&u.ID, &u.Login, &u.FullName, &u.Role, &u.Branches, &active, &must,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt, &u.SessionVersion,
	)
	if err == sql.ErrNoRows {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.Active = active != 0
	u.MustChangePassword = must != 0
	return u, nil
}

// EnsureFirstAdmin inserts the admin from the shared dashboard password once.
// An empty password leaves the table empty (dashboard stays disabled).
func EnsureFirstAdmin(ctx context.Context, db *sql.DB, login, password string) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_users`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return false, nil
	}
	login = strings.TrimSpace(login)
	if login == "" {
		login = "admin"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
INSERT INTO staff_users (
  login, full_name, password_hash, role, branches, active, must_change_password, created_at, updated_at
) VALUES (?, 'Администратор', ?, 'admin', '*', 1, 1, ?, ?)`,
		login, string(hash), now, now)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) Locked(ctx context.Context, login string, now time.Time) (bool, error) {
	since := now.Add(-LockWindow).UTC().Format(time.RFC3339)
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM staff_login_log
WHERE login = ? AND success = 0 AND created_at >= ?
  AND id > IFNULL((SELECT MAX(id) FROM staff_login_log WHERE login = ? AND success = 1), 0)`,
		login, since, login).Scan(&n)
	if err != nil {
		return false, err
	}
	return n >= LockFailures, nil
}

func (s *Store) logAttempt(ctx context.Context, login, ip string, success bool, now time.Time) error {
	bit := 0
	if success {
		bit = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO staff_login_log (login, ip, success, created_at) VALUES (?, ?, ?, ?)`,
		login, ip, bit, now.UTC().Format(time.RFC3339))
	return err
}

// Authenticate reserves one journal row before bcrypt. A journal error refuses the login.
func (s *Store) Authenticate(ctx context.Context, login, password, ip string, now time.Time) (Principal, error) {
	login = strings.TrimSpace(login)
	logID, allowed, err := s.reserveAttempt(ctx, login, ip, now)
	if err != nil {
		return Principal{}, err
	}
	if !allowed {
		return Principal{}, ErrLocked
	}
	u, hash, err := s.userByLogin(ctx, login)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Principal{}, err
	}
	compare := s.compare
	if errors.Is(err, ErrNotFound) || !u.Active {
		_ = compare(dummyPasswordHash(), []byte(password))
		return Principal{}, ErrBadCredentials
	}
	if compare([]byte(hash), []byte(password)) != nil {
		return Principal{}, ErrBadCredentials
	}
	p, err := principalFrom(u)
	if err != nil {
		return Principal{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE staff_login_log SET success = 1 WHERE id = ?`, logID); err != nil {
		return Principal{}, ErrJournal
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE staff_users SET last_login_at = ? WHERE id = ?`, now.UTC().Format(time.RFC3339), u.ID)
	return p, nil
}

func (s *Store) compare(hash, password []byte) error {
	if s.Compare != nil {
		return s.Compare(hash, password)
	}
	return bcrypt.CompareHashAndPassword(hash, password)
}

func (s *Store) reserveAttempt(ctx context.Context, login, ip string, now time.Time) (int64, bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, false, ErrJournal
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, false, ErrJournal
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	res, err := conn.ExecContext(ctx, `
INSERT INTO staff_login_log (login, ip, success, created_at) VALUES (?, ?, 0, ?)`,
		login, ip, now.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, false, ErrJournal
	}
	logID, err := res.LastInsertId()
	if err != nil {
		return 0, false, ErrJournal
	}
	since := now.Add(-LockWindow).UTC().Format(time.RFC3339)
	var n int
	if err := conn.QueryRowContext(ctx, `
SELECT COUNT(*) FROM staff_login_log
WHERE login = ? AND success = 0 AND created_at >= ?
  AND id > IFNULL((SELECT MAX(id) FROM staff_login_log WHERE login = ? AND success = 1), 0)`,
		login, since, login).Scan(&n); err != nil {
		return 0, false, ErrJournal
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, false, ErrJournal
	}
	committed = true
	return logID, n <= LockFailures, nil
}

var (
	dummyHash     []byte
	dummyHashOnce sync.Once
)

func dummyPasswordHash() []byte {
	dummyHashOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("staff-unknown-user"), BcryptCost)
	})
	return dummyHash
}

func passwordLongEnough(password string) bool {
	return utf8.RuneCountInString(password) >= MinPasswordLen
}

// BootstrapPasswordWarning is logged at startup when the first admin password
// is shorter than the rule that applies after the mandatory change.
func BootstrapPasswordWarning(password string) string {
	password = strings.TrimSpace(password)
	if password == "" || passwordLongEnough(password) {
		return ""
	}
	return "смените пароль администратора при первом входе"
}

// withImmediate runs fn inside BEGIN IMMEDIATE and closes the connection
// before it returns, so the single production connection is free for the next query.
func (s *Store) withImmediate(ctx context.Context, fn func(context.Context, *sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func userByIDConn(ctx context.Context, conn *sql.Conn, id int64) (User, error) {
	row := conn.QueryRowContext(ctx, `
SELECT id, login, full_name, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,''), IFNULL(session_version,1)
FROM staff_users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) ChangePassword(ctx context.Context, id int64, current, next string) error {
	if !passwordLongEnough(next) {
		return ErrShortPassword
	}
	var hash, login string
	var version int
	err := s.db.QueryRowContext(ctx, `
SELECT password_hash, login, IFNULL(session_version,1)
FROM staff_users WHERE id = ?`, id).Scan(&hash, &login, &version)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	compare := s.Compare
	if compare == nil {
		compare = bcrypt.CompareHashAndPassword
	}
	if compare([]byte(hash), []byte(current)) != nil {
		return ErrBadCredentials
	}
	nextHash, err := bcrypt.GenerateFromPassword([]byte(next), BcryptCost)
	if err != nil {
		return err
	}
	return s.withImmediate(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var hash2 string
		var ver2 int
		err := conn.QueryRowContext(ctx, `
SELECT password_hash, IFNULL(session_version,1)
FROM staff_users WHERE id = ?`, id).Scan(&hash2, &ver2)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if hash2 != hash || ver2 != version {
			return ErrStale
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err = conn.ExecContext(ctx, `
UPDATE staff_users
SET password_hash = ?, must_change_password = 0, session_version = session_version + 1, updated_at = ?
WHERE id = ?`, string(nextHash), now, id); err != nil {
			return err
		}
		return insertAdminLog(ctx, conn, id, login, id, "password", now)
	})
}

type CreateUser struct {
	Login      string
	FullName   string
	Role       string
	Branches   string
	Password   string
	ActorID    int64
	ActorLogin string
}

func (s *Store) Create(ctx context.Context, in CreateUser) (User, string, error) {
	login := strings.TrimSpace(in.Login)
	if login == "" || strings.ContainsAny(login, " \t") {
		return User{}, "", errors.New("login")
	}
	in.Role = strings.ToLower(strings.TrimSpace(in.Role))
	if !ValidRole(in.Role) {
		return User{}, "", ErrBadRole
	}
	branches, err := normalizeBranches(in.Role, in.Branches)
	if err != nil {
		return User{}, "", err
	}
	in.Branches = branches
	temp := ""
	password := in.Password
	must := false
	if password == "" {
		var err error
		password, err = randomPassword(12)
		if err != nil {
			return User{}, "", err
		}
		temp = password
		must = true
	}
	if !passwordLongEnough(password) {
		return User{}, "", ErrShortPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return User{}, "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	mustBit := 0
	if must {
		mustBit = 1
	}
	var id int64
	err = s.withImmediate(ctx, func(ctx context.Context, conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
INSERT INTO staff_users (
  login, full_name, password_hash, role, branches, active, must_change_password, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			login, strings.TrimSpace(in.FullName), string(hash), strings.ToLower(in.Role), strings.TrimSpace(in.Branches), mustBit, now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		change := fmt.Sprintf("create;role=%s;branches=%s", strings.ToLower(in.Role), strings.TrimSpace(in.Branches))
		return insertAdminLog(ctx, conn, in.ActorID, in.ActorLogin, id, change, now)
	})
	if err != nil {
		return User{}, "", err
	}
	u, err := s.userByID(ctx, id)
	return u, temp, err
}

type UpdateUser struct {
	Role          string
	Branches      string
	Active        *bool
	ResetPassword bool
}

func (s *Store) Update(ctx context.Context, actorID, id int64, in UpdateUser) (User, string, error) {
	if in.Role != "" && !ValidRole(strings.ToLower(strings.TrimSpace(in.Role))) {
		return User{}, "", ErrBadRole
	}
	temp := ""
	var nextHash []byte
	if in.ResetPassword {
		var err error
		temp, err = randomPassword(12)
		if err != nil {
			return User{}, "", err
		}
		if !passwordLongEnough(temp) {
			return User{}, "", ErrShortPassword
		}
		nextHash, err = bcrypt.GenerateFromPassword([]byte(temp), BcryptCost)
		if err != nil {
			return User{}, "", err
		}
	}
	actorLogin := ""
	if actorID != 0 {
		if actor, err := s.userByID(ctx, actorID); err == nil {
			actorLogin = actor.Login
		}
	}
	err := s.withImmediate(ctx, func(ctx context.Context, conn *sql.Conn) error {
		u, err := userByIDConn(ctx, conn, id)
		if err != nil {
			return err
		}
		role := u.Role
		if in.Role != "" {
			role = strings.ToLower(strings.TrimSpace(in.Role))
		}
		branches := u.Branches
		branchSource := u.Branches
		if in.Branches != "" {
			branchSource = in.Branches
		}
		if in.Branches != "" || role != u.Role {
			branches, err = normalizeBranches(role, branchSource)
			if err != nil {
				return err
			}
		}
		active := u.Active
		if in.Active != nil {
			active = *in.Active
		}
		now := time.Now().UTC().Format(time.RFC3339)
		bit := 0
		if active {
			bit = 1
		}
		bump := 0
		if role != u.Role || branches != u.Branches || active != u.Active || nextHash != nil {
			bump = 1
		}
		demote := u.Role == RoleAdmin && u.Active && (!active || role != RoleAdmin)
		query := `UPDATE staff_users SET role = ?, branches = ?, active = ?, session_version = session_version + ?, updated_at = ?`
		args := []any{role, branches, bit, bump, now}
		if nextHash != nil {
			query += `, password_hash = ?, must_change_password = 1`
			args = append(args, string(nextHash))
		}
		query += ` WHERE id = ?`
		args = append(args, id)
		if demote {
			query += ` AND (SELECT COUNT(*) FROM staff_users WHERE role = 'admin' AND active = 1 AND id <> ?) >= 1`
			args = append(args, id)
		}
		res, err := conn.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if demote && n == 0 {
			return ErrLastAdmin
		}
		resetBit := 0
		if nextHash != nil {
			resetBit = 1
		}
		what := fmt.Sprintf("role=%s;branches=%s;active=%d;reset_password=%d", role, branches, bit, resetBit)
		return insertAdminLog(ctx, conn, actorID, actorLogin, id, what, now)
	})
	if err != nil {
		return User{}, "", err
	}
	u, err := s.userByID(ctx, id)
	return u, temp, err
}

func insertAdminLog(ctx context.Context, conn *sql.Conn, actorID int64, actorLogin string, targetID int64, change, now string) error {
	_, err := conn.ExecContext(ctx, `
INSERT INTO staff_admin_log (actor_id, actor_login, target_id, change, created_at)
VALUES (?, ?, ?, ?, ?)`, actorID, actorLogin, targetID, change, now)
	return err
}

func normalizeBranches(role, raw string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == RoleAdmin || role == RoleManager {
		return "*", nil
	}
	all, ids, err := ParseBranches(raw)
	if err != nil || all || len(ids) == 0 {
		return "", ErrNoBranches
	}
	return joinBranchIDs(ids), nil
}

func joinBranchIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func (s *Store) List(ctx context.Context) ([]User, error) {
	rs, err := s.db.QueryContext(ctx, `
SELECT id, login, full_name, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,''), IFNULL(session_version,1)
FROM staff_users ORDER BY login`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []User
	for rs.Next() {
		u, err := scanUser(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []User{}
	}
	return out, rs.Err()
}

func randomPassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}
