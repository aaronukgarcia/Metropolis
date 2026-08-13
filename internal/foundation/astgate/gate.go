package astgate

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SEC-048: loadPackages/resolveRepoRoot's three internal scan-failure
// sites (registered MET-F700/MET-F701/MET-F702, F700-F799) mint their own
// correlation ID inline via errs.NewCorrelationID() rather than accepting
// one as a parameter — mirroring internal/engine/invariant/registry.go:96's
// precedent for a call site with no caller-supplied ID. Run/loadPackages/
// resolveRepoRoot have no real caller today besides gate_test.go (astgate
// has no CLI/CI wrapper yet — it runs as an ordinary go test), so there is
// no correlation ID anywhere upstream to thread down; adding a
// correlationID parameter to Run's exported signature would touch every
// existing call site for no observability gain over minting at the point
// of use. ASM-435 leaves this choice to the builder — this is that choice,
// logged in the SEC-048 report.

// guardMethodName is the one guard-call spelling this gate recognises
// (see doc.go's "Known blind spots" — a differently named guard is
// invisible to this gate by design, and that limitation is declared,
// not silently absorbed).
const guardMethodName = "checkNotCopied"

// ringLikePattern is the name-based heuristic FindFloodRisks uses to
// decide whether a touched package-level variable "looks" like a
// fixed-capacity shared resource (see doc.go's blind-spot note on why
// this is a heuristic, not a structural proof).
var ringLikePattern = regexp.MustCompile(`(?i)ring|buffer`)

// pkgFile is one parsed, non-test .go file plus its relative-to-repo-root
// path, kept together for location info in violation messages.
type pkgFile struct {
	AST  *ast.File
	Rel  string // forward-slash, relative to repo root
	Fset *token.FileSet
}

// CandidateType is one struct type declaration matching AC-1's shape: a
// sync.Mutex/RWMutex value field plus an aliasable reference field.
type CandidateType struct {
	Name string
	Dir  string // directory (package) it was found in, relative to repo root
	File string
	Line int
}

// FuncKind distinguishes AC-3(a)'s receiver-method path from AC-3(b)'s
// package-level-function-by-parameter path — the exact split whose
// second half BUG-024 exists because nine hand-sweeps only ever did the
// first half.
type FuncKind int

const (
	// KindReceiverMethod is func (x T) M(...) / func (x *T) M(...).
	KindReceiverMethod FuncKind = iota
	// KindParameterFunc is a package-level function taking T/*T as a
	// parameter, e.g. func SetSink(l *Logger) — AC-3(b), the documented
	// blind spot this gate exists to close.
	KindParameterFunc
	// KindFieldAccess is SEC-049's fix: a method declared on a WRAPPING
	// type W (e.g. WorldAPI) that holds a candidate type C (e.g. World)
	// via a struct field — embedded/anonymous, named-by-value, or a
	// pointer field, e.g. `w *World` — reaches C through a field-then-
	// method access chain (a.w.M(...)) rather than a direct method call
	// on C itself or C arriving as a parameter. Neither
	// KindReceiverMethod nor KindParameterFunc recognises this shape,
	// because W is not itself a candidate type (it has no mutex field of
	// its own) and does not take C by parameter (C arrives already
	// embedded in W's own struct layout) — see findFieldReachableFuncs.
	KindFieldAccess
)

func (k FuncKind) String() string {
	switch k {
	case KindReceiverMethod:
		return "receiver method"
	case KindFieldAccess:
		return "field access chain"
	}
	// "function (parameter)" rather than "package-level function
	// (parameter)": a receiver method can ALSO be reached via this path
	// for a second candidate-typed parameter (e.g. func (g *Guarded)
	// Merge(other *Guarded)) — the KindParameterFunc path is not
	// exclusively package-level, so its label must not claim it is
	// (Destructive finding #2).
	return "function (parameter)"
}

// ReachableFunc is one function found reachable for a CandidateType, per
// AC-3: either a receiver method or a package-level function taking the
// type by parameter.
type ReachableFunc struct {
	TypeName  string
	Kind      FuncKind
	FuncName  string
	ValueName string // the receiver's or the matched parameter's identifier name
	// ReceiverTypeName is BUG-119 round 2's fix: the name of the type fd
	// itself is declared on as a receiver method, or noReceiverSentinel if
	// fd is a free (package-level) function. For KindReceiverMethod this is
	// always equal to TypeName (the receiver IS the candidate type being
	// matched). For KindParameterFunc it is frequently DIFFERENT from
	// TypeName: TypeName there names the candidate type matched by
	// PARAMETER (e.g. "Guarded" in func (a *TypeA) Attach(g *Guarded)),
	// while ReceiverTypeName names fd's own receiver, if any (e.g. "TypeA")
	// -- see violationKey's doc comment for why this field must be part of
	// the identity key.
	ReceiverTypeName string

	// ReceiverExprPrinted is BUG-119 round 6/7's structural fix (Bill's
	// ruling, 2026-08-12): the receiver's type expression printed VERBATIM
	// via go/printer.Fprint, straight from the AST node -- not a hand-
	// unwrapped classification like ReceiverTypeName/baseTypeName above.
	// "" for a free (package-level) function; for a receiver method it
	// holds whatever Go source text the receiver type expression actually
	// is, however exotic -- plain (T), pointer (*T), generic instantiation
	// (Set[T], Map[K, V]), or a shape that merely PARSES but never
	// compiles (e.g. a map/chan receiver, round 6/7's own Destructive
	// finding, attacker "Ashcombe"). go/printer can produce SOME literal
	// text for any expression the parser accepted the source as, with no
	// enumerated set of "recognised" shapes to fall behind -- which is
	// exactly why this field, not ReceiverTypeName's hand-assembled
	// sentinel scheme, is what violationKey now keys on. See violationKey's
	// doc comment for the full round-by-round history this closes.
	ReceiverExprPrinted string

	// MatchedExprPrinted is the full, as-printed (go/printer) type
	// expression of the specific thing that matched this ReachableFunc
	// against a candidate type: for KindReceiverMethod, identical to
	// ReceiverExprPrinted (the receiver IS the match); for
	// KindParameterFunc, the matched parameter's OWN type expression,
	// printed in full -- so *T, []T, ...T, Set[T], map[string]*T etc. are
	// each distinguishable from one another and from a bare T, rather than
	// reduced to the shared base name TypeName carries for human-readable
	// messages.
	MatchedExprPrinted string

	File    string
	Line    int
	Guarded bool
	Body    *ast.BlockStmt
}

