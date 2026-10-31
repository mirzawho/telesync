package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Category validation limits, in characters after trimming.
const (
	MaxCategoryNameLen        = 100
	MaxCategoryDescriptionLen = 500
)

// Category errors. Callers match them with errors.Is.
var (
	// ErrCategoryNotFound means no category has that id.
	ErrCategoryNotFound = errors.New("channelstore: category not found")
	// ErrDuplicateName means another category already has that name, compared
	// ignoring case.
	ErrDuplicateName = errors.New("channelstore: category name is already in use")
	// ErrInvalidCategory means the name or description failed validation.
	ErrInvalidCategory = errors.New("channelstore: invalid category")
)

// Category is one operator-defined grouping of channels. It is local: it is not
// a Telegram folder and is never written back to Telegram.
type Category struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ListQuery selects one page of categories.
type ListQuery struct {
	Page    int
	PerPage int
	// Query, when non-empty, matches the name or description, ignoring case.
	Query string
}

// Assignment is one (client key, channel id) pair filed under a category.
//
// ChannelID is as the gateway reports it — a decimal string, so it is stored as
// text rather than round-tripped through a number.
type Assignment struct {
	ClientKey string
	ChannelID string
	Category  Category
	CreatedAt time.Time
}

// NormalizeCategory validates and trims a name and description.
//
// The name is required after trimming; the description is optional.
func NormalizeCategory(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return "", "", fmt.Errorf("%w: name is required", ErrInvalidCategory)
	}
	if len([]rune(name)) > MaxCategoryNameLen {
		return "", "", fmt.Errorf("%w: name must be at most %d characters",
			ErrInvalidCategory, MaxCategoryNameLen)
	}
	if len([]rune(description)) > MaxCategoryDescriptionLen {
		return "", "", fmt.Errorf("%w: description must be at most %d characters",
			ErrInvalidCategory, MaxCategoryDescriptionLen)
	}
	return name, description, nil
}

// CreateCategory inserts a category and returns the stored row.
func (s *Store) CreateCategory(ctx context.Context, name, description string) (Category, error) {
	name, description, err := NormalizeCategory(name, description)
	if err != nil {
		return Category{}, err
	}

	now := formatTime(s.now())
	res, err := s.db.ExecContext(ctx, `
INSERT INTO categories (name, description, created_at, updated_at)
VALUES (?, ?, ?, ?)`, name, description, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return Category{}, ErrDuplicateName
		}
		return Category{}, fmt.Errorf("channelstore: create category: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Category{}, fmt.Errorf("channelstore: create category: %w", err)
	}
	return s.categoryByID(ctx, id)
}

// UpdateCategory replaces a category's name and description and stamps
// updated_at. created_at is preserved.
func (s *Store) UpdateCategory(ctx context.Context, id int64, name, description string) (Category, error) {
	name, description, err := NormalizeCategory(name, description)
	if err != nil {
		return Category{}, err
	}

	// The subquery on the id makes a self-rename ("News" → "news") a no-op
	// conflict rather than a duplicate-name failure.
	res, err := s.db.ExecContext(ctx, `
UPDATE categories SET name = ?, description = ?, updated_at = ?
WHERE id = ? AND NOT EXISTS (SELECT 1 FROM categories WHERE name = ? AND id <> ?)`,
		name, description, formatTime(s.now()), id, name, id)
	if err != nil {
		if isUniqueViolation(err) {
			return Category{}, ErrDuplicateName
		}
		return Category{}, fmt.Errorf("channelstore: update category %d: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Category{}, fmt.Errorf("channelstore: update category %d: %w", id, err)
	}
	if changed == 0 {
		// Either the row is gone or the name belongs to another category; the
		// read distinguishes the two.
		if _, err := s.categoryByID(ctx, id); err != nil {
			return Category{}, err
		}
		return Category{}, ErrDuplicateName
	}
	return s.categoryByID(ctx, id)
}

// DeleteCategory removes one category. Its channel_categories rows are removed
// by the ON DELETE CASCADE foreign key in the same transaction; no channel data
// is touched.
func (s *Store) DeleteCategory(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: delete category %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete category %d: %w", id, err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("channelstore: delete category %d: %w", id, err)
	}
	if removed == 0 {
		return ErrCategoryNotFound
	}
	// Belt and braces: with foreign_keys=1 the cascade has already run. Stating
	// it explicitly keeps the invariant visible to someone reading the schema
	// without the pragma in mind.
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_categories WHERE category_id = ?`, id); err != nil {
		return fmt.Errorf("channelstore: delete category %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: delete category %d: %w", id, err)
	}
	return nil
}

// GetCategory returns one category.
func (s *Store) GetCategory(ctx context.Context, id int64) (Category, bool, error) {
	cat, err := s.categoryByID(ctx, id)
	if errors.Is(err, ErrCategoryNotFound) {
		return Category{}, false, nil
	}
	if err != nil {
		return Category{}, false, err
	}
	return cat, true, nil
}

// ListCategories returns one page of categories, newest first, with the total
// number of matching rows.
func (s *Store) ListCategories(ctx context.Context, q ListQuery) ([]Category, int, error) {
	page, perPage := q.Page, q.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}

	where, args := "", []any{}
	if q.Query != "" {
		// instr() is a literal substring test, so a query containing % or _ is
		// data, not a wildcard. lower() gives case-insensitive matching
		// without relying on like()'s ASCII-only case folding.
		where = ` WHERE instr(lower(name), lower(?)) > 0 OR instr(lower(description), lower(?)) > 0`
		args = append(args, q.Query, q.Query)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("channelstore: count categories: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		selectCategoryColumns+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("channelstore: list categories: %w", err)
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		cat, err := scanCategory(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("channelstore: list categories: %w", err)
		}
		out = append(out, cat)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("channelstore: list categories: %w", err)
	}
	return out, total, nil
}

// AssignChannel files one channel under one category.
//
// Idempotent: assigning the same channel to the same category twice keeps the
// original row and its created_at. The channel does not have to exist in
// managed_channels.
func (s *Store) AssignChannel(ctx context.Context, clientKey, channelID string, categoryID int64) (Assignment, error) {
	clientKey = strings.TrimSpace(clientKey)
	channelID = strings.TrimSpace(channelID)
	if clientKey == "" {
		return Assignment{}, errors.New("channelstore: client_key is required")
	}
	if channelID == "" {
		return Assignment{}, errors.New("channelstore: channel_id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Assignment{}, fmt.Errorf("channelstore: assign channel %s: %w", channelID, err)
	}
	defer func() { _ = tx.Rollback() }()

	cat, err := categoryIn(ctx, tx, categoryID)
	if err != nil {
		return Assignment{}, err
	}
	now := formatTime(s.now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (client_key, channel_id, category_id) DO NOTHING`,
		clientKey, channelID, categoryID, now,
	); err != nil {
		return Assignment{}, fmt.Errorf("channelstore: assign channel %s: %w", channelID, err)
	}

	var created string
	if err := tx.QueryRowContext(ctx, `
SELECT created_at FROM channel_categories
WHERE client_key = ? AND channel_id = ? AND category_id = ?`,
		clientKey, channelID, categoryID).Scan(&created); err != nil {
		return Assignment{}, fmt.Errorf("channelstore: assign channel %s: %w", channelID, err)
	}
	if err := tx.Commit(); err != nil {
		return Assignment{}, fmt.Errorf("channelstore: assign channel %s: %w", channelID, err)
	}
	return Assignment{
		ClientKey: clientKey,
		ChannelID: channelID,
		Category:  cat,
		CreatedAt: parseTime(created),
	}, nil
}

