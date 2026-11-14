package gotdclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/mirza/telesync/internal/automation/recovery"
	"github.com/mirza/telesync/internal/peerstore"
	"github.com/mirza/telesync/internal/profile"
	"github.com/mirza/telesync/internal/rpc"
	"github.com/mirza/telesync/internal/tgapi"
)

// Manager maintains one long-running Telegram client for one configured instance.
//
// It owns the connection lifecycle; every RPC a worker issues goes through the
// live client, never through a freshly dialed one.
type Manager struct {
	spec *profile.Spec
	// log receives gotd diagnostics; nil disables them.
	log     io.Writer
	mu      sync.Mutex
	client  *telegram.Client
	api     *tg.Client
	state   tgapi.State
	lastErr string
	account *tgapi.Account
	thisDC  int
	// lastSuccess is when the last authorized probe succeeded. It is evidence
	// of a working account, so it is only ever set from a successful Self call.
	lastSuccess time.Time
	ready       chan struct{}
	// runDone closes when the client goroutine started by Start has fully
	// stopped, including its final session write.
	runDone chan struct{}
	// everReady is true once the first connection completed initConnection.
	// After that gotd itself blocks requests until a reconnect succeeds, so
	// callers must not wait for a second "ready" event of their own.
	everReady bool
	// peers remembers channel access hashes across requests and restarts,
	// because MTProto cannot address a channel by numeric id alone.
	peers *peerstore.Store
	// reconnect is how long the watchdog waits before rebuilding a client that
	// is still not connected. Zero disables the watchdog: gotd's own backoff is
	// then alone.
	reconnect time.Duration
	// logged is the last connection state written to the log; a line is emitted
	// per transition, never per callback.
	logged tgapi.State
	// connectedAt is when the current connected spell started.
	connectedAt time.Time
	// lastDisconnect is the most recent reason the connection dropped, kept for
	// the next "disconnected" line and exposed through Status.
	lastDisconnect string
	// cancel stops the current client generation.
	cancel context.CancelFunc
	// now dates the cached custom-emoji set. Nil means time.Now.
	now func() time.Time
	// emojiMu guards the cached reaction sticker set's documents.
	emojiMu      sync.Mutex
	emojiDocs    map[int64]*tg.Document
	emojiExpires time.Time
	// logMu serializes log writes with the state they describe.
	logMu sync.Mutex
	// eventf emits one line per connection transition. Never nil after
	// NewManager.
	eventf func(format string, args ...any)
	// automation turns on the update listener when set.
	automation *AutomationOptions
	// accountID is the opaque account this connection serves. It scopes every
	// piece of durable state the connection keeps.
	accountID            string
	expectedRemoteUserID int64
	// updatesMgr is the current generation's gotd update manager. It is rebuilt
	// per client generation and reloads its counters from the daemon, so a
	// reconnect resumes where the durable state left off. Guarded by mu.
	updatesMgr *updates.Manager
}

// ManagerOptions configures the manager.
type ManagerOptions struct {
	// AccountID is the opaque account this connection serves. It is the scope
	// key for update state, peer access hashes, checkpoints and observations.
	//
	// It is required for an account worker: without it the manager would fall
	// back to the client key and two accounts on one server definition would
	// share one pts. See Validate.
	AccountID string
	// RemoteUserID is the authorized Telegram identity stored on the account
	// record. Self must continue to match it before the manager marks the
	// account ready or starts update ingestion.
	RemoteUserID int64
	// Log receives gotd's structured logs. They contain no credentials.
	Log io.Writer
	// Verbose enables gotd's debug logs. They are very noisy, so the worker
	// leaves them off by default.
	Verbose bool
	// Peers remembers channel access hashes. Nil disables persistence, which
	// only means numeric channel ids cannot be resolved after a restart.
	Peers *peerstore.Store
	// Reconnect is how long to wait between attempts to restore a connection
	// that dropped. Zero means DefaultReconnect.
	//
	// This exists for networks where the link is simply absent for minutes at a
	// time (a filtered network, a VPN that is not up yet): a fixed interval
	// brings the connection back as soon as the network returns, which an
	// exponential backoff that has already reached its ceiling does not.
	Reconnect time.Duration
	// OnEvent receives one line per connection transition (connected,
	// disconnected, reconnect failed). Nil discards them.
	OnEvent func(format string, args ...any)
	// Now overrides the clock, which dates the cached custom-emoji set
	// (tests). Nil means time.Now.
	Now func() time.Time
	// Automation turns on the MTProto update listener, so new channel messages
	// reach the durable automation pipeline. Nil leaves updates discarded (the
	// pre-automation behaviour).
	Automation *AutomationOptions
}

