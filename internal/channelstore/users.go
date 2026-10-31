package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// User validation limits, in characters after trimming.
const (
	MaxFullnameLen = 200
	MaxUsernameLen = 64
	MinPasswordLen = 8
	MaxPasswordLen = 1024
)

// User errors. Callers match them with errors.Is.
var (
	// ErrUserNotFound means no user has that id.
	ErrUserNotFound = errors.New("channelstore: user not found")
	// ErrDuplicateUsername means another user already has that username,
	// compared ignoring case.
	ErrDuplicateUsername = errors.New("channelstore: username is already in use")
	// ErrInvalidUser means a field failed validation.
	ErrInvalidUser = errors.New("channelstore: invalid user")
	// ErrLastAdministrator means the write would leave no active account able
	// to manage users and permissions.
	ErrLastAdministrator = errors.New("channelstore: this is the last active user administrator")
)

// User is one local operator account. PasswordHash is an encoded hash and is
// never rendered by the API.
type User struct {
	ID           int64
	Fullname     string
	Username     string
	PasswordHash string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UserFilter selects one page of users.
type UserFilter struct {
	Page    int
	PerPage int
	// Query, when non-empty, matches the username or full name, ignoring case.
	Query string
	// IDs, when not nil, restricts the page to these user ids.
	IDs []int64
}

// NormalizeUser validates a full name and username.
func NormalizeUser(fullname, username string) (string, string, error) {
	fullname = strings.TrimSpace(fullname)
	username = strings.TrimSpace(username)
	if fullname == "" {
		return "", "", fmt.Errorf("%w: fullname is required", ErrInvalidUser)
	}
	if len([]rune(fullname)) > MaxFullnameLen {
		return "", "", fmt.Errorf("%w: fullname must be at most %d characters", ErrInvalidUser, MaxFullnameLen)
	}
	if username == "" {
		return "", "", fmt.Errorf("%w: username is required", ErrInvalidUser)
	}
	if len([]rune(username)) > MaxUsernameLen {
		return "", "", fmt.Errorf("%w: username must be at most %d characters", ErrInvalidUser, MaxUsernameLen)
	}
	for _, r := range username {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-') {
			return "", "", fmt.Errorf("%w: username may only contain letters, digits, '.', '_' and '-'", ErrInvalidUser)
		}
	}
	return fullname, username, nil
}

// ValidatePassword checks a new password's length. Hashing is the caller's
// job: this package never chooses a hash.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLen {
		return fmt.Errorf("%w: password must be at least %d characters", ErrInvalidUser, MinPasswordLen)
	}
	if len(password) > MaxPasswordLen {
		return fmt.Errorf("%w: password must be at most %d characters", ErrInvalidUser, MaxPasswordLen)
	}
	return nil
}

// CreateUser inserts a user and returns the stored row.
func (s *Store) CreateUser(ctx context.Context, fullname, username, passwordHash string, active bool) (User, error) {
	fullname, username, err := NormalizeUser(fullname, username)
	if err != nil {
		return User{}, err
	}
	if strings.TrimSpace(passwordHash) == "" {
		return User{}, fmt.Errorf("%w: a password hash is required", ErrInvalidUser)
	}

	now := formatTime(s.now())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO users (fullname, username, password_hash, is_active, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, fullname, username, passwordHash, active, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrDuplicateUsername
		}
		return User{}, fmt.Errorf("channelstore: create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("channelstore: create user: %w", err)
	}
	return s.userByID(ctx, s.db, id)
}

// UserUpdate is a partial change. A nil field is left as it is; a non-nil
// PasswordHash replaces the stored hash.
type UserUpdate struct {
	Fullname     *string
	Username     *string
	IsActive     *bool
	PasswordHash *string
}

