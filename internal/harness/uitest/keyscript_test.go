package uitest

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestParseScriptLeaderSequence is AC-2: a multi-key leader sequence
// ("b r s", UI-SPEC §3's build-road-street example) parses into one
// KeyRune event per key, in order.
func TestParseScriptLeaderSequence(t *testing.T) {
	events, err := ParseScript("b r s")
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	want := []rune{'b', 'r', 's'}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d", len(events), len(want))
	}
	for i, ev := range events {
		if ev.Key() != tcell.KeyRune {
			t.Errorf("event %d: Key() = %v, want KeyRune", i, ev.Key())
		}
		if ev.Rune() != want[i] {
			t.Errorf("event %d: Rune() = %q, want %q", i, ev.Rune(), want[i])
		}
	}
}

// TestParseScriptNamedSpecials covers every documented <Name> special
// resolves to a distinct, non-KeyRune tcell.Key (except <Space>, which is
// KeyRune ' ' by design — doc.go explains why).
func TestParseScriptNamedSpecials(t *testing.T) {
	events, err := ParseScript("<Space> <Esc> <Enter> <Tab> <F1> <F12>")
	if err != nil {
		t.Fatalf("ParseScript: %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("got %d events, want 6", len(events))
	}
	if events[0].Key() != tcell.KeyRune || events[0].Rune() != ' ' {
		t.Errorf("<Space> = %v/%q, want KeyRune/' '", events[0].Key(), events[0].Rune())
	}
	wantKeys := []tcell.Key{tcell.KeyEsc, tcell.KeyEnter, tcell.KeyTab, tcell.KeyF1, tcell.KeyF12}
	for i, want := range wantKeys {
		if got := events[i+1].Key(); got != want {
			t.Errorf("event %d: Key() = %v, want %v", i+1, got, want)
		}
	}
}

// TestParseScriptMalformedToken is AC-2b: a token outside the documented
// grammar is a parse-time rejection naming the offending token and its
// position — never silently dropped, never treated as a literal
// character sequence.
func TestParseMalformedScriptToken(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   string // substring expected in the token position/name
	}{
		{"multi-rune-not-special", "b road s", "road"},
		{"unclosed-angle", "b <Esc s", "<Esc"},
		{"unknown-special", "b <Frobnicate> s", "Frobnicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := ParseScript(tc.script)
			if err == nil {
				t.Fatalf("ParseScript(%q): got nil error, want rejection", tc.script)
			}
			if events != nil {
				t.Errorf("ParseScript(%q): got %d events on error, want nil (no partial application)", tc.script, len(events))
			}
			if !strings.Contains(err.Error(), codeMalformedScriptToken) {
				t.Errorf("error %q does not carry %s", err.Error(), codeMalformedScriptToken)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the offending token/position (%q)", err.Error(), tc.want)
			}
		})
	}
}

// TestParseScriptUnmappedToken is AC-7: a script token that decodes to
// no registered mapping (this package's grammar, doc.go) returns a
// typed/registry-sourced error rather than silently no-op'ing.
func TestParseScriptUnmappedToken(t *testing.T) {
	_, err := ParseScript("<NotARealKey>")
	if err == nil {
		t.Fatal("ParseScript(<NotARealKey>): got nil error, want rejection")
	}
	if !strings.Contains(err.Error(), codeMalformedScriptToken) {
		t.Errorf("error %q does not carry %s", err.Error(), codeMalformedScriptToken)
	}
}

// TestParseScriptEmptyIsEmpty confirms whitespace-only input parses to a
// zero-length, non-nil-error result (no phantom tokens from split
// artefacts).
func TestParseScriptEmptyIsEmpty(t *testing.T) {
	events, err := ParseScript("   ")
	if err != nil {
		t.Fatalf("ParseScript(whitespace): unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("got %d events for whitespace-only script, want 0", len(events))
	}
}
