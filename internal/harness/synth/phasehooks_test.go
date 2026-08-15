package synth

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestPhaseHookCountAssertionStillTrue is PhaseHookCountInHeadlessPath's
// doc-comment promise made mechanical (defence in depth, not full proof
// — see that function's doc comment for the exact limit of what this
// test can and cannot catch, and BUG-053 for the incident that widened
// this check from a plain-text grep to an AST scan).
//
// # BUG-053: why this is an AST scan, not a `RegisterPhaseHook(` grep
//
// The original version of this test grepped source text for the exact
// substring "RegisterPhaseHook(" — a call site with an immediately
// following open paren. Destructive-2 defeated it live, in a scratch
// copy, with a plain Go method value:
//
//	register := e.RegisterPhaseHook
//	register(kind, hook)
//
// No line in that snippet contains "RegisterPhaseHook(" — the call
// happens through the local variable `register(...)`, not
// `e.RegisterPhaseHook(...)` — so the old grep passed while a real new
// call site landed inside internal/harness/headless itself, the exact
// package this test exists to watch. That is WIDER than the limitation
// the original author declared (only "headless.Run reusing an
// already-known file's own pattern" was named as the gap): a text
// substring match cannot see through ANY syntactic indirection —
// method values, differently-shaped wrapper calls, aliasing — because
// it has no notion of what an identifier binds to.
//
// scanForCallSites below fixes the DEMONSTRATED class, not just the one
// instance: it parses every file into a real Go AST and matches on
// *ast.Ident nodes named "RegisterPhaseHook", not on source text. In Go,
// a method value `e.RegisterPhaseHook` is a *ast.SelectorExpr whose
// Sel field IS an *ast.Ident named "RegisterPhaseHook" — the call and
// the method-value forms are indistinguishable at the AST-Ident level,
// so this catches both uniformly, along with any other spelling of "the
// identifier RegisterPhaseHook appears as a real, code-level reference"
// (an interface satisfaction, a struct field of function type assigned
// from it, a package-level var initialised from it, etc.). It also, for
// free, no longer matches a doc-comment MENTION of "RegisterPhaseHook"
// (comments are not part of the parsed ast.Ident tree in a plain
// ast.Inspect walk, whereas the old text-substring grep could not tell
// a mention from a call at all and had to rely on the open-paren
// heuristic specifically to approximate that distinction).
//
// # What this STILL cannot catch — declared, not hidden
//
// An identifier-based AST scan is a real improvement over a text grep,
// but it is not a formal proof of the invariant "no new hook gets
// registered against headless.Run's engine", and this comment says so
// plainly rather than presenting the scan as airtight:
//
//   - Reflection-based dispatch that builds the method name at runtime
//     (e.g. `reflect.ValueOf(e).MethodByName("Register" + "PhaseHook")`)
//     never places the literal identifier RegisterPhaseHook as an
//     *ast.Ident anywhere — it is a string, or several concatenated
//     strings, evaluated at runtime. This scan additionally flags any
//     *ast.BasicLit string literal containing the exact substring
//     "RegisterPhaseHook" as a best-effort, ADVISORY signal (still
//     listed as a found call site, same as an Ident match) precisely
//     because reflect.MethodByName is the most likely realistic
//     evasion once the Ident-based path is closed — but a caller that
//     builds the string by concatenation, base64, or any other runtime
//     transform defeats even that heuristic. No static source scan can
//     close this without also proving what strings a program can never
//     construct at runtime, which is undecidable in general.
//   - CORRECTION (BUG-072): an earlier version of this comment claimed
//     build-tag-excluded files were invisible to this scan, "exactly as
//     they were to the grep". Destructive-5 tested that claim directly
//     and FALSIFIED it: parser.ParseFile is called with mode 0 and never
//     evaluates //go:build / // +build constraints, so a file carrying a
//     build tag for a GOOS/GOARCH/tag this scan is not even running
//     under is STILL walked by filepath.Walk and STILL parsed and
//     scanned exactly like any other .go file — see
//     TestBuildTagFileIsScanned below, added specifically to keep this
//     claim honest by proving it mechanically rather than asserting it
//     in prose again. (Symlink-based directory/file evasion of
//     filepath.Walk was ALSO tested and falsified the same way — Go
//     followed both file and directory symlinks on the system this was
//     verified on, and the scan caught both.) What this scan genuinely
//     does not walk is narrower than the old claim: _test.go files
//     (skipped by name, deliberately — this is a test file's own
//     invariant, not a blind spot) and cgo/generated code only in the
//     sense that ordinary Go source parsing does not run cgo
//     preprocessing — a real .go file's AST is otherwise always in
//     scope, tag or no tag.
//   - It cannot prove headless.Run's OWN engine-construction code is
//     still hook-free today — same limitation the original author
//     declared and BUG-053's finding did not change: proving that
//     requires touching internal/engine/core or
//     internal/harness/headless, both out of this package's ownership.
//     What it proves is narrower and mechanical: no NEW identifier
//     reference to RegisterPhaseHook exists anywhere in the scanned
//     source tree outside the known-good file list, for ANY syntactic
//     shape Go's grammar gives that identifier a token — a strictly
//     larger set of shapes than the old grep covered, but still not
//     "every possible way to eventually call a method named
//     RegisterPhaseHook on a *core.Engine".
//   - BUG-072, the sharpest limitation, and cheaper than any of the
//     above: knownFiles above whitelists FILES, not call sites — it has
//     no notion of which SPECIFIC reference inside an already-whitelisted
//     file it originally approved. Destructive-5 demonstrated this with
//     an entirely ordinary refactor, no reflection or indirection
//     required: add a new, plainly-named exported wrapper method (e.g.
//     WireDefaultHooks) inside a file already in knownFiles that
//     internally calls RegisterPhaseHook — that file is already
//     approved, so it changes nothing this scan flags. Now call ONLY
//     that wrapper from a brand-new file, such as
//     internal/harness/headless/run.go, the exact package this test
//     exists to watch. The identifier RegisterPhaseHook appears NOWHERE
//     in that new file — scanForCallSites finds the already-known file
//     (expected, unremarkable) and never finds the new caller, so
//     TestPhaseHookCountAssertionStillTrue stays GREEN while
//     PhaseHookCountInHeadlessPath silently goes stale, which is the
//     exact failure this whole mechanism exists to prevent.
//     TestFileLevelWhitelistMissesWrapperCallSite below proves this
//     mechanically, the same way TestReflectionStringLiteralIsFlagged
//     and TestDocCommentMentionIsNotFlagged prove the scan's other
//     boundaries, so this is a demonstrated, provable limitation, not a
//     hedge in prose. It is a stronger finding than the reflection gap
//     above because it needs no unusual Go feature at all — it is a
//     routine extract-method refactor, the kind of change that lands in
//     ordinary PRs from people with no idea this guard exists, not an
//     adversarial one.
//
// # Verdict on whether a test at this layer can ever fully guarantee
// # this (asked for explicitly by BUG-053's dispatch)
//
// No. The honest answer is that NO purely syntactic scan — text grep or
// AST — run from internal/harness/synth can be a complete proof of "the
// engine headless.Run constructs has exactly N registered phase hooks",
// because Go's reflect package makes "does this identifier appear in
// the source" and "can this program invoke this method at runtime"
// provably different questions once string-built method names are
// possible. The class of syntactic scan can shrink the gap (this fix
// closes the exact demonstrated method-value bypass and a wide set of
// related indirections) but cannot close it to zero from this vantage
// point. The guarantee this package actually wants — "PhaseHookCount
// reflects what headless.Run's engine actually has registered, right
// now" — structurally belongs at RUNTIME, read from the engine itself
// (an exported accessor such as core.Engine.HookCount() or
// headless.Result.PhaseHookCount that RunPerf reads instead of asserting
// a hand-maintained constant), not as a source-level assertion about
// what code exists. That change requires touching internal/engine/core
// and/or internal/harness/headless, both outside this dispatch's file
// ownership (BUG-034's brief: "FILES YOU OWN:
// internal/harness/synth/**, .github/workflows/ci.yml"), so it is
// logged as a recommendation, not built here.
//
// On whether this belongs in internal/foundation/astgate instead of a
// bespoke scan here: DIRECTIONALLY YES, for the mechanical-detection
// half only, not the runtime-provenance half. astgate is already the
// standing home for exactly this class of problem — "an invariant about
// what code exists, checked by parsing the AST rather than trusting a
// hand swept list" (see its doc.go: it replaced a hand-swept SEC-020
// enumeration with a mechanical AST gate for an unrelated invariant,
// copy-guard coverage, using the identical shape: candidate types,
// reachable functions, a declared list of known blind spots). A
// "no unlisted RegisterPhaseHook reference" check is the same shape of
// problem and would benefit from living where FindCandidateTypes/
// FindUnguardedFunctions already do, both for reuse (a future package
// wanting the same kind of guard would not need to re-invent this scan)
// and so ITS blind spots get audited alongside astgate's own rather
// than living in a second, harder-to-find place. This dispatch does not
// move it there because astgate is being actively built by another
// agent this same wave and touching it is out of this dispatch's file
// ownership — recorded here as a direct recommendation for Bill/the
// astgate owner rather than silently left as "good enough where it is".
func TestPhaseHookCountAssertionStillTrue(t *testing.T) {
	root := repoRoot(t)

	// Known-good call sites as of this test's writing (2026-08-10): the
	// interface's own declaration/internal use inside internal/engine/
	// core, and exactly one real external caller (internal/engine/
	// invariant/wire.go) which internal/harness/headless does NOT
	// import (verified separately: `grep -rn "invariant\." internal/
	// harness/headless/*.go` finds nothing). Any file NOT in this set
	// that references the identifier RegisterPhaseHook — in ANY
	// syntactic shape the AST scan recognises, not just a direct call —
	// fails this test.
	knownFiles := map[string]bool{
		filepath.Join("internal", "engine", "core", "commands.go"):  true,
		filepath.Join("internal", "engine", "core", "engine.go"):    true,
		filepath.Join("internal", "engine", "core", "errors.go"):    true,
		filepath.Join("internal", "engine", "core", "persist.go"):   true,
		filepath.Join("internal", "engine", "core", "phase.go"):     true,
		filepath.Join("internal", "engine", "core", "subscribe.go"): true,
		filepath.Join("internal", "engine", "invariant", "wire.go"): true,
		// FEAT-082: the composition root is now the one legitimate caller
		// of RegisterPhaseHook for the real modules (plus
		// invariant.WireDaily). It registers the baseline-one hook set into
		// the SAME engine harness.headless.Run constructs, so it is the
		// expected, sanctioned reference — not a new walking-skeleton leak.
		filepath.Join("internal", "engine", "compose", "compose.go"): true,
	}

	found, err := scanForCallSites(root)
	if err != nil {
		t.Fatalf("scanning repo for RegisterPhaseHook references: %v", err)
	}

	for _, rel := range found {
		if !knownFiles[filepath.FromSlash(rel)] {
			t.Errorf("new RegisterPhaseHook reference found at %q, not in this test's known-file list — "+
				"if internal/harness/headless/run.go's engine construction now registers a real hook, "+
				"PhaseHookCountInHeadlessPath (phasehooks.go) must be updated in the SAME change, "+
				"not left asserting a stale 0", rel)
		}
	}
	if len(found) == 0 {
		t.Fatal("scan found zero RegisterPhaseHook references at all — the scan itself is broken " +
			"(internal/engine/core's own definition/use should always match), not a sign hooks were removed")
	}
}

