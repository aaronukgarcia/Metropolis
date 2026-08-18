package integration

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is INCREMENT 3, part 1 of the Integration Engine (proposal
// §8): the RESILIENCE state machine (proposal §1 point 5, §2's
// "Resilience layer", §7's "connection state machine"). It models every
// integration as if it COULD be a remote service, even though today it
// never is: retry with a deterministic, logical (never wall-clock)
// backoff; reconnect via re-authentication + name-lookup hooks that are
// no-ops for the local in-process case; and catch-up, which replays
// increment 2's QueuedTransport backlog FIFO, exactly-once, via its
// existing Drain method. Crash recovery (replaying the overflow queue's
// DISK log after a full process restart) is recovery.go's job, not
// this file's — this file's "catch-up" is the IN-PROCESS reconnect path
// (backlog still resident in the live QueuedTransport), while
// recovery.go's replay works from a cold start with no live queue at
// all. See recovery.go's doc comment for that distinction.
//
// # The state machine
//
// Four states (proposal §2/§7, ConnState below): Connected (the steady
// state — every Attempt succeeds), Retrying (the last Attempt's op
// failed and the retry counter has not yet exceeded its cap),
// CatchingUp (Reconnect has re-established the connection and is now
// draining the backlog before declaring Connected again), and
// Reconnecting (Attempt's retries are exhausted, or the caller has
// otherwise decided to re-establish the connection from scratch —
// Reconnect is running its Lookup/Authenticate hooks).
//
// # Determinism (GR#21) — the whole point of this increment
//
// Every state transition in this file is a pure function of (a) whether
// op()/the hooks/Drain returned an error, and (b) the retry COUNTER —
// never the wall clock, never a timer, never goroutine-scheduling order
// beyond the caller's own call order. Backoff (below) is a deterministic
// function of the retry counter alone: two runs that call Attempt with
// the exact same sequence of op() results, in the same order, produce
// the exact same sequence of (state, retries, backoff, error) tuples,
// which is what resilience_test.go's determinism test proves by running
// the identical failure-injection script twice (-race -count=2) and
// diffing the recorded transitions.
type ConnState int

const (
	// StateConnected is the steady state: the most recent Attempt (or
	// Reconnect) succeeded and the retry counter is reset to zero.
	StateConnected ConnState = iota
	// StateRetrying is entered on any failed Attempt whose retry counter
	// has not yet exceeded MaxRetries — the caller is expected to call
	// Attempt again (after however many logical backoff units NextBackoff
	// reports it wants), or to call Reconnect directly to skip straight
	// to re-establishing the connection.
	StateRetrying
	// StateReconnecting is Reconnect's first phase: Lookup then
	// Authenticate are running (both no-ops for LocalReconnectHooks).
	StateReconnecting
	// StateCatchingUp is Reconnect's second phase, entered once
	// Lookup/Authenticate both succeed: the configured Drainer (normally
	// a *QueuedTransport) is being drained FIFO, exactly-once, to deliver
	// whatever backlog accumulated while the connection was down.
	StateCatchingUp
)

// String renders a ConnState for logs/diagnostics. Never used on a
// determinism-sensitive path (no transition decision reads this).
func (s ConnState) String() string {
	switch s {
	case StateConnected:
		return "Connected"
	case StateRetrying:
		return "Retrying"
	case StateReconnecting:
		return "Reconnecting"
	case StateCatchingUp:
		return "CatchingUp"
	default:
		return "unknown"
	}
}

// Backoff computes the LOGICAL delay (in caller-defined units — e.g.
// ticks a tick driver should let pass before calling Attempt again) for
// the retry-th attempt. It MUST be a pure function of retry alone —
// never time.Now(), never a random source, never anything else — so that
// replaying the identical retry sequence always produces the identical
// backoff sequence (GR#21, this file's header comment).
type Backoff func(retry int64) int64

// maxLogicalBackoff bounds DefaultBackoff's growth so a long retry
// sequence in a test or a runaway caller never overflows int64 — a
// placeholder ceiling (BALANCE PLACEHOLDER-shaped, but this is a
// technical bound, not a player-felt number, so it does not go through
// the balance-number regime) chosen generously above any MaxRetries this
// package's own tests or DefaultMaxRetries would ever reach.
const maxLogicalBackoff = 1 << 20

// DefaultBackoff is a deterministic exponential schedule: 1, 2, 4, 8,
// ..., capped at maxLogicalBackoff. retry <= 1 returns 1 (the first
// retry's delay); retry <= 0 is treated as 1, same as retry == 1 — there
// is no "zeroth" backoff, since Attempt only ever calls this after
// incrementing the counter to at least 1.
func DefaultBackoff(retry int64) int64 {
	if retry < 1 {
		retry = 1
	}
	delay := int64(1)
	for i := int64(1); i < retry; i++ {
		if delay >= maxLogicalBackoff {
			return maxLogicalBackoff
		}
		delay *= 2
	}
	if delay > maxLogicalBackoff {
		delay = maxLogicalBackoff
	}
	return delay
}

// ReconnectHooks is the future-proofing seam proposal §1 point 5 asks
// for: "reconnect (incl. re-authentication + name lookup)". Authenticate
// re-establishes the caller's credentials against the reconnected
// endpoint; Lookup resolves a logical integration name to whatever
// address/handle the transport layer needs to talk to it. Both are
// NO-OPS for LocalReconnectHooks (the in-process, always-connected
// degenerate case this codebase runs today) but exist as a real
// interface so a future remote/cloud WorkerPool (doc.go's "future
// RemotePool seam") has somewhere to plug in real network
// authentication and service discovery without changing Connection's
// state machine at all.
//
// Both methods must be deterministic given their inputs (GR#21) — no
// wall-clock timeout, no random jitter — so a replay of the same
// Lookup/Authenticate outcomes reproduces the same state transitions.
type ReconnectHooks interface {
	// Authenticate re-establishes credentials for the connection
	// identified by correlationID. A no-op for the local case.
	Authenticate(correlationID string) error
	// Lookup resolves name to an address/handle string. A no-op for the
	// local case: it returns name unchanged, since "the local case" has
	// no separate addressing scheme.
	Lookup(correlationID string, name string) (string, error)
}

// LocalReconnectHooks is the degenerate, always-connected, no-op
// implementation of ReconnectHooks for today's in-process integrations
// (proposal §1 point 5: "Local = the degenerate always-connected case").
// Both methods always succeed and never touch the network, a clock, or
// any mutable state — trivially deterministic.
type LocalReconnectHooks struct{}

// Authenticate always succeeds — there is no remote credential to
// re-establish for an in-process integration.
func (LocalReconnectHooks) Authenticate(string) error { return nil }

// Lookup always succeeds, returning name unchanged — there is no
// separate address to resolve for an in-process integration.
func (LocalReconnectHooks) Lookup(_ string, name string) (string, error) { return name, nil }

var _ ReconnectHooks = LocalReconnectHooks{}

// Drainer is the catch-up seam: Connection.Reconnect drains whatever
// implements this after a successful Lookup/Authenticate, to flush any
// backlog that accumulated while the connection was down. QueuedTransport
// (queue.go, increment 2) already implements this signature exactly — no
// adapter needed; Drain's own contract (FIFO, priority-tiered,
// exactly-once — a peeked-but-not-yet-inner-accepted command is never
// double-committed, per Drain's own doc comment) is exactly proposal §1
// point 5's "catch-up... FIFO... exactly-once" requirement, so this file
// adds no new ordering logic of its own — it only decides WHEN to call
// Drain, never HOW Drain orders what it drains.
type Drainer interface {
	Drain(budget int) (DrainStats, error)
}

var _ Drainer = (*QueuedTransport)(nil)

// DefaultMaxRetries is the retry cap ConnectionConfig.MaxRetries falls
// back to when <= 0 — chosen generously above any realistic transient
// failure run while still small enough that exhaustion is easy to
// exercise deliberately in a test.
const DefaultMaxRetries = 5

// ConnectionConfig configures a Connection. MaxRetries <= 0 falls back
// to DefaultMaxRetries; Backoff == nil falls back to DefaultBackoff;
// Hooks == nil falls back to LocalReconnectHooks{}; Queue == nil means
// Reconnect's catch-up phase is a no-op (no backlog to drain — valid for
// an integration that has no associated QueuedTransport at all).
type ConnectionConfig struct {
	MaxRetries    int64
	Backoff       Backoff
	Hooks         ReconnectHooks
	Queue         Drainer
	CorrelationID string
}

// Connection is the per-integration/per-connection resilience state
// machine (proposal §2/§7): Connected/Retrying/CatchingUp/Reconnecting,
// with a logical (non-wall-clock) retry/backoff counter and a
// reconnect+catch-up path built on ReconnectHooks/Drainer above. The
// zero value is not usable — construct via NewConnection.
//
// A *Connection is safe for concurrent use: mu guards every field this
// type owns. Like tierQueue/QueuedTransport (queue.go) and
// checkpoint.Manager/save.Manager, Connection carries both a
// sync.Mutex VALUE and aliasable reference fields (hooks, queue, backoff
// are all interfaces/funcs a struct copy would alias while gaining an
// independent, non-exclusive mutex) — the same SEC-020-class "two locks,
// one referent" hazard, guarded the same way: checkNotCopied, called
// before mu is ever touched, at every real entry point.
type Connection struct {
	mu sync.Mutex

	state      ConnState
	retries    int64
	maxRetries int64
	backoff    Backoff
	hooks      ReconnectHooks
	queue      Drainer

	correlationID string

	// self is the SEC-020-class copy-identity guard — same pattern and
	// rationale as tierQueue.self/QueuedTransport.self (queue.go) and
	// checkpoint.Manager.self/save.Manager.self.
	self atomic.Pointer[Connection]
}

// NewConnection constructs a *Connection in StateConnected (a fresh
// connection starts trusted-good, exactly like QueuedTransport starting
// with empty tiers — there is nothing to retry or catch up on yet).
func NewConnection(cfg ConnectionConfig) *Connection {
	backoff := cfg.Backoff
	if backoff == nil {
		backoff = DefaultBackoff
	}
	hooks := cfg.Hooks
	if hooks == nil {
		hooks = LocalReconnectHooks{}
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	c := &Connection{
		state:         StateConnected,
		maxRetries:    maxRetries,
		backoff:       backoff,
		hooks:         hooks,
		queue:         cfg.Queue,
		correlationID: cfg.CorrelationID,
	}
	// Stored once, here, before c is returned to any caller — mirrors
	// NewQueuedTransport/newTierQueue's self.Store timing exactly (see
	// QueuedTransport.self's doc comment for why that ordering matters).
	c.self.Store(c)
	return c
}

// checkNotCopied mirrors tierQueue.checkNotCopied/QueuedTransport.
// checkNotCopied exactly: a lock-free identity check, safe to call
// before c.mu is ever touched (SEC-016 pre-lock ordering).
func (c *Connection) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return errs.New(ErrConnectionCopied, c.correlationID, map[string]any{"method": method})
	}
	return nil
}

