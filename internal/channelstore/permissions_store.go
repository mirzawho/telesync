package channelstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ResourcePermission is one action's effect on one resource.
type ResourcePermission struct {
	Action Action
	Effect Effect
}

// ResourcePermissions is every assignment for one resource.
type ResourcePermissions struct {
	ResourceID  int64
	Permissions []ResourcePermission
}

// PermissionSet is every assignment one user holds, as stored.
type PermissionSet struct {
	Global     []GlobalPermission
	Channels   []ResourcePermissions
	Categories []ResourcePermissions
	Wordsites  []ResourcePermissions
}

// PermissionsOf reads one user's assignments.
//
// The result is what is stored, not what is effective: a deny stays visible as
// a deny.
func (s *Store) PermissionsOf(ctx context.Context, userID int64) (PermissionSet, error) {
	set := PermissionSet{Global: []GlobalPermission{}, Channels: []ResourcePermissions{},
		Categories: []ResourcePermissions{}, Wordsites: []ResourcePermissions{}}
	if err := s.readGlobal(ctx, userID, &set); err != nil {
		return set, err
	}
	channels, err := s.resourcePermissions(ctx, "user_channels_permission", "channel_id", userID)
	if err != nil {
		return set, err
	}
	set.Channels = channels
	categories, err := s.resourcePermissions(ctx, "user_categories_permission", "category_id", userID)
	if err != nil {
		return set, err
	}
	set.Categories = categories
	wordsites, err := s.resourcePermissions(ctx, "user_wordpress_permission", "site_id", userID)
	if err != nil {
		return set, err
	}
	set.Wordsites = wordsites
	return set, nil
}

