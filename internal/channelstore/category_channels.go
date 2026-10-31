package channelstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The channels filed under one category, and the operation that sets them.
//
// An assignment names a client and a Telegram channel id; a category's member
// list joins it to the managed channel rows, so a channel that was subscribed
// through this application can be listed by what it carries. A channel filed
// from discovery (before it is managed) has no row yet and is simply not in the
// list, without ever being invented.

// CategoryChannels returns the managed channels filed under one category,
// ordered by title.
//
// A missing category is ErrCategoryNotFound.
func (s *Store) CategoryChannels(ctx context.Context, categoryID int64) ([]Channel, error) {
	if _, err := s.categoryByID(ctx, categoryID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT m.id, m.client_key, m.channel_id, m.peer_type, m.title, m.username,
	m.description, m.avatar_photo_id, m.avatar_available, m.membership_status,
	m.participants_count, m.verified, m.scam, m.fake, m.notices_known,
	m.permission_new, m.permission_edit, m.permission_delete, m.permission_read,
	m.permissions_known, m.created_at, m.updated_at, m.last_synced_at
FROM managed_channels m
JOIN channel_categories cc ON cc.client_key = m.client_key AND cc.channel_id = m.channel_id
WHERE cc.category_id = ?
ORDER BY m.title, m.id`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list the channels of category %d: %w", categoryID, err)
	}
	defer rows.Close()

	out := []Channel{}
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("channelstore: list the channels of category %d: %w", categoryID, err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list the channels of category %d: %w", categoryID, err)
	}
	return out, nil
}

// ReplaceCategories files one channel under exactly the categories named.
//
// It is one transaction: each category is checked to exist and the channel is
// filed in it (assigning twice is a no-op), then every other category it was
// filed in is emptied of it. An empty set files it nowhere. A category that
// does not exist is ErrCategoryNotFound and nothing is written.
func (s *Store) ReplaceCategories(ctx context.Context, clientKey string, channelID int64, categoryIDs []int64) error {
	if strings.TrimSpace(clientKey) == "" {
		return errors.New("channelstore: client_key is required")
	}
	if channelID <= 0 {
		return errors.New("channelstore: channel_id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
	}
	defer func() { _ = tx.Rollback() }()

	telegramID := strconv.FormatInt(channelID, 10)
	now := formatTime(s.now())
	keep := make([]int64, 0, len(categoryIDs))
	member := map[int64]bool{}
	for _, categoryID := range categoryIDs {
		if member[categoryID] {
			continue
		}
		if _, err := categoryIn(ctx, tx, categoryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO channel_categories (client_key, channel_id, category_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (client_key, channel_id, category_id) DO NOTHING`,
			clientKey, telegramID, categoryID, now,
		); err != nil {
			return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
		}
		member[categoryID] = true
		keep = append(keep, categoryID)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT category_id FROM channel_categories WHERE client_key = ? AND channel_id = ?`,
		clientKey, telegramID)
	if err != nil {
		return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
	}
	var drop []int64
	for rows.Next() {
		var categoryID int64
		if err := rows.Scan(&categoryID); err != nil {
			rows.Close()
			return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
		}
		if !member[categoryID] {
			drop = append(drop, categoryID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
	}
	for _, categoryID := range drop {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM channel_categories
WHERE client_key = ? AND channel_id = ? AND category_id = ?`,
			clientKey, telegramID, categoryID); err != nil {
			return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
	}
	return nil
}
