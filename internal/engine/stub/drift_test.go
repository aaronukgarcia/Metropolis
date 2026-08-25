package stub_test

import (
	"testing"

	engcore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	stub "github.com/aaronukgarcia/Metropolis/internal/engine/stub"
)

// TestMaxAdvanceTicksPerCall_MatchesEngineCore guards against
// stub.maxAdvanceTicksPerCall (a literal copy, required by SEC-006's
// fix: engine.stub deliberately does not import internal/engine/core's
// package internals — see codes.go's doc comment — the same GR#20-style
// decoupling reason internal/ui/screens/debug/phase.go used to mirror
// the phase names before BUG-382 moved them into internal/protocol)
// drifting from the real, authoritative
// internal/engine/core.MaxAdvanceTicksPerCall.
//
// This is a _test.go file specifically so it CAN import
// internal/engine/core without creating a production dependency between
// the two packages (mirroring the sanctioned exemption
// internal/ui/screens/debug/determinism_test.go already established for
// the phase-order mirror) — production code in this package still never
// imports internal/engine/core.
//
// If this test ever fails: the two packages' AdvanceTicks upper bounds
// have diverged. Do NOT "fix" this test by changing one side to match
// the other without thinking — find out which value is intentional
// (check both packages' recent history/BOW items) and update whichever
// constant is stale so engine.core.AdvanceTicks and StubEngine's
// AdvanceTicks command reject exactly the same inputs again. The two
// constants are duplicated ONLY because engine.stub must not depend on
// engine.core (contract-first/stub-forever, GR#20); they are not
// allowed to independently drift once duplicated, which is what this
// test enforces in CI instead of leaving it to a client to discover the
// asymmetry (the exact class of bug SEC-006 originally reported).
func TestMaxAdvanceTicksPerCall_MatchesEngineCore(t *testing.T) {
	if got, want := stub.MaxAdvanceTicksPerCallForTest(), engcore.MaxAdvanceTicksPerCall; got != want {
		t.Fatalf(
			"engine/stub's copy of the AdvanceTicks upper bound (%d) has drifted from "+
				"engine/core.MaxAdvanceTicksPerCall (%d). These are deliberately duplicated "+
				"literals, not a shared constant, because engine.stub must not import "+
				"internal/engine/core (GR#20 contract-first/stub-forever decoupling — see "+
				"internal/engine/stub/codes.go's doc comment). Whoever changed one side must "+
				"change the other: update internal/engine/stub/codes.go's "+
				"maxAdvanceTicksPerCall to match internal/engine/core/engine.go's "+
				"MaxAdvanceTicksPerCall (or vice versa, if the stub's value is the one that "+
				"should now win) so StubEngine and the real engine reject exactly the same "+
				"AdvanceTicksPayload.N values again — see SEC-006.",
			got, want,
		)
	}
}
