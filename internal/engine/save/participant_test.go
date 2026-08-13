package save

import "testing"

// TestDefaultParticipants_IsLiteralRegistry is AC-1's structural check
// expressed as a test: DefaultParticipants must be a plain, enumerable
// slice (not populated by any reflection-driven discovery — there is no
// such code in this package at all, verified by this package containing
// no import of "reflect" in any non-test file, checked separately by
// grep in the dispatch report). This test just confirms the var exists,
// is directly rangeable, and starts empty (no domain module has
// registered yet, per participant.go's doc comment).
func TestDefaultParticipants_IsLiteralRegistry(t *testing.T) {
	count := 0
	for range DefaultParticipants {
		count++
	}
	if count != len(DefaultParticipants) {
		t.Fatalf("unreachable: range count %d != len %d", count, len(DefaultParticipants))
	}
	if len(DefaultParticipants) != 0 {
		t.Fatalf("DefaultParticipants has %d entries; this build expected 0 (no domain module has registered a Participant yet) — if this fails because one now has, update this test's expectation and confirm the new participant's owning package carries the AC-2 field-parity drift test", len(DefaultParticipants))
	}
}