func (s *Store) readGlobal(ctx context.Context, userID int64, set *PermissionSet) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT permission FROM user_permissions WHERE user_id = ? ORDER BY permission`, userID)
	if err != nil {
		return fmt.Errorf("channelstore: read user permissions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return fmt.Errorf("channelstore: read user permissions: %w", err)
		}
		set.Global = append(set.Global, GlobalPermission(p))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: read user permissions: %w", err)
	}
	return nil
}

func (s *Store) resourcePermissions(ctx context.Context, table, column string, userID int64) ([]ResourcePermissions, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+column+`, permission, effect FROM `+table+` WHERE user_id = ? ORDER BY `+column,
		userID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: read user permissions: %w", err)
	}
	defer rows.Close()

	out := []ResourcePermissions{}
	for rows.Next() {
		var (
			id                 int64
			permission, effect string
		)
		if err := rows.Scan(&id, &permission, &effect); err != nil {
			return nil, fmt.Errorf("channelstore: read user permissions: %w", err)
		}
		if n := len(out); n == 0 || out[n-1].ResourceID != id {
			out = append(out, ResourcePermissions{ResourceID: id})
		}
		last := &out[len(out)-1]
		last.Permissions = append(last.Permissions,
			ResourcePermission{Action: Action(permission), Effect: Effect(effect)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: read user permissions: %w", err)
	}
	return out, nil
}

// AuthorizerFor builds the authorization view for one user.
//
// Packages that need only "may this user do X" depend on AuthorizerFor; the
// resource-specific tables stay behind it.
func (s *Store) AuthorizerFor(ctx context.Context, userID int64) (*Authorizer, error) {
	set, err := s.PermissionsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	return authorizerOf(userID, set), nil
}

func authorizerOf(userID int64, set PermissionSet) *Authorizer {
	var grants []Grant
	for kind, group := range map[ResourceKind][]ResourcePermissions{
		KindChannel:   set.Channels,
		KindCategory:  set.Categories,
		KindWordPress: set.Wordsites,
	} {
		for _, rp := range group {
			for _, p := range rp.Permissions {
				grants = append(grants, Grant{
					UserID: userID, Kind: kind, ResourceID: rp.ResourceID,
					Action: p.Action, Effect: p.Effect,
				})
			}
		}
	}
	return NewAuthorizerFor(userID, set.Global, grants)
}

// ReplacePermissions writes a user's full permission set in one transaction.
//
// Everything the set does not mention is removed, so no stale assignment is
// left behind. Every value is validated and every resource id is looked up
// before anything is written: a set naming a nonexistent channel changes
// nothing.
func (s *Store) ReplacePermissions(ctx context.Context, userID int64, set PermissionSet) error {
	if err := validatePermissionSet(set); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: replace permissions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("channelstore: replace permissions: %w", err)
	}
	if exists == 0 {
		return ErrUserNotFound
	}
	if err := verifyResourceIDs(ctx, tx, set.Channels, "managed_channels", "channel"); err != nil {
		return err
	}
	if err := verifyResourceIDs(ctx, tx, set.Categories, "categories", "category"); err != nil {
		return err
	}
	if err := verifyResourceIDs(ctx, tx, set.Wordsites, "wordpress_sites", "wordpress site"); err != nil {
		return err
	}

	now := formatTime(s.now())
	for _, table := range []string{"user_permissions", "user_channels_permission",
		"user_categories_permission", "user_wordpress_permission"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("channelstore: replace permissions: %w", err)
		}
	}
	for _, p := range set.Global {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_permissions (user_id, permission, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			userID, string(p), now, now); err != nil {
			return fmt.Errorf("channelstore: replace permissions: %w", err)
		}
	}
	for _, group := range []struct {
		table, column string
		set           []ResourcePermissions
	}{
		{"user_channels_permission", "channel_id", set.Channels},
		{"user_categories_permission", "category_id", set.Categories},
		{"user_wordpress_permission", "site_id", set.Wordsites},
	} {
		for _, rp := range group.set {
			for _, p := range rp.Permissions {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO `+group.table+` (user_id, `+group.column+`, permission, effect, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
					userID, rp.ResourceID, string(p.Action), string(p.Effect), now, now); err != nil {
					return fmt.Errorf("channelstore: replace permissions: %w", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: replace permissions: %w", err)
	}
	return nil
}

// validatePermissionSet checks the set before anything is written.
func validatePermissionSet(set PermissionSet) error {
	seenGlobal := map[GlobalPermission]bool{}
	for _, p := range set.Global {
		if !p.Valid() {
			return fmt.Errorf("%w: unknown global permission %q", ErrInvalidUser, p)
		}
		if seenGlobal[p] {
			return fmt.Errorf("%w: global permission %q is listed twice", ErrInvalidUser, p)
		}
		seenGlobal[p] = true
	}
	for _, group := range []struct {
		name string
		set  []ResourcePermissions
	}{
		{"channel", set.Channels},
		{"category", set.Categories},
		{"site", set.Wordsites},
	} {
		seenIDs := map[int64]bool{}
		for _, rp := range group.set {
			if rp.ResourceID <= 0 {
				return fmt.Errorf("%w: %s_id must be a positive integer", ErrInvalidUser, group.name)
			}
			if seenIDs[rp.ResourceID] {
				return fmt.Errorf("%w: %s_id %d is listed twice", ErrInvalidUser, group.name, rp.ResourceID)
			}
			seenIDs[rp.ResourceID] = true
			seenActions := map[Action]bool{}
			for _, p := range rp.Permissions {
				if !ValidAction(p.Action) {
					return fmt.Errorf("%w: unknown %s action %q", ErrInvalidUser, group.name, p.Action)
				}
				if p.Action == ActionCreate {
					return fmt.Errorf("%w: creating a %s is a global permission, not a per-%s one",
						ErrInvalidUser, group.name, group.name)
				}
				if !ValidEffect(p.Effect) {
					return fmt.Errorf("%w: effect for %s %q must be allow or deny",
						ErrInvalidUser, group.name, p.Action)
				}
				if seenActions[p.Action] {
					return fmt.Errorf("%w: %s %d action %q is listed twice",
						ErrInvalidUser, group.name, rp.ResourceID, p.Action)
				}
				seenActions[p.Action] = true
			}
		}
	}
	return nil
}

// verifyResourceIDs rejects an assignment to a resource that is not in table.
func verifyResourceIDs(ctx context.Context, tx *sql.Tx, set []ResourcePermissions, table, label string) error {
	if len(set) == 0 {
		return nil
	}
	args := make([]any, 0, len(set))
	for _, rp := range set {
		args = append(args, rp.ResourceID)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM `+table+` WHERE id IN (`+placeholders(len(set))+`)`, args...)
	if err != nil {
		return fmt.Errorf("channelstore: replace permissions: %w", err)
	}
	found := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("channelstore: replace permissions: %w", err)
		}
		found[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: replace permissions: %w", err)
	}

	var missing []string
	for _, rp := range set {
		if !found[rp.ResourceID] {
			missing = append(missing, strconv.FormatInt(rp.ResourceID, 10))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: no %s has id %s", ErrResourceNotFound, label, strings.Join(missing, ", "))
	}
	return nil
}

// GrantGlobal adds global permissions an account does not hold yet.
//
// It only adds: a permission the account already has keeps its original row,
// and nothing is removed. Parameters: userID is the local user id, one or more
// permission names. It returns ErrInvalidUser for an unknown name and
// ErrUserNotFound for an unknown account.
func (s *Store) GrantGlobal(ctx context.Context, userID int64, permissions ...GlobalPermission) error {
	for _, p := range permissions {
		if !p.Valid() {
			return fmt.Errorf("%w: unknown global permission %q", ErrInvalidUser, p)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: grant permissions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("channelstore: grant permissions: %w", err)
	}
	if exists == 0 {
		return ErrUserNotFound
	}
	now := formatTime(s.now())
	for _, p := range permissions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_permissions (user_id, permission, created_at, updated_at)
VALUES (?, ?, ?, ?) ON CONFLICT (user_id, permission) DO NOTHING`,
			userID, string(p), now, now); err != nil {
			return fmt.Errorf("channelstore: grant permissions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: grant permissions: %w", err)
	}
	return nil
}