// scopeKey is the key this connection's durable state is scoped by.
//
// It is the account id whenever the manager serves an account, and the client
// key only on the legacy configured-client path, where one worker per key made
// the key an identity. Two accounts on one server definition therefore never
// share a counter, a peer cache, a checkpoint or an observation.
func (m *Manager) scopeKey() string {
	if m.accountID != "" {
		return m.accountID
	}
	return m.spec.Key
}

// buildStateStore binds the durable update state to this connection's account.
func (m *Manager) buildStateStore() *recovery.StateStore {
	return recovery.NewStateStore(m.scopeKey(), m.automationState())
}

// automationState is the durable state backend, or nil when automation is off.
func (m *Manager) automationState() recovery.StatePersistence {
	if m.automation == nil {
		return nil
	}
	return m.automation.State
}

// Validate reports whether this manager has a complete, isolated identity for
// serving one account.
//
// An account worker that did not name its account would fall back to the server
// key, and every account on that server would then share one pts, one peer
// cache and one dedup scope. That is refused rather than allowed to happen
// silently.
func (m *Manager) Validate() error {
	if err := rpc.ValidAccountID(m.accountID); err != nil {
		return fmt.Errorf("gotdclient: a manager that serves an account must name the account; " +
			"without it update state would be shared across every account on this server")
	}
	if m.spec == nil {
		return errors.New("gotdclient: account manager has no resolved server profile")
	}
	if m.expectedRemoteUserID <= 0 {
		return errors.New("gotdclient: account manager has no persisted remote user identity")
	}
	return nil
}

// ValidateAccount checks that this Telegram manager was built from the same
// account metadata the worker service will report.
func (m *Manager) ValidateAccount(accountID string, remoteUserID int64, serverKey, networkType string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.accountID != accountID || m.expectedRemoteUserID != remoteUserID ||
		m.spec == nil || m.spec.Key != serverKey {
		return errors.New("gotdclient: manager profile does not match the account record")
	}
	switch networkType {
	case "telegram":
		if m.spec.Kind != profile.Official {
			return errors.New("gotdclient: manager network does not match the account record")
		}
	case "custom":
		if m.spec.Kind != profile.Custom {
			return errors.New("gotdclient: manager network does not match the account record")
		}
	default:
		return errors.New("gotdclient: manager network does not match the account record")
	}
	return nil
}

func (m *Manager) verifyRemoteUser(userID int64) error {
	if m.accountID != "" && (m.expectedRemoteUserID <= 0 || userID != m.expectedRemoteUserID) {
		return errors.New("authenticated identity does not match the account record")
	}
	return nil
}

// emitLockedForTest exposes the one-line-per-transition logger to tests, which
// is the only way to assert what an operator would see without a live client.
func (m *Manager) emitLockedForTest(phase tgapi.State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitLocked(phase)
}

// AutomationOptions is what the update listener needs to reach the daemon.
//
// The worker holds no database, so both the observations and the update counter
// cross to the daemon over the private automation bridge. State and Checkpoints
// are the two halves of gotd's durable update state: the counters say where
// Telegram's difference API got to, the checkpoints say which messages the gap
// backfill has already consumed.
type AutomationOptions struct {
	// Sink receives one normalized observation per new channel message.
	Sink IngestSink
	// State persists gotd's pts/qts/seq counters in the daemon.
	State recovery.StatePersistence
	// Checkpoints persists the bounded gap-backfill high-water marks.
	Checkpoints recovery.CheckpointStore
	// NetworkKey is the network this connection belongs to, so two connections
	// on one network describe one logical message.
	NetworkKey string
	// Read and Accept run the bounded gap backfill: Read lists up to limit
	// message ids newer than afterID; Accept ingests one. Both may be nil, in
	// which case a channel gap is reported as unrecoverable instead of being
	// backfilled.
	Read   backfillReader
	Accept backfillAcceptor
	// BackfillLimit bounds one gap-backfill batch. Zero means defaultBackfillBatch.
	BackfillLimit int
	// MaxChannelDifferenceConcurrency bounds concurrent getChannelDifference
	// calls across tracked channels (gotd). Zero means unlimited.
	MaxChannelDifferenceConcurrency int
}

