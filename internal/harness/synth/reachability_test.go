package synth

// This file is BUG-087/BUG-072's closure for the ordinary-indirection
// sub-case ASM-354 wrongly declared unclosable: a same-package-agnostic,
// whole-repo, NAIVE DIRECT-CALL reachability scan, layered on top of
// scanForCallSites' identifier scan (phasehooks_test.go). It answers a
// different question than that scan does:
//
//	scanForCallSites:        "does the identifier RegisterPhaseHook
//	                          appear ANYWHERE in the scanned tree,
//	                          outside a known-good file?"
//	reachableFromHeadlessRun: "starting from headless.Run, can a chain
//	                          of ordinary function/method CALLS reach a
//	                          function whose body contains that
//	                          identifier?"
//
// The second question is what BUG-072 showed the first one cannot
// answer: knownFiles above is a per-FILE whitelist, so an ordinary
// wrapper landing in an already-whitelisted file is invisible to it.
// Reachability instead asks "can headless.Run's call graph get there at
// all", which does not care which file the identifier's declaration
// happens to live in.
//
// # What this resolves, and what it does not — each proven by a test
// below, not asserted in this comment (BUG-075: verify what you cite)
//
//   - Ordinary same-package and cross-package function calls: YES.
//     TestReachability_CatchesOrdinaryWrapperAcrossFiles.
//   - Method calls through a concrete receiver (e.foo()): YES, but only
//     because this scan does ZERO type/interface resolution — see
//     "false positives" below for the cost of that.
//     TestReachability_CatchesOrdinaryWrapperAcrossFiles.
//   - Method values captured into a local variable and called through it
//     (`register := e.RegisterPhaseHook; register(...)`): YES — the
//     identifier appears directly in the SAME function body that does
//     the capturing, so the existing per-node identifier check (shared
//     with scanForCallSites' technique) flags that function directly,
//     with no need to resolve the call through the variable at all.
//     TestReachability_MethodValueThroughLocalVarIsCaught.
//   - Closures (a FuncLit nested inside a named function, later called
//     through a variable the outer function returned): YES, but only by
//     the same coincidence as method values — go/ast's Inspect walks
//     into nested FuncLits when scanning the enclosing FuncDecl's body,
//     so the identifier is found inside the OUTER function's own
//     subtree the moment the closure is constructed, before this scan
//     ever needs to reason about how the closure gets invoked.
//     TestReachability_ClosureConstructedInReachableFuncIsCaught.
//   - Interface dispatch (a call through an interface-typed value,
//     resolving at runtime to one of several concrete implementations):
//     caught, but NOT because the scan understands interfaces — it does
//     not. Every method call is resolved by matching the call's method
//     NAME against every method of that name in the whole repository,
//     regardless of receiver type or package. If a same-named method on
//     ANY type reaches the target identifier, the scan reports the
//     path as reachable even though the interface call this scan
//     "resolved" might, at runtime, never actually dispatch to that
//     implementation. TestReachability_InterfaceDispatchNameCollisionOverApproximates
//     demonstrates the concrete cost: a call that, at runtime, can NEVER
//     reach the identifier-bearing implementation is still reported
//     reachable, because an unrelated same-named method elsewhere does
//     reach it — a false positive from skipping type resolution.
//   - Function values passed as ordinary parameters and invoked through
//     the PARAMETER's name, not the original function's name (e.g.
//     `wire(e, register)` then `fn(e, "y")` inside wire, where fn is
//     register's formal parameter): NOT resolved. This scan only
//     follows a call's own Fun expression (an *ast.Ident or the Sel of
//     an *ast.SelectorExpr) — it does not track dataflow of a function
//     value passed as an ARGUMENT and later invoked under a different
//     local name. TestReachability_FunctionValuePassedAsParameterEvades
//     proves this is a real, demonstrated gap, not a hedge.
//   - Package-level FUNCTION-TYPED VARS (the wire-table/handler-registry
//     idiom: `var DefaultWire = wireDefaultHook` declared inside an
//     already-whitelisted file, called elsewhere as `DefaultWire(e)`):
//     NOT resolved — and NOT in this list's original draft. Found by
//     this chain's own Destructive (BUG-101), which is worth recording
//     plainly: the declared-gap list was narrower than reality, the
//     exact recurring failure this project's process doc names, on the
//     very file whose job is declaring gaps honestly.
//     buildReachabilityGraph only visits *ast.FuncDecl bodies, so an
//     *ast.GenDecl var initializer contributes no node and no alias
//     edge, and reachableFromEntry's byName map — keyed only by
//     DECLARATION names — gives the call through the var's name nothing
//     to resolve to; the BFS stops dead there. Materially worse than
//     the parameter gap above in KIND, not just instance: it also
//     evades scanForCallSites (the identifier's only textual appearance
//     is inside the whitelisted file), so it defeats both defence
//     layers at once. TestReachability_PackageLevelFuncVarEvades proves
//     it. The real fix — walking GenDecl var initializers and treating
//     a func-valued var as an alias edge — is BUG-101's scope, not a
//     comment's.
//   - reflect.MethodByName with a runtime-built string: NOT resolved,
//     same as scanForCallSites — no *ast.Ident named RegisterPhaseHook
//     exists anywhere in that call's syntax at all, so there is nothing
//     for either scan to match on. TestReachability_ReflectionStillEvades
//     proves gap (2) survives this scan exactly as declared.
//
// # False positives (a drift guard that fires on legitimate code gets
// # deleted — the brief's own framing)
//
// The name-only, no-type-resolution method matching above is a
// deliberate over-approximation: it can connect a call to an UNRELATED
// method that merely shares a name (see the interface-dispatch test).
// That is the direction of error worth having for a guard whose failure
// mode is "a stale perf-provenance number gets quoted as if it means
// something it does not" — a spurious CI failure is a one-line
// annotation to fix (rename the unrelated method, or special-case it);
// a spurious PASS is silent and only discovered by someone reading the
// number skeptically, which is the exact failure BUG-034 exists to
// prevent. Two concrete mitigations against that cost, both already
// true of the identifier-level scan and inherited unchanged here rather
// than re-decided:
//
//   - _test.go files are skipped (same as scanForCallSites) — test
//     helpers and mocks routinely define same-named methods for
//     unrelated fakes, which would otherwise be the single biggest
//     source of false-positive name collisions in this repository.
//   - Generated code is NOT special-cased. Neither scan inspects "//
//     Code generated" header comments (comments are invisible to
//     ast.Inspect's node walk by construction) — this is a real gap if
//     this repo starts vendoring or generating code that happens to
//     declare a same-named method, but it is not a NEW gap: it is the
//     same posture scanForCallSites already has, so this dispatch is
//     not narrowing an existing guarantee by leaving it as-is.
//   - Build-tagged files are STILL walked and parsed regardless of tag,
//     for the same reason BUG-072 corrected the doc comment above:
//     parser.ParseFile with mode 0 never evaluates //go:build
//     constraints. TestReachability_BuildTagFileParticipates proves this
//     scan inherits that behaviour rather than assuming it does.
//
// In this specific repository, RegisterPhaseHook-adjacent method names
// (RegisterPhaseHook itself, WireDefaultHooks, and similar) are
// distinctive enough that an accidental same-name collision is unlikely
// today; if one ever fires, the fix is to narrow matching by receiver
// type (which requires a type-checked pass, e.g. go/types or
// x/tools/go/packages, not just go/parser) — deliberately NOT built
// here to avoid gold-plating a guard whose demonstrated gap (the
// parameter-passed-function-value case above) is unrelated to type
// precision.
//
// # Is x/tools/go/callgraph/cha worth adding as a dependency?
//
// Argued both ways, verdict at the end:
//
// FOR: cha (Class Hierarchy Analysis) is exactly the off-the-shelf tool
// ASM-354 itself named as available for the general points-to-adjacent
// problem, and it would replace this file's name-only method matching
// with real receiver-type-aware resolution, closing the interface-
// dispatch false-positive class above outright, and doing so with code
// this project did not have to write or maintain the correctness of.
// It composes with go/packages, which DOES evaluate build constraints
// correctly (unlike raw go/parser), incidentally fixing the build-tag
// over-inclusion noted above as a side effect rather than a design goal.
//
// AGAINST: it is a new module dependency (golang.org/x/tools) pulled
// into a project whose go.mod today has exactly one dependency
// (tcell) for the entire TUI, in a TEST-ONLY guard, not shipped code —
// GR#10 (dependency update discipline) and the general "every
// dependency is something to keep patched" cost apply to test tooling
// too. go/packages-based loading is also drastically slower than
// go/parser (it type-checks the whole module, which showed up as a
// real cost class earlier in this project per BUG-034's own framing of
// perf-CI budget), a real concern for something that runs on every `go
// test`. And critically: cha would not close the ACTUAL demonstrated
// gap in this dispatch (function value passed as a parameter and
// invoked under a different local name) — that is a dataflow problem,
// which CHA does not solve either; CHA only sharpens METHOD dispatch
// resolution, a precision problem this dispatch's naive version
// deliberately over-approximates in the SAFE direction (false positive,
// not false negative) rather than under-approximates.
//
// VERDICT: not worth it FOR THIS GAP. cha would trade a real dependency
// and a real speed cost to convert a documented, safe-direction false
// positive (interface dispatch) into a precise result, while leaving
// the actual proven miss (parameter-passed function values) exactly as
// open as it is today. It becomes worth it the day this guard's false
// positive RATE in practice (not in theory) makes the guard annoying
// enough to be worked around — that has not happened yet; there is
// exactly one real caller of RegisterPhaseHook in this repository
// (core.Engine's own declaration) and the guard has never fired
// spuriously against real code. Recorded as a "reconsider if" rather
// than built preemptively.
//
// # Should this remain a test at all?
//
// No — and the code's own doc comment (phasehooks.go) already says so:
// the guarantee this package actually wants is "PhaseHookCount reflects
// what headless.Run's engine has registered, RIGHT NOW", which is a
// runtime fact, not a source-code fact. Every version of this guard,
// including this one, answers "as of the source tree on disk right
// now, is a call chain to RegisterPhaseHook reachable" — sound to the
// extent of what it resolves, but still a proxy for the real question,
// and proxies drift. The runtime fix outside this dispatch's ownership
// (internal/engine/core, internal/harness/headless) would look like:
//
//	// On core.Engine (internal/engine/core/engine.go):
//	func (e *Engine) HookCount() int {
//	    e.mu.Lock(); defer e.mu.Unlock()
//	    n := 0
//	    for _, hs := range e.hooks { n += len(hs) }
//	    return n
//	}
//
//	// In headless.Run, after core.NewEngine(opts...):
//	result.PhaseHookCount = e.HookCount()  // replaces the manual
//	                                        // PhaseHookCountInHeadlessPath()
//	                                        // constant in perf.go entirely
//
// Cost: one exported accessor + one field-read call site, both inside
// files this dispatch is not permitted to touch (BUG-034's brief: FILES
// YOU OWN: internal/harness/synth/**, .github/workflows/ci.yml) — this
// is not a hard problem, it is an ownership boundary. Once it lands,
// this entire file (and scanForCallSites, and knownFiles) can be
// deleted outright: RunPerf would assert against the number the engine
// itself reports, which cannot drift by construction, and no static
// scan — naive or CHA-based — would be needed at all. Until then, this
// scan is exactly what BUG-087 asked for: the cheapest thing that
// closes BUG-072's DEMONSTRATED gap, explicitly a stopgap, not a
// substitute for the runtime fix.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registerPhaseHookIdent is the identifier this whole file exists to
// trace reachability to — kept as one constant so the graph builder and
// scanForCallSites' own const (phasehooks_test.go) can never silently
// drift apart in spelling.
const registerPhaseHookIdent = "RegisterPhaseHook"