// ListAssignments returns the categories filed under one client's channel.
func (s *Store) ListAssignments(ctx context.Context, clientKey, channelID string) ([]Assignment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+categoryAliasColumns+`, cc.created_at
FROM channel_categories cc
JOIN categories c ON c.id = cc.category_id
WHERE cc.client_key = ? AND cc.channel_id = ?
ORDER BY cc.created_at DESC, c.id DESC`, clientKey, channelID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list assignments: %w", err)
	}
	defer rows.Close()

	var out []Assignment
	for rows.Next() {
		var (
			cat     Category
			created string
			updated string
			assign  string
		)
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Description, &created, &updated,
			&assign); err != nil {
			return nil, fmt.Errorf("channelstore: list assignments: %w", err)
		}
		cat.CreatedAt = parseTime(created)
		cat.UpdatedAt = parseTime(updated)
		out = append(out, Assignment{
			ClientKey: clientKey,
			ChannelID: channelID,
			Category:  cat,
			CreatedAt: parseTime(assign),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list assignments: %w", err)
	}
	return out, nil
}

func (s *Store) categoryByID(ctx context.Context, id int64) (Category, error) {
	row := s.db.QueryRowContext(ctx, selectCategoryColumns+` WHERE id = ?`, id)
	cat, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("channelstore: read category %d: %w", id, err)
	}
	return cat, nil
}

func categoryIn(ctx context.Context, tx *sql.Tx, id int64) (Category, error) {
	row := tx.QueryRowContext(ctx, selectCategoryColumns+` WHERE id = ?`, id)
	cat, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("channelstore: read category %d: %w", id, err)
	}
	return cat, nil
}

const selectCategoryColumns = `SELECT id, name, description, created_at, updated_at FROM categories`

const categoryAliasColumns = `c.id, c.name, c.description, c.created_at, c.updated_at`

func scanCategory(row scanner) (Category, error) {
	var cat Category
	var created, updated string
	if err := row.Scan(&cat.ID, &cat.Name, &cat.Description, &created, &updated); err != nil {
		return Category{}, err
	}
	cat.CreatedAt = parseTime(created)
	cat.UpdatedAt = parseTime(updated)
	return cat, nil
}

// isUniqueViolation reports whether err is SQLite's UNIQUE constraint failure
// (extended code 2067).
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// AssignmentCounts returns how many categories are filed under one client's
// channel, keyed by category id.
func (s *Store) AssignmentCounts(ctx context.Context, clientKey, channelID string) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT category_id, COUNT(*) FROM channel_categories
WHERE client_key = ? AND channel_id = ?
GROUP BY category_id`, clientKey, channelID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: count assignments: %w", err)
	}
	defer rows.Close()

	out := map[int64]int{}
	for rows.Next() {
		var (
			id    int64
			count int
		)
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("channelstore: count assignments: %w", err)
		}
		out[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: count assignments: %w", err)
	}
	return out, nil
}

// ListCategoriesByIDs returns the categories among ids, ordered by id.
//
// It is how a caller that may see only some categories pages over exactly the
// ones it is allowed to see; a filter applied after a page is read cannot
// promise an order or a page size.
func (s *Store) ListCategoriesByIDs(ctx context.Context, ids []int64) ([]Category, error) {
	if len(ids) == 0 {
		return []Category{}, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		selectCategoryColumns+` WHERE id IN (`+placeholders(len(ids))+`) ORDER BY id`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list categories: %w", err)
	}
	defer rows.Close()

	out := []Category{}
	for rows.Next() {
		cat, err := scanCategory(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list categories: %w", err)
		}
		out = append(out, cat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list categories: %w", err)
	}
	return out, nil
}