// DefaultReconnect is the retry interval when the configuration does not set
// one.
const DefaultReconnect = 10 * time.Second

// NewManager creates a Manager for a fully resolved profile spec.
func NewManager(spec *profile.Spec, opts ManagerOptions) *Manager {
	// Verbose logging is opt-in; see Options.Log.
	logw := opts.Log
	if logw != nil && !opts.Verbose {
		// Connection state is reported through Status; gotd's own debug stream
		// is only useful when diagnosing a handshake.
		logw = io.Discard
	}
	reconnect := opts.Reconnect
	if reconnect <= 0 {
		reconnect = DefaultReconnect
	}
	m := &Manager{
		spec:                 spec,
		log:                  logw,
		state:                tgapi.StateDisconnected,
		ready:                make(chan struct{}),
		peers:                opts.Peers,
		reconnect:            reconnect,
		now:                  opts.Now,
		automation:           opts.Automation,
		accountID:            opts.AccountID,
		expectedRemoteUserID: opts.RemoteUserID,
	}
	m.eventf = opts.OnEvent
	if m.eventf == nil {
		m.eventf = func(string, ...any) {}
	}
	return m
}

// remember records a channel's access hash so later requests can address it by
// id. It never fails a request: the cache is an optimization over Telegram.
func (m *Manager) remember(ch *tg.Channel) {
	if m.peers == nil || ch == nil || ch.ID <= 0 || ch.AccessHash == 0 {
		return
	}
	_ = m.peers.Save(ch.ID, ch.AccessHash, ch.Title, ch.Username)
}

// peerHash returns a remembered access hash.
func (m *Manager) peerHash(id int64) (int64, bool) {
	if m.peers == nil {
		return 0, false
	}
	return m.peers.Find(id)
}

// Start connects and keeps the connection alive until ctx is cancelled.
//
// It returns immediately; Status reports progress. gotd reconnects on its own,
// and a watchdog on top of it rebuilds the whole client whenever the connection
// stays down: a network that is unreachable for minutes (a filtered link, a
// VPN that is not up yet) can leave gotd retrying an address from a DNS answer
// that has long expired, and only a fresh client re-resolves and redials.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.runDone != nil {
		m.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		m.state = tgapi.StateDisconnected
		m.mu.Unlock()
		return
	}
	if m.accountID != "" {
		if err := m.Validate(); err != nil {
			m.mu.Unlock()
			m.setStateError(err)
			return
		}
	}
	m.state = tgapi.StateConnecting
	done := make(chan struct{})
	m.runDone = done
	m.mu.Unlock()

	go m.watchFirstConnect(ctx)
	go func() {
		defer close(done)
		m.supervise(ctx)
	}()
}

// supervise runs one client after another until ctx ends.
//
// Each generation is a fresh gotd client (and therefore a fresh DNS lookup and
// connection), because an existing one keeps retrying what it has already
// resolved. A generation that stays connected is left alone; one that is still
// down after the retry interval is replaced.
func (m *Manager) supervise(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		client := m.newClient()
		ran := make(chan struct{})
		go func() {
			defer close(ran)
			m.runClient(ctx, client)
		}()

		timer := time.NewTimer(m.reconnect)
		select {
		case <-ctx.Done():
			timer.Stop()
			<-ran
			return
		case <-ran:
			timer.Stop()
			// The generation ended on its own (shutdown or a fatal client
			// error). Give the next one a moment rather than spinning.
			if ctx.Err() != nil {
				return
			}
			m.eventf("client %s: connection loop ended (%s); retrying in %s",
				m.logKey(), m.reason(), m.reconnect)
			if !sleep(ctx, m.reconnect) {
				return
			}
			continue
		case <-timer.C:
			// Still not connected after one full interval: replace the client.
		}

		m.mu.Lock()
		up := m.state == tgapi.StateReady || m.state == tgapi.StateUnauthorized
		m.mu.Unlock()
		if up {
			// gotd is healthy; wait for this generation to end on its own.
			<-ran
			continue
		}

		m.eventf("client %s: still not connected after %s; reconnecting with a fresh client",
			m.logKey(), m.reconnect)
		// Cancel the old generation and give it a moment to stop before a new
		// client writes the same session file.
		m.detach()
		m.cancelRun()
		if !waitWithin(ran, 5*time.Second) {
			m.eventf("client %s: the previous connection did not stop in time; starting a new one anyway",
				m.logKey())
		}
	}
}