// printExpr renders expr's literal Go source text via go/printer, using
// fset for position/formatting context -- BUG-119 round 6/7's structural
// fix (Bill's ruling): the ratchet-matching key must be derived from the
// COMPLETE type expression "as printed" (go/printer), not a hand-picked
// subset of fields a switch statement happens to recognise. Unlike
// baseTypeName (which only understands a specific, enumerated set of AST
// node shapes and returns ok=false for anything else -- the exact defect
// class rounds 4-6 kept rediscovering), go/printer operates on the literal
// AST node the parser produced: it can render ANY expression the parser
// accepted, including shapes that parse but do not compile (a map/chan
// receiver, astgate's own no-type-checking scope per doc.go), with no
// "unrecognised shape" case to fall through and collide on.
//
// BUG-119 round 8 (Destructive reattack, attacker "Halyard", REJECT):
// round 7's printExpr called printer.Fprint directly on the ORIGINAL AST
// node against the ORIGINAL fset, which is not actually layout-independent
// -- go/printer replicates the source's OWN line-break placement for
// constructs like a multi-type-param generic instantiation
// (*ast.IndexListExpr, e.g. Map[K, V]) whenever the original source
// happened to wrap it across lines (confirmed NOT normalized away by
// gofmt: `*Mapp[string, int]` and a deliberately reflowed
// `*Mapp[string,\n\tint]` are both gofmt-stable as written, and produce
// DIFFERENT printer.Fprint output). Since violationKey is built directly
// from this text, a purely cosmetic reflow of an already-flagged
// declaration's own generic type -- semantically identical, no behaviour
// change -- flipped a previously-accepted finding into a false
// NEW-violation: round 1's original bug (cosmetic edit -> false new
// violation) reappearing via the very mechanism (go/printer) the round 7
// structural fix was built on.
//
// Round 9's fix makes printExpr's output independent of the original
// source's layout in two steps, applied in order:
//
//  1. canonicalizeTypeExpr (below) rebuilds expr as a FRESH tree with every
//     redundant *ast.ParenExpr wrapper removed at any nesting depth (e.g.
//     `(*Mapp[string, int])` and `*Mapp[string, int]` are the same type,
//     and must print identically) -- go/printer's Mode flags (checked
//     against the go/printer source before reaching for an AST rewrite,
//     per the round 9 dispatch instructions: UseSpaces, TabIndent,
//     RawFormat, SourcePos) have no flag that strips redundant parens or
//     ignores the original source's line-break hints, so there is no
//     simpler fix available than rebuilding the tree.
//  2. canonicalizeWhitespace (below) then collapses EVERY run of
//     whitespace in printer.Fprint's output -- spaces, tabs, and newlines
//     alike -- to a single space. This is what actually neutralises
//     go/printer's line-break replication: whatever layout the original
//     source had (`Map[K,\n\tV]`, extra blank lines, tabs vs. spaces), the
//     printed text collapses to the SAME single-line canonical form as the
//     tightly-packed original (`Map[K, V]`). This is safe specifically
//     because printExpr's output is used ONLY as a key component
//     (violationKey) -- never surfaced in violationMessage's human-readable
//     text (grep-confirmed: violationMessage never reads
//     ReceiverExprPrinted/MatchedExprPrinted) -- so collapsing whitespace
//     costs nothing in readability and cannot blur two DIFFERENT type
//     expressions together: whitespace collapse never changes token
//     content, only the run-length of whitespace BETWEEN tokens, so two
//     expressions differing in actual content (e.g. `int` vs. `int8`)
//     remain distinguishable after collapse.
//
// Round 9 self-assessment (per the round 9 dispatch's own instruction to
// look for OTHER source-layout-dependent aspects before calling this
// closed): two candidate mechanisms were checked and found NOT to be live
// gaps, one residual gap WAS found and is logged below rather than left
// undiscovered.
//
//   - Comments interleaved inside a type expression (e.g.
//     `*Pair[/* c */ string, int]`) are NOT a gap: verified empirically
//     (go/ast attaches floating comments only to *ast.File.Comments, never
//     to any node inside the expression subtree itself) and confirmed
//     printer.Fprint(buf, fset, expr) -- called here with a bare expr, not
//     an *ast.File or a printer.CommentedNode wrapping one -- never emits
//     comments regardless of source layout. printExpr's output is already
//     comment-free by construction, in every round, not just round 9's.
//   - Extra/redundant parens and reflowed generic type-argument lists
//     (Halyard's own finding) are closed by canonicalizeTypeExpr and
//     canonicalizeWhitespace above.
//   - RESIDUAL, LOGGED, not cheaply closed further than the partial fix
//     already applied: an array LENGTH expression ([N]T's N) is a general
//     Go constant expression (BinaryExpr, CallExpr, etc.), not one of the
//     bounded "type" shapes canonicalizeTypeExpr's switch enumerates.
//     canonicalizeTypeExpr recurses into Len (see its *ast.ArrayType case)
//     only far enough to strip an OUTER redundant paren -- a genuinely
//     unusual layout difference INSIDE such an expression (e.g.
//     `[1+\n2]T` vs `[1 + 2]T`, or `[(1)+2]T` vs `[1+2]T`) still collapses
//     via canonicalizeWhitespace for pure line-break/spacing differences,
//     but a nested redundant paren inside a compound length expression
//     (e.g. `[(1)+2]T`) is NOT unwrapped. This is assessed as acceptable
//     residual scope, not a cheap further fix: a parenthesized array-
//     length CONSTANT expression as part of a receiver or parameter type
//     is not observed anywhere in this repo and would be a strange thing
//     to write; fully canonicalising arbitrary constant-expression syntax
//     would require recursing go/constant's whole expression grammar for
//     a shape this gate has never seen in practice. Logged here rather
//     than left undiscovered, per the round 9 dispatch's own instruction.
//
// A nil expr (a receiver-less function has no receiver expression to
// print) returns "" -- see ReceiverExprPrinted's doc comment for why an
// empty printed expression can never collide with a real one.
func printExpr(fset *token.FileSet, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	canon := canonicalizeTypeExpr(expr)
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, canon); err != nil {
		// go/printer failing on a node the parser itself already accepted
		// is not expected to occur in practice, but GR#1 forbids silently
		// degrading to an empty or ambiguous string here -- that would
		// reopen exactly the "unrecognised shape collides with something
		// real" failure mode this fix exists to close. Encoding the error
		// into the text instead makes it its own unique, loud signal: it
		// can only ever equal itself for the identical error message, and
		// can never coincidentally match a real printed expression.
		return fmt.Sprintf("<astgate:printer-error:%v>", err)
	}
	return canonicalizeWhitespace(buf.String())
}

// canonicalizeTypeExpr returns a FRESH copy of expr with every redundant
// *ast.ParenExpr wrapper removed, at any nesting depth -- BUG-119 round 9.
// It never mutates expr itself (the original AST is still used elsewhere,
// e.g. for isGuarded/rejectionBlocks' own inspection of fd.Body, so
// mutating shared nodes in place would be its own hazard); every branch
// below constructs a new node rather than editing t in place.
//
// The switch covers every node shape a RECEIVER or PARAMETER type
// expression can realistically take in this gate's scope (plain/qualified
// identifier, pointer, slice/array, variadic, map, channel, one- and
// multi-type-param generic instantiation, and any parenthesised
// combination of those). A type expression built from *ast.StructType,
// *ast.FuncType, or *ast.InterfaceType directly (e.g. a receiver or
// parameter typed as an inline `struct{...}` literal) is legal but
// vanishingly rare in real code and is passed through unrewritten by the
// default case below -- logged as round 9's residual scope note (see
// gate.go's package-level ASM reference) rather than silently claimed as
// covered.
func canonicalizeTypeExpr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch t := expr.(type) {
	case *ast.ParenExpr:
		return canonicalizeTypeExpr(t.X)
	case *ast.StarExpr:
		return &ast.StarExpr{X: canonicalizeTypeExpr(t.X)}
	case *ast.ArrayType:
		// t.Len is a general constant expression (BasicLit, BinaryExpr,
		// even a call like len(x)), not itself a "type" shape this switch
		// otherwise enumerates -- recursing it through canonicalizeTypeExpr
		// is deliberately CHEAP, not exhaustive: it strips an outer
		// redundant paren if Len literally IS one (e.g. `[(N)]T`, via the
		// *ast.ParenExpr case above), and leaves any other expression shape
		// (BinaryExpr, CallExpr, ...) untouched via the default case below,
		// unchanged from round 7/8's behaviour for those. Round 9's own
		// self-assessment note (see printExpr's doc comment) logs full
		// recursive canonicalisation of array-length constant expressions
		// as an explicitly out-of-scope residual, not silently missed.
		return &ast.ArrayType{Len: canonicalizeTypeExpr(t.Len), Elt: canonicalizeTypeExpr(t.Elt)}
	case *ast.Ellipsis:
		return &ast.Ellipsis{Elt: canonicalizeTypeExpr(t.Elt)}
	case *ast.MapType:
		return &ast.MapType{Key: canonicalizeTypeExpr(t.Key), Value: canonicalizeTypeExpr(t.Value)}
	case *ast.ChanType:
		return &ast.ChanType{Dir: t.Dir, Value: canonicalizeTypeExpr(t.Value)}
	case *ast.IndexExpr:
		return &ast.IndexExpr{X: canonicalizeTypeExpr(t.X), Index: canonicalizeTypeExpr(t.Index)}
	case *ast.IndexListExpr:
		indices := make([]ast.Expr, len(t.Indices))
		for i, idx := range t.Indices {
			indices[i] = canonicalizeTypeExpr(idx)
		}
		return &ast.IndexListExpr{X: canonicalizeTypeExpr(t.X), Indices: indices}
	case *ast.SelectorExpr:
		return &ast.SelectorExpr{X: canonicalizeTypeExpr(t.X), Sel: t.Sel}
	default:
		// *ast.Ident, *ast.BasicLit (an array length expression),
		// *ast.StructType, *ast.FuncType, *ast.InterfaceType, and anything
		// else this switch does not special-case: returned as-is. A bare
		// Ident/BasicLit has no parens to strip and no sub-expression to
		// recurse into, so this is exact, not an approximation, for those.
		return expr
	}
}

// canonicalizeWhitespace collapses every run of whitespace (spaces, tabs,
// newlines) in s to a single space, trimming leading/trailing whitespace
// -- BUG-119 round 9, see printExpr's doc comment for why this is what
// actually neutralises go/printer's source-line-break replication.
func canonicalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// noReceiverSentinel marks a ReachableFunc whose underlying *ast.FuncDecl
// has no receiver (a package-level free function) -- BUG-119 round 2. It
// is deliberately not a valid Go identifier (parentheses, a space) so it
// can never collide with a real receiver type's name.
const noReceiverSentinel = "(free function)"

