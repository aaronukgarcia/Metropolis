package rules

import "testing"

// TestSetLiveVersionForTest_OlderBuildAndRestore: the helper stands in for a
// build older than Current, Effective answers it, and the override is undone
// when the test ends; a value outside 1..Current panics.
func TestSetLiveVersionForTest_OlderBuildAndRestore(t *testing.T) {
	t.Run("older build", func(t *testing.T) {
		SetLiveVersionForTest(t, RefuseOccupiedCell)
		if Effective() != RefuseOccupiedCell {
			t.Fatalf("Effective = %d, want %d", Effective(), RefuseOccupiedCell)
		}
		if Classify(RefuseOccupiedCell+1) != SupportTooNew && Current > RefuseOccupiedCell {
			t.Fatalf("a stamp above the stood-in build must be too new")
		}
	})
	if Effective() != Current {
		t.Fatalf("override not restored: Effective = %d, want %d", Effective(), Current)
	}
	for _, bad := range []Version{0, -1, Current + 1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("SetLiveVersionForTest(%d) did not panic", bad)
				}
			}()
			SetLiveVersionForTest(t, bad)
		}()
	}
}