// newClient builds one gotd client generation and publishes it.
func (m *Manager) newClient() *telegram.Client {
	// Build this generation's update manager first: it is the handler gotd must
	// have before the client is constructed, and it reloads its counters from
	// the daemon so a reconnect resumes instead of starting over.
	mgr := m.buildUpdatesManager()
	if mgr != nil {
		m.spec.UpdateHandler = mgr
	}
	client := m.spec.NewClientWithBackoff(m.onConnectionState, m.log, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = client
	m.api = client.API()
	m.updatesMgr = mgr
	return client
}

// buildUpdatesManager wires the automation update listener for one client
// generation. It returns nil when automation is off, leaving gotd's no-op
// handler in place.
func (m *Manager) buildUpdatesManager() *updates.Manager {
	if m.automation == nil {
		return nil
	}
	// The scope key is the account, never the server key: two accounts watching
	// the same remote channel keep two cursors, two peer caches and two
	// checkpoints.
	scope := m.scopeKey()
	handler := newUpdateHandler(scope, m.accountID, m.automation.NetworkKey, m.automation.Sink, m.now, m.eventf)

	stateStore := recovery.NewStateStore(scope, m.automation.State)
	read, accept := m.automation.Read, m.automation.Accept
	if read == nil {
		read = m.backfillRead
	}
	if accept == nil {
		accept = m.backfillAccept
	}
	bf := m.buildBackfiller(read, accept)

	return updates.New(updates.Config{
		Handler:                         handler,
		Storage:                         stateStore,
		AccessHasher:                    stateStore,
		UserAccessHasher:                stateStore,
		OnChannelTooLong:                bf.onChannelTooLong,
		OnTooLong:                       bf.onTooLong,
		OnLoadUserStateFailed:           bf.onLoadUserStateFailed,
		OnLoadChannelStateFailed:        bf.onLoadChannelStateFailed,
		MaxChannelDifferenceConcurrency: m.automation.MaxChannelDifferenceConcurrency,
	})
}

// buildBackfiller wires the bounded gap backfill for this connection's account.
// The checkpoint store is the account's own, so recovering a gap for one account
// never advances another's high-water mark.
func (m *Manager) buildBackfiller(read backfillReader, accept backfillAcceptor) *backfiller {
	return newBackfiller(m.scopeKey(), m.automation.Checkpoints, read, accept,
		m.automation.BackfillLimit, m.eventf)
}

// buildBackfillerForTest exposes the account-scoped backfiller to tests.
func (m *Manager) buildBackfillerForTest() *backfiller { return m.buildBackfiller(nil, nil) }

// runClient runs one client generation until it stops.
func (m *Manager) runClient(ctx context.Context, client *telegram.Client) {
	runCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()
	defer cancel()

	err := client.Run(runCtx, func(cbCtx context.Context) error {
		m.setReady()
		m.refreshAccount(cbCtx)
		// Start the update manager now that the account is known. It reloads the
		// durable counters from the daemon and recovers whatever gap the
		// disconnect left, then keeps the pipeline fed until this generation
		// ends. Run blocks, so it runs beside the callback.
		m.startUpdates(cbCtx, client)
		// Block until the manager is stopped; the client's Run treats a
		// returning callback as "stop the client".
		<-cbCtx.Done()
		return cbCtx.Err()
	})
	// A run that ends while the manager is still alive left the connection
	// down; record why, so the log line and the next attempt carry a reason.
	if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		m.setError(err)
	}
}

