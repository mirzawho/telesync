package channelstore

import (
	"context"
	"fmt"
)

// Site categories: the global categories a WordPress site publishes into.
//
// They are the same categories channels are filed under (the categories
// table); this table is the site's side of the link. Both foreign keys
// cascade, so deleting a category drops it from every site and deleting a site
// drops its links.

// ReplaceSiteCategories files one site under exactly the categories named.
//
// One transaction: every category must exist, then the site's links become
// ids — a duplicate id is harmless and an empty list unfiles the site from
// every category. Unknown category ids are ErrCategoryNotFound and nothing is
// written.
func (s *Store) ReplaceSiteCategories(ctx context.Context, siteID int64, categoryIDs []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wordpress_sites WHERE id = ?`, siteID).Scan(&exists); err != nil {
		return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
	}
	if exists == 0 {
		return ErrSiteNotFound
	}

	now := formatTime(s.now())
	seen := map[int64]bool{}
	for _, categoryID := range categoryIDs {
		if seen[categoryID] {
			continue
		}
		if _, err := categoryIn(ctx, tx, categoryID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO wordpress_site_categories (site_id, category_id, created_at)
VALUES (?, ?, ?)
ON CONFLICT (site_id, category_id) DO NOTHING`, siteID, categoryID, now); err != nil {
			return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
		}
		seen[categoryID] = true
	}

	// Everything not named goes. The ids are read first, since the connection
	// runs one statement at a time.
	rows, err := tx.QueryContext(ctx, `SELECT category_id FROM wordpress_site_categories WHERE site_id = ?`, siteID)
	if err != nil {
		return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
	}
	var drop []int64
	for rows.Next() {
		var categoryID int64
		if err := rows.Scan(&categoryID); err != nil {
			rows.Close()
			return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
		}
		if !seen[categoryID] {
			drop = append(drop, categoryID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
	}
	for _, categoryID := range drop {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM wordpress_site_categories WHERE site_id = ? AND category_id = ?`,
			siteID, categoryID); err != nil {
			return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("channelstore: file site %d: %w", siteID, err)
	}
	return nil
}

// SiteCategories lists the categories one site is filed under, by id.
func (s *Store) SiteCategories(ctx context.Context, siteID int64) ([]int64, error) {
	if _, found, err := s.GetWordPressSite(ctx, siteID); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrSiteNotFound
	}
	out, err := s.siteCategories(ctx, siteID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CategoriesOfSites lists every site's category ids at once, keyed by site id.
//
// It is what the list route reads, so a page of sites costs one query however
// many sites it has.
func (s *Store) CategoriesOfSites(ctx context.Context, siteIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(siteIDs))
	for _, id := range siteIDs {
		out[id] = []int64{}
	}
	if len(siteIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(siteIDs))
	for _, id := range siteIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT site_id, category_id FROM wordpress_site_categories WHERE site_id IN (`+
			placeholders(len(siteIDs))+`) ORDER BY site_id, category_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list the categories of sites: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var siteID, categoryID int64
		if err := rows.Scan(&siteID, &categoryID); err != nil {
			return nil, fmt.Errorf("channelstore: list the categories of sites: %w", err)
		}
		out[siteID] = append(out[siteID], categoryID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list the categories of sites: %w", err)
	}
	return out, nil
}

func (s *Store) siteCategories(ctx context.Context, siteID int64) ([]int64, error) {
	bySite, err := s.CategoriesOfSites(ctx, []int64{siteID})
	if err != nil {
		return nil, err
	}
	return bySite[siteID], nil
}

// siteCategoryCount is how many categories one site is filed under.
func (s *Store) siteCategoryCount(ctx context.Context, siteID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wordpress_site_categories WHERE site_id = ?`, siteID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("channelstore: count the categories of site %d: %w", siteID, err)
	}
	return n, nil
}

// SiteCategoryMembers returns the site ids filed under one category.
//
// It is the read the list's category filter uses: a site is listed when it is
// filed under at least one of the selected categories. A category with no
// sites is an empty list, never an error; a missing category is
// ErrCategoryNotFound so the caller does not treat an unknown id as "nothing
// filed".
func (s *Store) SiteCategoryMembers(ctx context.Context, categoryID int64) ([]int64, error) {
	if _, err := s.categoryByID(ctx, categoryID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT site_id FROM wordpress_site_categories WHERE category_id = ? ORDER BY site_id`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("channelstore: list the sites of category %d: %w", categoryID, err)
	}
	defer rows.Close()

	out := []int64{}
	for rows.Next() {
		var siteID int64
		if err := rows.Scan(&siteID); err != nil {
			return nil, fmt.Errorf("channelstore: list the sites of category %d: %w", categoryID, err)
		}
		out = append(out, siteID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("channelstore: list the sites of category %d: %w", categoryID, err)
	}
	return out, nil
}
