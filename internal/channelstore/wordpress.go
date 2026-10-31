package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mirza/telesync/internal/secrets"
)

// WordPressSite errors. Callers match them with errors.Is.
var (
	ErrSiteNotFound  = errors.New("channelstore: wordpress site not found")
	ErrDuplicateSite = errors.New("channelstore: a wordpress site with that url is already registered")
	ErrInvalidSite   = errors.New("channelstore: invalid wordpress site")
)

// WordPressSite is one registered publishing destination.
//
// Credential is the sealed application password; it is never written or read
// in the clear here.
type WordPressSite struct {
	ID         int64
	URL        string
	Title      string
	IconURL    *string
	Username   string
	Credential secrets.Box
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CreateWordPressSite stores a new site.
func (s *Store) CreateWordPressSite(ctx context.Context, rec WordPressSite) (WordPressSite, error) {
	rec, err := validSite(rec)
	if err != nil {
		return WordPressSite{}, err
	}
	now := s.now().UTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now

	res, err := s.db.ExecContext(ctx, `
INSERT INTO wordpress_sites (url, title, site_icon_url, username,
	credential_version, credential_nonce, credential_ciphertext, credential_sealed_at,
	created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.URL, rec.Title, nullableString(rec.IconURL), rec.Username,
		rec.Credential.Version, rec.Credential.Nonce, rec.Credential.Ciphertext,
		nullableTime(rec.Credential.SealedAt),
		formatTime(rec.CreatedAt), formatTime(rec.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return WordPressSite{}, ErrDuplicateSite
		}
		return WordPressSite{}, fmt.Errorf("channelstore: create wordpress site: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return WordPressSite{}, fmt.Errorf("channelstore: create wordpress site: %w", err)
	}
	saved, _, err := s.GetWordPressSite(ctx, id)
	return saved, err
}

// GetWordPressSite returns one site by its local id.
func (s *Store) GetWordPressSite(ctx context.Context, id int64) (WordPressSite, bool, error) {
	row := s.db.QueryRowContext(ctx, selectSiteColumns+` WHERE id = ?`, id)
	rec, err := scanSite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return WordPressSite{}, false, nil
	}
	if err != nil {
		return WordPressSite{}, false, fmt.Errorf("channelstore: read wordpress site %d: %w", id, err)
	}
	return rec, true, nil
}

// ListWordPressSites returns every site, newest first.
func (s *Store) ListWordPressSites(ctx context.Context) ([]WordPressSite, error) {
	rows, err := s.db.QueryContext(ctx, selectSiteColumns+` ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list wordpress sites: %w", err)
	}
	defer rows.Close()
	out := []WordPressSite{}
	for rows.Next() {
		rec, err := scanSite(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list wordpress sites: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list wordpress sites: %w", err)
	}
	return out, nil
}

// UpdateWordPressSite replaces every stored field of one site.
func (s *Store) UpdateWordPressSite(ctx context.Context, rec WordPressSite) (WordPressSite, error) {
	if rec.ID <= 0 {
		return WordPressSite{}, fmt.Errorf("%w: id is required", ErrInvalidSite)
	}
	rec, err := validSite(rec)
	if err != nil {
		return WordPressSite{}, err
	}
	// created_at is never rewritten here: it records when the site was first
	// registered.
	res, err := s.db.ExecContext(ctx, `
UPDATE wordpress_sites SET url = ?, title = ?, site_icon_url = ?, username = ?,
	credential_version = ?, credential_nonce = ?, credential_ciphertext = ?,
	credential_sealed_at = ?, updated_at = ?
WHERE id = ?`,
		rec.URL, rec.Title, nullableString(rec.IconURL), rec.Username,
		rec.Credential.Version, rec.Credential.Nonce, rec.Credential.Ciphertext,
		nullableTime(rec.Credential.SealedAt), formatTime(s.now()),
		rec.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return WordPressSite{}, ErrDuplicateSite
		}
		return WordPressSite{}, fmt.Errorf("channelstore: update wordpress site %d: %w", rec.ID, err)
	}
	if changed, err := res.RowsAffected(); err != nil {
		return WordPressSite{}, fmt.Errorf("channelstore: update wordpress site %d: %w", rec.ID, err)
	} else if changed == 0 {
		return WordPressSite{}, ErrSiteNotFound
	}
	saved, _, err := s.GetWordPressSite(ctx, rec.ID)
	return saved, err
}

// DeleteWordPressSite removes one site and its permission assignments.
func (s *Store) DeleteWordPressSite(ctx context.Context, id int64) error {
	// The site's permission assignments go by ON DELETE CASCADE. No other
	// table references a site, and nothing about the remote WordPress site is
	// touched.
	res, err := s.db.ExecContext(ctx, `DELETE FROM wordpress_sites WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete wordpress site %d: %w", id, err)
	}
	if removed, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("channelstore: delete wordpress site %d: %w", id, err)
	} else if removed == 0 {
		return ErrSiteNotFound
	}
	return nil
}

// validSite checks what the repository can check without a key: the record is
// well formed and carries a sealed credential, never a bare one.
func validSite(rec WordPressSite) (WordPressSite, error) {
	rec.URL = strings.TrimSpace(rec.URL)
	rec.Username = strings.TrimSpace(rec.Username)
	rec.Title = strings.TrimSpace(rec.Title)
	if rec.URL == "" {
		return WordPressSite{}, fmt.Errorf("%w: url is required", ErrInvalidSite)
	}
	if !strings.HasPrefix(rec.URL, "https://") && !strings.HasPrefix(rec.URL, "http://") {
		return WordPressSite{}, fmt.Errorf("%w: url must be an http(s) URL", ErrInvalidSite)
	}
	u, err := url.Parse(rec.URL)
	if err != nil || u.Hostname() == "" {
		return WordPressSite{}, fmt.Errorf("%w: url is malformed", ErrInvalidSite)
	}
	if u.User != nil {
		return WordPressSite{}, fmt.Errorf("%w: url must not carry credentials", ErrInvalidSite)
	}
	if rec.Username == "" {
		return WordPressSite{}, fmt.Errorf("%w: username is required", ErrInvalidSite)
	}
	if rec.Credential.Version != secrets.Version1 {
		return WordPressSite{}, fmt.Errorf("%w: the credential must be sealed (version %d)", ErrInvalidSite, secrets.Version1)
	}
	if len(rec.Credential.Nonce) == 0 || len(rec.Credential.Ciphertext) == 0 {
		return WordPressSite{}, fmt.Errorf("%w: the sealed credential is incomplete", ErrInvalidSite)
	}
	return rec, nil
}

const selectSiteColumns = `SELECT id, url, title, site_icon_url, username,
	credential_version, credential_nonce, credential_ciphertext, credential_sealed_at,
	created_at, updated_at FROM wordpress_sites`

func scanSite(row scanner) (WordPressSite, error) {
	var (
		rec                        WordPressSite
		icon                       sql.NullString
		sealedAt, created, updated sql.NullString
	)
	if err := row.Scan(&rec.ID, &rec.URL, &rec.Title, &icon, &rec.Username,
		&rec.Credential.Version, &rec.Credential.Nonce, &rec.Credential.Ciphertext,
		&sealedAt, &created, &updated); err != nil {
		return WordPressSite{}, err
	}
	if icon.Valid {
		v := icon.String
		rec.IconURL = &v
	}
	rec.Credential.SealedAt = parseTime(sealedAt.String)
	rec.CreatedAt = parseTime(created.String)
	rec.UpdatedAt = parseTime(updated.String)
	return rec, nil
}

func nullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}