// startUpdates runs the current generation's update manager once the account is
// known. It does nothing when automation is off or the account is not yet
// authorized (the manager needs a user id). Run blocks for the life of the
// generation, so it runs in its own goroutine and ends with cbCtx.
func (m *Manager) startUpdates(cbCtx context.Context, client *telegram.Client) {
	m.mu.Lock()
	mgr := m.updatesMgr
	var userID int64
	if m.account != nil {
		userID = m.account.ID
	}
	m.mu.Unlock()
	if mgr == nil || userID == 0 || m.verifyRemoteUser(userID) != nil {
		return
	}
	go func() {
		// forget=false: resume from the durable counters the daemon holds, so a
		// reconnect replays the gap instead of silently skipping it.
		if err := mgr.Run(cbCtx, client.API(), userID, updates.AuthOptions{
			IsBot:  false,
			Forget: false,
		}); err != nil && cbCtx.Err() == nil {
			m.eventf("client %s: update manager stopped: %v", m.logKey(), err)
		}
	}()
}

// detach forgets the current client so Status stops reporting its state.
func (m *Manager) detach() {
	m.mu.Lock()
	m.client = nil
	m.api = nil
	m.mu.Unlock()
}

// cancelRun stops the current generation if one is running.
func (m *Manager) cancelRun() {
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// reason is the last recorded connection problem, or "no error" when none.
func (m *Manager) reason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastErr == "" && m.lastDisconnect == "" {
		return "no error"
	}
	if m.lastErr != "" {
		return m.lastErr
	}
	return m.lastDisconnect
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func waitWithin(done <-chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// firstConnectTimeout is how long a client may keep retrying before health
// carries a diagnostic saying it has not connected yet.
const firstConnectTimeout = 45 * time.Second

// watchFirstConnect records a diagnostic when the client keeps retrying without
// ever becoming ready. The client keeps reconnecting; only the message changes.
func (m *Manager) watchFirstConnect(ctx context.Context) {
	timer := time.NewTimer(firstConnectTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
		m.mu.Lock()
		stillConnecting := m.state == tgapi.StateConnecting || m.state == tgapi.StateDisconnected
		if stillConnecting {
			m.lastErr = "No Telegram connection yet; retrying in the background."
		}
		m.mu.Unlock()
	}
}

// Wait blocks until the client goroutine has stopped and its session has been
// written. It is what makes shutdown ordered: the process must not exit while
// gotd is still persisting credentials.
func (m *Manager) Wait(ctx context.Context) error {
	m.mu.Lock()
	done := m.runDone
	m.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop closes the client. The session file stays on disk.
func (m *Manager) Stop() {
	m.mu.Lock()
	client := m.client
	m.client = nil
	m.api = nil
	m.state = tgapi.StateDisconnected
	m.mu.Unlock()
	_ = client
}

// onConnectionState mirrors gotd's states into tgapi states.
func (m *Manager) onConnectionState(state telegram.ConnectionState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch state {
	case telegram.ConnectionStateConnecting:
		if m.state != tgapi.StateReady {
			m.state = tgapi.StateConnecting
		}
	case telegram.ConnectionStateReady:
		// Authorization is decided by refreshAccount, which must also run after
		// every reconnect: a dropped connection returns the state to
		// "connecting" and would otherwise never recover.
		if m.state != tgapi.StateReady && m.state != tgapi.StateUnauthorized {
			m.state = tgapi.StateConnecting
		}
		go m.refreshAfterReconnect()
	case telegram.ConnectionStateDisconnected:
		if m.state != tgapi.StateDisconnected {
			m.state = tgapi.StateConnecting
		}
	}
	m.emitLocked(m.state)
}

// emitLocked logs one line when the observable state changes. Caller holds mu.
//
// The state is decided here rather than by gotd's transient callbacks, so the
// log carries a reconnect exactly once instead of per connection attempt.
func (m *Manager) emitLocked(phase tgapi.State) {
	if phase == m.logged {
		return
	}
	m.logged = phase

	key := m.logKey()
	remote := m.remoteLocked()
	switch phase {
	case tgapi.StateReady:
		m.connectedAt = time.Now()
		m.logMu.Lock()
		m.eventf("client %s: connected to %s", key, remote)
		m.logMu.Unlock()
	case tgapi.StateUnauthorized:
		m.logMu.Lock()
		m.eventf("client %s: connected to %s but the account is not authorized; run `auth --key %s`",
			key, remote, key)
		m.logMu.Unlock()
	case tgapi.StateError:
		m.logMu.Lock()
		m.eventf("client %s: %s", key, m.reasonLocked())
		m.logMu.Unlock()
	case tgapi.StateDisconnected:
		// A clean stop; nothing to report beyond the shutdown line.
	case tgapi.StateConnecting:
		downtime := ""
		if !m.connectedAt.IsZero() {
			downtime = fmt.Sprintf(" (connection lost after %s)", time.Since(m.connectedAt).Round(time.Second))
		}
		connected := m.everReady
		m.connectedAt = time.Time{}
		m.logMu.Lock()
		if connected || downtime != "" {
			m.eventf("client %s: disconnected from %s%s; reconnecting", key, remote, downtime)
		} else {
			m.eventf("client %s: connecting to %s", key, remote)
		}
		m.logMu.Unlock()
	}
}

// logKey is the identity a log line names. It is the opaque account id whenever
// one is bound, so a log never identifies an account by a server key, a
// username or a phone number.
func (m *Manager) logKey() string {
	if m.accountID != "" {
		return m.accountID
	}
	return m.spec.Key
}

// reasonLocked renders the current problem without leaking credentials.
func (m *Manager) reasonLocked() string {
	if m.lastDisconnect != "" {
		return "connection error: " + m.lastDisconnect
	}
	if m.lastErr != "" {
		return "connection error: " + m.lastErr
	}
	return "connection error"
}

// remoteLocked is the endpoint this client talks to, without credentials.
func (m *Manager) remoteLocked() string {
	if m.spec.Endpoint != "" {
		return m.spec.Endpoint
	}
	return "Telegram production"
}

func (m *Manager) setReady() {
	m.mu.Lock()
	ch := m.ready
	m.ready = make(chan struct{})
	m.everReady = true
	m.mu.Unlock()
	close(ch)
}

// firstRequestWait bounds how long a request waits for the very first
// connection before the panel answers "not connected" instead of hanging.
const firstRequestWait = 4 * time.Second

// waitReady waits for the first connection, bounded in time.
//
// Once the client has connected at least once, this returns immediately: gotd
// keeps retrying in the background and its own Invoke waits for the next
// connection, so a second wait here would only duplicate it.
func (m *Manager) waitReady(ctx context.Context) error {
	m.mu.Lock()
	ch := m.ready
	everReady := m.everReady
	state := m.state
	m.mu.Unlock()
	if everReady || (state != tgapi.StateConnecting && state != tgapi.StateDisconnected) {
		return nil
	}
	timer := time.NewTimer(firstRequestWait)
	defer timer.Stop()
	select {
	case <-ch:
		return nil
	case <-timer.C:
		return tgapi.ErrNotConnected
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", tgapi.ErrNotConnected, ctx.Err())
	}
}

func (m *Manager) setError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = describe(err)
	if m.lastDisconnect == "" {
		m.lastDisconnect = m.lastErr
	}
	m.state = tgapi.StateError
	m.emitLocked(m.state)
}

// refreshAfterReconnect re-probes the session after gotd reports a ready
// connection. It is bounded so a hung probe cannot leak a goroutine forever.
func (m *Manager) refreshAfterReconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	m.mu.Lock()
	running := m.client != nil
	m.mu.Unlock()
	if !running {
		return
	}
	m.refreshAccount(ctx)
}

// refreshAccount probes the session and updates the state.
func (m *Manager) refreshAccount(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	m.mu.Lock()
	api := m.client
	m.mu.Unlock()
	if api == nil {
		return
	}

	user, err := api.Self(probeCtx)
	switch {
	case err == nil && user != nil:
		_ = m.acceptRemoteUser(user, api.Config().ThisDC)
	case err == nil:
		m.mu.Lock()
		m.state = tgapi.StateUnauthorized
		m.account = nil
		m.emitLocked(m.state)
		m.mu.Unlock()
	case auth.IsUnauthorized(err), tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_EXPIRED"):
		m.mu.Lock()
		m.state = tgapi.StateUnauthorized
		m.lastErr = ""
		m.account = nil
		m.emitLocked(m.state)
		m.mu.Unlock()
	default:
		m.setStateError(err)
	}
}

// acceptRemoteUser records a successful Self response only after proving that
// it still belongs to the persisted account. This runs before startUpdates, so
// an account mismatch cannot become ready or ingest another user's updates.
func (m *Manager) acceptRemoteUser(user *tg.User, thisDC int) error {
	if user == nil {
		return errors.New("gotdclient: Telegram returned no authenticated user")
	}
	if err := m.verifyRemoteUser(user.ID); err != nil {
		m.mu.Lock()
		m.state = tgapi.StateError
		m.lastErr = err.Error()
		m.lastDisconnect = m.lastErr
		m.account = nil
		m.emitLocked(m.state)
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	m.state = tgapi.StateReady
	m.lastErr = ""
	m.lastDisconnect = ""
	m.account = accountFromUser(user)
	m.thisDC = thisDC
	m.lastSuccess = time.Now().UTC()
	m.emitLocked(m.state)
	m.mu.Unlock()
	return nil
}

func (m *Manager) setStateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = tgapi.StateError
	m.lastErr = describe(err)
	m.lastDisconnect = m.lastErr
	m.emitLocked(m.state)
}

// Status implements tgapi.Client.
func (m *Manager) Status(ctx context.Context) tgapi.Status {
	m.mu.Lock()
	state := m.state
	thisDC := m.thisDC
	lastErr := m.lastErr
	lastSuccess := m.lastSuccess
	var account *tgapi.Account
	if m.account != nil {
		copy := *m.account
		account = &copy
	}
	m.mu.Unlock()

	if state == tgapi.StateReady && account == nil {
		// Connected but we never got a user; ask once, cheaply.
		m.refreshAccount(ctx)
		m.mu.Lock()
		state = m.state
		thisDC = m.thisDC
		lastErr = m.lastErr
		lastSuccess = m.lastSuccess
		if m.account != nil {
			copy := *m.account
			account = &copy
		}
		m.mu.Unlock()
	}

	return tgapi.Status{State: state, ThisDC: thisDC, User: account, Err: lastErr, LastSuccess: lastSuccess}
}

func accountFromUser(u *tg.User) *tgapi.Account {
	if u == nil {
		return nil
	}
	return &tgapi.Account{
		ID:        u.ID,
		Username:  u.Username,
		FirstName: u.FirstName,
		LastName:  u.LastName,
	}
}

// apiClient returns the live tg client, waiting briefly for the first
// connection before Telegram is reachable.
func (m *Manager) apiClient(ctx context.Context) (*telegram.Client, *tg.Client, error) {
	m.mu.Lock()
	client, api := m.client, m.api
	state := m.state
	everReady := m.everReady
	m.mu.Unlock()
	if client == nil {
		return nil, nil, tgapi.ErrNotConnected
	}
	if everReady {
		// Once connected, gotd itself blocks requests until the next
		// connection, including across a client the watchdog replaced.
		if api == nil {
			return nil, nil, tgapi.ErrNotConnected
		}
		if state == tgapi.StateUnauthorized {
			return nil, nil, tgapi.ErrNotAuthorized
		}
		return client, api, nil
	}
	if api == nil || state == tgapi.StateConnecting || state == tgapi.StateDisconnected {
		if err := m.waitReady(ctx); err != nil {
			return nil, nil, err
		}
	}
	m.mu.Lock()
	client, api = m.client, m.api
	state = m.state
	m.mu.Unlock()
	if client == nil || api == nil {
		return nil, nil, tgapi.ErrNotConnected
	}
	if state == tgapi.StateUnauthorized {
		return nil, nil, tgapi.ErrNotAuthorized
	}
	return client, api, nil
}

// describe renders a short, sanitized explanation.
//
// The technical error goes to the log; health exposes only this summary so a
// client's state is legible without leaking RPC internals.
func describe(err error) string {
	if err == nil {
		return ""
	}
	var fw tgapi.FloodWait
	if errors.As(err, &fw) {
		return fmt.Sprintf("Telegram is rate limiting this client for about %d seconds.", fw.Seconds)
	}
	switch {
	case errors.Is(err, tgapi.ErrNotAuthorized):
		return "The Telegram account is not authorized."
	case errors.Is(err, tgapi.ErrNotConnected):
		return "No live Telegram connection."
	}
	return "The Telegram connection failed; see the worker log for details."
}
