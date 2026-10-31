// Package channelstore persists the channels an operator selected through the
// join API.
//
// It is not a mirror of Telegram. Telegram stays the source of truth for
// membership, metadata and history; this store only records which channels this
// application has been asked to manage, so the flag is available without
// re-reading Telegram. Nothing about ordinary reads (`/channels`, `/messages`,
// `/profile`, `/health`) depends on it.
package channelstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/mirza/telesync/internal/tgapi"
)

// Peer types recorded for a managed channel.
const (
	PeerChannel = "channel"
	PeerGroup   = "group"
)

// MembershipStatus is the last membership state Telegram confirmed for this
// channel. It is a cached observation, not a claim about right now.
const (
	StatusMember      = "member"
	StatusJoinPending = "join_pending"
	StatusUnknown     = "unknown"
)

// Channel is one locally managed channel.
type Channel struct {
	ID          int64
	ClientKey   string
	ChannelID   int64
	PeerType    string
	Title       string
	Username    string
	Description string
	// AvatarPhotoID is Telegram's photo version, used to invalidate a cached
	// avatar when the channel changes it. Zero means "no photo was reported".
	AvatarPhotoID int64
	// AvatarAvailable is whether a photo was reported at all. It is what a
	// snapshot can say when the backend reported that the channel has a photo
	// but not the photo's version, which the cache uses and the presence of the
	// avatar does not.
	AvatarAvailable bool
	// MembershipStatus is the state Telegram last confirmed.
	MembershipStatus string
	// ParticipantsCount is nil when Telegram did not report one.
	ParticipantsCount *int
	// Verified, Scam and Fake are Telegram's channel notices as the last report
	// carried them. They are meaningful only when NoticesKnown is set: a record
	// written before they were stored, or by a client that never reported them,
	// keeps them false and says so instead of claiming the channel is clear.
	Verified bool
	Scam     bool
	Fake     bool
	// NoticesKnown says whether a report that carried the notices wrote them.
	NoticesKnown bool
	// Permissions are Telegram's channel-level rights as the last report
	// normalized them, meaningful only when PermissionsKnown is set: a row from
	// before they were stored is unknown, not a set of denials.
	Permissions      tgapi.Permissions
	PermissionsKnown bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// LastSyncedAt is when the row was last written from a live Telegram read.
	LastSyncedAt time.Time
}

// Store is the SQLite-backed managed-channel repository.
type Store struct {
	db   *sql.DB
	path string
	now  func() time.Time
}

// Options configures Open.
type Options struct {
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Open opens (and creates) the database at path, applying pending migrations.
//
// The parent directory is created 0700 and the database file is restricted to
// 0600: it holds channel selection, not credentials, but it is still private
// application state.
func Open(path string, opts Options) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("channelstore: database path is empty")
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("channelstore: database path %q must be absolute", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("channelstore: create database directory %q: %w", dir, err)
	}

	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("channelstore: open %q: %w", path, err)
	}
	// The database is a single local file used by one process; one connection
	// removes lock contention between goroutines and keeps pragmas applied.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("channelstore: open %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
		db.Close()
		return nil, fmt.Errorf("channelstore: restrict %q: %w", path, err)
	}

	s := &Store{db: db, path: path, now: opts.Now}
	if s.now == nil {
		s.now = time.Now
	}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Get returns one managed channel.
func (s *Store) Get(ctx context.Context, clientKey string, channelID int64) (Channel, bool, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE client_key = ? AND channel_id = ?`,
		clientKey, channelID)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, fmt.Errorf("channelstore: read channel %d: %w", channelID, err)
	}
	return ch, true, nil
}

// LocalIDsByChannelID maps each of channelIDs to its local row id on clientKey.
//
// A channel with no record is absent from the result. It is one query for a
// whole listing, so authorization can resolve every page without reading one
// row at a time.
func (s *Store) LocalIDsByChannelID(ctx context.Context, clientKey string, channelIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(channelIDs))
	if len(channelIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(channelIDs)+1)
	args = append(args, clientKey)
	for _, id := range channelIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, id FROM managed_channels WHERE client_key = ? AND channel_id IN (`+
			placeholders(len(channelIDs))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: resolve local channel ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var telegramID, localID int64
		if err := rows.Scan(&telegramID, &localID); err != nil {
			return nil, fmt.Errorf("channelstore: resolve local channel ids: %w", err)
		}
		out[telegramID] = localID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: resolve local channel ids: %w", err)
	}
	return out, nil
}