// reachFuncNode is one function or method declaration discovered while
// walking a source tree: enough identity for a report (file + optional
// receiver type + name), whether the target identifier appears directly
// in its own body (any syntactic shape — declaration, call, method
// value, or best-effort string literal, the same predicate
// scanForCallSites uses), and the simple names of every function/method
// this body's own top-level calls reference.
type reachFuncNode struct {
	file     string
	recv     string
	name     string
	hasIdent bool
	calls    []string
}

// buildReachabilityGraph parses every non-test .go file under root (same
// walk policy as scanForCallSites: skips .git, node_modules, and
// _test.go files; does NOT skip build-tagged files, because go/parser
// mode 0 never evaluates build constraints — TestReachability_
// BuildTagFileParticipates proves this scan inherits that behaviour) and
// returns one reachFuncNode per top-level function or method declaration
// found, in no particular order.
func buildReachabilityGraph(root string) ([]*reachFuncNode, error) {
	fset := token.NewFileSet()
	var nodes []*reachFuncNode

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
			// Same posture as scanForCallSites: a file this package
			// cannot parse cannot be mechanically verified, so this is
			// reported as a scan error, not silently skipped (GR#1/#17).
			return fmt.Errorf("parsing %s: %w", path, parseErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			node := &reachFuncNode{
				file: rel,
				recv: receiverTypeName(fd.Recv),
				name: fd.Name.Name,
			}

			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if x.Name == registerPhaseHookIdent {
						node.hasIdent = true
					}
				case *ast.BasicLit:
					if x.Kind == token.STRING && strings.Contains(x.Value, registerPhaseHookIdent) {
						node.hasIdent = true
					}
				case *ast.CallExpr:
					switch fn := x.Fun.(type) {
					case *ast.Ident:
						node.calls = append(node.calls, fn.Name)
					case *ast.SelectorExpr:
						node.calls = append(node.calls, fn.Sel.Name)
					}
				}
				return true
			})

			nodes = append(nodes, node)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return nodes, nil
}