// unrecognizedReceiverSentinel marks a ReachableFunc whose underlying
// *ast.FuncDecl DOES have a receiver (fd.Recv != nil) but whose receiver
// type expression baseTypeName does not (yet) recognise -- BUG-119 round 5
// (Destructive reattack, attacker "Riftline"). The concrete trigger that
// exposed this was a generic receiver like *Set[T] (baseTypeName had no
// *ast.IndexExpr/*ast.IndexListExpr case, now fixed below), but the
// sentinel exists to close the whole CLASS of "unrecognized receiver
// shape", not just that one gap: before it existed, baseTypeName
// returning ok=false for ANY unrecognised-but-present receiver shape made
// recvTypeName fall back to noReceiverSentinel -- indistinguishable from
// an ACTUAL free function of the same name and parameter, silently
// colliding two genuinely different, separately-reviewable hazards onto
// one violationKey. That is the same "accept silently suppresses a
// different unreviewed hazard" failure mode as round 2's TypeA/TypeB
// collision, via a new mechanism: recvTypeName mistaking "shape I don't
// understand" for "no receiver at all". Using a sentinel distinct from
// noReceiverSentinel means even a FUTURE unrecognised receiver shape
// (something neither this round nor round 2 anticipated) lands in its own
// bucket instead of re-colliding with genuine free functions. Like
// noReceiverSentinel, it is deliberately not a valid Go identifier so it
// can never collide with a real receiver type's name either.
const unrecognizedReceiverSentinel = "(unrecognized receiver)"

// FloodRisk is one AC-6 advisory finding: a guarded function whose
// rejection path touches a package-level variable that looks (by name)
// like a shared, fixed-capacity resource.
type FloodRisk struct {
	TypeName string
	FuncName string
	File     string
	Line     int
	VarName  string
}

// pkgVar is one package-level `var` binding collected for AC-6's
// "used elsewhere, outside any rejection path" check.
type pkgVar struct {
	Name      string
	TypeHint  string    // declared type name, or the callee name of its constructing call — best-effort, name-based
	DeclIdent token.Pos // position of the `var <Name>` declaration's own Ident — excluded from "used elsewhere", since declaring a variable is not a use of it
}

// baseTypeName extracts the bare type name from expr if expr is, after
// unwrapping any combination of pointer (*T), slice/array ([]T), and
// variadic (...T) wrapping, a plain identifier — reporting whether a
// pointer was seen anywhere along the unwrap chain. This recursion is
// what lets a candidate-typed parameter be recognised regardless of
// whether it arrives as T, *T, []T, []*T, ...T, or ...*T — a slice or
// variadic parameter is still a live value of the candidate type at the
// call site (each variadic argument is its own fresh copy), so it must
// be enumerated the same way a direct parameter is (Destructive finding
// #4). It also unwraps a GENERIC receiver's type-parameter list --
// *ast.IndexExpr (one type parameter, e.g. Set[T]) and *ast.IndexListExpr
// (more than one, e.g. Map[K, V]) -- down to the bare base identifier,
// discarding the type parameter(s) themselves (BUG-119 round 5,
// Destructive reattack, attacker "Riftline": *Set[T] as a pointer
// receiver is *ast.StarExpr{X: *ast.IndexExpr{X: Set, Index: T}}, so this
// case is what StarExpr's own recursive fallback (`return
// baseTypeName(t.X)`, taken whenever t.X isn't a plain *ast.Ident) now
// bottoms out on instead of returning ok=false). Any other shape
// (qualified pkg.T, map/chan-of-T, etc.) is unrecognised and returns
// ok=false — see doc.go's "no type-checking" blind spot, and see
// unrecognizedReceiverSentinel for how callers that need to distinguish
// "unrecognised but a receiver was present" from "no receiver at all"
// must handle that ok=false themselves rather than treating it as
// equivalent to a free function.
func baseTypeName(expr ast.Expr) (name string, isPointer bool, ok bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, false, true
	case *ast.StarExpr:
		if id, ok2 := t.X.(*ast.Ident); ok2 {
			return id.Name, true, true
		}
		return baseTypeName(t.X)
	case *ast.ArrayType:
		// t.Len == nil is a slice ([]T); a fixed-size array ([N]T) is
		// also unwrapped the same way — an array element is likewise a
		// distinct addressable value of the candidate type.
		return baseTypeName(t.Elt)
	case *ast.Ellipsis:
		return baseTypeName(t.Elt)
	case *ast.IndexExpr:
		// Generic type with exactly one type parameter, e.g. Set[T] --
		// t.X is the base identifier (Set), t.Index is the single type
		// argument (T), which is deliberately discarded: the candidate
		// match and the violation key both care about the base type
		// name, not which concrete/type-parameter instantiation is in
		// play at this declaration site.
		return baseTypeName(t.X)
	case *ast.IndexListExpr:
		// Generic type with two or more type parameters, e.g. Map[K, V]
		// -- t.X is the base identifier (Map), t.Indices the type
		// argument list, likewise discarded for the same reason as
		// *ast.IndexExpr above.
		return baseTypeName(t.X)
	}
	return "", false, false
}

// isSyncMutexValue reports whether expr is exactly sync.Mutex or
// sync.RWMutex used BY VALUE (not *sync.Mutex/*sync.RWMutex — AC-1
// excludes pointer fields deliberately), OR a bare identifier that
// mutexAliases (collectMutexAliases) has resolved to one of those two
// types via a same-package `type X = sync.Mutex`/`type X = sync.RWMutex`
// alias declaration (Destructive finding #3) — a locally aliased mutex
// is the exact same value-copy hazard as the unaliased spelling, and
// must not be invisible to AC-1 just because of the name it was
// declared under. Cross-package alias resolution is a disclosed blind
// spot (doc.go).
func isSyncMutexValue(expr ast.Expr, mutexAliases map[string]bool) bool {
	if id, ok := expr.(*ast.Ident); ok {
		return mutexAliases[id.Name]
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "sync" {
		return false
	}
	return sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex"
}

// collectMutexAliases scans files for same-package type-alias
// declarations of the exact shape `type X = sync.Mutex` or
// `type X = sync.RWMutex` (ast.TypeSpec.Assign is only a valid position
// for a genuine `=` alias, never for a defined type `type X sync.Mutex`,
// which does NOT inherit sync.Mutex's method set and is a different,
// not-a-mutex type) and returns the set of alias names that resolve to
// a by-value sync.Mutex/RWMutex. This is deliberately same-file/
// same-package only — full cross-package alias resolution is a
// disclosed blind spot (doc.go), not attempted here.
func collectMutexAliases(files []pkgFile) map[string]bool {
	aliases := make(map[string]bool)
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Assign.IsValid() {
					continue // not a `type X = Y` alias declaration
				}
				sel, ok := ts.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "sync" {
					continue
				}
				if sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex" {
					aliases[ts.Name.Name] = true
				}
			}
		}
	}
	return aliases
}

// isAliasableField reports whether expr is a slice, map, channel, or
// pointer type — the "aliasable reference field" set AC-1 names.
func isAliasableField(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.ArrayType:
		return t.Len == nil // nil Len means slice, not fixed-size array
	case *ast.MapType:
		return true
	case *ast.ChanType:
		return true
	case *ast.StarExpr:
		return true
	}
	return false
}

// findCandidateTypes implements AC-1: for every struct type declared in
// files, report the ones with both a sync.Mutex/RWMutex value field and
// an aliasable reference field. dir is the directory files came from
// (for CandidateType.Dir, used to scope AC-3's same-package function
// match).
func findCandidateTypes(dir string, files []pkgFile) []CandidateType {
	mutexAliases := collectMutexAliases(files)
	var out []CandidateType
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				hasMutex, hasAliasable := false, false
				for _, f := range st.Fields.List {
					if isSyncMutexValue(f.Type, mutexAliases) {
						hasMutex = true
					}
					if isAliasableField(f.Type) {
						hasAliasable = true
					}
				}
				if hasMutex && hasAliasable {
					pos := pf.Fset.Position(ts.Pos())
					out = append(out, CandidateType{
						Name: ts.Name.Name,
						Dir:  dir,
						File: pf.Rel,
						Line: pos.Line,
					})
				}
			}
		}
	}
	return out
}

