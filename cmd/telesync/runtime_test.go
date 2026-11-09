package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mirza/telesync/internal/channelstore"
)

// A background publication is recorded against the account it runs under: the
// idempotency record names one, and the account's permissions are read again at
// execution time so a grant that has since been withdrawn stops the next
// publication. The publisher the daemon builds for the background therefore has
// to carry a resolver.
//
// Without one the interactive route would never reveal the problem — it passes
// its own actor — and every background publish is refused with "this deployment
// cannot resolve the account a background publication runs under".
func TestBackgroundPublisherResolvesTheAccountItPublishesUnder(t *testing.T) {
	store, err := channelstore.Open(filepath.Join(t.TempDir(), "channels.db"), channelstore.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	publisher := newBackgroundPublisher(nil, store, nil, t.Logf)
	if publisher.Actors == nil {
		t.Fatal("the background publisher cannot resolve the account it publishes under")
	}
	// The resolver is the store the rest of the pipeline reads, so an account
	// is looked up on every publication rather than carried in from a request.
	if _, found, err := publisher.Actors.GetUser(context.Background(), 1); err != nil {
		t.Fatalf("resolving an account: %v", err)
	} else if found {
		t.Fatal("an empty store resolved an account that does not exist")
	}
}