// receiverTypeName extracts a method's receiver type name (e.g. "engine"
// for both `func (e *engine)` and `func (e engine)`), or "" for a
// receiver-less plain function.
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// reachableFromEntry performs a breadth-first search over the naive call
// graph implied by nodes, starting from the function/method identified
// by entryFile/entryRecv/entryName, and returns the first node it visits
// whose own body directly contains the target identifier (nil, nil if
// none is reachable). Matching a call to its callee node is by SIMPLE
// NAME ONLY across the whole node set, regardless of package or receiver
// type — a deliberate over-approximation; see this file's doc comment
// for exactly what that resolves, what it does not, and what it costs.
func reachableFromEntry(nodes []*reachFuncNode, entryFile, entryRecv, entryName string) (*reachFuncNode, error) {
	byName := make(map[string][]*reachFuncNode)
	var entry *reachFuncNode
	for _, n := range nodes {
		byName[n.name] = append(byName[n.name], n)
		if n.file == entryFile && n.recv == entryRecv && n.name == entryName {
			entry = n
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("entry point %s#%s.%s not found among %d scanned declarations", entryFile, entryRecv, entryName, len(nodes))
	}

	visited := map[*reachFuncNode]bool{entry: true}
	queue := []*reachFuncNode{entry}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.hasIdent {
			return cur, nil
		}
		for _, calleeName := range cur.calls {
			for _, callee := range byName[calleeName] {
				if !visited[callee] {
					visited[callee] = true
					queue = append(queue, callee)
				}
			}
		}
	}
	return nil, nil
}