// findReachableFuncs implements AC-3: every receiver method and every
// function (with or without a receiver) taking any of candidates by
// parameter, within the same directory (package) the candidates were
// found in — see doc.go's "no type-checking" blind spot for why this is
// scoped to one directory.
//
// Receiver-method status and parameter-taking status are NOT mutually
// exclusive (Destructive findings #1/#2): a function can simultaneously
// be a receiver method on some type X AND take a second, independent
// candidate-typed value by parameter — e.g. func (g *Guarded)
// Merge(other *Guarded) is checked via its receiver AND via its
// parameter, and func (r *Attacher) Attach(g *Guarded) — whose receiver
// type Attacher is NOT itself a candidate — must still be checked via
// its parameter. The old code treated "has a receiver" as a reason to
// skip parameter scanning entirely (an unconditional continue), which
// reopened exactly the SetSink/SEC-031 hazard this gate exists to close
// for any future SetSink-shaped function refactored into a method. The
// two checks below are therefore independent, not else-branches.
func findReachableFuncs(candidates []CandidateType, files []pkgFile) []*ReachableFunc {
	byName := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		byName[c.Name] = true
	}

	var out []*ReachableFunc
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			pos := pf.Fset.Position(fd.Pos())

			// The guard method itself (checkNotCopied) is excluded from
			// BOTH checks below — it IS the check, so requiring it to
			// call itself (via its receiver or any parameter) is a
			// self-referential non-question, not a real candidate for
			// AC-4.
			if fd.Recv != nil && len(fd.Recv.List) > 0 && fd.Name.Name == guardMethodName {
				continue
			}

			// recvExprPrinted is fd's OWN receiver type expression, printed
			// verbatim via go/printer (BUG-119 round 6/7, Bill's ruling) --
			// computed once per fd, ahead of both the receiver-method and
			// parameter-function branches below, since both need it: the
			// receiver-method branch uses it as its own MatchedExprPrinted,
			// and the parameter-function branch uses it as the "enclosing
			// declaration" component of ReceiverExprPrinted regardless of
			// whether that receiver is itself a candidate type. "" for a
			// free (package-level) function.
			recvExprPrinted := ""
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				recvExprPrinted = printExpr(pf.Fset, fd.Recv.List[0].Type)
			}

			// AC-3(a): receiver method.
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				recvField := fd.Recv.List[0]
				if name, _, ok := baseTypeName(recvField.Type); ok && byName[name] {
					valueName := "_"
					if len(recvField.Names) > 0 {
						valueName = recvField.Names[0].Name
					}
					out = append(out, &ReachableFunc{
						TypeName:            name,
						Kind:                KindReceiverMethod,
						FuncName:            fd.Name.Name,
						ValueName:           valueName,
						ReceiverTypeName:    name, // the receiver IS the candidate type here
						ReceiverExprPrinted: recvExprPrinted,
						MatchedExprPrinted:  recvExprPrinted, // the receiver IS the match here
						File:                pf.Rel,
						Line:                pos.Line,
						Body:                fd.Body,
					})
				}
			}

			// recvTypeName is fd's OWN receiver type name (BUG-119 round 2),
			// independent of whether that receiver is itself a candidate
			// type -- func (r *Attacher) Attach(g *Guarded) must record
			// "Attacher" here even though Attacher never appears in byName,
			// precisely because it is what disambiguates this ReachableFunc
			// from a same-named method on a different receiver type further
			// down in the KindParameterFunc loop below.
			//
			// The else branch below is BUG-119 round 5: fd.Recv != nil but
			// baseTypeName returning ok=false (an unrecognised receiver
			// shape -- originally exposed live by generic receivers like
			// *Set[T], now fixed in baseTypeName above, but this branch
			// stays as defense-in-depth for any FUTURE unrecognised shape)
			// must NOT fall through to noReceiverSentinel: that sentinel
			// means "no receiver at all", and reusing it here would
			// silently collide a real (if unrecognised) receiver method
			// with a genuine free function of the same name/parameter --
			// see unrecognizedReceiverSentinel's doc comment.
			recvTypeName := noReceiverSentinel
			if fd.Recv != nil && len(fd.Recv.List) > 0 {
				if name, _, ok := baseTypeName(fd.Recv.List[0].Type); ok {
					recvTypeName = name
				} else {
					recvTypeName = unrecognizedReceiverSentinel
				}
			}

			// AC-3(b): every parameter — of ANY function, receiver or
			// not — taking a candidate type, regardless of position or
			// name. This runs unconditionally alongside the receiver
			// check above, not only when fd.Recv is nil (Destructive
			// findings #1/#2).
			out = appendParamFuncs(out, byName, pf, fd.Name.Name, recvTypeName, recvExprPrinted, fd.Type, fd.Body, pos.Line)
		}

		// BUG-138: *ast.FuncLit is an entire AST node kind the walk above
		// never visits — a candidate-typed copy taken by a function
		// literal (closure) assigned to a var, struct field, or passed
		// as an argument is invisible to AC-3(b) as long as no ordinary
		// *ast.FuncDecl in the package also takes that candidate type by
		// parameter. Live-verified: a package-level `var Copier = func(g
		// Guarded) *int { g.mu.Lock(); ...; return g.ref }` sailed
		// through with zero violations reported. Function literals have
		// no receiver (KindReceiverMethod is a FuncDecl-only concept),
		// so only the AC-3(b) parameter-scan applies here, never AC-3(a).
		out = append(out, scanFuncLits(pf, byName)...)
	}

	// SEC-049: every method declared on a type that WRAPS a candidate
	// type via a struct field (rather than being a candidate type
	// itself, or taking a candidate type by parameter) is a third,
	// independent reachable-function path — see findFieldReachableFuncs's
	// own doc comment. Computed once over the WHOLE package's files (not
	// per-file inside the loop above), since the wrapping type's struct
	// declaration and its methods can live in different files of the
	// same package.
	out = append(out, findFieldReachableFuncs(byName, files)...)

	return out
}

// funcLitName derives a readable identity for a function literal so
// violationKey (which has no Line component, by design — see its own
// doc comment) still distinguishes two different closures in the same
// file: prefer the name of the var/field it is directly assigned to
// (var Copier = func(...){...} -> "Copier"), and fall back to a
// position-qualified synthetic name for closures with no direct
// assignment (passed inline as an argument, returned bare, etc.) so
// two such closures in the same file can never collide.
func funcLitName(fset *token.FileSet, lit *ast.FuncLit, assignedNames map[*ast.FuncLit]string) string {
	if name, ok := assignedNames[lit]; ok {
		return name
	}
	pos := fset.Position(lit.Pos())
	return fmt.Sprintf("<func literal at line %d>", pos.Line)
}

// scanFuncLits implements BUG-138's fix: walk pf.AST for every
// *ast.FuncLit, at any nesting depth (closures can nest arbitrarily —
// inside another function, inside a var initializer, inside a struct
// literal field, passed as a call argument), and run the same AC-3(b)
// candidate-parameter scan against each one that applies to an
// ordinary *ast.FuncDecl.
func scanFuncLits(pf pkgFile, byName map[string]bool) []*ReachableFunc {
	// First pass: record the direct var/field name for any FuncLit that
	// is the immediate RHS of a ValueSpec (`var X = func(...){}`) or an
	// AssignStmt (`X := func(...){}` / `X = func(...){}`) — purely
	// cosmetic (funcLitName falls back to a position-qualified name for
	// every other shape), so a FuncLit this pass misses is still found
	// and still uniquely keyed by the second pass below.
	assignedNames := map[*ast.FuncLit]string{}
	ast.Inspect(pf.AST, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			for i, v := range node.Values {
				if lit, ok := v.(*ast.FuncLit); ok && i < len(node.Names) {
					assignedNames[lit] = node.Names[i].Name
				}
			}
		case *ast.AssignStmt:
			for i, v := range node.Rhs {
				if lit, ok := v.(*ast.FuncLit); ok && i < len(node.Lhs) {
					if id, ok := node.Lhs[i].(*ast.Ident); ok {
						assignedNames[lit] = id.Name
					}
				}
			}
		}
		return true
	})

	var out []*ReachableFunc
	ast.Inspect(pf.AST, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok || lit.Body == nil {
			return true
		}
		pos := pf.Fset.Position(lit.Pos())
		name := funcLitName(pf.Fset, lit, assignedNames)
		out = appendParamFuncs(out, byName, pf, name, noReceiverSentinel, "", lit.Type, lit.Body, pos.Line)
		return true
	})
	return out
}