// ListChannelsExcept returns every managed channel whose local id is not in
// excluded, in the listing order with the query filter applied.
//
// It is for a caller whose global view is narrowed by a few explicit denies:
// the catalog minus those records is one query, and the caller pages over the
// result exactly as the unfiltered listing would.
func (s *Store) ListChannelsExcept(ctx context.Context, q ChannelListQuery, excluded []int64) ([]Channel, error) {
	where, args := "", []any{}
	if q.Query != "" {
		where = ` WHERE (instr(lower(title), lower(?)) > 0
			OR instr(lower(username), lower(?)) > 0
			OR instr(lower(client_key), lower(?)) > 0)`
		args = append(args, q.Query, q.Query, q.Query)
	}
	if len(excluded) > 0 {
		if where == "" {
			where = " WHERE "
		} else {
			where += " AND "
		}
		where += `id NOT IN (` + placeholders(len(excluded)) + `)`
		for _, id := range excluded {
			args = append(args, id)
		}
	}
	rows, err := s.db.QueryContext(ctx, selectColumns+where+` ORDER BY channel_id DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// ListChannelsForUser returns the managed channels userID may view, in listing
// order with the query filter applied.
//
// The decision is made by the query, not after it: the rows selected are the
// ones joined to userID's own per-channel view assignment, or — when userID
// holds no explicit view deny anywhere — every row the global "channels.view"
// permission admits. A row with no assignment is selected only through that
// global permission. A caller holding neither therefore selects nothing.
func (s *Store) ListChannelsForUser(ctx context.Context, userID int64, q ChannelListQuery) ([]Channel, error) {
	if userID <= 0 {
		return []Channel{}, nil
	}
	var global, denies int
	err := s.db.QueryRowContext(ctx, `
SELECT
	EXISTS (SELECT 1 FROM user_permissions WHERE user_id = ? AND permission = 'channels.view'),
	EXISTS (SELECT 1 FROM user_channels_permission WHERE user_id = ? AND permission = 'view' AND effect = 'deny')`,
		userID, userID).Scan(&global, &denies)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read channel scope: %w", err)
	}
	if global == 0 {
		// Only an explicit view:allow admits a record.
		return s.listChannelsWhere(ctx, q, `m.id IN (SELECT p.channel_id FROM user_channels_permission p
	WHERE p.user_id = ? AND p.permission = 'view' AND p.effect = 'allow')`, userID)
	}
	if denies == 0 {
		return s.listChannelsWhere(ctx, q, "", nil)
	}
	return s.listChannelsWhere(ctx, q, `m.id NOT IN (SELECT p.channel_id FROM user_channels_permission p
	WHERE p.user_id = ? AND p.permission = 'view' AND p.effect = 'deny')`, userID)
}

// listChannelsWhere lists every managed channel matching the query and the
// extra condition, in listing order.
func (s *Store) listChannelsWhere(ctx context.Context, q ChannelListQuery, condition string, args ...any) ([]Channel, error) {
	var conds []string
	if condition != "" {
		conds = append(conds, condition)
	}
	if q.Query != "" {
		conds = append(conds,
			`(instr(lower(m.title), lower(?)) > 0 OR instr(lower(m.username), lower(?)) > 0 OR instr(lower(m.client_key), lower(?)) > 0)`)
		args = append(args, q.Query, q.Query, q.Query)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := s.db.QueryContext(ctx,
		selectJoinedColumns+where+` ORDER BY m.channel_id DESC, m.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// GetByID returns one managed channel by its local row id.
//
// The local id, not the Telegram channel id, is what a permission assignment
// references.
func (s *Store) GetByID(ctx context.Context, id int64) (Channel, bool, error) {
	row := s.db.QueryRowContext(ctx, selectColumns+` WHERE id = ?`, id)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, fmt.Errorf("channelstore: read channel row %d: %w", id, err)
	}
	return ch, true, nil
}

// ManagedIDs returns every channel id managed for one client key.
//
// It is what marks search results as locally selected without loading the whole
// table or touching Telegram.
func (s *Store) ManagedIDs(ctx context.Context, clientKey string) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id FROM managed_channels WHERE client_key = ?`, clientKey)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()

	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// List returns the managed channels for one client key, newest selection first.
func (s *Store) List(ctx context.Context, clientKey string) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		selectColumns+` WHERE client_key = ? ORDER BY created_at DESC, channel_id DESC`, clientKey)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// ChannelListQuery selects one page of managed channels.
type ChannelListQuery struct {
	Page    int
	PerPage int
	// Query, when non-empty, matches the title, username or client key,
	// ignoring case. It is matched literally: % and _ are data, not wildcards.
	Query string
}

// ListChannels returns one page of managed channels across every client, and
// the total number of rows matching the query.
//
// The order is the one finding establishes: Telegram's channel id descending,
// then the local row id. It does not depend on insertion time, so a row whose
// record is refreshed (Upsert keeps created_at but rewrites every other
// column) keeps its place in the list.
func (s *Store) ListChannels(ctx context.Context, q ChannelListQuery) ([]Channel, int, error) {
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
		// data, not a wildcard; lower() matches without like()'s ASCII-only
		// case folding.
		where = ` WHERE instr(lower(title), lower(?)) > 0
			OR instr(lower(username), lower(?)) > 0
			OR instr(lower(client_key), lower(?)) > 0`
		args = append(args, q.Query, q.Query, q.Query)
	}

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM managed_channels`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("channelstore: count managed channels: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		selectColumns+where+` ORDER BY channel_id DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, total, nil
}

// ListChannelsByIDs returns the managed channels among ids, in the listing
// order.
//
// It is how a caller that may see only some channels pages over exactly the
// ones it is allowed to see: filtering after a page is read could not promise
// an order or a page size.
func (s *Store) ListChannelsByIDs(ctx context.Context, ids []int64) ([]Channel, error) {
	out := []Channel{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		selectColumns+` WHERE id IN (`+placeholders(len(ids))+`) ORDER BY channel_id DESC, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list managed channels: %w", err)
	}
	return out, nil
}

// Upsert inserts or refreshes one managed channel.
//
// The pair (client_key, channel_id) is unique, so a repeated join updates the
// existing row instead of creating a second one. CreatedAt is preserved on
// update: it records when the operator first selected the channel.
//
// Notices and permissions are written only by a report that carried them
// (NoticesKnown, PermissionsKnown): a write that did not report them keeps the
// stored values instead of replacing them with false.
func (s *Store) Upsert(ctx context.Context, ch Channel) (Channel, error) {
	if strings.TrimSpace(ch.ClientKey) == "" {
		return Channel{}, errors.New("channelstore: client_key is required")
	}
	if ch.ChannelID <= 0 {
		return Channel{}, errors.New("channelstore: channel_id must be positive")
	}
	if ch.PeerType != PeerChannel && ch.PeerType != PeerGroup {
		return Channel{}, fmt.Errorf("channelstore: unsupported peer_type %q", ch.PeerType)
	}
	switch ch.MembershipStatus {
	case StatusMember, StatusJoinPending, StatusUnknown:
	default:
		return Channel{}, fmt.Errorf("channelstore: unsupported membership_status %q", ch.MembershipStatus)
	}

	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO managed_channels (
	client_key, channel_id, peer_type, title, username, description,
	avatar_photo_id, avatar_available, membership_status, participants_count,
	verified, scam, fake, notices_known, created_at, updated_at, last_synced_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (client_key, channel_id) DO UPDATE SET
	peer_type = excluded.peer_type,
	title = excluded.title,
	username = excluded.username,
	description = excluded.description,
	avatar_photo_id = excluded.avatar_photo_id,
	avatar_available = excluded.avatar_available,
	membership_status = excluded.membership_status,
	participants_count = excluded.participants_count,
	updated_at = excluded.updated_at,
	last_synced_at = excluded.last_synced_at`,
		ch.ClientKey, ch.ChannelID, ch.PeerType, ch.Title, ch.Username, ch.Description,
		ch.AvatarPhotoID, boolInt(ch.AvatarAvailable), ch.MembershipStatus,
		nullableInt(ch.ParticipantsCount),
		boolInt(ch.Verified), boolInt(ch.Scam), boolInt(ch.Fake), boolInt(ch.NoticesKnown),
		formatTime(now), formatTime(now), formatTime(now),
	); err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	// A report that did not carry the notices keeps the stored ones: not
	// reported is not "cleared", and writing the zero value would silently
	// un-flag a channel Telegram still flags.
	if ch.NoticesKnown {
		if _, err := tx.ExecContext(ctx, `
UPDATE managed_channels SET verified = ?, scam = ?, fake = ?, notices_known = 1
WHERE client_key = ? AND channel_id = ?`,
			boolInt(ch.Verified), boolInt(ch.Scam), boolInt(ch.Fake),
			ch.ClientKey, ch.ChannelID,
		); err != nil {
			return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
		}
	}
	// The same rule for the permissions, which only a membership-confirming
	// report carries.
	if ch.PermissionsKnown {
		if _, err := tx.ExecContext(ctx, `
UPDATE managed_channels SET permission_new = ?, permission_edit = ?, permission_delete = ?,
	permission_read = ?, permissions_known = 1
WHERE client_key = ? AND channel_id = ?`,
			boolInt(ch.Permissions.New), boolInt(ch.Permissions.Edit),
			boolInt(ch.Permissions.Delete), boolInt(ch.Permissions.Read),
			ch.ClientKey, ch.ChannelID,
		); err != nil {
			return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
		}
	}

	row := tx.QueryRowContext(ctx, selectColumns+` WHERE client_key = ? AND channel_id = ?`,
		ch.ClientKey, ch.ChannelID)
	saved, err := scanChannel(row)
	if err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, fmt.Errorf("channelstore: write channel %d: %w", ch.ChannelID, err)
	}
	return saved, nil
}

