package protocol

import "testing"

// capability_test.go — FEAT-1972079936 Phase 0 increment 3 (AC-5): the
// fine-grained capability registry (capability.go).

func TestRequiredCapability_GatedKind(t *testing.T) {
	cap, ok := RequiredCapability(KindDebug)
	if !ok {
		t.Fatal("expected KindDebug to require a capability (this increment's one illustrative gated Kind)")
	}
	if cap != CapDebugCommands {
		t.Fatalf("expected capability %q, got %q", CapDebugCommands, cap)
	}
}

func TestRequiredCapability_UngatedKind(t *testing.T) {
	// A pre-existing Kind Phase 0 never touches must require NOTHING --
	// every command that worked before this increment keeps working
	// unconditionally (this is the false-pass guard: a map that
	// accidentally gated every Kind would still pass the GatedKind test
	// above, but would break every other command's round trip).
	if _, ok := RequiredCapability(KindPause); ok {
		t.Fatal("expected KindPause to require no capability")
	}
	if _, ok := RequiredCapability(KindAdvanceTicks); ok {
		t.Fatal("expected KindAdvanceTicks to require no capability")
	}
}

func TestHasCapability(t *testing.T) {
	negotiated := []string{"A", CapDebugCommands}
	if !HasCapability(negotiated, CapDebugCommands) {
		t.Fatal("expected CapDebugCommands to be found in the negotiated set")
	}
	if HasCapability(negotiated, "not-present") {
		t.Fatal("expected an absent capability to report false")
	}
}

func TestHasCapability_EmptyNegotiatedSet(t *testing.T) {
	// AC-5's empty-intersection case: a client that declared no
	// capabilities (or shares nothing with the server) gets access to
	// nothing capability-gated -- HasCapability must report false for
	// every capability against a nil or empty set, never panic or
	// default-permit.
	if HasCapability(nil, CapDebugCommands) {
		t.Fatal("expected a nil negotiated set to never satisfy any capability")
	}
	if HasCapability([]string{}, CapDebugCommands) {
		t.Fatal("expected an empty negotiated set to never satisfy any capability")
	}
}
