package stub

import (
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SEC-020 wave 2 (StubEngine copy guard). Mirrors the enumeration and
// test-construction discipline established in engine.core's
// sec014_poc_test.go / sec016_zerovalue_test.go / sec018_poc_test.go and
// internal/protocol's sec020_test.go — read those first; this file
// deliberately does not invent a new technique.
//
// ENUMERATION METHOD (Bill's ask, Weakness pattern #3 — "state your
// method"): grep for the literal call `s.mu.Lock()` across every
// non-test .go file in this package and map each match back to its
// enclosing `func (s *StubEngine) ...` by source position, the same awk
// pass SEC-018/SEC-019 used:
//
//	awk '/^func \(s \*StubEngine\)/{fn=$0} /s\.mu\.Lock\(\)/{if ($0 !~ /\/\//) print FILENAME":"FNR": "fn}' \
//	    internal/engine/stub/engine.go
//
// then cross-checked by listing every exported AND unexported method on
// *StubEngine (`grep -n "func (s \*StubEngine)" internal/engine/stub/*.go`)
// and accounting for each one that was NOT a s.mu.Lock() site. That
// found exactly:
//
//  1. engine.go Tick()               — s.mu.Lock() site, guarded
//  2. engine.go handleAdvanceTicks() — s.mu.Lock() site, guarded
//  3. engine.go handleSetSpeed()     — s.mu.Lock() site, guarded
//  4. engine.go handlePause()        — s.mu.Lock() site, guarded
//  5. engine.go handleResume()       — s.mu.Lock() site, guarded
//  6. engine.go handleSubscribe()    — s.mu.Lock() site, guarded
//  7. engine.go handleUnsubscribe()  — s.mu.Lock() site, guarded
//
// plus one non-s.mu choke point added ahead of ALL of the above:
//
//   - engine.go handle()             — the dispatcher every Command
//     passes through before reaching any of the seven sites above
//     (mirrors engine.core.HandleCommand's entry check); guarded so a
//     copy is rejected before the switch even runs, not just before its
//     eventual s.mu.Lock().
//
// Two methods were found and deliberately left UNGUARDED, each
// justified individually rather than blanket-copied from a sibling
// package's posture (Bill's ask: "think about what the right analogue
// is here rather than copying it blindly"):
//
//   - World() — a plain, never-reassigned *World pointer to fixture
//     data that is never mutated post-construction; see World()'s own
//     doc comment in engine.go for the full reasoning (the right
//     analogue is engine.core's Registry()/WorldSeed()/PoolSize(), not
//     InProcTransport's torn-down-on-Close channel accessors).
//   - advanceSubscriptionScriptLocked/emitDeltaLocked — unexported,
//     called only from handleAdvanceTicks/handleSubscribe while s.mu is
//     ALREADY held by a caller that has already passed both its
//     pre-lock and post-lock checks; adding a third check here would
//     cost an atomic load per delta for zero additional safety, the
//     same judgement engine.core's advanceOneDailyTick doc comment
//     makes for the identical shape.
//
// Run() itself touches no s.mu and reaches s.mu only via handle(), so it
// is covered transitively and carries no check of its own.
//
// TestStubEngine_Copy_Deterministic_AllGuardedSites_RejectedNotHung
// proves all seven s.mu sites plus the handle() choke point are safe
// using a DETERMINISTIC construction of the attack state (per the
// v1.7.2-era "concurrency tests must be deterministic, not probable"
// rule) — no timing-luck hammer:
//
//	s.mu.Lock()          // single goroutine, no race with anything
//	s2 := copyStubEngine(s) // copy while WE hold the lock
//	s.mu.Unlock()
//	// s2.mu now byte-identically represents "locked, owned by nobody
//	// who will ever unlock THIS copy's address" — the exact attack
//	// state SEC-016 described for Engine, reproduced deterministically
//	// here for StubEngine.
func TestStubEngine_Copy_Deterministic_AllGuardedSites_RejectedNotHung(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
	t.Cleanup(func() { _ = tr.Close() })

	s, err := NewStubEngine(tr)
	if err != nil {
		t.Fatalf("NewStubEngine: %v", err)
	}

	// Deterministic construction of the attack state: single-goroutine
	// copy while s's own mu is held, no timing luck, no data race (only
	// one goroutine ever touches s or s2's memory during the copy).
	s.mu.Lock()
	s2 := copyStubEngine(s)
	s.mu.Unlock()

	corrID := protocol.NewCorrelationID()
	advanceCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}}
	setSpeedCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindSetSpeed, Payload: protocol.SetSpeedPayload{Speed: 2}}
	pauseCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	resumeCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindResume, Payload: protocol.ResumePayload{}}
	subCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindSubscribe, Payload: protocol.SubscribePayload{ViewName: viewportViewName}}
	unsubCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindUnsubscribe, Payload: protocol.UnsubscribePayload{SubscriptionID: "sub-1"}}

	cases := []struct {
		name string
		call func() protocol.CommandResult
	}{
		{"handleAdvanceTicks", func() protocol.CommandResult { return s2.handleAdvanceTicks(advanceCmd) }},
		{"handleSetSpeed", func() protocol.CommandResult { return s2.handleSetSpeed(setSpeedCmd) }},
		{"handlePause", func() protocol.CommandResult { return s2.handlePause(pauseCmd) }},
		{"handleResume", func() protocol.CommandResult { return s2.handleResume(resumeCmd) }},
		{"handleSubscribe", func() protocol.CommandResult { return s2.handleSubscribe(subCmd) }},
		{"handleUnsubscribe", func() protocol.CommandResult { return s2.handleUnsubscribe(unsubCmd) }},
	}

	for _, c := range cases {
		done := make(chan protocol.CommandResult, 1)
		go func() { done <- c.call() }()
		select {
		case res := <-done:
			if res.Accepted {
				t.Errorf("%s on a mid-lock-copied StubEngine: Accepted = true, want a rejected result carrying codeStubEngineCopied", c.name)
				continue
			}
			if res.Error == nil || res.Error.Code != codeStubEngineCopied {
				t.Errorf("%s on a mid-lock-copied StubEngine: Error = %v, want code %s", c.name, res.Error, codeStubEngineCopied)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", c.name)
		}
	}

	// Tick() has no error return; assert it resolves promptly to the
	// zero value instead of hanging.
	tickDone := make(chan protocol.Tick, 1)
	go func() { tickDone <- s2.Tick() }()
	select {
	case got := <-tickDone:
		if got != 0 {
			t.Errorf("s2.Tick() on a copy = %d, want 0", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SEC-020 REGRESSION: Tick() on a mid-lock-copied StubEngine hung")
	}

	// The dispatcher choke point (handle()) itself, exercised through
	// the real transport so SendResult's path is covered too.
	handleDone := make(chan struct{})
	go func() {
		defer close(handleDone)
		s2.handle(advanceCmd)
	}()
	select {
	case <-handleDone:
	case <-time.After(3 * time.Second):
		t.Fatal("SEC-020 REGRESSION: handle() on a mid-lock-copied StubEngine hung")
	}
	select {
	case res := <-tr.Results():
		if res.Accepted || res.Error == nil || res.Error.Code != codeStubEngineCopied {
			t.Fatalf("handle() dispatch on a copy produced result %+v, want a rejected result carrying %s", res, codeStubEngineCopied)
		}
	case <-time.After(testTimeout):
		t.Fatal("handle() on a copy never pushed a CommandResult")
	}

	// The ORIGINAL StubEngine must be completely unaffected — its mu
	// was only ever held and released normally by us, once, to take the
	// copy.
	res := s.handleAdvanceTicks(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(), Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 5}})
	if !res.Accepted {
		t.Fatalf("original StubEngine AdvanceTicks after copy attack: rejected, %+v", res.Error)
	}
	if got := s.Tick(); got != 5 {
		t.Fatalf("original StubEngine Tick() after copy attack = %d, want 5", got)
	}
}