// appendParamFuncs implements AC-3(b) — scanning a function's own
// parameter list for any candidate type — shared between an ordinary
// *ast.FuncDecl (findReachableFuncs) and an *ast.FuncLit (scanFuncLits,
// BUG-138) so the two node kinds are checked identically rather than
// via two independently-maintained copies of the same logic (GR#3).
func appendParamFuncs(out []*ReachableFunc, byName map[string]bool, pf pkgFile, funcName, recvTypeName, recvExprPrinted string, ft *ast.FuncType, body *ast.BlockStmt, line int) []*ReachableFunc {
	if ft == nil || ft.Params == nil {
		return out
	}
	for _, field := range ft.Params.List {
		name, _, ok := baseTypeName(field.Type)
		if !ok || !byName[name] {
			continue
		}
		paramNames := field.Names
		if len(paramNames) == 0 {
			paramNames = []*ast.Ident{ast.NewIdent("_")}
		}
		for _, pn := range paramNames {
			out = append(out, &ReachableFunc{
				TypeName:            name,
				Kind:                KindParameterFunc,
				FuncName:            funcName,
				ValueName:           pn.Name,
				ReceiverTypeName:    recvTypeName,
				ReceiverExprPrinted: recvExprPrinted,
				MatchedExprPrinted:  printExpr(pf.Fset, field.Type),
				File:                pf.Rel,
				Line:                line,
				Body:                body,
			})
		}
	}
	return out
}

// fieldWrap records one struct field, found anywhere in a scanned
// package, whose type names a candidate type — SEC-049. OwnerType is
// the struct declaring the field (e.g. "WorldAPI"); FieldName is the
// field's own identifier for a named field (e.g. "w" in `w *World`), or
// the base type name itself for an anonymous/embedded field (e.g.
// "World" for a bare `*World`/`World` field, since that is the
// identifier Go itself uses to reach an embedded field); Candidate is
// the candidate type reached through the field (e.g. "World").
type fieldWrap struct {
	OwnerType string
	FieldName string
	Candidate string
}

// isContainerFieldType reports whether expr is, after unwrapping at
// most one level of pointer, a slice/array (*ast.ArrayType), map
// (*ast.MapType), or channel (*ast.ChanType) type expression.
// collectFieldWraps uses this to REJECT such fields explicitly (SEC-049
// round 2, Destructive reattack): baseTypeName has an *ast.ArrayType
// case (added for the slice-parameter fix, Destructive finding #4 —
// see baseTypeName's own doc comment) that recurses into the element
// type, so without this guard a field like `ws []*World` would be
// silently unwrapped down to "World" and matched exactly like a direct
// `w *World` field — even though a container can never satisfy
// isGuarded's literal `<chain>.checkNotCopied(...)` call shape (you
// cannot call a method on a slice/map/chan value), making any finding
// registered for it permanently, uncorrectably unguardable. This
// mirrors the doc.go "Known blind spots" disclosure precisely: a
// container-of-candidate field is a genuinely different hazard shape
// (independently copyable elements, not one field-then-method access
// chain) and is out of scope for collectFieldWraps by construction, not
// by accident of baseTypeName's unrelated slice-parameter behaviour.
func isContainerFieldType(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch expr.(type) {
	case *ast.ArrayType, *ast.MapType, *ast.ChanType:
		return true
	}
	return false
}

// collectFieldWraps implements SEC-049's fix: for every struct type
// declared in files, record every field — named, or
// anonymous/embedded — whose type, after unwrapping at most one level
// of pointer (the same unwrap baseTypeName already performs for
// receivers and parameters), names one of candidates. A field typed as
// a slice/map/chan of a candidate type is explicitly rejected by
// isContainerFieldType above and never matched here: those are already
// a DIFFERENT, already-disclosed blind spot (doc.go's "map-typed
// candidate values are invisible" note) or a genuinely different hazard
// shape (a container of independently copyable values, not one single
// field-then-method access chain); SEC-049's own scope is the direct
// field case that exposed it live (WorldAPI.w *World).
func collectFieldWraps(byName map[string]bool, files []pkgFile) []fieldWrap {
	var out []fieldWrap
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, f := range st.Fields.List {
					if isContainerFieldType(f.Type) {
						continue
					}
					name, _, ok := baseTypeName(f.Type)
					if !ok || !byName[name] {
						continue
					}
					if len(f.Names) == 0 {
						// Anonymous/embedded field — Go reaches it by the
						// base type name itself.
						out = append(out, fieldWrap{OwnerType: ts.Name.Name, FieldName: name, Candidate: name})
						continue
					}
					for _, fn := range f.Names {
						out = append(out, fieldWrap{OwnerType: ts.Name.Name, FieldName: fn.Name, Candidate: name})
					}
				}
			}
		}
	}
	return out
}

// findFieldReachableFuncs implements SEC-049's fix: for every method
// declared on a type W that WRAPS a candidate type C via a struct field
// (collectFieldWraps), register a ReachableFunc for C reached via that
// field-then-method chain — KindFieldAccess, distinct from
// KindReceiverMethod (a direct method on C) and KindParameterFunc (C
// arriving as a parameter). This is the exact shape SEC-049 reported:
// WorldAPI's own methods reach World's mutex via a.w.mu/
// a.w.checkNotCopied(...), and were invisible to reachable-function
// enumeration because WorldAPI is not itself a candidate type (no mutex
// field of its own) and does not take World by parameter — the only two
// shapes findReachableFuncs recognised before this fix.
//
// A method whose receiver has NO name (`func (*WorldAPI) M()`) is
// skipped: an unnamed receiver's fields are syntactically unreachable
// inside the method body, so there is no access chain to construct or
// to be missing a guard on.
//
// This is deliberately ONE field-access hop deep, matching the shape
// SEC-049 reported live: W.field.Method(...). A wrapper-of-a-wrapper
// (X holds a *WorldAPI field, WorldAPI holds a *World field) is NOT
// transitively resolved to a 3-segment X-to-World chain — see doc.go's
// "Known blind spots" for this logged as residual scope, not silently
// missed.
func findFieldReachableFuncs(byName map[string]bool, files []pkgFile) []*ReachableFunc {
	wraps := collectFieldWraps(byName, files)
	if len(wraps) == 0 {
		return nil
	}
	wrapsByOwner := make(map[string][]fieldWrap, len(wraps))
	for _, w := range wraps {
		wrapsByOwner[w.OwnerType] = append(wrapsByOwner[w.OwnerType], w)
	}

	var out []*ReachableFunc
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv == nil || len(fd.Recv.List) == 0 {
				continue
			}
			if fd.Name.Name == guardMethodName {
				// See findReachableFuncs' identical exclusion: the guard
				// method itself is the check, not a candidate for it.
				continue
			}
			ownerName, _, ok := baseTypeName(fd.Recv.List[0].Type)
			if !ok {
				continue
			}
			owns, ok := wrapsByOwner[ownerName]
			if !ok {
				continue
			}
			if len(fd.Recv.List[0].Names) == 0 {
				continue // unnamed receiver — its fields are unreachable in the body
			}
			recvName := fd.Recv.List[0].Names[0].Name
			if recvName == "_" {
				continue // blank receiver — same reasoning as an unnamed one
			}
			recvExprPrinted := printExpr(pf.Fset, fd.Recv.List[0].Type)
			pos := pf.Fset.Position(fd.Pos())
			for _, w := range owns {
				out = append(out, &ReachableFunc{
					TypeName:            w.Candidate,
					Kind:                KindFieldAccess,
					FuncName:            fd.Name.Name,
					ValueName:           recvName + "." + w.FieldName,
					ReceiverTypeName:    ownerName,
					ReceiverExprPrinted: recvExprPrinted,
					MatchedExprPrinted:  recvExprPrinted + "." + w.FieldName,
					File:                pf.Rel,
					Line:                pos.Line,
					Body:                fd.Body,
				})
			}
		}
	}
	return out
}

// callTargetsValue reports whether call is `<valueName>.checkNotCopied(...)`
// or `(*<valueName>).checkNotCopied(...)`/`(<valueName>).checkNotCopied(...)`.
//
// SEC-049: valueName may now also be a DOTTED chain (e.g. "a.w") — the
// shape findFieldReachableFuncs constructs for a field-then-method
// access (a.w.checkNotCopied(...)), as opposed to the plain single
// identifier every KindReceiverMethod/KindParameterFunc ReachableFunc
// has always used. exprMatchesChain handles both: a chain of length 1
// is exactly the old plain-identifier check, unchanged in behaviour.
func callTargetsValue(call *ast.CallExpr, valueName string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != guardMethodName {
		return false
	}
	return exprMatchesChain(sel.X, strings.Split(valueName, "."))
}

