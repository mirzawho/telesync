package channelstore

import (
	"context"
	"testing"
)

// The link table's cascade is the schema's promise, so it is checked on the
// table itself: a deleted site and a deleted category must both take the link
// with them.
func TestTheSiteCategoryLinksCascadeFromBothSides(t *testing.T) {
	store := openInternal(t)
	now := "2026-10-29T00:00:00Z"
	mustExec(t, store, `INSERT INTO wordpress_sites
		(url, title, username, credential_version, credential_nonce, credential_ciphertext,
		 credential_sealed_at, created_at, updated_at)
		VALUES ('https://a.example.com', 'A', 'admin', 1, x'00', x'01', ?, ?, ?)`, now, now, now)
	mustExec(t, store, `INSERT INTO wordpress_sites
		(url, title, username, credential_version, credential_nonce, credential_ciphertext,
		 credential_sealed_at, created_at, updated_at)
		VALUES ('https://b.example.com', 'B', 'admin', 1, x'00', x'01', ?, ?, ?)`, now, now, now)
	mustExec(t, store, `INSERT INTO categories (name, description, created_at, updated_at)
		VALUES ('Shared', '', ?, ?)`, now, now)
	mustExec(t, store, `INSERT INTO wordpress_site_categories (site_id, category_id, created_at)
		VALUES (1, 1, ?), (2, 1, ?)`, now, now)
	mustExec(t, store, `DELETE FROM wordpress_sites WHERE id = 1`)
	if n := countLinks(t, store); n != 1 {
		t.Fatalf("links after deleting one site = %d, want 1", n)
	}
	mustExec(t, store, `DELETE FROM categories WHERE id = 1`)
	if n := countLinks(t, store); n != 0 {
		t.Fatalf("links after deleting the category = %d, want 0", n)
	}
}
func mustExec(t *testing.T, store *Store, stmt string, args ...any) {
	t.Helper()
	if _, err := store.db.ExecContext(context.Background(), stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}
func countLinks(t *testing.T, store *Store) int {
	t.Helper()
	var n int
	if err := store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM wordpress_site_categories`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
