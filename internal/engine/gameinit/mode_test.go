package gameinit

import "testing"

func testConfig() Config {
	return Config{
		Version: 1,
		Meta:    Meta{Module: "feat.gameinit", BowCode: "FEAT-143"},
		Params: Params{
			StartingCapitalMicropounds: Number{
				Value: 5000000000, Unit: "micro-pounds", Disclosure: "test fixture",
			},
		},
	}
}

// TestGameModeExactlyOneOfTwo (AC-1): a new game is initialized in
// exactly one of the two modes, never both, never neither, and the
// engine's own mode is a real enum value read back from the init call —
// never a zero-value default silently passing as a real mode.
func TestGameModeExactlyOneOfTwo(t *testing.T) {
	real, err := New(ModeReal, testConfig(), "t-real")
	if err != nil {
		t.Fatalf("New(ModeReal): %v", err)
	}
	if got, err := real.Mode("t-real"); err != nil {
		t.Fatalf("real.Mode: %v", err)
	} else if got != ModeReal {
		t.Fatalf("real.Mode() = %q, want %q", got, ModeReal)
	}
	if got, err := real.Unlimited("t-real"); err != nil {
		t.Fatalf("real.Unlimited: %v", err)
	} else if got {
		t.Fatalf("real.Unlimited() = true, want false")
	}

	unlimited, err := New(ModeUnlimited, testConfig(), "t-unlimited")
	if err != nil {
		t.Fatalf("New(ModeUnlimited): %v", err)
	}
	if got, err := unlimited.Mode("t-unlimited"); err != nil {
		t.Fatalf("unlimited.Mode: %v", err)
	} else if got != ModeUnlimited {
		t.Fatalf("unlimited.Mode() = %q, want %q", got, ModeUnlimited)
	}
	if got, err := unlimited.Unlimited("t-unlimited"); err != nil {
		t.Fatalf("unlimited.Unlimited: %v", err)
	} else if !got {
		t.Fatalf("unlimited.Unlimited() = false, want true")
	}
}

// TestGameModeZeroValueRejected (AC-1's false-pass-risk note): the zero
// value Mode("") must never construct successfully — a mode that only
// lives in the UI or defaults silently would be exactly this bug.
func TestGameModeZeroValueRejected(t *testing.T) {
	if _, err := New(Mode(""), testConfig(), "t-zero"); err == nil {
		t.Fatalf("New(Mode(\"\")) succeeded, want ErrUnknownGameMode")
	}
	if _, err := New(Mode("bogus"), testConfig(), "t-bogus"); err == nil {
		t.Fatalf("New(Mode(\"bogus\")) succeeded, want ErrUnknownGameMode")
	}
}

// TestParseModeRoundTrip proves ParseMode/String are inverses for both
// known modes and that ParseMode rejects everything else (the exact
// mechanism GameModeWire()/save's WithExpectedGameMode round-trips
// through).
func TestParseModeRoundTrip(t *testing.T) {
	for _, m := range []Mode{ModeReal, ModeUnlimited} {
		got, err := ParseMode(m.String(), "t-parse")
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", m.String(), err)
		}
		if got != m {
			t.Fatalf("ParseMode(%q) = %q, want %q", m.String(), got, m)
		}
	}
	if _, err := ParseMode("", "t-parse-empty"); err == nil {
		t.Fatalf("ParseMode(\"\") succeeded, want ErrUnknownGameMode")
	}
	if _, err := ParseMode("REAL", "t-parse-case"); err == nil {
		t.Fatalf("ParseMode(\"REAL\") succeeded, want ErrUnknownGameMode (case-sensitive)")
	}
}
