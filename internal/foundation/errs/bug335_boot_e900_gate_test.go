package errs

// BUG-335: the boot-failure message a user actually sees must never drop its
// cause. MET-E900 ("metropolis failed to boot: {component} failed to
// initialize: {cause}") is the worst possible surface for the BUG-357 literal-
// {token} family — it is the one line the operator gets when the game refuses
// to start, and {component}/{cause} left unsubstituted tells the person best
// placed to act on the failure the least.
//
// BUG-388 already fixed and runtime-tested the errs.Wrap(codeBootFailure,…)
// boot sites: construct() auto-injects {cause} from a non-nil wrapped error,
// so those render. This gate is the DISTINCT complement BUG-388's runtime
// render test does not cover: the errs.New(codeBootFailure, …) NIL-CAUSE boot
// sites (cmd/metropolis/boot.go's primeScreenSubscription, ~1106-1167). A New
// has no wrapped cause to inject, so it MUST carry an explicit "cause" (and
// "component") key or {cause}/{component} renders literal. Today all five do —
// this gate is the can-fail tripwire that keeps them that way, and catches any
// future MET-E900 site (New or Wrap) that leaves a template token unfilled.
//
// It is deliberately SCOPED to MET-E900 (via the code filter): the repo-wide
// scanTree survivor set is dominated by other modules' unrelated BUG-357 debt,
// which is not this item's surface. Two independent checks, both proven able
// to fail:
//   1. a RENDER check (BUG-335's explicit "verify by rendering, not reading")
//      that proves the template genuinely carries {component}/{cause} and that
//      a site omitting a key really does leak the literal token; and
//   2. a STATIC AST check that reuses the BUG-357 real-call-site walker over
//      the live tree, asserting zero MET-E900 findings of any kind, with a
//      coverage tripwire so a mis-scoped scan can never pass by finding nothing.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bootFailureCode = "MET-E900"

// TestBootE900_RendersBothTokens is the render-based half (BUG-335: "VERIFY BY
// RENDERING"). It proves, against the LIVE registry, that MET-E900's template
// carries both {component} and {cause}, that a boot site supplying both renders
// clean, and — the can-fail direction — that omitting the "cause" key (exactly
// the risk an errs.New nil-cause boot site runs) leaks the literal "{cause}".
func TestBootE900_RendersBothTokens(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	// A New site that supplies BOTH keys — the shape every real boot New site
	// uses — must render with no surviving placeholder.
	full := New(bootFailureCode, "corr-1", map[string]any{
		"component": "primeScreenSubscription", "cause": "Subscribe rejected",
	}).Display()
	if strings.Contains(full, "{") || strings.Contains(full, "}") {
		t.Fatalf("MET-E900 with component+cause still contains an unresolved placeholder: %q", full)
	}
	if !strings.Contains(full, "primeScreenSubscription") || !strings.Contains(full, "Subscribe rejected") {
		t.Fatalf("MET-E900 render dropped a supplied value: %q", full)
	}

	// Can-fail proof #1: a New site that OMITS "cause" (nothing to auto-inject,
	// since New wraps no cause) must leak the literal {cause}. If this ever
	// stops happening the template no longer carries the token and the gate is
	// meaningless — so we assert the leak is real.
	missingCause := New(bootFailureCode, "corr-2", map[string]any{
		"component": "primeScreenSubscription",
	}).Display()
	if !strings.Contains(missingCause, "{cause}") {
		t.Fatalf("expected MET-E900 to leak literal {cause} when the New site omits it, got %q", missingCause)
	}

	// Can-fail proof #2: omitting "component" leaks {component} likewise.
	missingComponent := New(bootFailureCode, "corr-3", map[string]any{
		"cause": "Subscribe rejected",
	}).Display()
	if !strings.Contains(missingComponent, "{component}") {
		t.Fatalf("expected MET-E900 to leak literal {component} when the New site omits it, got %q", missingComponent)
	}
}

