package stub

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestWorld_NeverMutatedPostConstruction is BUG-039's regression test for
// ASM-066. World()'s own doc comment (engine.go) states, as the load-bearing
// reasoning for NOT giving World() a checkNotCopied call (unlike every other
// StubEngine accessor in this file), that "the World it points to is built
// by GenerateFolkestone64 (fixture.go) and never mutated post-construction".
// That is an ASSUMPTION about every current and future code path through
// this package, not a compiler-checked fact — exactly the shape ASM-066
// records it as. This test makes it mechanical, matching the precedent
// internal/harness/synth/phasehooks.go + phasehooks_test.go set for
// BUG-034/PhaseHookCountInHeadlessPath: a hand-asserted invariant about the
// package's own behaviour, backed by a test that goes red the day the
// assumption stops being true, rather than trusting a doc comment to be
// re-read forever.
//
// # Why a behavioural snapshot, not an AST/grep scan
//
// phasehooks.go's problem ("does ANY file anywhere in the repo call
// RegisterPhaseHook against headless.Run's engine") is a provenance question
// a syntactic scan is the only tool that can even attempt, because the
// engine construction it cares about is outside the scanning package's
// reach. ASM-066's claim is narrower and lives entirely inside this
// package: does exercising every StubEngine command through the real
// s.transport/s.handle dispatch path (AC-1's own seam — the same one every
// caller actually drives World through) change the Folkestone-64 fixture
// StubEngine.World() returns. That is a runtime behavioural question this
// package CAN answer directly and empirically, by driving the real code
// paths and diffing before/after state — which is strictly stronger
// evidence for THIS claim than a source-level scan for a named identifier
// would be (a scan proves "no call site with this name exists"; this test
// proves "running the real dispatch loop left the fixture byte-identical").
// A grep/AST companion is not layered on top here because World has no
// exported (or unexported) mutating method for such a scan to watch for in
// the first place — Cell() and String() (fixture.go) are its only methods
// and both are read-only — so a phasehooks-style "watch for calls to
// method X" scan would have nothing to watch for. If World ever grows a
// mutating method, the right mechanical companion at that point is a
// phasehooks-shaped scan for callers of THAT method, matching the same
// precedent this test already draws on.
//
// # What this proves, and what it does not
//
// This drives NewStubEngine's construction, then every documented v1
// Command Kind (protocol.KnownKinds(), the same enumeration
// TestStubEngine_AllKnownKindsHandled in engine_test.go already uses to
// guarantee every kind gets covered as the protocol grows) through the real
// transport/Run/handle path — AdvanceTicks (which also drives every open
// subscription's scripted delta stream, the one path that reads world for
// fullViewportSnapshot), SetSpeed, Pause, Resume, Subscribe/Unsubscribe, and
// the two side-channel event kinds (InspectEntity, Debug) — then deep-copies
// the fixture's Cells grid before and after and asserts byte-identical
// equality via reflect.DeepEqual, plus confirms World() keeps returning the
// SAME pointer throughout (engine.go's doc comment: "world is a plain
// *World pointer, set exactly once ... and never reassigned afterwards").
// It does NOT prove no future code path anywhere in the repo could ever
// mutate World — like phasehooks.go's scan, it is a snapshot of "true as of
// this revision, and mechanically re-checked on every `go test` run", not a
// static proof against code that does not exist yet. See
// TestWorld_MutationWouldBeCaught below for the live proof that a real
// violation of this specific assumption turns this test red.
func TestWorld_NeverMutatedPostConstruction(t *testing.T) {
	tr, eng := newTestEngine(t)

	w := eng.World()
	before := snapshotWorldCells(w)

	// Drive every documented v1 command kind through the real dispatch
	// path — the same enumeration engine_test.go's
	// TestStubEngine_AllKnownKindsHandled uses, so this test automatically
	// covers any kind the protocol adds later rather than needing to be
	// hand-updated.
	//
	// protocol.KnownKinds() iterates commandRegistry, a Go map — its
	// returned slice order is NOT deterministic across runs (confirmed:
	// this test hung waiting on a Delta intermittently until this was
	// tracked explicitly instead of assumed). So whether a Delta is
	// waiting after each send is tracked via `subscribed` rather than
	// inferred from kind alone: AdvanceTicks only pushes a subscription-
	// script Delta (advanceSubscriptionScriptLocked, engine.go) when a
	// subscription happens to already be open, which depends on whether
	// this loop has reached Subscribe yet — order-dependent under a map
	// iteration.
	var subID protocol.SubscriptionID
	subscribed := false
	for _, kind := range protocol.KnownKinds() {
		if kind == protocol.KindUnsubscribe && !subscribed {
			// Needs a live subscription first, same setup
			// TestStubEngine_AllKnownKindsHandled uses.
			sr := send(t, tr, protocol.KindSubscribe, wellFormedPayload(protocol.KindSubscribe, ""))
			if !sr.Accepted {
				t.Fatalf("setup Subscribe for Unsubscribe test was rejected: %+v", sr)
			}
			d := recvDelta(t, tr)
			subID = d.SubscriptionID
			subscribed = true
		}

		r := send(t, tr, kind, wellFormedPayload(kind, subID))
		if !r.Accepted {
			t.Fatalf("%s was rejected, cannot exercise its World-touching path: %+v", kind, r)
		}

		switch kind {
		case protocol.KindSubscribe:
			// Always pushes exactly one initial Delta (handleSubscribe,
			// engine.go), regardless of prior state. Capture its
			// SubscriptionID too — a later Unsubscribe iteration (loop
			// order is non-deterministic, see above) needs a real ID to
			// unsubscribe, not just to know a subscription exists.
			d := recvDelta(t, tr)
			subID = d.SubscriptionID
			subscribed = true
		case protocol.KindAdvanceTicks:
			// Only pushes a Delta if a subscription happens to already be
			// open (advanceSubscriptionScriptLocked iterates s.subs) — see
			// the ordering note above for why this can't be assumed
			// unconditionally.
			if subscribed {
				recvDelta(t, tr)
			}
		case protocol.KindUnsubscribe:
			subscribed = false
		}
	}

	after := snapshotWorldCells(eng.World())
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("StubEngine.World()'s Cells grid changed after exercising every command kind — " +
			"ASM-066 is now FALSE: World() must gain a checkNotCopied call (matching every other " +
			"StubEngine accessor in engine.go) and its doc comment's 'never mutated post-construction' " +
			"claim must be corrected or the responsible code path fixed to stop mutating the shared fixture")
	}
	if eng.World() != w {
		t.Fatal("StubEngine.World() returned a different *World pointer after exercising every command " +
			"kind — engine.go's doc comment states world 'is set exactly once in NewStubEngine and never " +
			"reassigned afterwards'; that is no longer true")
	}
}