// TestMethodValueIndirectionIsDetected reproduces BUG-053's exact
// live-verified attack as a regression test: a one-line Go method value
// captured into a local variable, then called through that variable —
// no source line contains the literal substring "RegisterPhaseHook(",
// which is precisely why the pre-fix, regexp-based version of this
// test's scan missed it. This test is RED against the old
// `RegisterPhaseHook\(` substring approach (asserted explicitly below,
// so the class this fix closes is provable, not just claimed) and GREEN
// against scanForCallSites, which finds the reference via the AST
// regardless of whether it is immediately called or captured as a
// value.
func TestMethodValueIndirectionIsDetected(t *testing.T) {
	// The method declaration lives in its own file, standing in for the
	// ALREADY-KNOWN core.Engine.RegisterPhaseHook declaration (real
	// life: internal/engine/core/engine.go, already in this test's
	// knownFiles list) — the attack is entirely about the SEPARATE
	// wiring file below, which is the one that must be a NEW,
	// previously-unknown call site.
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}
`
	// wireSrc is the exact Destructive-2 reproduction: a plain Go method
	// value captured into a local variable, then called through the
	// variable. No line in THIS file contains the literal substring
	// "RegisterPhaseHook(" with an immediately following open paren.
	wireSrc := `package headless

func wire(e *engine, kind, hook string) {
	register := e.RegisterPhaseHook
	register(kind, hook)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(engineSrc), 0o644); err != nil {
		t.Fatalf("writing engine fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hookvar_bypass.go"), []byte(wireSrc), 0o644); err != nil {
		t.Fatalf("writing attack fixture: %v", err)
	}

	// Prove the class of weakness first: the OLD guard's exact
	// text-substring approach does NOT find a call site in the WIRING
	// file, because the call happens through the local variable
	// `register(...)`, not `e.RegisterPhaseHook(...)`. If this assertion
	// ever starts failing, the fixture above no longer demonstrates the
	// bypass BUG-053 reported and must be revisited.
	oldGrep := regexp.MustCompile(`RegisterPhaseHook\(`)
	if oldGrep.MatchString(wireSrc) {
		t.Fatal("test fixture is invalid: the old substring grep DID match hookvar_bypass.go — this " +
			"fixture no longer demonstrates the method-value bypass the pre-fix guard missed")
	}

	// The AST-based scan must catch it anyway: Go cannot take a method
	// value of, or call, RegisterPhaseHook without the identifier
	// RegisterPhaseHook appearing as an *ast.Ident somewhere in the
	// syntax tree, regardless of whether that identifier is immediately
	// followed by a call. It legitimately also finds engine.go (the
	// declaration itself, exactly as the real grep-based test's
	// knownFiles list already expects for core/engine.go) — the
	// regression assertion cares specifically that hookvar_bypass.go,
	// the NEW call site, is among the results.
	found, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}
	foundBypass := false
	for _, rel := range found {
		if rel == "hookvar_bypass.go" {
			foundBypass = true
		}
	}
	if !foundBypass {
		t.Fatalf("expected the AST scan to catch the method-value bypass in hookvar_bypass.go, got %v", found)
	}
}