// UpdateUser applies a partial change and returns the stored row.
//
// Deactivating or renaming does not disturb permissions; the hash of an
// existing password is never readable through this package.
func (s *Store) UpdateUser(ctx context.Context, id int64, update UserUpdate) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("channelstore: update user %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.userByID(ctx, tx, id)
	if err != nil {
		return User{}, err
	}

	fullname, username := current.Fullname, current.Username
	if update.Fullname != nil {
		fullname = *update.Fullname
	}
	if update.Username != nil {
		username = *update.Username
	}
	fullname, username, err = NormalizeUser(fullname, username)
	if err != nil {
		return User{}, err
	}
	sets := []string{"fullname = ?", "username = ?"}
	args := []any{fullname, username}
	if update.PasswordHash != nil {
		if strings.TrimSpace(*update.PasswordHash) == "" {
			return User{}, fmt.Errorf("%w: a password hash is required", ErrInvalidUser)
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, *update.PasswordHash)
	}

	if update.IsActive != nil && *update.IsActive != current.IsActive {
		if !*update.IsActive {
			if err := s.ensureAnotherAdministrator(ctx, tx, id); err != nil {
				return User{}, err
			}
		}
		sets = append(sets, "is_active = ?")
		args = append(args, *update.IsActive)
	}

	if len(sets) == 0 {
		return current, nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, formatTime(s.now()), id)
	res, err := tx.ExecContext(ctx,
		`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrDuplicateUsername
		}
		return User{}, fmt.Errorf("channelstore: update user %d: %w", id, err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return User{}, ErrUserNotFound
	}
	saved, err := s.userByID(ctx, tx, id)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("channelstore: update user %d: %w", id, err)
	}
	return saved, nil
}

// DeleteUser removes one user. The three permission tables cascade.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: delete user %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.userByID(ctx, tx, id); err != nil {
		return err
	}
	if err := s.ensureAnotherAdministrator(ctx, tx, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete user %d: %w", id, err)
	}
	if removed, _ := res.RowsAffected(); removed == 0 {
		return ErrUserNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: delete user %d: %w", id, err)
	}
	return nil
}

// ensureAnotherAdministrator refuses to remove (or deactivate) id when it is
// the only active account holding users.manage.
//
// It runs inside the write's transaction, so two concurrent removals cannot
// both see the other remaining.
func (s *Store) ensureAnotherAdministrator(ctx context.Context, tx *sql.Tx, id int64) error {
	var others int
	err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM users u
JOIN user_permissions p ON p.user_id = u.id AND p.permission = ?
WHERE u.is_active = 1 AND u.id <> ?`, string(PermissionUsersManage), id).Scan(&others)
	if err != nil {
		return fmt.Errorf("channelstore: count administrators: %w", err)
	}
	var current bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM users u JOIN user_permissions p ON p.user_id = u.id
WHERE u.id = ? AND u.is_active = 1 AND p.permission = ?)`,
		id, string(PermissionUsersManage)).Scan(&current); err != nil {
		return fmt.Errorf("channelstore: read administrator: %w", err)
	}
	if current && others == 0 {
		return ErrLastAdministrator
	}
	return nil
}

// GetUser returns one user.
func (s *Store) GetUser(ctx context.Context, id int64) (User, bool, error) {
	u, err := s.userByID(ctx, s.db, id)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

// UserByUsername returns the user whose username matches, ignoring case.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, bool, error) {
	row := s.db.QueryRowContext(ctx, selectUserColumns+` WHERE username = ?`, strings.TrimSpace(username))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("channelstore: read user by username: %w", err)
	}
	return u, true, nil
}

// ListUsers returns one page of users, newest first, with the total number of
// matching rows.
func (s *Store) ListUsers(ctx context.Context, f UserFilter) ([]User, int, error) {
	page, perPage := f.Page, f.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}

	where, args := "", []any{}
	var conds []string
	if f.Query != "" {
		conds = append(conds, `(instr(lower(username), lower(?)) > 0 OR instr(lower(fullname), lower(?)) > 0)`)
		args = append(args, f.Query, f.Query)
	}
	if f.IDs != nil {
		if len(f.IDs) == 0 {
			return []User{}, 0, nil
		}
		conds = append(conds, `id IN (`+placeholders(len(f.IDs))+`)`)
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("channelstore: count users: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		selectUserColumns+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("channelstore: list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("channelstore: list users: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("channelstore: list users: %w", err)
	}
	return out, total, nil
}

// UserIDsWithAnyPermission returns every user holding at least one global
// permission.
func (s *Store) UserIDsWithAnyPermission(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT user_id FROM user_permissions`)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list permission holders: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("channelstore: list permission holders: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetPasswordHash replaces one user's stored hash without touching anything
// else.
//
// It is the recovery path for an operator who has a hash from
// `telesync hash-password` (or from a first bootstrap run that printed one)
// and needs to replace an account's password without a working login.
// Parameters: id is the local user id, hash an Argon2id PHC string. It returns
// ErrUserNotFound when no user has that id and ErrInvalidUser for an empty
// hash.
func (s *Store) SetPasswordHash(ctx context.Context, id int64, hash string) error {
	if strings.TrimSpace(hash) == "" {
		return fmt.Errorf("%w: a password hash is required", ErrInvalidUser)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, formatTime(s.now()), id)
	if err != nil {
		return fmt.Errorf("channelstore: set password: %w", err)
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (s *Store) userByID(ctx context.Context, q queryer, id int64) (User, error) {
	u, err := scanUser(q.QueryRowContext(ctx, selectUserColumns+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("channelstore: read user %d: %w", id, err)
	}
	return u, nil
}

// queryer is what both *sql.DB and *sql.Tx offer.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const selectUserColumns = `SELECT id, fullname, username, password_hash, is_active, created_at, updated_at FROM users`

func scanUser(row scanner) (User, error) {
	var (
		u       User
		active  bool
		created string
		updated string
	)
	if err := row.Scan(&u.ID, &u.Fullname, &u.Username, &u.PasswordHash, &active, &created, &updated); err != nil {
		return User{}, err
	}
	u.IsActive = active
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	return u, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