// TestWorld_MutationWouldBeCaught is the red/green proof that
// TestWorld_NeverMutatedPostConstruction is not a test that can never fail
// (Metropolis's verification standard: every regression test must be able
// to fail, not just able to pass). It reproduces ASM-066's violated state
// directly — mutating the SAME *World StubEngine.World() returns, in place,
// the exact shape a future mutating code path inside this package would
// take — and asserts that a before/after snapshot comparison of that
// mutated state detects it. This does not touch engine.go; it proves the
// snapshot/DeepEqual mechanism itself is sound, which is what
// TestWorld_NeverMutatedPostConstruction depends on to ever go red for a
// real violation.
func TestWorld_MutationWouldBeCaught(t *testing.T) {
	_, eng := newTestEngine(t)

	w := eng.World()
	before := snapshotWorldCells(w)

	// Simulate exactly the kind of violation ASM-066 warns about: some
	// future code path reaching into the shared fixture and changing it
	// after construction.
	w.Cells[0][0].Building = "BUG-039 injected mutation"

	after := snapshotWorldCells(eng.World())
	if reflect.DeepEqual(before, after) {
		t.Fatal("snapshot comparison failed to detect a direct, deliberate mutation of World.Cells — " +
			"the mechanism TestWorld_NeverMutatedPostConstruction relies on is broken")
	}
}

// snapshotWorldCells deep-copies w's Cells grid so a caller can compare
// state captured at two different points in time — comparing
// reflect.DeepEqual against the SAME *World twice would trivially always
// pass, since both reads see whatever the live object currently holds; a
// real snapshot must copy the row slices out from under it first. Cell
// (fixture.go) holds only value types (ints and strings), so a shallow
// per-row copy is a complete, independent deep copy.
func snapshotWorldCells(w *World) [][]Cell {
	out := make([][]Cell, len(w.Cells))
	for i, row := range w.Cells {
		out[i] = append([]Cell(nil), row...)
	}
	return out
}