// exprMatchesChain reports whether expr, after unwrapping any
// combination of parens and pointer-dereferences at each step, is
// exactly the dotted identifier chain named by chain — e.g. chain
// ["a", "w"] matches the source shapes `a.w`, `(*a).w`, `(a.w)`, and
// `a.(w)` alike, mirroring the paren/star tolerance callTargetsValue has
// always had for the single-identifier case. chain has length 1 for
// every pre-SEC-049 ReachableFunc.ValueName (a receiver's or a matched
// parameter's own name) and length 2 for SEC-049's field-access chain
// (`<receiver>.<field>`); the recursion supports any length uniformly,
// though today's only caller (findFieldReachableFuncs) constructs
// exactly 2 segments.
func exprMatchesChain(expr ast.Expr, chain []string) bool {
	if len(chain) == 0 {
		return false
	}
	x := expr
	for {
		switch t := x.(type) {
		case *ast.ParenExpr:
			x = t.X
			continue
		case *ast.StarExpr:
			x = t.X
			continue
		}
		break
	}
	last := chain[len(chain)-1]
	if len(chain) == 1 {
		id, ok := x.(*ast.Ident)
		return ok && id.Name == last
	}
	sel, ok := x.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != last {
		return false
	}
	return exprMatchesChain(sel.X, chain[:len(chain)-1])
}

// isGuarded implements AC-4's presence check: rf.Body contains, anywhere,
// a call to guardMethodName reached on rf.ValueName. See doc.go's blind
// spot on "presence, not effect".
func isGuarded(rf *ReachableFunc) bool {
	found := false
	ast.Inspect(rf.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callTargetsValue(call, rf.ValueName) {
			found = true
			return false
		}
		return true
	})
	return found
}

// rejectionBlocks returns the statement lists of every recognised
// rejection branch inside rf.Body: an `if` statement whose condition
// contains, anywhere, `!<ValueName>.checkNotCopied()` (directly, or
// combined via && with other conditions, as SetSink's `l != nil &&
// !l.checkNotCopied()` does).
func rejectionBlocks(rf *ReachableFunc) []*ast.BlockStmt {
	var out []*ast.BlockStmt
	ast.Inspect(rf.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		hasRejectCheck := false
		ast.Inspect(ifs.Cond, func(c ast.Node) bool {
			unary, ok := c.(*ast.UnaryExpr)
			if !ok || unary.Op != token.NOT {
				return true
			}
			call, ok := unary.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callTargetsValue(call, rf.ValueName) {
				hasRejectCheck = true
			}
			return true
		})
		if hasRejectCheck {
			out = append(out, ifs.Body)
		}
		return true
	})
	return out
}

// collectPkgVars gathers every package-level `var` binding across files,
// for AC-6's "shared, fixed-capacity-looking resource" check.
func collectPkgVars(files []pkgFile) []pkgVar {
	var out []pkgVar
	for _, pf := range files {
		for _, decl := range pf.AST.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typeHint := ""
				if vs.Type != nil {
					if id, ok := vs.Type.(*ast.Ident); ok {
						typeHint = id.Name
					}
				}
				for i, name := range vs.Names {
					hint := typeHint
					if hint == "" && i < len(vs.Values) {
						if call, ok := vs.Values[i].(*ast.CallExpr); ok {
							if id, ok := call.Fun.(*ast.Ident); ok {
								hint = id.Name
							}
						}
					}
					out = append(out, pkgVar{Name: name.Name, TypeHint: hint, DeclIdent: name.Pos()})
				}
			}
		}
	}
	return out
}

// posRange is an inclusive [start, end) token.Pos window.
type posRange struct{ start, end token.Pos }

func (r posRange) contains(p token.Pos) bool { return p >= r.start && p < r.end }

// findFloodRisks implements AC-6/AC-7: for every guarded reachable
// function, inspect its recognised rejection blocks (rejectionBlocks)
// for calls of the shape `<pkgVar>.Method(...)` where pkgVar looks
// ring/buffer-shaped (ringLikePattern, against its declared type or
// constructing-call name) AND pkgVar is also referenced somewhere in the
// package OUTSIDE any rejection block — i.e. it is shared with ordinary,
// non-rejection code, the SEC-030/SEC-031(b) shape.
//
// This is advisory only (AC-7): it never contributes to hard violations.
// See doc.go's blind-spot note on why this is single-level and
// name-heuristic, not a structural proof.
func findFloodRisks(pf string, files []pkgFile, reachable []*ReachableFunc) []FloodRisk {
	vars := collectPkgVars(files)
	ringVars := make(map[string]bool)
	for _, v := range vars {
		if ringLikePattern.MatchString(v.Name) || ringLikePattern.MatchString(v.TypeHint) {
			ringVars[v.Name] = true
		}
	}
	if len(ringVars) == 0 {
		return nil
	}

	// Collect every rejection block's position range, across every
	// reachable function in this package, so "used elsewhere" can be
	// checked against the union.
	var allRejectRanges []posRange
	rejectBlocksByFunc := make(map[*ReachableFunc][]*ast.BlockStmt)
	for _, rf := range reachable {
		if !rf.Guarded {
			continue
		}
		blocks := rejectionBlocks(rf)
		rejectBlocksByFunc[rf] = blocks
		for _, b := range blocks {
			allRejectRanges = append(allRejectRanges, posRange{b.Pos(), b.End()})
		}
	}

	inAnyRejectRange := func(p token.Pos) bool {
		for _, r := range allRejectRanges {
			if r.contains(p) {
				return true
			}
		}
		return false
	}

	// declPositions excludes each var's OWN `var X = ...` declaration
	// Ident from counting as a "use" — declaring a variable is not a
	// reference to it, and without this exclusion every package-level
	// var would trivially look "used elsewhere" via its own decl site.
	declPositions := make(map[token.Pos]bool)
	for _, v := range vars {
		if ringVars[v.Name] {
			declPositions[v.DeclIdent] = true
		}
	}

	// "Used elsewhere" — any Ident matching a ring-shaped var name,
	// anywhere in the package's declarations/bodies, outside every
	// collected rejection range AND outside its own declaration.
	usedElsewhere := make(map[string]bool)
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || !ringVars[id.Name] || declPositions[id.Pos()] {
				return true
			}
			if !inAnyRejectRange(id.Pos()) {
				usedElsewhere[id.Name] = true
			}
			return true
		})
	}

	var risks []FloodRisk
	for _, rf := range reachable {
		if !rf.Guarded {
			continue
		}
		for _, block := range rejectBlocksByFunc[rf] {
			ast.Inspect(block, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || !ringVars[id.Name] {
					return true
				}
				if !usedElsewhere[id.Name] {
					return true // dedicated, unshared buffer — not flagged (AC-6 negative case)
				}
				risks = append(risks, FloodRisk{
					TypeName: rf.TypeName,
					FuncName: rf.FuncName,
					File:     rf.File,
					Line:     rf.Line,
					VarName:  id.Name,
				})
				return true
			})
		}
	}
	return risks
}

// loadPackages walks repoRoot/<dir> for every dir in dirs, parses every
// non-test .go file, and groups the resulting files by their containing
// directory (repoAST's proxy for "one Go package" — see doc.go's
// no-type-checking blind spot for why this is a syntactic proxy, not a
// real package resolution).
func loadPackages(repoRoot string, dirs ...string) (map[string][]pkgFile, error) {
	fset := token.NewFileSet()
	byDir := make(map[string][]pkgFile)

	for _, d := range dirs {
		root := filepath.Join(repoRoot, d)
		err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if perr != nil {
				return errs.Wrap("MET-F700", errs.NewCorrelationID(), perr, map[string]any{"path": path, "cause": perr.Error()})
			}
			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
			dirRel := filepath.ToSlash(filepath.Dir(rel))
			byDir[dirRel] = append(byDir[dirRel], pkgFile{AST: file, Rel: rel, Fset: fset})
			return nil
		})
		if err != nil {
			// If err is already a registry-sourced *errs.E (e.g. the
			// MET-F700 parse error minted inside the WalkDir callback
			// above, which WalkDir simply propagates as its own return
			// value), pass it through as-is rather than wrapping a
			// registry error inside another registry error — the
			// original bare-fmt.Errorf code double-wrapped in exactly
			// this situation ("scanning %s: %w" around an already-
			// "parsing %s: %w"-wrapped cause), which buried the real
			// MET-F700 code the caller actually needs one layer deep for
			// no benefit; SEC-048's conversion is a natural point to fix
			// that rather than reproduce it. A genuine WalkDir-level
			// failure (missing/unreadable root) is not an *errs.E and is
			// still wrapped as MET-F701 below.
			if regErr, ok := err.(*errs.E); ok {
				return nil, regErr
			}
			return nil, errs.Wrap("MET-F701", errs.NewCorrelationID(), err, map[string]any{"root": root, "cause": err.Error()})
		}
	}
	return byDir, nil
}

