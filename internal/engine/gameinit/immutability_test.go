package gameinit

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"unsafe"
)

// gameInitByteCopy performs SEC-020's attack -- a plain GameInit struct
// copy -- via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// deathservices.deathServicesAPIByteCopy/logistics.logisticsAPIByteCopy
// (the sanctioned pattern, GR#24-safe): a literal `cp := *g` is legal Go
// but go vet's copylocks check statically flags it (GameInit embeds an
// atomic.Pointer), and this package must pass `go vet ./...`.
func gameInitByteCopy(g *GameInit) *GameInit {
	cp := new(GameInit)
	*(*[unsafe.Sizeof(GameInit{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(GameInit{})]byte)(unsafe.Pointer(g))
	return cp
}

// modeAssignmentOutsideConstructor matches any assignment to a field named
// `mode`, whether written as a bare identifier (`mode = ...`, inside the
// gameinit package itself) or through a selector (`g.mode = ...`,
// `x.mode = ...`). The previous version of this pattern
// (`(^|[^.\w])mode\s*=[^=]`) required a NON-DOT character (or
// start-of-string) immediately before `mode`, which means a selector
// assignment -- exactly the shape a setter would actually use -- could
// NEVER match: the character immediately before `mode` in `g.mode = m` is
// `.`, which the old pattern explicitly excluded. That made the "no
// setter" proof vacuous (see TestAttackFEAT143_GrepProofIsReal's negative
// control). `\b` (a word boundary) matches between `.` and `m` just as it
// does between whitespace and `m` or at the start of a line, so this
// version catches both shapes while `[^=]` after the `=` still excludes
// `==` (comparison, e.g. `g.mode == ModeUnlimited`) and the struct
// literal's `mode: mode` field (a `:`, never `=`, follows the field name
// there).
var modeAssignmentOutsideConstructor = regexp.MustCompile(`\bmode\s*=[^=]`)

// TestGameModeImmutable_NoSetterInSource (AC-3, GR#12): mechanically
// proves the "no setter" invariant by scanning every non-test .go file in
// this package for a `mode = ` assignment. New/Load's struct-literal
// construction (`mode: mode` inside `&GameInit{...}`) does not match this
// pattern (no `=` immediately after the field name outside a colon), so a
// clean scan means the ONLY way g.mode is ever set is at construction —
// there is no exported or unexported mutator anywhere in the package.
func TestGameModeImmutable_NoSetterInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	found := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" || len(name) >= 8 && name[len(name)-8:] == "_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if loc := modeAssignmentOutsideConstructor.FindIndex(b); loc != nil {
			found = true
			t.Errorf("%s: found a `mode = ` assignment outside New's struct literal at byte offset %d — the mode field must never be mutated after construction (AC-3)", name, loc[0])
		}
	}
	if found {
		t.FailNow()
	}
}

// TestGameModeImmutable_SetGameModeAlwaysRejects (AC-3): every
// mode-changing surface this package exposes — here, the one explicit
// SetGameMode command entry point a dev-console or settings write would
// route to — is rejected with a registry-sourced typed error, and the
// mode read-back is unchanged, regardless of which mode is requested
// (including a request for the SAME mode, and an invalid mode string).
func TestGameModeImmutable_SetGameModeAlwaysRejects(t *testing.T) {
	g, err := New(ModeReal, testConfig(), "t-immutable")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	attempts := []Mode{ModeUnlimited, ModeReal, Mode("bogus"), Mode("")}
	for _, attempt := range attempts {
		if err := g.SetGameMode(attempt); err == nil {
			t.Fatalf("SetGameMode(%q) succeeded, want ErrModeLocked", attempt)
		}
		if got, err := g.Mode("t-immutable"); err != nil {
			t.Fatalf("Mode: %v", err)
		} else if got != ModeReal {
			t.Fatalf("after SetGameMode(%q): Mode() = %q, want unchanged %q", attempt, got, ModeReal)
		}
	}
}

// TestGameInitCopyGuard (SEC-020 family): a method call on a struct-copied
// GameInit is rejected rather than silently operating on the copy's own
// (identical, but no longer the canonical) self-pointer state.
func TestGameInitCopyGuard(t *testing.T) {
	g, err := New(ModeReal, testConfig(), "t-copy")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cp := gameInitByteCopy(g)
	// FEAT-143 round finding P2-B: a copy-guard rejection must surface as
	// an ERROR, never as a silently-returned zero value indistinguishable
	// from a real Real-mode answer (see
	// TestAttackFEAT143_CopiedGameInitSilentlyReportsRealMode).
	if _, err := cp.Mode("t-copy"); err == nil {
		t.Fatalf("copied GameInit.Mode() succeeded, want copy-guard rejection")
	}
	if cp.SetGameMode(ModeUnlimited) == nil {
		t.Fatalf("copied GameInit.SetGameMode succeeded, want copy-guard rejection")
	}
}
