package gameinit

import "testing"

// TestLoadDefaultConfig proves the real, checked-in data/gameinit.json
// (not a test fixture) parses and validates — the same file
// LoadDefault/compose's real wiring will read.
func TestLoadDefaultConfig(t *testing.T) {
	cfg, err := LoadDefaultConfig("t-default")
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	if got := cfg.StartingCapitalMicropounds(); got <= 0 {
		t.Fatalf("data/gameinit.json StartingCapitalMicropounds() = %d, want > 0 (AC-6)", got)
	}
	if !cfg.Params.StartingCapitalMicropounds.Placeholder {
		t.Fatalf("data/gameinit.json's startingCapitalMicropounds must be marked placeholder:true (the standing balance-number regime)")
	}
}