// TestReflectionStringLiteralIsFlagged is the advisory half of the scan
// (see TestPhaseHookCountAssertionStillTrue's doc comment, "What this
// STILL cannot catch"): a string literal spelling out "RegisterPhaseHook"
// verbatim — the shape a reflect.MethodByName("RegisterPhaseHook") call
// would take — is flagged too, as a best-effort signal, even though it
// is NOT a proof of dynamic dispatch (and a caller who builds the string
// at runtime, e.g. by concatenation, defeats this heuristic entirely,
// which the doc comment states plainly rather than hides).
func TestReflectionStringLiteralIsFlagged(t *testing.T) {
	src := `package headless

import "reflect"

func wireByReflection(e any, kind, hook string) {
	m := reflect.ValueOf(e).MethodByName("RegisterPhaseHook")
	m.Call([]reflect.Value{reflect.ValueOf(kind), reflect.ValueOf(hook)})
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reflect_bypass.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing attack fixture: %v", err)
	}

	found, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}
	if len(found) != 1 || found[0] != "reflect_bypass.go" {
		t.Fatalf("expected the scan to flag the reflect.MethodByName string literal in reflect_bypass.go, got %v", found)
	}
}

// TestDocCommentMentionIsNotFlagged proves the AST scan's improvement
// over the old text grep in the OTHER direction too: a doc comment that
// merely NAMES RegisterPhaseHook in prose (this package's own files do
// this routinely, e.g. phasehooks.go's own doc comment) must not be
// treated as a call site. The old grep relied entirely on the
// open-paren heuristic to approximate this distinction (and a comment
// like "mirrors Engine.RegisterPhaseHook(...)'s ordering" could still
// have tripped it); the AST scan gets this for free because go/parser's
// comment nodes are never *ast.Ident/*ast.SelectorExpr/*ast.BasicLit
// nodes in the walked tree.
func TestDocCommentMentionIsNotFlagged(t *testing.T) {
	src := `package headless

// wire does NOT call RegisterPhaseHook — this comment just mentions the
// name in prose, the way phasehooks.go's own doc comment does.
func wire() {}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "comment_only.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	found, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected the scan to ignore a doc-comment-only mention, got %v", found)
	}
}

