package integration

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// scriptedOp returns an op() func that fails for the first failCount
// calls and succeeds thereafter — deterministic given the call count
// alone (no wall-clock, no randomness), so re-running the exact same
// script against a fresh Connection always produces the exact same
// transition sequence.
func scriptedOp(failCount int) func() error {
	calls := 0
	return func() error {
		calls++
		if calls <= failCount {
			return errors.New("scripted transient failure")
		}
		return nil
	}
}

// (a) Retry with logical backoff is deterministic, caps, and raises a
// registry error on exhaustion.
func TestConnection_Attempt_RetryCapsAndExhausts(t *testing.T) {
	c := NewConnection(ConnectionConfig{MaxRetries: 3, CorrelationID: "corr-retry"})

	failingOp := func() error { return errors.New("always fails") }

	var lastBackoffs []int64
	for i := 1; i <= 3; i++ {
		err := c.Attempt(failingOp)
		if err == nil {
			t.Fatalf("attempt %d: expected an error, got nil", i)
		}
		if c.State() != StateRetrying {
			t.Fatalf("attempt %d: state = %v, want Retrying", i, c.State())
		}
		if c.Retries() != int64(i) {
			t.Fatalf("attempt %d: retries = %d, want %d", i, c.Retries(), i)
		}
		lastBackoffs = append(lastBackoffs, c.NextBackoff())
	}

	// Backoff must be a deterministic, strictly non-decreasing function
	// of the retry counter alone (DefaultBackoff: 1, 2, 4, ...).
	want := []int64{1, 2, 4}
	for i, b := range lastBackoffs {
		if b != want[i] {
			t.Fatalf("backoff[%d] = %d, want %d (sequence: %v)", i, b, want[i], lastBackoffs)
		}
	}

	// The 4th attempt exceeds MaxRetries=3 -> ErrRetriesExhausted, a
	// registry error (GR#7), and the connection stays Retrying (Attempt
	// never auto-escalates to Reconnecting).
	err := c.Attempt(failingOp)
	if err == nil {
		t.Fatal("expected ErrRetriesExhausted on the 4th attempt, got nil")
	}
	if !containsCode(err, ErrRetriesExhausted) {
		t.Fatalf("expected error to carry code %s, got: %v", ErrRetriesExhausted, err)
	}
	if c.State() != StateRetrying {
		t.Fatalf("state after exhaustion = %v, want Retrying", c.State())
	}
	if c.Retries() != 4 {
		t.Fatalf("retries after exhaustion = %d, want 4", c.Retries())
	}

	// A subsequent SUCCESSFUL attempt resets everything to Connected/0 —
	// exhaustion is not a permanent trap.
	if err := c.Attempt(func() error { return nil }); err != nil {
		t.Fatalf("recovery attempt: unexpected error: %v", err)
	}
	if c.State() != StateConnected {
		t.Fatalf("state after recovery = %v, want Connected", c.State())
	}
	if c.Retries() != 0 {
		t.Fatalf("retries after recovery = %d, want 0", c.Retries())
	}
	if c.NextBackoff() != 0 {
		t.Fatalf("backoff after recovery = %d, want 0 (nothing to back off from)", c.NextBackoff())
	}
}