// TestPhaseHookReachabilityFromHeadlessRun is the new drift guard this
// dispatch adds (BUG-087/BUG-072). Where TestPhaseHookCountAssertionStillTrue
// (phasehooks_test.go) asks "does the identifier appear anywhere in the
// tree outside a known-good FILE", this asks "can headless.Run's own
// call graph reach it" — the question that closes BUG-072's demonstrated
// wrapper-inside-an-already-whitelisted-file gap, because reachability
// does not care which file the identifier's declaration happens to live
// in, only whether a call chain gets there from the real entry point.
func TestPhaseHookReachabilityFromHeadlessRun(t *testing.T) {
	root := repoRoot(t)
	nodes, err := buildReachabilityGraph(root)
	if err != nil {
		t.Fatalf("building reachability graph: %v", err)
	}
	entryFile := filepath.ToSlash(filepath.Join("internal", "harness", "headless", "run.go"))
	hit, err := reachableFromEntry(nodes, entryFile, "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit != nil {
		t.Errorf("headless.Run's call graph now reaches RegisterPhaseHook via %s (recv %q, func %q) — "+
			"PhaseHookCountInHeadlessPath (phasehooks.go) must be updated in the SAME change, not left "+
			"asserting a stale 0 (BUG-087/BUG-072)", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_CatchesOrdinaryWrapperAcrossFiles is BUG-072's exact
// reproduction (identical fixture to TestFileLevelWhitelistMissesWrapperCallSite
// above), proved RED against the old identifier-only scan and GREEN
// against the new reachability scan in the same test, so the
// before/after is mechanical rather than asserted in two places that
// could drift apart.
func TestReachability_CatchesOrdinaryWrapperAcrossFiles(t *testing.T) {
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

// WireDefaultHooks is an ordinary extract-method wrapper — no
// reflection, no indirection.
func (e *engine) WireDefaultHooks() {
	e.RegisterPhaseHook("default", "hook")
}
`
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

	// RED: the identifier-only scan this dispatch is layered on top of
	// does not, and structurally cannot, find run.go — this is BUG-072's
	// finding restated as a mechanical assertion, not prose.
	identFound, err := scanForCallSites(dir)
	if err != nil {
		t.Fatalf("scanForCallSites: %v", err)
	}
	for _, rel := range identFound {
		if rel == "run.go" {
			t.Fatalf("scanForCallSites unexpectedly found run.go directly — this fixture no longer " +
				"demonstrates BUG-072's gap, revisit before trusting the GREEN assertion below")
		}
	}

	// GREEN: the reachability scan finds it, because it follows the call
	// from Run into WireDefaultHooks and WireDefaultHooks' own body
	// contains the identifier.
	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit == nil {
		t.Fatal("expected the reachability scan to find RegisterPhaseHook reachable from Run through " +
			"WireDefaultHooks — BUG-072's gap is not closed")
	}
	if hit.file != "engine.go" || hit.name != "WireDefaultHooks" {
		t.Errorf("expected the hit to be engine.go's WireDefaultHooks, got %s#%s.%s", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_MethodValueThroughLocalVarIsCaught proves a method
// value captured into a local variable and called through it is caught
// — not because the scan resolves the call through the variable (it
// does not track variable bindings at all), but because the identifier
// appears directly, as an *ast.Ident, in the SAME function body that
// does the capturing, which the per-node hasIdent check already covers.
func TestReachability_MethodValueThroughLocalVarIsCaught(t *testing.T) {
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func Wire(e *engine) {
	register := e.RegisterPhaseHook
	register("default", "hook")
}
`
	runSrc := `package headless

func Run(e *engine) {
	Wire(e)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(engineSrc), 0o644); err != nil {
		t.Fatalf("writing engine fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(runSrc), 0o644); err != nil {
		t.Fatalf("writing run fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit == nil {
		t.Fatal("expected the reachability scan to catch the method-value indirection through Wire")
	}
}

// TestReachability_ClosureConstructedInReachableFuncIsCaught proves a
// closure (FuncLit) containing the target identifier is caught the
// moment the function that CONSTRUCTS it is reachable — not because this
// scan resolves the closure's eventual invocation, but because
// go/ast.Inspect walks into nested FuncLits when scanning the enclosing
// FuncDecl's body, so the identifier is part of that outer function's
// own subtree.
func TestReachability_ClosureConstructedInReachableFuncIsCaught(t *testing.T) {
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func makeHook(e *engine) func() {
	return func() {
		e.RegisterPhaseHook("default", "hook")
	}
}
`
	runSrc := `package headless

func Run(e *engine) {
	h := makeHook(e)
	h()
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(engineSrc), 0o644); err != nil {
		t.Fatalf("writing engine fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(runSrc), 0o644); err != nil {
		t.Fatalf("writing run fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit == nil {
		t.Fatal("expected the reachability scan to catch the identifier inside the closure makeHook constructs")
	}
	if hit.name != "makeHook" {
		t.Errorf("expected the hit to be makeHook (the identifier lives in its subtree even though the "+
			"closure's OWN invocation, h(), is never resolved) — got %s#%s.%s", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_InterfaceDispatchNameCollisionOverApproximates proves
// the concrete cost of skipping type resolution: Run's actual, real,
// only-ever runtime dispatch target is fakePublisher.Notify, which never
// touches RegisterPhaseHook — the SOUND answer is "not reachable". This
// scan instead reports reachable, because it resolves p.Notify() by
// matching the method NAME "Notify" against every method named "Notify"
// in the whole scanned tree, and a completely unrelated type
// (unrelatedType, never constructed, never assigned to p, never passed
// to Run) happens to also declare a Notify method that does reach the
// identifier. This is the false-positive direction the scan's doc
// comment argues is the safe one to over-approximate toward.
func TestReachability_InterfaceDispatchNameCollisionOverApproximates(t *testing.T) {
	src := `package headless

type Publisher interface{ Notify() }

type fakePublisher struct{}

func (f *fakePublisher) Notify() {}

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

// unrelatedType is never constructed anywhere in this fixture. At
// runtime it is unreachable by construction. It exists only to share a
// method name with fakePublisher.Notify.
type unrelatedType struct{ e *engine }

func (u *unrelatedType) Notify() {
	u.e.RegisterPhaseHook("default", "hook")
}

func Run() {
	var p Publisher = &fakePublisher{}
	p.Notify()
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit == nil {
		t.Fatal("expected the name-only method match to (wrongly, but safely) report reachable through " +
			"unrelatedType.Notify — if this now returns not-reachable, the over-approximation behaviour " +
			"documented at the top of this file has changed and that doc comment needs revisiting")
	}
	if hit.name != "Notify" || hit.recv != "unrelatedType" {
		t.Errorf("expected the (false-positive) hit to be unrelatedType.Notify, got %s#%s.%s", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_FunctionValuePassedAsParameterEvades proves a real,
// demonstrated gap: register is passed as an ordinary function-valued
// ARGUMENT to wire, which invokes it through its own parameter name fn —
// a different identifier entirely from "register". This scan only
// follows a call's own Fun expression; it does not track dataflow of a
// function value threaded through a parameter, so register (which does
// contain the identifier) is never visited even though Run, at runtime,
// unconditionally reaches it.
func TestReachability_FunctionValuePassedAsParameterEvades(t *testing.T) {
	src := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func register(e *engine, hook string) {
	e.RegisterPhaseHook("default", hook)
}

func wire(e *engine, fn func(*engine, string)) {
	fn(e, "hook")
}

func Run(e *engine) {
	wire(e, register)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected the parameter-passed function value to EVADE this scan (a demonstrated, "+
			"declared gap, not a hedge) — got a hit at %s#%s.%s; if this now catches it, the gap this "+
			"test documents has closed and the doc comment at the top of this file needs revisiting", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_PackageLevelFuncVarEvades proves BUG-101's gap — a
// package-level function-typed var forwarding to the real registrar —
// EVADES both this scan and (by construction of the fixture: the
// identifier RegisterPhaseHook appears only in the file that would be
// whitelisted) the identifier scan. Found by the fix chain's own
// Destructive, live-verified, and recorded here the moment it was found
// so the declared-gap list stays as wide as reality (the original list
// shipped without it). If this test ever FAILS with a hit, BUG-101's
// real fix (GenDecl alias edges) has landed and both this test's
// expectation and the doc list above must flip to "resolved".
func TestReachability_PackageLevelFuncVarEvades(t *testing.T) {
	engineSrc := `package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func wireDefaultHook(e *engine) {
	e.RegisterPhaseHook("default", "hook")
}

var DefaultWire = wireDefaultHook
`
	runSrc := `package headless

func Run(e *engine) {
	DefaultWire(e)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "engine.go"), []byte(engineSrc), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(runSrc), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected the package-level func-typed var to EVADE this scan (BUG-101's demonstrated, "+
			"now-declared gap) — got a hit at %s#%s.%s; if this now catches it, BUG-101's fix has landed and "+
			"this test plus the doc list at the top of this file must flip to 'resolved'", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_ReflectionStillEvades proves gap (2) — reflect.
// MethodByName with a runtime-CONCATENATED (not literal) string —
// survives this scan exactly as it survives scanForCallSites, and for
// the identical reason: no *ast.Ident or whole *ast.BasicLit spelling
// "RegisterPhaseHook" exists anywhere in wireByReflection's syntax, so
// neither the exact-match nor the advisory string-literal heuristic has
// anything to match on.
func TestReachability_ReflectionStillEvades(t *testing.T) {
	src := `package headless

import "reflect"

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func wireByReflection(e any, kind, hook string) {
	name := "Register" + "PhaseHook"
	m := reflect.ValueOf(e).MethodByName(name)
	m.Call([]reflect.Value{reflect.ValueOf(kind), reflect.ValueOf(hook)})
}

func Run(e *engine) {
	wireByReflection(e, "default", "hook")
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected reflect.MethodByName with a concatenated string to evade this scan (gap 2, "+
			"genuinely undecidable statically) — got a hit at %s#%s.%s", hit.file, hit.recv, hit.name)
	}
}

// TestReachability_BuildTagFileParticipates proves this scan inherits
// scanForCallSites' corrected (BUG-072) behaviour rather than assuming
// it: parser.ParseFile mode 0 never evaluates //go:build constraints, so
// a build-tagged file IS walked, parsed, and included in the graph, and
// a reachable call into it is found exactly like any other file.
func TestReachability_BuildTagFileParticipates(t *testing.T) {
	taggedSrc := `//go:build ignore

package headless

type engine struct{}

func (e *engine) RegisterPhaseHook(kind, hook string) {}

func TaggedWire(e *engine) {
	e.RegisterPhaseHook("default", "hook")
}
`
	runSrc := `package headless

func Run(e *engine) {
	TaggedWire(e)
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tagged.go"), []byte(taggedSrc), 0o644); err != nil {
		t.Fatalf("writing tagged fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.go"), []byte(runSrc), 0o644); err != nil {
		t.Fatalf("writing run fixture: %v", err)
	}

	nodes, err := buildReachabilityGraph(dir)
	if err != nil {
		t.Fatalf("buildReachabilityGraph: %v", err)
	}
	hit, err := reachableFromEntry(nodes, "run.go", "", "Run")
	if err != nil {
		t.Fatalf("reachableFromEntry: %v", err)
	}
	if hit == nil {
		t.Fatal("expected the build-tagged file's TaggedWire to be found reachable despite its //go:build tag")
	}
	if hit.file != "tagged.go" {
		t.Errorf("expected the hit to be in tagged.go, got %s", hit.file)
	}
}