// TestBuildTagFileIsScanned corrects a false claim a prior version of
// TestPhaseHookCountAssertionStillTrue's doc comment made ("build-tag-
// excluded files ... are invisible to it, exactly as they were to the
// grep") — Destructive-5 tested that claim directly and falsified it.
// parser.ParseFile is called with mode 0, which never evaluates
// //go:build / // +build constraints, so a file carrying a build tag
// this test process is NOT running under is still walked by
// filepath.Walk and still parsed and scanned like any other .go file.
// This test proves the TRUE behaviour mechanically (a build-tagged
// fixture containing a RegisterPhaseHook reference IS found) so the
// corrected doc comment above stays honest rather than swapping one
// unverified claim for another.
func TestBuildTagFileIsScanned(t *testing.T) {
	src := `//go:build ignore

package headless

type engine struct{}

func wireIgnored(e *engine) {
	e.RegisterPhaseHook("x", "y")
}

func (e *engine) RegisterPhaseHook(kind, hook string) {}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "buildtag_bypass.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing build-tagged fixture: %v", err)
	}

	found, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}
	if len(found) != 1 || found[0] != "buildtag_bypass.go" {
		t.Fatalf("expected the scan to find buildtag_bypass.go despite its //go:build tag (BUG-072 doc correction), got %v", found)
	}
}

// TestFileLevelWhitelistMissesWrapperCallSite is BUG-072's core
// regression test: it proves, mechanically, the sharpest declared
// limitation in TestPhaseHookCountAssertionStillTrue's doc comment
// above — that knownFiles whitelists FILES, not call sites, so an
// ordinary extract-method refactor defeats the guard with zero
// reflection or indirection.
//
// Fixture shape: engine.go stands in for an already-whitelisted file
// (e.g. the real internal/engine/core/engine.go) and gains an ordinary,
// statically-named exported wrapper method, WireDefaultHooks, that
// internally calls RegisterPhaseHook. run.go stands in for
// internal/harness/headless/run.go — NOT whitelisted in real life — and
// calls ONLY e.WireDefaultHooks(); the identifier RegisterPhaseHook
// never appears in run.go at all.
//
// The assertion that matters: scanForCallSites finds engine.go (expected
// — it is where the identifier textually lives) but does NOT find
// run.go, even though run.go is the file that actually causes a new hook
// to be registered. If TestPhaseHookCountAssertionStillTrue's knownFiles
// treated engine.go as "approved" (as it must, since it is a real,
// already-known file in production), this exact scenario would land
// silently: a brand-new call site, invisible to the file-level check,
// while PhaseHookCountInHeadlessPath keeps asserting a stale count. This
// test documents the limitation as proven, not hidden — the fix for it
// is the runtime accessor recommendation in
// TestPhaseHookCountAssertionStillTrue's "Verdict" section, not
// anything buildable from this file-scoped scan.
func TestFileLevelWhitelistMissesWrapperCallSite(t *testing.T) {
	// Stands in for an already-whitelisted file (e.g. the real
	// internal/engine/core/engine.go) that has gained an ordinary
	// wrapper method — a ROUTINE refactor, not an attack.
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

// WireDefaultHooks is an ordinary extract-method wrapper — no
// reflection, no indirection, ordinary Go a code reviewer would wave
// through without a second thought.
func (e *engine) WireDefaultHooks() {
	e.RegisterPhaseHook("default", "hook")
}
`
	// Stands in for internal/harness/headless/run.go: calls ONLY the
	// wrapper. RegisterPhaseHook, as an identifier, appears nowhere in
	// this file.
	runSrc := `package headless

func Run(e *engine) {
	e.WireDefaultHooks()
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(engineSrc), 0o644); err != nil {
		t.Fatalf("writing engine fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(runSrc), 0o644); err != nil {
		t.Fatalf("writing run fixture: %v", err)
	}

	found, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}

	foundEngine, foundRun := false, false
	for _, rel := range found {
		if rel == "engine.go" {
			foundEngine = true
		}
		if rel == "run.go" {
			foundRun = true
		}
	}
	if !foundEngine {
		t.Fatalf("expected the scan to find engine.go (the identifier's real declaration/call site), got %v", found)
	}
	if foundRun {
		t.Fatalf("scan unexpectedly found run.go — BUG-072's reproduction fixture no longer demonstrates the file-level-whitelist gap (the wrapper indirection was supposed to make run.go invisible to an identifier scan); revisit this fixture, got %v", found)
	}
}

// scanForCallSites walks root for every non-test .go file and returns
// the root-relative (forward-slash) paths of files containing an
// AST-level reference to the identifier "RegisterPhaseHook" — as a
// function/method declaration, a call, a method value/selector
// reference in any position, or (best-effort, advisory) a string
// literal spelling the name verbatim (the reflect.MethodByName shape).
// See TestPhaseHookCountAssertionStillTrue's doc comment for the full
// account of what this catches, what it still cannot, and why (BUG-053).
func scanForCallSites(root string) ([]string, error) {
	const identName = "RegisterPhaseHook"

	fset := token.NewFileSet()
	var found []string

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			// A file this package cannot even parse cannot be
			// mechanically verified — report it as a scan error rather
			// than silently skipping it (GR#1/#17: a scan that quietly
			// ignores what it cannot read is not a guarantee).
			return fmt.Errorf("parsing %s: %w", path, parseErr)
		}

		matched := false
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if node.Name == identName {
					matched = true
				}
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(node.Value, identName) {
					matched = true
				}
			}
			return true
		})
		if matched {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return found, nil
}

// repoRoot walks up from this test file's own source location to the
// directory containing go.mod — resilient to `go test` being invoked
// from any working directory (CI, a subpackage, etc.), unlike relying
// on os.Getwd().
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked up to filesystem root without finding go.mod")
		}
		dir = parent
	}
}
