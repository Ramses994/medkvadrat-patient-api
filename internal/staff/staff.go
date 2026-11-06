package staff

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ErrBranchForbidden = errors.New("BRANCH_FORBIDDEN")
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
}

type Principal struct {
	ID                 int64
	Login              string
	FullName           string
	Role               string
	All                bool
	BranchIDs          []int
	MustChangePassword bool
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
	db *sql.DB
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

// ParseBranches accepts "*" or a CSV of 106, 3, 496.
func ParseBranches(raw string) (all bool, ids []int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return true, append([]int(nil), DashboardBranchIDs...), nil
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
	if err != nil {
		return Principal{}, err
	}
	if u.Role == RoleAdmin || u.Role == RoleManager {
		all = true
		ids = append([]int(nil), DashboardBranchIDs...)
	}
	return Principal{
		ID: u.ID, Login: u.Login, FullName: u.FullName, Role: u.Role,
		All: all, BranchIDs: ids, MustChangePassword: u.MustChangePassword,
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
       created_at, updated_at, IFNULL(last_login_at,'')
FROM staff_users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) userByLogin(ctx context.Context, login string) (User, string, error) {
	var u User
	var hash string
	var active, must int
	err := s.db.QueryRowContext(ctx, `
SELECT id, login, full_name, password_hash, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,'')
FROM staff_users WHERE login = ?`, login).Scan(
		&u.ID, &u.Login, &u.FullName, &hash, &u.Role, &u.Branches, &active, &must,
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
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
		&u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt,
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

// Authenticate checks the lock, the password and the role. It always writes the journal.
func (s *Store) Authenticate(ctx context.Context, login, password, ip string, now time.Time) (Principal, error) {
	login = strings.TrimSpace(login)
	locked, err := s.Locked(ctx, login, now)
	if err != nil {
		return Principal{}, err
	}
	if locked {
		_ = s.logAttempt(ctx, login, ip, false, now)
		return Principal{}, ErrLocked
	}
	u, hash, err := s.userByLogin(ctx, login)
	if err != nil {
		_ = s.logAttempt(ctx, login, ip, false, now)
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrBadCredentials
		}
		return Principal{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil || !u.Active {
		_ = s.logAttempt(ctx, login, ip, false, now)
		return Principal{}, ErrBadCredentials
	}
	p, err := principalFrom(u)
	if err != nil {
		_ = s.logAttempt(ctx, login, ip, false, now)
		return Principal{}, err
	}
	if err := s.logAttempt(ctx, login, ip, true, now); err != nil {
		return Principal{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE staff_users SET last_login_at = ? WHERE id = ?`, now.UTC().Format(time.RFC3339), u.ID)
	return p, nil
}

func (s *Store) ChangePassword(ctx context.Context, id int64, current, next string) error {
	if len(next) < MinPasswordLen {
		return ErrShortPassword
	}
	u, hash, err := s.userByIDWithHash(ctx, id)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return ErrBadCredentials
	}
	return s.setPassword(ctx, u.ID, next, false)
}

func (s *Store) userByIDWithHash(ctx context.Context, id int64) (User, string, error) {
	u, err := s.userByID(ctx, id)
	if err != nil {
		return User{}, "", err
	}
	_, hash, err := s.userByLogin(ctx, u.Login)
	return u, hash, err
}

func (s *Store) setPassword(ctx context.Context, id int64, password string, mustChange bool) error {
	if len(password) < MinPasswordLen {
		return ErrShortPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return err
	}
	must := 0
	if mustChange {
		must = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `
UPDATE staff_users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		string(hash), must, now, id)
	return err
}

type CreateUser struct {
	Login    string
	FullName string
	Role     string
	Branches string
	Password string
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
	if _, _, err := ParseBranches(in.Branches); err != nil {
		return User{}, "", err
	}
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
	if len(password) < MinPasswordLen {
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
	res, err := s.db.ExecContext(ctx, `
INSERT INTO staff_users (
  login, full_name, password_hash, role, branches, active, must_change_password, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)`,
		login, strings.TrimSpace(in.FullName), string(hash), strings.ToLower(in.Role), strings.TrimSpace(in.Branches), mustBit, now, now)
	if err != nil {
		return User{}, "", err
	}
	id, _ := res.LastInsertId()
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
	u, err := s.userByID(ctx, id)
	if err != nil {
		return User{}, "", err
	}
	role := u.Role
	if in.Role != "" {
		role = strings.ToLower(strings.TrimSpace(in.Role))
		if !ValidRole(role) {
			return User{}, "", ErrBadRole
		}
	}
	branches := u.Branches
	if in.Branches != "" {
		if _, _, err := ParseBranches(in.Branches); err != nil {
			return User{}, "", err
		}
		branches = strings.TrimSpace(in.Branches)
	}
	active := u.Active
	if in.Active != nil {
		active = *in.Active
	}
	if u.Role == RoleAdmin && u.Active && (!active || role != RoleAdmin) {
		if err := s.keepAdmin(ctx, id); err != nil {
			return User{}, "", err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	bit := 0
	if active {
		bit = 1
	}
	if _, err := s.db.ExecContext(ctx, `
UPDATE staff_users SET role = ?, branches = ?, active = ?, updated_at = ? WHERE id = ?`,
		role, branches, bit, now, id); err != nil {
		return User{}, "", err
	}
	temp := ""
	if in.ResetPassword {
		temp, err = randomPassword(12)
		if err != nil {
			return User{}, "", err
		}
		if err := s.setPassword(ctx, id, temp, true); err != nil {
			return User{}, "", err
		}
	}
	u, err = s.userByID(ctx, id)
	return u, temp, err
}

func (s *Store) keepAdmin(ctx context.Context, exceptID int64) error {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM staff_users WHERE role = 'admin' AND active = 1 AND id <> ?`, exceptID).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (s *Store) List(ctx context.Context) ([]User, error) {
	rs, err := s.db.QueryContext(ctx, `
SELECT id, login, full_name, role, branches, active, must_change_password,
       created_at, updated_at, IFNULL(last_login_at,'')
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
