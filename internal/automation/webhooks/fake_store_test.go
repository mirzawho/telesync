package webhooks_test

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/mirza/telesync/internal/automation"
	"github.com/mirza/telesync/internal/automation/webhooks"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/secrets"
)

// fakeStore is an in-memory webhooks.Store. The real store is exercised against
// a real database in internal/channelstore; what these tests need is control
// over what is written, and a way to assert that a retry did not create a
// second delivery row.
type fakeStore struct {
	mu         sync.Mutex
	webhooks   map[int64]*fakeWebhook
	deliveries map[int64]*automation.WebhookDelivery
	attempts   map[int64][]automation.WebhookAttempt
	nextHook   int64
	nextDel    int64
	nextAtt    int64

	// creates counts delivery rows written, so a test can prove idempotency.
	creates int
}

type fakeWebhook struct {
	rec    automation.Webhook
	secret secrets.Box
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		webhooks:   map[int64]*fakeWebhook{},
		deliveries: map[int64]*automation.WebhookDelivery{},
		attempts:   map[int64][]automation.WebhookAttempt{},
	}
}

var _ webhooks.Store = (*fakeStore)(nil)

func (f *fakeStore) CreateWebhook(ctx context.Context, createdBy int64, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextHook++
	rec.ID = f.nextHook
	rec.CreatedAt = time.Now().UTC()
	rec.UpdatedAt = rec.CreatedAt
	rec.HasSecret = secret != nil && (secret.Version != 0 || len(secret.Ciphertext) > 0)
	stored := &fakeWebhook{rec: rec}
	if secret != nil {
		stored.secret = *secret
	}
	f.webhooks[rec.ID] = stored
	return rec, nil
}

func (f *fakeStore) GetWebhook(ctx context.Context, id int64) (automation.Webhook, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.webhooks[id]
	if !ok {
		return automation.Webhook{}, false, nil
	}
	return h.rec, true, nil
}

func (f *fakeStore) ListWebhooks(ctx context.Context, opts automation.WebhookListOptions) (automation.WebhookPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []automation.Webhook{}
	for _, h := range f.webhooks {
		if opts.Enabled != nil && h.rec.Enabled != *opts.Enabled {
			continue
		}
		out = append(out, h.rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return automation.WebhookPage{Webhooks: out, Total: len(out)}, nil
}

func (f *fakeStore) UpdateWebhook(ctx context.Context, rec automation.Webhook, secret *secrets.Box) (automation.Webhook, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.webhooks[rec.ID]
	if !ok {
		return automation.Webhook{}, false, nil
	}
	rec.CreatedAt = h.rec.CreatedAt
	rec.UpdatedAt = time.Now().UTC()
	rec.HasSecret = h.rec.HasSecret
	if secret != nil {
		// A non-nil empty box is an explicit removal, not a rotation.
		h.secret = *secret
		rec.HasSecret = secret.Version != 0 || len(secret.Ciphertext) > 0
	}
	h.rec = rec
	return rec, true, nil
}

func (f *fakeStore) DeleteWebhook(ctx context.Context, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.webhooks[id]; !ok {
		return false, nil
	}
	delete(f.webhooks, id)
	return true, nil
}

func (f *fakeStore) WebhookSecretBox(ctx context.Context, id int64) (secrets.Box, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.webhooks[id]
	if !ok {
		return secrets.Box{}, false, nil
	}
	return h.secret, true, nil
}

func (f *fakeStore) WebhookIDsForChannel(ctx context.Context, managedChannelID int64) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []int64{}
	for _, h := range f.webhooks {
		for _, id := range h.rec.SourceChannelIDs {
			if id == managedChannelID && h.rec.Enabled {
				out = append(out, h.rec.ID)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *fakeStore) CreateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deliveries {
		if d.WebhookID == rec.WebhookID && d.EventID == rec.EventID {
			return automation.WebhookDelivery{}, channelstore.ErrDuplicateDelivery
		}
	}
	f.nextDel++
	rec.ID = f.nextDel
	rec.AttemptCount = 0
	rec.CreatedAt = time.Now().UTC()
	rec.UpdatedAt = rec.CreatedAt
	stored := rec
	f.deliveries[rec.ID] = &stored
	f.creates++
	return rec, nil
}

func (f *fakeStore) WebhookDeliveryForEvent(ctx context.Context, webhookID int64, eventID string) (automation.WebhookDelivery, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deliveries {
		if d.WebhookID == webhookID && d.EventID == eventID {
			return *d, true, nil
		}
	}
	return automation.WebhookDelivery{}, false, nil
}

func (f *fakeStore) GetWebhookDelivery(ctx context.Context, id int64) (automation.WebhookDelivery, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[id]
	if !ok {
		return automation.WebhookDelivery{}, false, nil
	}
	out := *d
	out.Attempts = append([]automation.WebhookAttempt{}, f.attempts[id]...)
	return out, true, nil
}

func (f *fakeStore) ListWebhookDeliveries(ctx context.Context, webhookID int64, opts automation.DeliveryListOptions) (automation.WebhookDeliveryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []automation.WebhookDelivery{}
	for _, d := range f.deliveries {
		if d.WebhookID != webhookID {
			continue
		}
		if opts.Status != "" && d.Status != opts.Status {
			continue
		}
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return automation.WebhookDeliveryPage{Deliveries: out, Total: len(out)}, nil
}

func (f *fakeStore) UpdateWebhookDelivery(ctx context.Context, rec automation.WebhookDelivery) (automation.WebhookDelivery, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.deliveries[rec.ID]
	if !ok {
		return automation.WebhookDelivery{}, false, nil
	}
	created := d.CreatedAt
	*d = rec
	d.CreatedAt = created
	d.UpdatedAt = time.Now().UTC()
	return *d, true, nil
}

func (f *fakeStore) AppendWebhookAttempt(ctx context.Context, deliveryID int64, att automation.WebhookAttempt) (automation.WebhookAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.attempts[deliveryID] {
		if existing.AttemptNumber == att.AttemptNumber {
			return automation.WebhookAttempt{}, channelstore.ErrDuplicateAttempt
		}
	}
	f.nextAtt++
	att.ID = f.nextAtt
	f.attempts[deliveryID] = append(f.attempts[deliveryID], att)
	return att, nil
}

func (f *fakeStore) WebhookAttempts(ctx context.Context, deliveryID int64) ([]automation.WebhookAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]automation.WebhookAttempt{}, f.attempts[deliveryID]...), nil
}

func (f *fakeStore) LatestWebhookAttempt(ctx context.Context, deliveryID int64) (automation.WebhookAttempt, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	history := f.attempts[deliveryID]
	if len(history) == 0 {
		return automation.WebhookAttempt{}, false, nil
	}
	return history[len(history)-1], true, nil
}

// deliveryCount reports how many delivery rows exist for one webhook and event.
func (f *fakeStore) deliveryCount(webhookID int64, eventID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, d := range f.deliveries {
		if d.WebhookID == webhookID && d.EventID == eventID {
			n++
		}
	}
	return n
}

func (f *fakeStore) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates
}
