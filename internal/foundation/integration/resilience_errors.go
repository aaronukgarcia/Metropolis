package integration

// Registry error codes for foundation.integration's resilience layer
// (FEAT-189 increment 3: the Connected/Retrying/CatchingUp/Reconnecting
// state machine, resilience.go). Range: F900-F919 (see queue_errors.go's
// header comment for the full claim story); increment 2 used F900-F904,
// so this file claims F905-F907. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-F9" internal/ cmd/` before
// claiming (BUG-008's lesson) — F905-F907 were unclaimed.
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrRetriesExhausted: Connection.Attempt's retry counter exceeded
	// its configured cap (ConnectionConfig.MaxRetries) without op ever
	// succeeding. Raised explicitly rather than looping forever or
	// silently giving up — proposal §1 point 5's "supports retry... cap
	// retries" and GR#7. The caller must explicitly call Reconnect to
	// move past this state; Attempt never auto-escalates to reconnect on
	// its own (that would hide the exhaustion from the caller, who may
	// want to apply its OWN policy — e.g. surface to a monitoring
	// dashboard, a later increment — before reconnecting).
	ErrRetriesExhausted = "MET-F905"

	// ErrReconnectFailed: Connection.Reconnect's name-lookup,
	// re-authentication, or post-reconnect catch-up drain failed. The
	// connection falls back to StateRetrying (with its retry counter
	// incremented) rather than being left in StateReconnecting/
	// StateCatchingUp indefinitely — a stuck non-terminal state would be
	// a silent hang, not a registry error (GR#17).
	ErrReconnectFailed = "MET-F906"

	// ErrConnectionCopied: a *Connection method was called on a struct
	// copy of the value NewConnection returned — SEC-020-class guard,
	// mirrors QueuedTransport.checkNotCopied/tierQueue.checkNotCopied
	// exactly (queue.go): Connection carries both a sync.Mutex VALUE and
	// aliasable reference fields (hooks, queue, backoff are all
	// interfaces/funcs), the same "two locks, one referent" shape.
	ErrConnectionCopied = "MET-F907"
)
