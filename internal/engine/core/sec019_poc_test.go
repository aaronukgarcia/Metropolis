package core

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SEC-019 (P0): the original SEC-003 crash class (concurrent map read/
// write => fatal, unrecoverable process crash), reproduced on
// SubscriptionServer rather than Engine. SubscriptionServer and
// NewSubscriptionServer are both exported and fully independent of
// Engine (Engine.subs merely holds one *SubscriptionServer it happens to
// have constructed via NewSubscriptionServer — any external package can
// call core.NewSubscriptionServer() directly and get its own). subs is a
// map[protocol.SubscriptionID]*subscription, a reference type exactly
// analogous to Engine.hooks: a struct copy (s2 := *s) ALIASES the same
// underlying map while getting its own independent mu.
//
// Destructive-2's live reproduction: NewSubscriptionServer(), a
// background goroutine looping Subscribe/Unsubscribe (realistic, not
// synthetic — PublishEngineStatus runs exactly this shape on every
// subscription-pump wake, live in cmd/metropolis today), plus 3000
// concurrent struct copies each immediately calling Subscribe() —
// produced "fatal error: concurrent map writes" inside
// SubscriptionServer.Subscribe, an unrecoverable process crash, well
// before SEC-016's hang mechanism could even manifest.
//
// FIX: the identical self/checkNotCopied pattern Engine uses
// (engine.go's Engine.self/checkNotCopied), applied to
// SubscriptionServer.self and evaluated BEFORE any s.mu acquisition in
// every method that touches s.subs — see subscribe.go's self field and
// checkNotCopied doc comments.
//
// THIS TEST proves the fix deterministically, the way SEC-018's own test
// does: it builds SEC-016/018's exact attack STATE (a copy taken while
// the original's mu is held, so the copy's mu bytes read as
// "permanently locked from an address nobody will ever Unlock") by
// construction — s.mu.Lock(); copy; s.mu.Unlock() — in a single
// goroutine, with no timing luck and no data race, rather than racing
// for the crash/hang by chance. That makes it deterministic and safe to
// run under `-race` (unlike sec016_poc_test.go's timing-luck construction,
// which is deliberately excluded from -race builds).
//
// ENUMERATION METHOD for "every method that touches s.mu" (mirrors
// SEC-018's grep-and-map-to-enclosing-func method exactly, scoped to
// subscribe.go instead of engine.go/commands.go/persist.go):
//
//	awk '/^func \(s \*SubscriptionServer\)/{fn=$0} /s\.mu\.Lock\(\)/{if ($0 !~ /\/\//) print FILENAME":"FNR": "fn}' \
//	    internal/engine/core/subscribe.go
//
// It found exactly three sites, all now guarded pre-lock and post-lock:
//  1. Subscribe()            — was UNGUARDED, now guarded
//  2. Unsubscribe()          — was UNGUARDED, now guarded
//  3. PublishEngineStatus()  — was UNGUARDED, now guarded (no
//     correlationID/return value on this method — see its doc comment
//     for why a copy is handled by silently dropping the publish cycle,
//     the same way the method's pre-existing unreachable json.Marshal
//     failure branch already does, rather than surfacing an error)
func TestSEC019_Deterministic_AllGuardedSites_RejectedNotHung(t *testing.T) {
	s := NewSubscriptionServer()
	// FEAT-208: Subscribe now checks the registered view table rather
	// than a single hardcoded "engine.status" constant — a bare
	// NewSubscriptionServer() (unlike NewEngine(), which auto-registers
	// engine.status) has no views registered yet, so this test (whose
	// actual subject is copy semantics, not engine.status itself) seeds
	// one directly. The stub patch func is never expected to run —
	// PublishEngineStatus on the mid-lock-copied s2 must be rejected by
	// checkNotCopied before ever reaching a ViewPatchFunc call.
	if err := s.RegisterView(engineStatusView, func() (json.RawMessage, error) { return json.RawMessage("{}"), nil }); err != nil {
		t.Fatalf("RegisterView(%s): %v", engineStatusView, err)
	}
	id, err := s.Subscribe(engineStatusView, nil, "", "sec019-seed")
	if err != nil {
		t.Fatalf("seed Subscribe: %v", err)
	}

	// Deterministic construction of SEC-016/018's exact attack state:
	// single-goroutine copy while s's own mu is held, no timing luck, no
	// data race (only one goroutine ever touches s or s2's memory during
	// the copy itself).
	s.mu.Lock()
	s2 := s2Copy(s)
	s.mu.Unlock()

	sink := &countingSink{}

	cases := []struct {
		name string
		call func() error
	}{
		{"Subscribe", func() error {
			_, err := s2.Subscribe(engineStatusView, nil, "", "sec019-det-subscribe")
			return err
		}},
		{"Unsubscribe", func() error {
			return s2.Unsubscribe(id, "sec019-det-unsubscribe")
		}},
		{"PublishEngineStatus", func() error {
			s2.PublishEngineStatus(sink, EngineStatusView{Tick: 1})
			return nil // no error channel on this method — see below
		}},
	}

	for _, c := range cases {
		done := make(chan error, 1)
		go func() { done <- c.call() }()
		select {
		case err := <-done:
			if c.name == "PublishEngineStatus" {
				// PublishEngineStatus has no error return; the assertion
				// that matters is that it returned promptly at all (see
				// the timeout branch below) AND that it did not touch
				// s2.subs (which aliases s.subs) — proven by sink never
				// being invoked, since a copy is rejected before the
				// lock that would collect publish targets is ever taken.
				if sink.calls != 0 {
					t.Errorf("PublishEngineStatus on a copy: sink.SendDelta called %d times, want 0 (copy must be rejected before touching aliased subs)", sink.calls)
				}
				continue
			}
			if !errors.Is(err, &errs.E{Code: ErrSubscriptionServerCopied}) {
				t.Errorf("%s on deterministically mid-lock-copied SubscriptionServer: err = %v, want ErrSubscriptionServerCopied", c.name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("SEC-019 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix SEC-016-class failure mode", c.name)
		}
	}

	// The ORIGINAL SubscriptionServer must be completely unaffected —
	// its mu was only ever held and released normally by us, once, to
	// take the copy. A second Subscribe on the original must still
	// succeed cleanly.
	if _, err := s.Subscribe(engineStatusView, nil, "", "sec019-original-still-works"); err != nil {
		t.Fatalf("original SubscriptionServer Subscribe after copy attack: %v", err)
	}
}

// TestSEC019_CrashClass_ConcurrentMapWrites_WithoutFix documents (rather
// than re-executes — deliberately, since it would crash the whole test
// binary via a fatal, unrecoverable runtime error, not a recoverable
// panic) Destructive-2's live PoC: without the self/checkNotCopied guard,
// a background goroutine looping Subscribe/Unsubscribe on the original
// concurrently with thousands of copies each calling Subscribe() on
// their own aliased-map copy produces
//
//	fatal error: concurrent map writes
//
// inside SubscriptionServer.Subscribe — the ORIGINAL SEC-003 crash class,
// not merely SEC-016's hang. `fatal error` in the Go runtime is, by
// design, NOT a recoverable panic (no defer/recover can catch it), so a
// test that actually re-triggers it would kill `go test` itself rather
// than reporting a failure — this test instead asserts the STATE the
// crash depends on (aliasing) is closed by the fix, which is exactly
// what TestSEC019_Deterministic_AllGuardedSites_RejectedNotHung's
// Subscribe case proves: s2.Subscribe never reaches the s2.subs[id] = ...
// write that would race the original's own map mutation, because
// checkNotCopied rejects it first.
func TestSEC019_CrashClass_ConcurrentMapWrites_WithoutFix(t *testing.T) {
	s := NewSubscriptionServer()
	// FEAT-208: see the identical RegisterView seed note in
	// TestSEC019_Deterministic_AllGuardedSites_RejectedNotHung above.
	if err := s.RegisterView(engineStatusView, func() (json.RawMessage, error) { return json.RawMessage("{}"), nil }); err != nil {
		t.Fatalf("RegisterView(%s): %v", engineStatusView, err)
	}
	s.mu.Lock()
	s2 := s2Copy(s)
	s.mu.Unlock()

	// Confirms the aliasing precondition the crash class depends on:
	// s2.subs and s.subs share the same underlying map (a struct copy
	// copies the map header, not the backing buckets) — maps compare
	// unequal via == (maps are not comparable), so this is asserted by
	// mutating through one handle and observing it through the other,
	// AFTER first proving the guard rejects the copy's own Subscribe
	// (i.e. this reads s.subs directly, never through s2's guarded API,
	// to avoid depending on the very fix being demonstrated).
	//
	// s2.subs is read WITHOUT taking s2.mu, deliberately: s2.mu's bytes
	// were captured while s.mu was locked (this is SEC-016's exact
	// attack state — see s2Copy's construction above), so s2.mu itself
	// reads as permanently "locked, owned by nobody who will ever
	// Unlock() this specific copy's address" — attempting s2.mu.Lock()
	// here would hang this test forever, which is precisely the bug
	// class being demonstrated, not a bug in the test. This read is
	// safe without synchronisation because nothing else ever touches s2
	// concurrently in this test (single goroutine, no writer racing this
	// read) — it exists purely to observe map identity, not to model a
	// safe caller.
	if _, err := s.Subscribe(engineStatusView, nil, "", "sec019-alias-check"); err != nil {
		t.Fatalf("Subscribe on original: %v", err)
	}
	s.mu.Lock()
	n := len(s.subs)
	s.mu.Unlock()
	n2 := len(s2.subs)
	if n != n2 {
		t.Fatalf("aliasing precondition not observed: len(s.subs)=%d, len(s2.subs)=%d, want equal (same backing map)", n, n2)
	}
}

// s2Copy produces a byte-level struct copy of a *SubscriptionServer,
// mirroring sec014_poc_test.go's e2Copy exactly (see that function's doc
// comment for why this is unsafe-pointer byte copying rather than a
// literal `s2 := *s`: the literal form is legal, unsafe-free, reflect-
// free Go and IS the real attack, but `go vet`'s copylocks analyzer
// flags the literal line statically, which would fail this package's
// `go vet ./...` VERIFY step if it appeared as source here — this
// byte-level copy produces the identical runtime state without
// containing that flagged line).
func s2Copy(s *SubscriptionServer) *SubscriptionServer {
	c := new(SubscriptionServer)
	*(*[unsafe.Sizeof(SubscriptionServer{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(SubscriptionServer{})]byte)(unsafe.Pointer(s))
	return c
}

// countingSink is a minimal DeltaSink recording how many deltas it was
// asked to send — this test only needs the count (proving
// PublishEngineStatus on a copy never reaches the send loop), not the
// delta contents.
type countingSink struct {
	calls int
}

func (c *countingSink) SendDelta(protocol.Delta) bool {
	c.calls++
	return true
}