// containsCode reports whether err's message mentions code — errs.E's
// Error() rendering includes the code, and this package's other tests
// (queue_test.go) don't already expose a typed accessor, so a substring
// check matches the existing test style in this package.
func containsCode(err error, code string) bool {
	return err != nil && contains(err.Error(), code)
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestConnection_Attempt_SuccessResetsRetries proves a successful
// Attempt at ANY point in a retry sequence fully resets the counter —
// not just the exhaustion-then-recover case above, but the ordinary
// "flaky then fine" case.
func TestConnection_Attempt_SuccessResetsRetries(t *testing.T) {
	c := NewConnection(ConnectionConfig{MaxRetries: 5, CorrelationID: "corr-reset"})

	op := scriptedOp(2) // fails twice, then succeeds
	if err := c.Attempt(op); err == nil {
		t.Fatal("attempt 1: expected failure")
	}
	if err := c.Attempt(op); err == nil {
		t.Fatal("attempt 2: expected failure")
	}
	if c.Retries() != 2 {
		t.Fatalf("retries = %d, want 2", c.Retries())
	}
	if err := c.Attempt(op); err != nil {
		t.Fatalf("attempt 3: expected success, got: %v", err)
	}
	if c.State() != StateConnected || c.Retries() != 0 {
		t.Fatalf("state=%v retries=%d, want Connected/0", c.State(), c.Retries())
	}
}

// (b) Reconnect is a no-op locally (LocalReconnectHooks) and
// deterministic: Lookup/Authenticate always succeed, the state
// transitions Reconnecting -> CatchingUp -> Connected, and (with no
// Queue configured) DrainStats comes back zero-valued.
func TestConnection_Reconnect_LocalNoOp(t *testing.T) {
	c := NewConnection(ConnectionConfig{CorrelationID: "corr-reconnect-local"})

	stats, err := c.Reconnect("some-integration")
	if err != nil {
		t.Fatalf("Reconnect: unexpected error: %v", err)
	}
	if stats.Total() != 0 {
		t.Fatalf("stats = %+v, want zero (no Queue configured)", stats)
	}
	if c.State() != StateConnected {
		t.Fatalf("state = %v, want Connected", c.State())
	}
	if c.Retries() != 0 {
		t.Fatalf("retries = %d, want 0", c.Retries())
	}
}

// failingHooks lets a test force Lookup or Authenticate to fail
// deterministically (never based on timing) to exercise Reconnect's
// failure path.
type failingHooks struct {
	failLookup       bool
	failAuthenticate bool
}

func (h failingHooks) Authenticate(string) error {
	if h.failAuthenticate {
		return errors.New("scripted auth failure")
	}
	return nil
}

func (h failingHooks) Lookup(_ string, name string) (string, error) {
	if h.failLookup {
		return "", errors.New("scripted lookup failure")
	}
	return name, nil
}

func TestConnection_Reconnect_LookupFailure_FallsBackToRetrying(t *testing.T) {
	c := NewConnection(ConnectionConfig{
		CorrelationID: "corr-reconnect-lookup-fail",
		Hooks:         failingHooks{failLookup: true},
	})

	_, err := c.Reconnect("target")
	if err == nil {
		t.Fatal("expected an error from Reconnect")
	}
	if !containsCode(err, ErrReconnectFailed) {
		t.Fatalf("expected error to carry code %s, got: %v", ErrReconnectFailed, err)
	}
	if c.State() != StateRetrying {
		t.Fatalf("state = %v, want Retrying", c.State())
	}
	if c.Retries() != 1 {
		t.Fatalf("retries = %d, want 1", c.Retries())
	}
}

func TestConnection_Reconnect_AuthenticateFailure_FallsBackToRetrying(t *testing.T) {
	c := NewConnection(ConnectionConfig{
		CorrelationID: "corr-reconnect-auth-fail",
		Hooks:         failingHooks{failAuthenticate: true},
	})

	_, err := c.Reconnect("target")
	if err == nil {
		t.Fatal("expected an error from Reconnect")
	}
	if !containsCode(err, ErrReconnectFailed) {
		t.Fatalf("expected error to carry code %s, got: %v", ErrReconnectFailed, err)
	}
	if c.State() != StateRetrying {
		t.Fatalf("state = %v, want Retrying", c.State())
	}
}

// (c) Catch-up replays FIFO, exactly once, after a simulated disconnect:
// a QueuedTransport accumulates a backlog (its inner transport is
// "down"), then Reconnect's catch-up phase drains it, in arrival order,
// with nothing lost and nothing delivered twice.
func TestConnection_Reconnect_CatchUp_DrainsBacklogFIFOExactlyOnce(t *testing.T) {
	inner := newMockTransport()
	inner.SetFull(true) // simulate "disconnected": every send queues instead of delivering

	q := NewQueuedTransport(inner, Config{DiskRoot: t.TempDir(), T1MemCap: 4})

	const n = 10
	for i := 0; i < n; i++ {
		if err := q.SendCommand(buyCmd("corr-catchup", i)); err != nil {
			t.Fatalf("SendCommand(%d): %v", i, err)
		}
	}
	if got := q.Depth().T1; got != n {
		t.Fatalf("backlog depth = %d, want %d before reconnect", got, n)
	}

	// "Reconnect": the inner transport comes back up, then catch-up
	// drains the backlog.
	inner.SetFull(false)
	c := NewConnection(ConnectionConfig{CorrelationID: "corr-catchup", Queue: q})

	stats, err := c.Reconnect("engine")
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if stats.Total() != n {
		t.Fatalf("drained %d commands, want %d", stats.Total(), n)
	}
	if c.State() != StateConnected {
		t.Fatalf("state = %v, want Connected", c.State())
	}
	if got := q.Depth().T1; got != 0 {
		t.Fatalf("backlog depth after catch-up = %d, want 0", got)
	}

	received := inner.Received()
	if len(received) != n {
		t.Fatalf("inner received %d commands, want %d (exactly-once delivery)", len(received), n)
	}
	for i, cmd := range received {
		want := buyCmd("corr-catchup", i)
		if cmd.Payload.(protocol.BuyPayload).Cell.X != want.Payload.(protocol.BuyPayload).Cell.X {
			t.Fatalf("received[%d] = %+v, want cell.X=%d (FIFO order violated)", i, cmd, i)
		}
	}
}

// (f) Determinism: identical failure-injection sequence -> identical
// outcome. Runs the SAME scripted sequence of op() results through two
// independently constructed Connections and asserts every recorded
// (state, retries, backoff, error-is-nil) tuple matches exactly. Run
// under `go test -race -count=2` per the verify step; this test's own
// body additionally re-runs the script twice in-process for a second,
// stronger check that does not depend on the test binary being invoked
// with -count=2.
func TestConnection_Determinism_IdenticalFailureInjectionIdenticalOutcome(t *testing.T) {
	// failAt marks which of 8 scripted attempts fail (true) vs succeed
	// (false) — an arbitrary but FIXED pattern, replayed twice.
	script := []bool{true, true, false, true, true, true, false, true}

	type transition struct {
		state   ConnState
		retries int64
		backoff int64
		failed  bool
	}

	run := func() []transition {
		c := NewConnection(ConnectionConfig{MaxRetries: 10, CorrelationID: "corr-determinism"})
		var out []transition
		for _, shouldFail := range script {
			err := c.Attempt(func() error {
				if shouldFail {
					return errors.New("scripted failure")
				}
				return nil
			})
			out = append(out, transition{
				state:   c.State(),
				retries: c.Retries(),
				backoff: c.NextBackoff(),
				failed:  err != nil,
			})
		}
		return out
	}

	first := run()
	second := run()

	if len(first) != len(second) {
		t.Fatalf("transition counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("transition %d diverged between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// connectionByteCopy performs the SEC-020-class attack — a plain
// Connection struct copy — via a raw byte-for-byte memcpy through
// unsafe.Pointer, mirroring save.Manager's managerByteCopy
// (internal/engine/save/copyguard_test.go): a literal `c2 := *c` is
// legal, unsafe-free Go from outside this package too, but go vet's
// copylocks check statically flags it, which this project's VERIFY step
// requires every package to pass. The byte-level copy produces identical
// runtime semantics (mu's bytes copied as-is, hooks/queue/backoff's
// interface/func headers copied — aliasing the same underlying values,
// self's pointer bytes copied unchanged) without a statically-flaggable
// copy expression.
func connectionByteCopy(c *Connection) *Connection {
	cp := new(Connection)
	*(*[unsafe.Sizeof(Connection{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(Connection{})]byte)(unsafe.Pointer(c))
	return cp
}

// TestConnection_CopyGuard proves Connection's SEC-020-class copy guard
// rejects a struct copy the same way tierQueue/QueuedTransport's do
// (queue.go's checkNotCopied).
func TestConnection_CopyGuard(t *testing.T) {
	c := NewConnection(ConnectionConfig{CorrelationID: "corr-copy"})
	cp := connectionByteCopy(c)

	err := cp.Attempt(func() error { return nil })
	if err == nil {
		t.Fatal("expected Attempt on a copied Connection to fail")
	}
	if !containsCode(err, ErrConnectionCopied) {
		t.Fatalf("expected copy-guard error to carry code %s, got: %v", ErrConnectionCopied, err)
	}
	if _, err := cp.Reconnect("x"); err == nil {
		t.Fatal("expected Reconnect on a copied Connection to fail")
	}
	if cp.State() != StateRetrying {
		t.Fatalf("State() on a copied Connection = %v, want the fail-conservative StateRetrying", cp.State())
	}
	if cp.Retries() != 0 {
		t.Fatalf("Retries() on a copied Connection = %d, want 0", cp.Retries())
	}
	if cp.NextBackoff() != 0 {
		t.Fatalf("NextBackoff() on a copied Connection = %d, want 0", cp.NextBackoff())
	}
}