const selectColumns = `SELECT id, client_key, channel_id, peer_type, title, username,
	description, avatar_photo_id, avatar_available, membership_status, participants_count,
	verified, scam, fake, notices_known,
	permission_new, permission_edit, permission_delete, permission_read, permissions_known,
	created_at, updated_at, last_synced_at FROM managed_channels`

// selectJoinedColumns is selectColumns' list qualified with m, for a query that
// filters the records with a subquery on the same table.
const selectJoinedColumns = `SELECT m.id, m.client_key, m.channel_id, m.peer_type, m.title, m.username,
	m.description, m.avatar_photo_id, m.avatar_available, m.membership_status, m.participants_count,
	m.verified, m.scam, m.fake, m.notices_known,
	m.permission_new, m.permission_edit, m.permission_delete, m.permission_read,
	m.permissions_known,
	m.created_at, m.updated_at, m.last_synced_at FROM managed_channels m`

type scanner interface {
	Scan(dest ...any) error
}

func scanChannel(row scanner) (Channel, error) {
	var (
		ch                          Channel
		participants                sql.NullInt64
		verified, scam, fake, known int
		permNew, permEdit, permDel  int
		permRead, permKnown         int
		created                     string
		updated                     string
		synced                      string
	)
	if err := row.Scan(
		&ch.ID, &ch.ClientKey, &ch.ChannelID, &ch.PeerType, &ch.Title, &ch.Username,
		&ch.Description, &ch.AvatarPhotoID, &ch.AvatarAvailable, &ch.MembershipStatus, &participants,
		&verified, &scam, &fake, &known,
		&permNew, &permEdit, &permDel, &permRead, &permKnown,
		&created, &updated, &synced,
	); err != nil {
		return Channel{}, err
	}
	ch.Verified, ch.Scam, ch.Fake = verified != 0, scam != 0, fake != 0
	ch.NoticesKnown = known != 0
	ch.Permissions = tgapi.Permissions{
		New: permNew != 0, Edit: permEdit != 0, Delete: permDel != 0, Read: permRead != 0,
	}
	ch.PermissionsKnown = permKnown != 0
	if participants.Valid {
		v := int(participants.Int64)
		ch.ParticipantsCount = &v
	}
	ch.CreatedAt = parseTime(created)
	ch.UpdatedAt = parseTime(updated)
	ch.LastSyncedAt = parseTime(synced)
	return ch, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// DeleteChannelRow removes one locally managed channel by its local id.
//
// The permission assignments that reference it are removed by the ON DELETE
// CASCADE foreign key. Telegram membership is unchanged: this forgets the local
// record, it does not leave a channel.
func (s *Store) DeleteChannelRow(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM managed_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("channelstore: delete channel row %d: %w", id, err)
	}
	if removed, _ := res.RowsAffected(); removed == 0 {
		return ErrChannelRowNotFound
	}
	return nil
}

// ErrChannelRowNotFound means no managed channel has that local id.
var ErrChannelRowNotFound = errors.New("channelstore: managed channel not found")