// TestStubEngine_ZeroValue_FailsClosed_NoMuTouch proves the zero-value
// case: `var s StubEngine` / `new(StubEngine)` (never passed through
// NewStubEngine, so self was never stored) is rejected the same way a
// copy is — and, because the identity check runs before mu, without
// ever touching mu or the nil subs map (a bare `StubEngine{}`'s subs
// field is nil, so reaching `s.subs[id] = sub` in handleSubscribe would
// nil-map-write panic; the pre-lock identity check means that line is
// never reached for this case either).
func TestStubEngine_ZeroValue_FailsClosed_NoMuTouch(t *testing.T) {
	var s StubEngine

	corrID := protocol.NewCorrelationID()
	advanceCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}}
	subCmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corrID, Kind: protocol.KindSubscribe, Payload: protocol.SubscribePayload{ViewName: viewportViewName}}

	if got := s.Tick(); got != 0 {
		t.Fatalf("zero-value StubEngine Tick() = %d, want 0", got)
	}
	if res := s.handleAdvanceTicks(advanceCmd); res.Accepted || res.Error == nil || res.Error.Code != codeStubEngineCopied {
		t.Fatalf("zero-value StubEngine handleAdvanceTicks: result = %+v, want rejected with %s", res, codeStubEngineCopied)
	}
	// handleSubscribe would nil-map-write panic if the pre-lock check
	// did not run first — this call succeeding (rejected, not panicking)
	// IS the assertion.
	if res := s.handleSubscribe(subCmd); res.Accepted || res.Error == nil || res.Error.Code != codeStubEngineCopied {
		t.Fatalf("zero-value StubEngine handleSubscribe: result = %+v, want rejected with %s", res, codeStubEngineCopied)
	}
}