// Finding is BUG-119's fix: it pairs a violation's LOCATION-STABLE
// identity key (violationKey) with its full human-readable message
// (violationMessage). Result keeps both res.Violations (message text,
// unchanged shape, still used for substring assertions throughout this
// file's fixture tests) and res.Findings (this type, keyed) in the same
// order so callers that need the ratchet-matching key and callers that
// only want the readable text can each use the one they need without
// re-deriving anything.
type Finding struct {
	Key     string
	Message string
}

// Result is the full output of running the gate over a tree.
type Result struct {
	Candidates    []CandidateType
	Reachable     []*ReachableFunc
	Violations    []string
	Findings      []Finding
	FloodRisks    []FloodRisk
	FloodMessages []string
}

// Run executes the whole gate (AC-1 through AC-7) over repoRoot/<dir> for
// every dir in dirs, one Go-package-per-directory at a time.
//
// BUG-119 round 6/7's second mandatory part (Bill's ruling, 2026-08-12):
// while building the key -> finding map below, if two DISTINCT
// ReachableFuncs (necessarily two distinct AST nodes -- this loop visits
// each unguarded ReachableFunc exactly once, so a repeated key here can
// only mean two different declarations produced it) ever compute the SAME
// violationKey, that is a HARD ERROR (MET-F703) that aborts the whole run.
// This is deliberate: a silent map overwrite is exactly the mechanism that
// let rounds 1-5 each ship a violationKey scheme that LOOKED
// collision-free and was not -- every prior round's collision was found
// only by a SEVENTH-ROUND-DEEP Destructive human/agent constructing a
// fixture by hand and noticing two findings shared a key after the fact.
// Turning "two distinct nodes, one key" into an immediate, unmissable test
// failure means any FUTURE incompleteness in violationKey's identity
// scheme (including one this fix does not anticipate) is caught the
// moment it is introduced -- by the gate's own tests, every run -- instead
// of surviving silently until the next hand-constructed attack finds it.
// dedupDirs removes duplicate directory arguments, preserving first-seen
// order -- BUG-119 round 8 (Halyard) also flagged, at lower severity than
// the printExpr collision, that Run's self-check comment claims a repeated
// key "can only mean two different declarations", which is false if a
// caller passes overlapping/duplicate dirs to Run (e.g. Run(root, "pkg",
// "pkg")): loadPackages would scan the same files twice, producing two
// ReachableFunc values for the SAME AST node/declaration, which then
// legitimately collide with themselves on violationKey and would trip the
// MET-F703 hard-fail for a reason that has nothing to do with a genuine
// identity-scheme gap. No current caller passes overlapping dirs, but
// guarding Run itself (rather than trusting every future caller to
// pre-dedup) is the cheaper, more robust fix per the round 9 dispatch
// instructions.
func dedupDirs(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func Run(repoRoot string, dirs ...string) (Result, error) {
	dirs = dedupDirs(dirs)
	byDir, err := loadPackages(repoRoot, dirs...)
	if err != nil {
		return Result{}, err
	}

	// Sort directories for deterministic output.
	dirNames := make([]string, 0, len(byDir))
	for d := range byDir {
		dirNames = append(dirNames, d)
	}
	sort.Strings(dirNames)

	// keyLocations records, for every violationKey seen so far across the
	// WHOLE run (not just the current directory -- a key embeds File, so a
	// genuine cross-directory collision is not expected, but the self-check
	// must not silently rely on that: it checks globally), the file:line of
	// the first ReachableFunc that produced it. This IS "the key map"
	// Bill's ruling refers to.
	keyLocations := make(map[string]string)

	var res Result
	for _, dir := range dirNames {
		files := byDir[dir]
		candidates := findCandidateTypes(dir, files)
		if len(candidates) == 0 {
			continue
		}
		reachable := findReachableFuncs(candidates, files)
		for _, rf := range reachable {
			rf.Guarded = isGuarded(rf)
		}

		res.Candidates = append(res.Candidates, candidates...)
		res.Reachable = append(res.Reachable, reachable...)

		for _, rf := range reachable {
			if !rf.Guarded {
				key := violationKey(rf)
				loc := fmt.Sprintf("%s:%d", rf.File, rf.Line)
				if firstLoc, seen := keyLocations[key]; seen {
					// Two distinct ReachableFuncs (this loop body runs once
					// per unguarded rf, so firstLoc and loc necessarily name
					// two different declarations -- this reasoning was
					// FALSE for a caller passing overlapping/duplicate dirs
					// until round 9's dedupDirs above made it true again by
					// construction; see dedupDirs' own doc comment, BUG-119
					// round 8/Halyard) produced the identical key --
					// violationKey is still incomplete. Fail the run
					// outright rather than silently keeping only the second
					// finding, which is exactly the "accept silently
					// suppresses a different unreviewed hazard" failure
					// mode rounds 2-6 each reopened by a different route.
					return Result{}, errs.New("MET-F703", errs.NewCorrelationID(), map[string]any{
						"key":            key,
						"firstLocation":  firstLoc,
						"secondLocation": loc,
					})
				}
				keyLocations[key] = loc

				msg := violationMessage(rf)
				res.Violations = append(res.Violations, msg)
				res.Findings = append(res.Findings, Finding{Key: key, Message: msg})
			}
		}

		risks := findFloodRisks(dir, files, reachable)
		res.FloodRisks = append(res.FloodRisks, risks...)
		for _, fr := range risks {
			res.FloodMessages = append(res.FloodMessages, floodMessage(fr))
		}
	}
	return res, nil
}

// violationKey implements BUG-119's fix: a ratchet-matching identity for
// rf that is LOCATION-STABLE -- it deliberately excludes rf.Line (and
// therefore anything that shifts because of it, like violationMessage's
// "%s:%d" prefix), so a purely cosmetic edit anywhere ABOVE the flagged
// line in the same file (a comment, a blank line, a reordered import --
// anything that does not change the flagged function's own shape) cannot
// change this key. The pre-fix ratchet matched on violationMessage's full
// text, which embeds file:line; BUG-119 demonstrated live that adding one
// comment line above an import block shifted every subsequent line number
// in internal/engine/core/commands.go, which flipped all 12 of that
// file's already-accepted findings into "NEW violation" build failures
// even though not one of them had actually changed.
//
// The key is the tuple violationMessage's own doc comment already
// describes as the finding's actual identity -- file, candidate type,
// function name, which enumeration path caught it (receiver vs.
// parameter), and the matched value's identifier name -- PLUS
// rf.ReceiverTypeName, added by round 2 of this fix.
//
// Round 2 history (Destructive reattack, attacker "Vantage"): round 1's
// key, (File, TypeName, Kind, FuncName, ValueName) with no receiver type,
// was claimed collision-proof on the reasoning "two functions with an
// identical tuple in the same file would require a duplicate declaration,
// which Go forbids." That reasoning is TRUE for KindReceiverMethod (a
// receiver type may not declare the same method name twice) but FALSE for
// KindParameterFunc: Go namespaces methods BY RECEIVER TYPE, not
// globally, so unrelated types in the same file may each declare a method
// of the same name taking the same candidate-typed parameter under the
// same parameter name -- e.g. func (a *TypeA) Attach(g *Guarded) and func
// (b *TypeB) Attach(g *Guarded), both unguarded, produced the IDENTICAL
// round-1 key file|Guarded|"function (parameter)"|Attach|g. That is not
// mere noise: an allowlist entry a human reviewed and accepted for
// TypeA.Attach's hazard would SILENTLY also suppress TypeB.Attach's
// genuinely different, unreviewed hazard -- a false PASS on unreviewed
// code, strictly worse than round 1's false-NEW-violation bug.
//
// rf.ReceiverTypeName (findReachableFuncs) closes this: it records fd's
// OWN receiver type (or noReceiverSentinel for a free function),
// independent of which type matched as the candidate parameter. Appending
// it disambiguates TypeA.Attach from TypeB.Attach. For KindReceiverMethod
// it is always equal to TypeName (redundant there, but keeping one key
// shape for both Kind variants is simpler than a Kind-conditional
// format), so that path's existing collision-freedom (a receiver type
// cannot declare the same method name twice) is unaffected.
//
// Two distinct violations colliding on this key now requires the
// identical 6-tuple (File, TypeName, Kind, FuncName, ValueName,
// ReceiverTypeName). For KindReceiverMethod that still reduces to a
// duplicate method declaration, which Go forbids. For KindParameterFunc,
// fixing ReceiverTypeName pins WHICH function declared the parameter, so
// a further collision would require literally the same function
// (same receiver, same name, same file) to be declared twice with the
// same matched parameter name -- again not achievable in valid Go source.
// Two free functions with the same name in the same file/package are
// likewise a duplicate declaration Go itself forbids (both would carry
// ReceiverTypeName == noReceiverSentinel, so they are not distinguished
// by receiver type, but they can never coexist to begin with).
//
// Round 3 (Destructive reattack, attacker unrecorded by name in this
// comment) re-verified round 2's fix and found no further collision --
// this key format shipped unchanged.
//
// Round 4 (Destructive reattack, attacker "Riftline") REJECTed it anyway,
// via a DIFFERENT mechanism than either prior round: baseTypeName (see
// its own doc comment) had no case for a GENERIC receiver's type
// parameter list (*ast.IndexExpr for one type parameter, e.g. Set[T];
// *ast.IndexListExpr for more than one, e.g. Map[K, V]). A generic
// receiver method, e.g. func (s *Set[T]) Attach(g *Guarded), therefore
// made baseTypeName return ok=false for fd.Recv.List[0].Type even though
// fd.Recv WAS non-nil -- and round 2's computation of recvTypeName (see
// findReachableFuncs) silently treated that ok=false the same as
// "fd.Recv == nil", falling back to noReceiverSentinel. Net effect: a
// generic-receiver method and an unrelated genuine free function of the
// same name/parameter (Set[T].Attach(g *Guarded) and a free
// func Attach(g *Guarded)) both computed ReceiverTypeName ==
// noReceiverSentinel and collided onto an IDENTICAL violationKey -- the
// same "accept silently suppresses a different unreviewed hazard" failure
// mode as round 2's TypeA/TypeB collision, reached via a new mechanism
// (an unrecognised-but-present receiver shape, not an unrecorded-but-
// present one).
//
// Round 5 closed it two ways: (1) baseTypeName now recognises
// *ast.IndexExpr/*ast.IndexListExpr and unwraps them to the base
// identifier, so Set[T] and Map[K, V] receivers resolve to "Set"/"Map"
// like any other named receiver type, instead of returning ok=false; (2)
// as defense-in-depth for whatever receiver shape neither this round nor
// round 2 anticipated, findReachableFuncs no longer treats ANY
// unrecognised-but-present receiver (fd.Recv != nil, baseTypeName
// ok=false) as equivalent to no receiver at all -- it now assigns the
// dedicated unrecognizedReceiverSentinel (see its own doc comment)
// instead of falling back to noReceiverSentinel, so an unrecognised
// receiver method can never again collide with a genuine free function on
// this key, even if a future Go receiver shape this gate doesn't parse
// shows up.
//
// Round 6 (Destructive reattack, attacker "Ashcombe") REJECTed round 5
// anyway. Round 5's own defense-in-depth explicitly anticipated a "FUTURE
// unrecognised receiver shape" and routed it to unrecognizedReceiverSentinel
// -- but that sentinel is itself a SINGLE fixed string. Ashcombe found two
// DIFFERENT unrecognised receiver shapes that both parse (syntactically
// valid Go source astgate's no-type-checking scope accepts per doc.go) but
// neither of which compiles -- e.g. a map-typed receiver and a chan-typed
// receiver -- and showed both collapse onto the identical
// unrecognizedReceiverSentinel string, so two genuinely different,
// both-unguarded hazards on those receivers collide onto one violationKey
// exactly like every prior round's pair. This is the SAME failure mode
// (round 2's TypeA/TypeB, round 4/5's generic-receiver-vs-free-function)
// for the third time, via a third mechanism: not "receiver shape
// unrecorded" (round 2), not "one receiver shape unrecognised" (round 4),
// but "the SET of receiver shapes a hand-written switch can enumerate is
// never provably complete" -- an enumerated case can always be reattacked
// with a case nobody enumerated yet.
//
// Round 7 (this fix, per Bill's lead ruling 2026-08-12) stops patching
// individual collision cases and replaces the identity scheme itself:
// violationKey no longer uses TypeName (the matched candidate's bare base
// name, e.g. "Guarded" for both a *Guarded and a []Guarded match alike) or
// ReceiverTypeName (baseTypeName's hand-unwrapped classification, capped
// at a fixed enumerated set of shapes plus one catch-all sentinel). It
// uses rf.ReceiverExprPrinted and rf.MatchedExprPrinted instead -- the
// receiver's and the matched value's type expressions, printed VERBATIM
// via go/printer.Fprint straight from the AST node (see printExpr). A
// go/printer print is not a classification with an "unrecognised" case to
// fall through: it renders literal source text for ANY expression the
// parser accepted the file as containing, map/chan receivers and any
// future exotic shape included, so two DIFFERENT receiver (or parameter)
// type expressions can never again reduce to the same printed text unless
// they really are textually identical Go source -- which, combined with
// File/Kind/FuncName/ValueName, only happens for declarations Go itself
// forbids from coexisting (the same closing argument rounds 2-5 already
// established, now resting on a complete-by-construction printed identity
// instead of a hand-enumerated one).
//
// This key is therefore the tuple Bill's ruling specifies: File (the
// node's file), the enclosing declaration chain (ReceiverExprPrinted --
// the receiver's printed type expression, or "" for a package-level free
// function -- plus FuncName, the function's own declared name, its
// innermost link), Kind (which enumeration path matched), and the full
// type expression as printed (MatchedExprPrinted). ValueName (the specific
// parameter/receiver identifier) remains part of the tuple because a
// single function can enumerate more than one ReachableFunc sharing every
// other component -- e.g. two candidate-typed parameters of a function
// under different names -- and those are still genuinely distinct
// violations that must not collide.
//
// Round 7's second mandatory part -- Run's self-check hard-failing on any
// two DISTINCT AST nodes still producing the same key, rather than a
// silent map overwrite -- is implemented in Run itself (see its own doc
// comment and the MET-F703 registry entry), not here: violationKey's job
// is only to compute the key, never to decide what happens when two of
// them collide.
//
// Round 9 (Destructive reattack, attacker "Halyard", REJECT) found that
// ReceiverExprPrinted/MatchedExprPrinted were not actually layout-
// independent despite being "the complete type expression as printed": see
// printExpr's own doc comment for the full mechanism (go/printer replaying
// the original source's line-break placement for a multi-type-param
// generic instantiation) and its fix (canonicalizeTypeExpr's redundant-
// paren stripping plus canonicalizeWhitespace's whitespace collapse,
// applied inside printExpr itself, upstream of this function). This
// function's own tuple shape is unchanged by round 9 -- the fix lives
// entirely in what ReceiverExprPrinted/MatchedExprPrinted now CONTAIN, not
// in how violationKey assembles them.
func violationKey(rf *ReachableFunc) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s", rf.File, rf.ReceiverExprPrinted, rf.Kind, rf.FuncName, rf.ValueName, rf.MatchedExprPrinted)
}