// TestBootE900_NoStaticPlaceholderSurvivors is the static half: the BUG-357
// scanTree AST walker, run over the LIVE cmd/ + internal/ tree but filtered to
// MET-E900, must report zero findings of any kind (survivor, dynamic-code,
// dynamic-ctx, unknown-code) — i.e. every boot site resolves statically and
// fills both {component} and {cause}. A coverage tripwire fails loudly if the
// scan saw no MET-E900 sites at all, so a broken path can never false-green.
func TestBootE900_NoStaticPlaceholderSurvivors(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	registry, err := loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	regPath, err := resolveRegistryPath()
	if err != nil {
		t.Fatalf("resolveRegistryPath: %v", err)
	}
	repoRoot := filepath.Dir(filepath.Dir(regPath)) // <root>/data/errors.json -> <root>

	var e900 []siteFinding
	for _, sub := range []string{"cmd", "internal"} {
		for _, f := range scanTree(filepath.Join(repoRoot, sub), registry) {
			if f.Code == bootFailureCode {
				e900 = append(e900, f)
			}
		}
	}

	if len(e900) > 0 {
		var b strings.Builder
		for _, f := range e900 {
			b.WriteString("\n  ")
			b.WriteString(f.File)
			b.WriteString(": ")
			b.WriteString(f.Kind)
			if f.Token != "" {
				b.WriteString(" token={" + f.Token + "}")
			}
			if f.Detail != "" {
				b.WriteString(" detail=" + f.Detail)
			}
		}
		t.Errorf("MET-E900 (boot failure) has %d unresolved-render finding(s) — a user would see a literal {token} on the boot-failure line (BUG-335):%s",
			len(e900), b.String())
	}

	// Coverage tripwire: prove the scan actually reached the boot sites. If the
	// repoRoot/path resolution ever breaks, the loop above finds nothing and
	// the zero-findings assertion passes vacuously — exactly the false-green
	// the BUG-008 gate guards against. Count the codeBootFailure call sites in
	// the boot source directly and require the scan to have had something to
	// check.
	if sites := countBootFailureSites(t, repoRoot); sites == 0 {
		t.Fatalf("coverage tripwire: found zero codeBootFailure call sites under %s/cmd — the scan is mis-scoped, not the code clean", repoRoot)
	}
}

// TestBootE900_GateCanFail proves the STATIC gate can fail on the exact
// failure mode it guards: an errs.New site against a MET-E900-shaped template
// that omits the "cause" key (a nil-cause New with nothing to auto-inject)
// must be reported as a survivor of token "cause". Uses a fixture registry +
// fixture package so no violation is ever left in the live tree.
func TestBootE900_GateCanFail(t *testing.T) {
	const fixtureRegistry = `{
  "version": 1,
  "codes": {
    "MET-E900": {"severity":"fatal","module":"feat.skeleton","message":"metropolis failed to boot: {component} failed to initialize: {cause}","remedy":"r"}
  }
}`
	entries, _, err := decodeCodes([]byte(fixtureRegistry))
	if err != nil {
		t.Fatalf("decode fixture registry: %v", err)
	}

	dir := t.TempDir()
	pkg := filepath.Join(dir, "boot")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A boot New site that supplies "component" but NOT "cause" — the precise
	// shape BUG-335 warns about for the errs.New nil-cause path.
	broken := `package boot

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

const codeBootFailure = "MET-E900"

func F() {
	_ = errs.New(codeBootFailure, "c", map[string]any{"component": "primeScreenSubscription"})
}
`
	if err := os.WriteFile(filepath.Join(pkg, "boot.go"), []byte(broken), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	findings := scanTree(pkg, entries)
	var causeSurvivor bool
	for _, f := range findings {
		if f.Code == bootFailureCode && f.Kind == "survivor" && f.Token == "cause" {
			causeSurvivor = true
		}
	}
	if !causeSurvivor {
		t.Fatalf("expected the gate to flag a MET-E900 survivor of token {cause} for the nil-cause New site, got %+v", findings)
	}

	// GREEN direction: supplying "cause" (as every real boot New site does)
	// clears the finding.
	fixed := `package boot

import errs "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

const codeBootFailure = "MET-E900"

func F() {
	_ = errs.New(codeBootFailure, "c", map[string]any{"component": "primeScreenSubscription", "cause": "Subscribe rejected"})
}
`
	if err := os.WriteFile(filepath.Join(pkg, "boot.go"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	for _, f := range scanTree(pkg, entries) {
		if f.Code == bootFailureCode {
			t.Fatalf("expected no MET-E900 findings once cause is supplied, got %+v", f)
		}
	}
}

// countBootFailureSites counts errs.New/errs.Wrap boot-failure call sites by
// scanning the boot source for the codeBootFailure constant's use as a call
// argument. It is a coverage tripwire only (not the render check), so a plain
// textual count of the const name in non-test .go files under cmd/ is
// sufficient — it just proves the static scan had real sites to evaluate.
func countBootFailureSites(t *testing.T, repoRoot string) int {
	t.Helper()
	count := 0
	root := filepath.Join(repoRoot, "cmd")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		count += strings.Count(string(b), "errs.New(codeBootFailure")
		count += strings.Count(string(b), "errs.Wrap(codeBootFailure")
		return nil
	})
	return count
}