// State reports the connection's current state.
func (c *Connection) State() ConnState {
	if err := c.checkNotCopied("State"); err != nil {
		// Fail conservative: a copied handle is an invalid state; report
		// Retrying (never Connected) so a caller checking "am I good to
		// go" never mistakes a copy-guard rejection for the all-clear.
		return StateRetrying
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Retries reports the current retry counter (reset to 0 on every
// successful Attempt or Reconnect).
func (c *Connection) Retries() int64 {
	if err := c.checkNotCopied("Retries"); err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retries
}

// NextBackoff reports the logical backoff units a caller should let pass
// before its next Attempt, given the CURRENT retry counter (i.e. the
// delay associated with the retry that already happened, not a
// speculative next one) — 0 while Connected (nothing to back off from).
func (c *Connection) NextBackoff() int64 {
	if err := c.checkNotCopied("NextBackoff"); err != nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nextBackoffLocked()
}

func (c *Connection) nextBackoffLocked() int64 {
	if c.retries == 0 {
		return 0
	}
	return c.backoff(c.retries)
}

// Attempt runs op exactly once and evolves the state machine
// deterministically from the result (proposal §1 point 5's "on a failed
// operation... retry with logical backoff"):
//
//   - op() == nil: state -> Connected, retries reset to 0, returns nil.
//   - op() != nil, retries (after incrementing) <= maxRetries: state ->
//     Retrying, returns op's error UNCHANGED (a transient failure still
//     within budget — the caller is expected to call Attempt again,
//     after NextBackoff's reported delay, or call Reconnect directly).
//   - op() != nil, retries (after incrementing) > maxRetries: state ->
//     Retrying still (Attempt never auto-escalates to Reconnecting on
//     its own — see ErrRetriesExhausted's doc comment), but returns
//     ErrRetriesExhausted (a registry error, GR#7) wrapping op's error
//     instead of op's error bare, so the caller can distinguish "still
//     within budget, try again" from "budget is gone, decide what to do
//     next" without inspecting the retry counter itself.
//
// op is called with NO lock held — Attempt's own state mutation happens
// only in the critical section AFTER op returns — so a slow or blocking
// op never stalls any other Connection method (mirrors tierQueue.
// sendOrEnqueue's documented non-blocking-inner-call assumption, except
// here op is explicitly ALLOWED to block/be slow, since unlike
// SendCommand's inner transport this is not assumed non-blocking by
// contract).
func (c *Connection) Attempt(op func() error) error {
	if err := c.checkNotCopied("Attempt"); err != nil {
		return err
	}

	opErr := op()

	c.mu.Lock()
	defer c.mu.Unlock()

	if opErr == nil {
		c.state = StateConnected
		c.retries = 0
		return nil
	}

	c.retries++
	c.state = StateRetrying
	if c.retries > c.maxRetries {
		return errs.Wrap(ErrRetriesExhausted, c.correlationID, opErr, map[string]any{
			"retries":    c.retries,
			"maxRetries": c.maxRetries,
		})
	}
	return opErr
}

// Reconnect re-establishes the connection (proposal §1 point 5's
// "reconnect: re-establish the connection, including re-authentication
// and name lookup"), then catches up any backlog (proposal's "catch-up:
// after reconnect, replay the pending/persisted commands FIFO... from
// the last acknowledged point, exactly-once, in order"):
//
//  1. state -> Reconnecting; call hooks.Lookup(name) then hooks.
//     Authenticate(). Either failing returns the connection to
//     StateRetrying (retries incremented, same accounting Attempt uses)
//     and returns ErrReconnectFailed wrapping the hook's error.
//  2. Both succeeding: state -> CatchingUp; if a Drainer is configured
//     (ConnectionConfig.Queue), call its Drain(0) — unlimited budget,
//     drain everything CURRENTLY pending, FIFO, exactly-once (Drainer's
//     doc comment) — exactly once. A Drain error also falls back to
//     StateRetrying + ErrReconnectFailed, same shape as step 1's
//     failures — a half-caught-up connection must never be reported as
//     Connected.
//  3. Drain succeeding (or no Drainer configured at all): state ->
//     Connected, retries reset to 0. Returns the DrainStats from step 2
//     (zero value if no Drainer was configured).
func (c *Connection) Reconnect(name string) (DrainStats, error) {
	if err := c.checkNotCopied("Reconnect"); err != nil {
		return DrainStats{}, err
	}

	c.mu.Lock()
	c.state = StateReconnecting
	hooks := c.hooks
	queue := c.queue
	corrID := c.correlationID
	c.mu.Unlock()

	addr, err := hooks.Lookup(corrID, name)
	if err != nil {
		return DrainStats{}, c.failReconnect("lookup", name, err)
	}
	if err := hooks.Authenticate(corrID); err != nil {
		return DrainStats{}, c.failReconnect("authenticate", addr, err)
	}

	c.mu.Lock()
	c.state = StateCatchingUp
	c.mu.Unlock()

	var stats DrainStats
	if queue != nil {
		stats, err = queue.Drain(0)
		if err != nil {
			return stats, c.failReconnect("catchup", addr, err)
		}
	}

	c.mu.Lock()
	c.state = StateConnected
	c.retries = 0
	c.mu.Unlock()
	return stats, nil
}

// failReconnect is Reconnect's shared failure path: falls the connection
// back to StateRetrying with its retry counter incremented (the same
// accounting Attempt's failure path uses, so a caller mixing Attempt and
// Reconnect calls sees one consistent retry budget), and wraps cause as
// ErrReconnectFailed with phase/target context.
func (c *Connection) failReconnect(phase, target string, cause error) error {
	c.mu.Lock()
	c.state = StateRetrying
	c.retries++
	c.mu.Unlock()
	return errs.Wrap(ErrReconnectFailed, c.correlationID, cause, map[string]any{
		"phase":  phase,
		"target": target,
	})
}
