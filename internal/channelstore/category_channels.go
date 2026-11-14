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

// CategoryChannelsForOwner returns only active, non-quarantined account
// registrations belonging to ownerUserID, ordered by title.
//
// A missing category is ErrCategoryNotFound.
func (s *Store) CategoryChannelsForOwner(ctx context.Context, ownerUserID, categoryID int64) ([]Channel, error) {
	if _, err := s.categoryByID(ctx, categoryID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+joinedChannelColumns+`
FROM managed_channels m
JOIN channel_categories cc ON cc.account_id = m.account_id AND cc.channel_id = CAST(m.channel_id AS TEXT)
JOIN telegram_accounts a ON a.id = m.account_id
WHERE cc.category_id = ? AND a.owner_user_id = ? AND a.deleted_at IS NULL AND a.status <> 'quarantined'
ORDER BY m.title, m.id`, categoryID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list the channels of category %d: %w", categoryID, err)
	}
	defer rows.Close()

	out := []Channel{}
	for rows.Next() {
		ch, err := s.scanChannel(rows)
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
func (s *Store) ReplaceCategories(ctx context.Context, accountID string, channelID int64, categoryIDs []int64) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("channelstore: account_id is required")
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
INSERT INTO channel_categories (client_key, account_id, channel_id, category_id, created_at)
SELECT server_key, ?, ?, ?, ? FROM telegram_accounts WHERE id = ? AND deleted_at IS NULL
ON CONFLICT (account_id, channel_id, category_id) DO NOTHING`,
			accountID, telegramID, categoryID, now, accountID,
		); err != nil {
			return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
		}
		member[categoryID] = true
		keep = append(keep, categoryID)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT category_id FROM channel_categories WHERE account_id = ? AND channel_id = ?`,
		accountID, telegramID)
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
WHERE account_id = ? AND channel_id = ? AND category_id = ?`,
			accountID, telegramID, categoryID); err != nil {
			return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: file channel %d: %w", channelID, err)
	}
	return nil
}