// violationMessage implements AC-14: names the candidate type, the
// unguarded function's name/file:line, which enumeration path caught it
// (receiver vs. parameter — directly answering "how would I have found
// this myself"), and a one-line remedy pointing at the worked example.
func violationMessage(rf *ReachableFunc) string {
	return fmt.Sprintf(
		"%s:%d: %s %q takes candidate type %q (as %s %q) but never calls %s — "+
			"add a %s(...) pre-lock guard (see internal/foundation/errs/log.go's Logger for a worked example)",
		rf.File, rf.Line, rf.Kind, rf.FuncName, rf.TypeName,
		valueRole(rf.Kind), rf.ValueName, guardMethodName, guardMethodName,
	)
}

func valueRole(k FuncKind) string {
	switch k {
	case KindReceiverMethod:
		return "receiver"
	case KindFieldAccess:
		return "field chain"
	}
	return "parameter"
}

// floodMessage implements AC-6/AC-15's advisory (not gate-failing)
// output: names the guarded function whose rejection path touches a
// shared, ring/buffer-shaped package-level variable.
func floodMessage(fr FloodRisk) string {
	return fmt.Sprintf(
		"%s:%d: %s's rejection path (guarding %q) writes to package-level variable %q, "+
			"which is also used outside any rejection path — advisory only (AC-7); "+
			"review whether a copy-holding caller hammering this rejection path could evict genuine entries (SEC-030/SEC-031(b) shape)",
		fr.File, fr.Line, fr.FuncName, fr.TypeName, fr.VarName,
	)
}

// resolveRepoRoot walks upward from the current working directory until
// it finds a go.mod, mirroring resolveRegistryPath's precedent in
// internal/foundation/errs/registry.go (never a hardcoded path).
func resolveRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errs.New("MET-F702", errs.NewCorrelationID(), map[string]any{"dir": dir})
		}
		dir = parent
	}
}
