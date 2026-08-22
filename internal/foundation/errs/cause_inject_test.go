package errs

import (
	"errors"
	"strings"
	"testing"
)

// BUG-357 root fix: Wrap must supply the cause it looks like it supplies.
//
// The 376-site measurement (BUG-357) found templates carrying {cause} raised
// via errs.Wrap without an explicit "cause" ctx key render the literal text
// "{cause}" to the user — Wrap reads as if it injects the cause, and does
// not. These tests pin the decision: cause != nil + no explicit ctx cause =>
// inject cause.Error(); explicit ctx "cause" always wins; nil cause leaves
// the literal (a genuine ctx gap the mechanical gate is designed to catch).
//
// Tests 1 and 4 are the RED-proving pair — they fail against the pre-fix
// construct() and pass after. Tests 2/3/5 are guards that must hold on
// both sides of the fix.

// causeRegistry is a minimal registry whose MET-F910 template carries {cause}.
const causeRegistry = `{
  "version": 1,
  "codes": {
    "MET-F910": {
      "severity": "error",
      "module": "foundation.errors",
      "message": "operation failed: {cause}",
      "remedy": "retry the operation"
    }
  }
}`

func setupCauseRegistry(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := writeRegistry(t, dir, causeRegistry)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	resetSinkForTest()
	t.Cleanup(func() {
		resetRegistryForTest()
		resetSinkForTest()
	})
}

// TestWrap_CauseInjectedFromWrappedError is the primary RED proof: a Wrap with
// a non-nil cause and no explicit ctx "cause" must render the cause text, not
// the literal "{cause}".
func TestWrap_CauseInjectedFromWrappedError(t *testing.T) {
	setupCauseRegistry(t)

	cause := errors.New("disk full")
	e := Wrap("MET-F910", "corr-inj-1", cause, nil)
	if strings.Contains(e.Msg, "{cause}") {
		t.Fatalf("literal {cause} rendered: %q", e.Msg)
	}
	if !strings.Contains(e.Msg, "disk full") {
		t.Errorf("Msg = %q, want the wrapped cause text rendered", e.Msg)
	}
}

// TestWrap_ExplicitCauseWins: a caller-supplied ctx "cause" must always win
// over the wrapped error's text.
func TestWrap_ExplicitCauseWins(t *testing.T) {
	setupCauseRegistry(t)

	cause := errors.New("wrapped: disk full")
	e := Wrap("MET-F910", "corr-inj-2", cause, map[string]any{"cause": "explicit: bad sector"})
	if strings.Contains(e.Msg, "{cause}") {
		t.Fatalf("literal {cause} rendered: %q", e.Msg)
	}
	if !strings.Contains(e.Msg, "explicit: bad sector") {
		t.Errorf("Msg = %q, want the explicit ctx cause rendered", e.Msg)
	}
	if strings.Contains(e.Msg, "wrapped: disk full") {
		t.Errorf("Msg = %q, explicit ctx cause must beat the wrapped error text", e.Msg)
	}
}

// TestWrap_NilCauseLeavesLiteral documents the genuine-gap contract: a nil
// cause is NOT auto-filled, so a {cause} template with no ctx cause still
// renders the literal — the mechanical gate's job is to catch that site, not
// to paper over it with an invented value.
func TestWrap_NilCauseLeavesLiteral(t *testing.T) {
	setupCauseRegistry(t)

	e := Wrap("MET-F910", "corr-inj-3", nil, nil)
	if !strings.Contains(e.Msg, "{cause}") {
		t.Errorf("nil cause must leave {cause} literal, got %q (genuine gap the gate catches)", e.Msg)
	}
}

// TestWrap_CauseInjectedIntoCtx is the second RED proof: the rendered message
// is a symptom — the ctx map itself must carry the injected cause so any
// downstream selectable/displayed context shows it (GR#1).
func TestWrap_CauseInjectedIntoCtx(t *testing.T) {
	setupCauseRegistry(t)

	cause := errors.New("boom")
	e := Wrap("MET-F910", "corr-inj-4", cause, nil)
	got, ok := e.Ctx["cause"]
	if !ok {
		t.Fatal("expected cause to be injected into e.Ctx")
	}
	if got != "boom" {
		t.Errorf("e.Ctx[\"cause\"] = %v, want %q", got, cause.Error())
	}
}

// TestNew_NoCauseRendersLiteral: New never wraps a cause, so a {cause}
// template reached via New with no explicit ctx cause stays literal (New does
// not inject).
func TestNew_NoCauseRendersLiteral(t *testing.T) {
	setupCauseRegistry(t)

	e := New("MET-F910", "corr-inj-5", nil)
	if !strings.Contains(e.Msg, "{cause}") {
		t.Errorf("New with no cause must leave {cause} literal, got %q", e.Msg)
	}
}

// TestWrap_DoesNotMutateCallerCtxMap is the RED-proving pair for the BUG-357
// LOW finding (pr6 round): construct() must not write the injected cause into
// the caller's ctx map. Before the copy-on-inject fix, a map shared across a
// Wrap (cause injected) and a later New (nil cause) leaked the stale cause
// text into the New's render — violating the "nil cause leaves {cause}
// literal" contract. The first assertion fails against the pre-fix
// construct(); the second proves the leak itself is gone.
func TestWrap_DoesNotMutateCallerCtxMap(t *testing.T) {
	setupCauseRegistry(t)

	shared := map[string]any{}
	first := Wrap("MET-F910", "corr-inj-6", errors.New("disk full"), shared)

	if first.Msg != "operation failed: disk full" {
		t.Fatalf("sanity: first Wrap must render the cause, got %q", first.Msg)
	}
	if _, mutated := shared["cause"]; mutated {
		t.Fatalf("Wrap mutated the caller's ctx map: shared now carries cause %q — stale-cause leak across reused maps", shared["cause"])
	}

	second := New("MET-F910", "corr-inj-7", shared)
	if strings.Contains(second.Msg, "disk full") {
		t.Fatalf("New reused the shared map and rendered the previous Wrap's cause: %q", second.Msg)
	}
	if !strings.Contains(second.Msg, "{cause}") {
		t.Errorf("nil-cause New over a reused-but-unmutated map must leave {cause} literal, got %q", second.Msg)
	}
}
