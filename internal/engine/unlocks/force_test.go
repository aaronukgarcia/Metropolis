package unlocks

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- AC-11: debug force-unlock is debug-gated and sticky-flags ---------

// TestForceUnlockNonDebugRejected asserts ForceUnlock is rejected when no
// debug authorizer is wired (the "debug off" default) — a cheat can never
// fire silently in a non-debug build (AC-11).
func TestForceUnlockNonDebugRejected(t *testing.T) {
	api := realAPI(t)

	err := api.ForceUnlock(ForceTarget{Tier: 7}, testCorrelationID())
	assertCode(t, err, ErrDebugRequired)

	if api.MilestoneReached(7) {
		t.Error("MilestoneReached(7) = true after a rejected ForceUnlock; the cheat mutated state with debug off")
	}
	if api.DebugTouched() {
		t.Error("DebugTouched = true after a rejected ForceUnlock")
	}
}

// TestForceUnlockDenyingGateRejected asserts a wired-but-denying gate also
// rejects (the authorizer, not merely its absence, is respected).
func TestForceUnlockDenyingGateRejected(t *testing.T) {
	api := realAPI(t)
	if err := api.SetDebugGate(func(string) error {
		return errs.New("MET-E200", testCorrelationID(), map[string]any{"capability": "force-unlock"})
	}); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}

	if err := api.ForceUnlock(ForceTarget{Tier: 7}, testCorrelationID()); err == nil {
		t.Error("ForceUnlock with a denying gate returned nil error")
	}
	if api.MilestoneReached(7) {
		t.Error("MilestoneReached(7) = true after a denied ForceUnlock")
	}
}

// TestForceUnlockDebugSucceedsAndTouches asserts that, with debug
// authorized, ForceUnlock succeeds, applies the unlock, and invokes the
// sticky DebugTouched flag (AC-11).
func TestForceUnlockDebugSucceedsAndTouches(t *testing.T) {
	api := realAPI(t)
	touched := false
	if err := api.SetDebugGate(func(string) error { return nil }); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}
	if err := api.SetDebugTouch(func() error { touched = true; return nil }); err != nil {
		t.Fatalf("SetDebugTouch: %v", err)
	}

	// Force-reach tier 7 (the port tier — §4's "port testing pre-100k").
	if err := api.ForceUnlock(ForceTarget{Tier: 7}, testCorrelationID()); err != nil {
		t.Fatalf("ForceUnlock(tier 7): %v", err)
	}
	if !api.MilestoneReached(7) {
		t.Error("MilestoneReached(7) = false after ForceUnlock(tier 7)")
	}
	if !touched {
		t.Error("debug touch callback was not invoked on a successful ForceUnlock")
	}
	if !api.DebugTouched() {
		t.Error("DebugTouched() = false after a successful ForceUnlock")
	}

	// Force-unlock a specific tree node.
	node := api.SignatureUnlocks(7)[0]
	if err := api.ForceUnlock(ForceTarget{NodeID: node}, testCorrelationID()); err != nil {
		t.Fatalf("ForceUnlock(node %s): %v", node, err)
	}
	if !api.IsNodeUnlocked(node) {
		t.Errorf("node %q not unlocked after ForceUnlock(node)", node)
	}
}

// TestForceUnlockInvalidTargetRejected rejects a target naming neither (or
// both of) a tier and a node (AC-11).
func TestForceUnlockInvalidTargetRejected(t *testing.T) {
	api := realAPI(t)
	if err := api.SetDebugGate(func(string) error { return nil }); err != nil {
		t.Fatalf("SetDebugGate: %v", err)
	}

	for name, target := range map[string]ForceTarget{
		"neither":  {},
		"both":     {Tier: 3, NodeID: api.SignatureUnlocks(3)[0]},
		"bad-tier": {Tier: 99},
		"bad-node": {NodeID: "no_such_node"},
	} {
		if err := api.ForceUnlock(target, testCorrelationID()); err == nil {
			t.Errorf("%s: ForceUnlock returned nil error, want ErrInvalidUnlockTarget", name)
		} else {
			assertCode(t, err, ErrInvalidUnlockTarget)
		}
	}
}