// TestStubEngine_SelfIdentity_FreshEnginesAreDistinct is a sanity check
// that two independently constructed StubEngines never collide on
// identity (the non-attack path checkNotCopied must never reject).
func TestStubEngine_SelfIdentity_FreshEnginesAreDistinct(t *testing.T) {
	tr1 := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	tr2 := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	t.Cleanup(func() { _ = tr1.Close(); _ = tr2.Close() })

	s1, err := NewStubEngine(tr1)
	if err != nil {
		t.Fatalf("NewStubEngine(tr1): %v", err)
	}
	s2, err := NewStubEngine(tr2)
	if err != nil {
		t.Fatalf("NewStubEngine(tr2): %v", err)
	}

	res1 := s1.handleAdvanceTicks(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(), Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}})
	if !res1.Accepted {
		t.Fatalf("s1.handleAdvanceTicks: rejected, %+v", res1.Error)
	}
	res2 := s2.handleAdvanceTicks(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(), Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}})
	if !res2.Accepted {
		t.Fatalf("s2.handleAdvanceTicks: rejected, %+v", res2.Error)
	}
}

// copyStubEngine performs the SEC-020 attack — a plain StubEngine
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer,
// rather than the literal `c := *s` a real attacker would write. Both
// produce IDENTICAL bytes and therefore identical runtime semantics
// (mu's zero-derived bytes copied as-is, subs' map header copied —
// aliasing the same map — transport/world/rng pointers copied unchanged,
// self's pointer bytes copied unchanged); the only difference is that
// `c := *s` is a typed Go assignment `go vet`'s copylocks check
// statically flags (this package cannot contain that literal line and
// still pass `go vet ./...`, which this fix's VERIFY step requires),
// while this byte-level copy operates on an untyped [N]byte array vet
// does not analyze for lock content. Precedent: engine.core's e2Copy
// (sec014_poc_test.go) and internal/protocol's equivalent helper
// (sec020_test.go) — same technique, applied to this package's type.
func copyStubEngine(s *StubEngine) *StubEngine {
	c := new(StubEngine)
	*(*[unsafe.Sizeof(StubEngine{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(StubEngine{})]byte)(unsafe.Pointer(s))
	return c
}
