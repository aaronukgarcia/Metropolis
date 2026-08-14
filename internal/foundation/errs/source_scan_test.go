package errs

// BUG-008: the registry (data/errors.json) that GR#7 calls the single
// source of truth had fallen behind the Go source that actually raises
// MET- codes — several packages carried "placeholder" error constants
// (see e.g. internal/engine/detgate/errors.go's doc comment) that were
// never entered in the registry. That silently degraded every one of
// those errors to the generic MET-F003 "unregistered code" fallback at
// runtime, and — worse — let one collide: a later registration claimed
// MET-E100-E103 for feat.debugmode without noticing detgate's source
// already used those same codes for a different meaning, because
// detgate's claim lived only in a source comment, invisible to a
// registry-only search.
//
// This file is the mechanical check the bug report asked for: it scans
// cmd/ and internal/ for every MET-<layer><NNN> literal actually raised
// in Go source and verifies, against data/errors.json itself (never a
// hardcoded list — GR#15), that (a) every raised code is registered and
// (b) every raised code's registered module matches the module that
// data/errors.json's own "ranges.reserved" table declares as owning the
// numeric range the code falls in. (b) is what would have caught the
// BUG-008 collision: MET-E100 raised in internal/engine/detgate but
// registered under a different module than the range's declared owner.
//
// BUG-037 and BUG-038 closed two blind spots the original mechanical
// scan documented but left open (see scanSourceCodes' doc comment for
// the full shape of each):
//
//   - BUG-037 (comment-only codes): a code that is NEVER constructed in
//     real code, only narrated in a comment, used to be invisible. The
//     scanner now also collects codes mentioned in comments and fails
//     the gate for any comment-only code (never a real string literal
//     anywhere) that is not registered — closing exactly the
//     "detgate's claim lived only in a source comment" shape from the
//     BUG-008 postmortem. A comment-only code that IS registered is not
//     a violation: it carries no collision risk because the registry
//     already knows about it.
//   - BUG-038 (dynamically-built codes): fmt.Sprintf/Sprint/Errorf-style
//     format strings and string concatenation that build a MET- code at
//     runtime used to be invisible because each Go string literal is
//     inspected independently. The scanner now recognises the two
//     textual shapes such construction leaves in source (a bare/partial
//     "MET-<layer><digits>" fragment used as a concatenation operand, or
//     a "MET-<layer>%<verb>" format string) and fails the gate
//     unconditionally for any match — this scanner has no way to
//     resolve the constructed value, so (per the BOW item) it bans the
//     pattern rather than silently trusting it.
//   - BUG-192 (split-prefix concatenation, round 2 of BUG-038): the
//     Destructive attacker on BUG-038 found that splitting the "MET-"
//     prefix ITSELF across two adjacent string literals — e.g.
//     `"MET" + "-E" + fmt.Sprintf("%03d", n)` — evaded both BUG-038
//     patterns entirely, because neither "MET" nor "-E" alone looks like
//     a MET- fragment. The scanner now also flattens each "+"
//     concatenation chain (flattenAddChain) and checks every run of
//     consecutive string-literal operands' CONCATENATED value
//     (checkConcatenatedFragments), not just each literal in isolation.

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// metCodeInStringPattern matches a MET-<layer><NNN or NNNN> code
// appearing anywhere inside the already-unquoted content of a Go string
// literal. The digit count was widened from exactly three to three-or-four
// in lockstep with codeFormat's BUG-234 widening, so a four-digit code is
// still caught (and not truncated to a three-digit false positive).
// It is deliberately applied only to string-literal content (see
// scanSourceCodes) and never to raw file text, so a code mentioned in a
// // comment or a doc string is not treated as "raised" — see the
// "known blind spots" note on scanSourceCodes.
var metCodeInStringPattern = regexp.MustCompile(`MET-[A-Z][0-9]{3,4}`)

// dynamicFragmentPattern matches a Go string literal whose ENTIRE
// (trimmed) content is nothing but a partial MET- code fragment — e.g.
// "MET-", "MET-E", or "MET-E1" — the shape a concatenation operand like
// `"MET-" + "E" + strconv.Itoa(n)` leaves behind (BUG-038). Anchored at
// both ends deliberately: a literal that merely MENTIONS "MET-" as part
// of a longer sentence (e.g. an error-format string like "want
// MET-<layer><NNN>") must NOT match, or every descriptive message about
// the code format would falsely trip the gate.
var dynamicFragmentPattern = regexp.MustCompile(`^MET-[A-Z]?[0-9]{0,2}$`)

// dynamicSprintfPattern matches a MET- prefix immediately followed by an
// optional single layer letter and then a fmt verb ('%'), the shape a
// Sprintf-built code's format string leaves behind (e.g. "MET-E%03d" or
// "MET-%s%03d") — BUG-038. It is NOT anchored, so it can find this shape
// inside a longer format string, but it still requires the '%' to sit
// immediately after "MET-" (or "MET-<letter>"), so prose like "want
// MET-<layer><NNN>" (no '%' there at all) does not match.
var dynamicSprintfPattern = regexp.MustCompile(`MET-[A-Z]?%`)

// flattenAddChain recursively flattens a chain of "+" (token.ADD)
// *ast.BinaryExpr nodes into its leaf operands, left to right — e.g.
// `"MET" + "-E" + fmt.Sprintf("%03d", n)` flattens to
// [BasicLit("MET"), BasicLit("-E"), CallExpr(Sprintf(...))]. Every
// BinaryExpr node visited along the way is recorded in handled so the
// caller's ast.Inspect walk (which will independently visit the same
// nested BinaryExpr nodes as it descends) does not re-flatten and
// re-report the same chain once per sub-expression (BUG-192).
func flattenAddChain(expr ast.Expr, handled map[*ast.BinaryExpr]bool) []ast.Expr {
	bin, ok := expr.(*ast.BinaryExpr)
	if !ok || bin.Op != token.ADD {
		return []ast.Expr{expr}
	}
	handled[bin] = true
	var out []ast.Expr
	out = append(out, flattenAddChain(bin.X, handled)...)
	out = append(out, flattenAddChain(bin.Y, handled)...)
	return out
}

// checkConcatenatedFragments closes the BUG-192 gap in BUG-038's
// detection: dynamicFragmentPattern/dynamicSprintfPattern only ever
// looked at ONE string literal at a time, so splitting the "MET-" prefix
// itself across two adjacent literals in a "+" chain — e.g.
// `"MET" + "-E" + fmt.Sprintf("%03d", n)` — evaded both patterns
// entirely (neither "MET" nor "-E" alone looks like a MET- fragment).
// This walks the flattened leaves of one "+" chain (see
// flattenAddChain) and, for every run of two-or-more CONSECUTIVE string
// literals within it, concatenates their values and runs the same
// complete/partial checks against that concatenation that a single
// literal would get. A run of length 1 is skipped: a lone literal is
// already checked individually where it is visited as a *ast.BasicLit,
// so re-checking it here would only duplicate that result.
//
// This still cannot see a fragment carried through an intermediate
// variable (e.g. `p := "MET"; return p + "-E" + ...`) or split across a
// function call boundary — closing that would need real dataflow
// analysis, not a syntactic walk of one expression tree. See this
// file's "Remaining known blind spots" note.
func checkConcatenatedFragments(leaves []ast.Expr, rel string, fset *token.FileSet, res *scanResult) {
	i := 0
	for i < len(leaves) {
		if lit, ok := leaves[i].(*ast.BasicLit); !ok || lit.Kind != token.STRING {
			i++
			continue
		}
		start := i
		var parts []string
		for i < len(leaves) {
			lit, ok := leaves[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				break
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				break
			}
			parts = append(parts, val)
			i++
		}
		if len(parts) < 2 {
			continue
		}
		concat := strings.Join(parts, "")
		if metCodeInStringPattern.MatchString(concat) ||
			dynamicFragmentPattern.MatchString(strings.TrimSpace(concat)) ||
			dynamicSprintfPattern.MatchString(concat) {
			pos := fset.Position(leaves[start].Pos())
			res.Dynamic = append(res.Dynamic, sourceCode{Code: concat, File: rel, Line: pos.Line})
		}
	}
}

// reservedOwnerPattern extracts the owning module key from one
// data/errors.json ranges.reserved description. Every entry in that
// table is written "reserved for <module.key> (<explanation>)" so the
// owner can be derived from the data itself rather than hardcoded here.
var reservedOwnerPattern = regexp.MustCompile(`^reserved for ([a-z][a-zA-Z0-9]*(?:\.[a-zA-Z0-9]+)*)`)

// sourceCode records one MET- code literal found in scanned Go source,
// with enough location info to build an actionable failure message.
type sourceCode struct {
	Code string
	File string // relative to repo root, forward-slash separated
	Line int
}

// reservedRange is one parsed row of data/errors.json's
// ranges.reserved table: the numeric [Low, High] window within layer
// Layer, and the module key that owns it.
type reservedRange struct {
	Layer    byte
	Low      int
	High     int
	Owner    string
	RawKey   string // e.g. "E100-E199", for error messages
	RawValue string // the original description, for error messages
}

// errorsDoc is the minimal shape of data/errors.json this scanner
// reads directly (independent of registryEntry/loadRegistry, which
// only expose the "codes" section) — just enough to recover
// ranges.reserved.
type errorsDoc struct {
	Ranges struct {
		Reserved map[string]string `json:"reserved"`
	} `json:"ranges"`
}

// loadReservedRanges reads and parses the ranges.reserved table from
// the data/errors.json at path.
func loadReservedRanges(path string) ([]reservedRange, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc errorsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding ranges.reserved from %s: %w", path, err)
	}

	var out []reservedRange
	for key, desc := range doc.Ranges.Reserved {
		lo, hi, layer, ok := parseRangeKey(key)
		if !ok {
			return nil, fmt.Errorf("ranges.reserved key %q in %s is not in <Layer><NNN>-<Layer><NNN> form", key, path)
		}
		m := reservedOwnerPattern.FindStringSubmatch(desc)
		if m == nil {
			return nil, fmt.Errorf(
				"ranges.reserved[%q] = %q in %s does not start with \"reserved for <module.key>\" — "+
					"the source scanner (this file) derives range ownership from that exact phrasing; "+
					"reword the entry (without changing which codes it covers) or teach reservedOwnerPattern about the new phrasing",
				key, desc, path)
		}
		out = append(out, reservedRange{Layer: layer, Low: lo, High: hi, Owner: m[1], RawKey: key, RawValue: desc})
	}
	return out, nil
}

// parseRangeKey parses a ranges.reserved key like "E100-E199" into its
// numeric bounds and shared layer letter.
func parseRangeKey(key string) (low, high int, layer byte, ok bool) {
	parts := strings.SplitN(key, "-", 2)
	if len(parts) != 2 || len(parts[0]) < 2 || len(parts[1]) < 2 {
		return 0, 0, 0, false
	}
	if parts[0][0] != parts[1][0] {
		return 0, 0, 0, false
	}
	lo, err1 := strconv.Atoi(parts[0][1:])
	hi, err2 := strconv.Atoi(parts[1][1:])
	if err1 != nil || err2 != nil {
		return 0, 0, 0, false
	}
	return lo, hi, parts[0][0], true
}

// ownerFor returns the module key that ranges owns code under, and
// whether any declared range actually covers it.
func ownerFor(code string, ranges []reservedRange) (owner string, ok bool) {
	if !codeFormat.MatchString(code) {
		return "", false
	}
	layer := code[4]
	num, err := strconv.Atoi(code[5:])
	if err != nil {
		return "", false
	}
	for _, r := range ranges {
		if r.Layer == layer && num >= r.Low && num <= r.High {
			return r.Owner, true
		}
	}
	return "", false
}

// scanResult is everything scanSourceCodes finds in one pass, split by
// how the code text was found — each list feeds a different check.
type scanResult struct {
	Literals []sourceCode // complete MET-<layer><NNN> codes inside real string literals — "raised" in the BUG-008 sense
	Comments []sourceCode // complete MET-<layer><NNN> codes found in // or /* */ comment text (BUG-037)
	Dynamic  []sourceCode // literals that look like a partially-built MET- code fragment or Sprintf format string (BUG-038)
}

// scanSourceCodes walks every non-test .go file under repoRoot/<dir>
// for each dir in dirs and collects, per scanResult:
//
//   - Literals: every complete MET-<layer><NNN> code found inside a Go
//     string literal (regular "..." or raw `...`).
//   - Comments: every complete MET-<layer><NNN> code found inside
//     comment text — collected so the caller can flag a code that is
//     NEVER a real literal anywhere, only ever narrated in a comment
//     (BUG-037). A code that appears in both Literals and Comments is
//     not comment-only, so it carries no extra risk; the caller is
//     expected to diff the two lists rather than treat every comment
//     hit as a violation (several packages' doc comments legitimately
//     narrate a range using its own boundary codes in prose alongside
//     the real, registered, literal-raised code).
//   - Dynamic: string literals that look like a MET- code under
//     construction rather than a complete code — a bare/partial
//     concatenation fragment ("MET-", "MET-E") or a Sprintf-style
//     format string ("MET-E%03d") — see dynamicFragmentPattern /
//     dynamicSprintfPattern. This scanner has no way to resolve what
//     value such code produces at runtime (that needs go/types constant
//     folding, out of scope here), so every match is reported for the
//     caller to fail on unconditionally (BUG-038).
//
// Remaining known blind spots (documented rather than silently
// unhandled — a scanner with unadvertised false negatives is worse than
// no scanner):
//
//   - _test.go files are excluded outright. Several existing tests
//     (e.g. registry_test.go, log_test.go) deliberately use
//     unregistered fixture codes like MET-F900/F901/F999 to exercise
//     the unregistered-code fallback path; scanning them would demand
//     those fixtures be registered, defeating their purpose. This also
//     means a dynamically-built or comment-only code that appears only
//     in a _test.go file is not seen — acceptable, since _test.go is
//     never part of a shipped binary's error surface.
//   - Non-.go MEDIA are architecturally invisible: the WalkDir callback
//     below rejects any path not ending in ".go" before anything is
//     ever parsed, so a MET- code embedded via go:embed in a
//     template/config/i18n resource file and referenced dynamically at
//     runtime is never seen, no matter how it's written inside that
//     file. Not live today (zero go:embed directives and zero MET-
//     codes in non-.go files under cmd/ and internal/, checked by hand
//     — ASM-019), but a real, permanent gap for whatever comes next.
//   - Multi-hop / dataflow concatenation is still invisible after
//     BUG-192's fix. checkConcatenatedFragments only sees literals that
//     are DIRECT, syntactically-adjacent operands of the same "+" chain
//     in one expression. A fragment carried through an intermediate
//     variable (`p := "MET"; return p + "-E" + fmt.Sprintf(...)`), built
//     across multiple statements, or assembled via strings.Builder /
//     strings.Join, still evades detection — that needs real dataflow
//     analysis (tracking values across assignments and calls), which is
//     disproportionate to this bug's risk per the BOW item's own
//     assessment; accepted as a residual gap rather than attempted here.
func scanSourceCodes(t testing.TB, repoRoot string, dirs ...string) scanResult {
	t.Helper()
	fset := token.NewFileSet()
	var res scanResult

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
				return fmt.Errorf("parsing %s: %w", path, perr)
			}

			rel, rerr := filepath.Rel(repoRoot, path)
			if rerr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)

			handledBin := map[*ast.BinaryExpr]bool{}
			ast.Inspect(file, func(n ast.Node) bool {
				if bin, ok := n.(*ast.BinaryExpr); ok && bin.Op == token.ADD {
					if !handledBin[bin] {
						leaves := flattenAddChain(bin, handledBin)
						checkConcatenatedFragments(leaves, rel, fset, &res)
					}
					return true
				}

				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, uqerr := strconv.Unquote(lit.Value)
				if uqerr != nil {
					return true
				}
				pos := fset.Position(lit.Pos())

				complete := metCodeInStringPattern.FindAllString(val, -1)
				if len(complete) > 0 {
					for _, code := range complete {
						res.Literals = append(res.Literals, sourceCode{Code: code, File: rel, Line: pos.Line})
					}
					return true
				}

				// Only consider a literal "dynamic" when it holds no
				// complete code of its own (checked above) — a literal
				// like "MET-E100 (see MET-E%03d family)" should be
				// reported as the raised MET-E100 it plainly is, not
				// flagged as an unresolved dynamic fragment on top.
				if dynamicFragmentPattern.MatchString(strings.TrimSpace(val)) || dynamicSprintfPattern.MatchString(val) {
					res.Dynamic = append(res.Dynamic, sourceCode{Code: val, File: rel, Line: pos.Line})
				}
				return true
			})

			for _, cg := range file.Comments {
				for _, c := range cg.List {
					for _, code := range metCodeInStringPattern.FindAllString(c.Text, -1) {
						pos := fset.Position(c.Pos())
						res.Comments = append(res.Comments, sourceCode{Code: code, File: rel, Line: pos.Line})
					}
				}
			}

			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}

	sortSourceCodes(res.Literals)
	sortSourceCodes(res.Comments)
	sortSourceCodes(res.Dynamic)
	return res
}

// sortSourceCodes orders a []sourceCode by file then line, in place, so
// scan results and the failure messages built from them are
// deterministic across runs.
func sortSourceCodes(codes []sourceCode) {
	sort.Slice(codes, func(i, j int) bool {
		if codes[i].File != codes[j].File {
			return codes[i].File < codes[j].File
		}
		return codes[i].Line < codes[j].Line
	})
}

// verifySourceCodes is the pure check at the heart of this file: given
// every code raised in source, the registered codes, and the parsed
// reserved-range table, it returns one human-readable violation per
// problem. An empty result means the registry is complete and
// internally consistent with source. Kept separate from
// scanSourceCodes/loadReservedRanges/loadRegistry so it can be driven
// with fixture data in tests, independent of the live tree (see
// TestVerifySourceCodes_* below).
func verifySourceCodes(found []sourceCode, registry map[string]registryEntry, ranges []reservedRange) []string {
	var violations []string
	for _, sc := range found {
		entry, registered := registry[sc.Code]
		if !registered {
			violations = append(violations, fmt.Sprintf(
				"%s:%d: raises %s, which is not registered in data/errors.json (GR#7) — add a codes entry with real severity/module/message/remedy",
				sc.File, sc.Line, sc.Code))
			continue
		}

		owner, hasRange := ownerFor(sc.Code, ranges)
		if !hasRange {
			violations = append(violations, fmt.Sprintf(
				"%s:%d: %s is registered but data/errors.json's ranges.reserved table has no entry covering it — add a reserved-range entry for its owning module",
				sc.File, sc.Line, sc.Code))
			continue
		}

		if owner != entry.Module {
			violations = append(violations, fmt.Sprintf(
				"%s:%d: %s is registered to module %q, but data/errors.json's ranges.reserved table declares that range owned by %q — "+
					"this is the exact range-collision failure mode BUG-008 was filed for (two modules assigned the same code)",
				sc.File, sc.Line, sc.Code, entry.Module, owner))
		}
	}
	return violations
}

// verifyDynamicSites turns every scanResult.Dynamic entry into a
// violation. There is nothing to weigh here (unlike the comment-only
// check below): this scanner cannot resolve what code a dynamically
// built literal actually produces, so it cannot verify registration or
// range ownership for it — per BUG-038, every match is banned
// unconditionally rather than silently trusted.
func verifyDynamicSites(dynamic []sourceCode) []string {
	var violations []string
	for _, d := range dynamic {
		violations = append(violations, fmt.Sprintf(
			"%s:%d: %q looks like a dynamically-constructed MET- code (string concatenation or a Sprintf-style format string) — "+
				"this scanner (BUG-008/BUG-038) cannot resolve the value such code produces at runtime, so it cannot verify "+
				"registration or range ownership for it; use a literal MET-<layer><NNN> string instead",
			d.File, d.Line, d.Code))
	}
	return violations
}

// verifyCommentOnlyCodes flags every code that appears ONLY in comment
// text — never in a real string literal anywhere in the scanned tree —
// and is not registered in data/errors.json. This is exactly the
// BUG-008 postmortem shape (BUG-037): a range claimed only in a doc
// comment, invisible to a registry-only search, later silently
// collided with a real registration elsewhere. A comment-only code that
// IS already registered is not flagged: the registry already knows
// about it, so there is no collision risk left to close.
func verifyCommentOnlyCodes(comments, literals []sourceCode, registry map[string]registryEntry) []string {
	raised := make(map[string]bool, len(literals))
	for _, l := range literals {
		raised[l.Code] = true
	}

	var violations []string
	reported := map[string]bool{}
	for _, c := range comments {
		if raised[c.Code] {
			continue // also raised in real code elsewhere — not comment-only
		}
		if _, registered := registry[c.Code]; registered {
			continue // already registered — no collision risk left
		}
		key := c.Code + "@" + c.File + ":" + strconv.Itoa(c.Line)
		if reported[key] {
			continue
		}
		reported[key] = true
		violations = append(violations, fmt.Sprintf(
			"%s:%d: %s appears only in a comment (never in a real code path) and is not registered in data/errors.json (BUG-008/BUG-037) — "+
				"register it if it names a real claimed/planned code, or reword the comment if it does not",
			c.File, c.Line, c.Code))
	}
	return violations
}

// TestSourceCodesAreRegisteredAndInRange is the mechanical gate BUG-008
// asked for: every MET- code actually raised anywhere in cmd/ or
// internal/ (excluding _test.go — see scanSourceCodes) must be
// registered in data/errors.json, and its registered module must match
// the module data/errors.json's own ranges.reserved table declares for
// that numeric range. Both the expected registrations and the expected
// ranges are read from data/errors.json itself at test time — nothing
// here is a hardcoded list of codes (GR#15).
func TestSourceCodesAreRegisteredAndInRange(t *testing.T) {
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
	// regPath is <repoRoot>/data/errors.json.
	repoRoot := filepath.Dir(filepath.Dir(regPath))

	ranges, err := loadReservedRanges(regPath)
	if err != nil {
		t.Fatalf("loadReservedRanges: %v", err)
	}

	result := scanSourceCodes(t, repoRoot, "cmd", "internal")
	if len(result.Literals) == 0 {
		t.Fatal("scanSourceCodes found zero MET- codes in cmd/ or internal/ — the scanner is almost certainly broken (repoRoot resolution, walk paths, or the literal pattern), not that the codebase stopped raising errors")
	}

	var violations []string
	violations = append(violations, verifySourceCodes(result.Literals, registry, ranges)...)
	violations = append(violations, verifyDynamicSites(result.Dynamic)...)
	violations = append(violations, verifyCommentOnlyCodes(result.Comments, result.Literals, registry)...)
	if len(violations) > 0 {
		t.Errorf("%d error-registry violation(s) found (BUG-008 class — see this file's package doc comment):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// --- negative controls: prove the gate actually fails, via fixture
// data rather than a deliberate violation left in the live tree (see
// the dispatch brief's verification requirement). ---

func TestVerifySourceCodes_CatchesUnregisteredCode(t *testing.T) {
	registry := map[string]registryEntry{
		"MET-E100": {Severity: "error", Module: "engine.detgate", Message: "m", Remedy: "r"},
	}
	ranges := []reservedRange{{Layer: 'E', Low: 100, High: 199, Owner: "engine.detgate"}}
	found := []sourceCode{{Code: "MET-E999", File: "internal/fixture/pkg/file.go", Line: 42}}

	violations := verifySourceCodes(found, registry, ranges)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation for an unregistered code, got %d: %v", len(violations), violations)
	}
	for _, want := range []string{"MET-E999", "internal/fixture/pkg/file.go:42", "not registered"} {
		if !strings.Contains(violations[0], want) {
			t.Errorf("violation message %q missing expected substring %q", violations[0], want)
		}
	}
}

func TestVerifySourceCodes_CatchesRangeOwnershipViolation(t *testing.T) {
	// Reproduces BUG-008 exactly: MET-E100 is registered, but under the
	// WRONG module for the range data/errors.json says owns it — the
	// silent-collision scenario, not a missing-registration one.
	registry := map[string]registryEntry{
		"MET-E100": {Severity: "error", Module: "feat.debugmode", Message: "m", Remedy: "r"},
	}
	ranges := []reservedRange{{Layer: 'E', Low: 100, High: 199, Owner: "engine.detgate"}}
	found := []sourceCode{{Code: "MET-E100", File: "internal/engine/detgate/errors.go", Line: 25}}

	violations := verifySourceCodes(found, registry, ranges)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 range-ownership violation, got %d: %v", len(violations), violations)
	}
	for _, want := range []string{"MET-E100", "engine.detgate", "feat.debugmode", "BUG-008"} {
		if !strings.Contains(violations[0], want) {
			t.Errorf("violation message %q missing expected substring %q", violations[0], want)
		}
	}
}

func TestVerifySourceCodes_CatchesUndeclaredRange(t *testing.T) {
	registry := map[string]registryEntry{
		"MET-E500": {Severity: "error", Module: "engine.somewhere", Message: "m", Remedy: "r"},
	}
	// No reserved range covers E500 at all.
	ranges := []reservedRange{{Layer: 'E', Low: 100, High: 199, Owner: "engine.detgate"}}
	found := []sourceCode{{Code: "MET-E500", File: "internal/engine/somewhere/errors.go", Line: 7}}

	violations := verifySourceCodes(found, registry, ranges)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 undeclared-range violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "no entry covering it") {
		t.Errorf("violation message %q missing expected undeclared-range wording", violations[0])
	}
}

func TestVerifySourceCodes_CleanInputProducesNoViolations(t *testing.T) {
	registry := map[string]registryEntry{
		"MET-E100": {Severity: "error", Module: "engine.detgate", Message: "m", Remedy: "r"},
	}
	ranges := []reservedRange{{Layer: 'E', Low: 100, High: 199, Owner: "engine.detgate"}}
	found := []sourceCode{{Code: "MET-E100", File: "internal/engine/detgate/errors.go", Line: 25}}

	if violations := verifySourceCodes(found, registry, ranges); len(violations) != 0 {
		t.Fatalf("expected no violations for consistent input, got: %v", violations)
	}
}

// TestScanSourceCodes_LiteralsOnlyNotComments proves scanSourceCodes'
// documented blind spot behaviour on a small fixture tree: a code
// inside a string literal is found; a code that appears only inside a
// // comment is not; a code inside a _test.go file is not.
func TestScanSourceCodes_LiteralsOnlyNotComments(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "fixture")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := `package fixture

// This comment mentions MET-Z999 but never constructs it.
const realCode = "MET-Z100"

func raise() string {
	return "MET-Z100"
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture.go: %v", err)
	}

	testSrc := `package fixture

const testOnlyCode = "MET-Z200"
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write fixture_test.go: %v", err)
	}

	result := scanSourceCodes(t, dir, "internal")

	seen := map[string]bool{}
	for _, sc := range result.Literals {
		seen[sc.Code] = true
	}
	if !seen["MET-Z100"] {
		t.Error("expected MET-Z100 (string literal in non-test file) to be found")
	}
	if seen["MET-Z999"] {
		t.Error("MET-Z999 only ever appears in a comment — it must NOT be reported as a raised (Literals) code")
	}
	if seen["MET-Z200"] {
		t.Error("MET-Z200 only appears in a _test.go file — it must NOT be reported as raised")
	}

	seenComments := map[string]bool{}
	for _, sc := range result.Comments {
		seenComments[sc.Code] = true
	}
	if !seenComments["MET-Z999"] {
		t.Error("expected MET-Z999 (comment-only mention) to be captured in Comments for the BUG-037 check")
	}
	if seenComments["MET-Z200"] {
		t.Error("MET-Z200 lives in a _test.go file, which is excluded entirely — it must not appear in Comments either")
	}
}

// TestScanSourceCodes_DetectsDynamicConstruction is BUG-038's regression
// fixture: a string-concatenation fragment and a Sprintf-style format
// string must both surface in scanResult.Dynamic (not Literals, since
// neither is a complete code), while an ordinary complete literal code
// stays out of Dynamic entirely.
func TestScanSourceCodes_DetectsDynamicConstruction(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "fixture")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := `package fixture

import "fmt"

const realCode = "MET-Z100"

func concatCode(n int) string {
	return "MET-" + fmt.Sprintf("%d", n)
}

func sprintfCode(n int) string {
	return fmt.Sprintf("MET-Z%03d", n)
}

func descriptiveMessage() string {
	// This must NOT be treated as dynamic construction: it is prose
	// describing the format, with no '%' verb sitting right after the
	// MET- prefix or layer letter.
	return fmt.Sprintf("invalid code %q (want MET-<layer><NNN>)", "x")
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture.go: %v", err)
	}

	result := scanSourceCodes(t, dir, "internal")

	if len(result.Dynamic) != 2 {
		t.Fatalf("expected exactly 2 dynamic-construction sites (concat fragment + Sprintf format string), got %d: %+v", len(result.Dynamic), result.Dynamic)
	}

	literalCodes := map[string]bool{}
	for _, sc := range result.Literals {
		literalCodes[sc.Code] = true
	}
	if !literalCodes["MET-Z100"] {
		t.Error("expected MET-Z100 (complete literal) to still be found in Literals")
	}

	for _, d := range result.Dynamic {
		if d.Code == "MET-Z100" {
			t.Error("MET-Z100 is a complete literal and must not also appear in Dynamic")
		}
	}
}

// TestScanSourceCodes_DetectsSplitPrefixConcatenation is BUG-192's
// regression fixture: the Destructive attacker's exact repro on BUG-038
// (`"MET" + "-E" + fmt.Sprintf("%03d", n)`) splits the "MET-" prefix
// itself across two literals, which used to defeat dynamicFragmentPattern
// and dynamicSprintfPattern entirely (neither operand alone looks like a
// MET- fragment). It must now surface in scanResult.Dynamic via
// checkConcatenatedFragments. Also reconfirms the registry.go-shaped
// false positive BUG-038's anchoring exists to avoid — a single
// non-concatenated literal like "invalid code %q (want MET-<layer><NNN>)"
// — still does not trip, now that concatenation chains are inspected too.
func TestScanSourceCodes_DetectsSplitPrefixConcatenation(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "internal", "fixture")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := `package fixture

import "fmt"

func splitPrefixCode(n int) string {
	return "MET" + "-E" + fmt.Sprintf("%03d", n)
}

func descriptiveMessage() string {
	// A single literal, not a "+" concatenation of two MET- fragments —
	// must NOT be treated as dynamic construction (BUG-038's
	// anchoring reason).
	return fmt.Sprintf("invalid code %q (want MET-<layer><NNN>)", "x")
}

func unrelatedConcatenation() string {
	// Ordinary string-building unrelated to MET- codes must not trip
	// the new concatenation check either.
	return "hello" + " " + "world"
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture.go: %v", err)
	}

	result := scanSourceCodes(t, dir, "internal")

	if len(result.Dynamic) != 1 {
		t.Fatalf("expected exactly 1 dynamic-construction site (the split \"MET\"+\"-E\" prefix), got %d: %+v", len(result.Dynamic), result.Dynamic)
	}
	if got := result.Dynamic[0].Code; got != "MET-E" {
		t.Errorf("expected the flagged concatenation to be %q, got %q", "MET-E", got)
	}
	if len(result.Literals) != 0 {
		t.Errorf("expected no complete Literals hits from this fixture, got %+v", result.Literals)
	}
}

// TestVerifyDynamicSites_AlwaysFlags proves verifyDynamicSites bans
// every dynamic-construction site unconditionally — there is no
// registry state that makes a dynamically-built code acceptable to this
// scanner, since it cannot resolve what value the code actually is.
func TestVerifyDynamicSites_AlwaysFlags(t *testing.T) {
	dynamic := []sourceCode{{Code: `MET-E%03d`, File: "internal/fixture/pkg/file.go", Line: 9}}

	violations := verifyDynamicSites(dynamic)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d: %v", len(violations), violations)
	}
	for _, want := range []string{"internal/fixture/pkg/file.go:9", "dynamically-constructed", "BUG-038"} {
		if !strings.Contains(violations[0], want) {
			t.Errorf("violation message %q missing expected substring %q", violations[0], want)
		}
	}
}

// TestVerifyCommentOnlyCodes_FlagsUnregisteredCommentOnly proves the
// BUG-037 gap is now caught: a code that appears only in a comment and
// has no registry entry must fail.
func TestVerifyCommentOnlyCodes_FlagsUnregisteredCommentOnly(t *testing.T) {
	comments := []sourceCode{{Code: "MET-Z999", File: "internal/fixture/pkg/file.go", Line: 3}}
	var literals []sourceCode
	registry := map[string]registryEntry{}

	violations := verifyCommentOnlyCodes(comments, literals, registry)
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d: %v", len(violations), violations)
	}
	for _, want := range []string{"MET-Z999", "internal/fixture/pkg/file.go:3", "only in a comment", "BUG-037"} {
		if !strings.Contains(violations[0], want) {
			t.Errorf("violation message %q missing expected substring %q", violations[0], want)
		}
	}
}

// TestVerifyCommentOnlyCodes_DoesNotFlagRegisteredOrAlsoLiteral proves
// the check does not over-fire: a comment-only code that IS registered
// is safe (no collision risk left), and a code that appears in both
// comments and literals is not "comment-only" at all.
func TestVerifyCommentOnlyCodes_DoesNotFlagRegisteredOrAlsoLiteral(t *testing.T) {
	comments := []sourceCode{
		{Code: "MET-Z998", File: "internal/fixture/pkg/registered.go", Line: 3},
		{Code: "MET-Z997", File: "internal/fixture/pkg/alsoliteral.go", Line: 5},
	}
	literals := []sourceCode{
		{Code: "MET-Z997", File: "internal/fixture/pkg/alsoliteral.go", Line: 12},
	}
	registry := map[string]registryEntry{
		"MET-Z998": {Severity: "error", Module: "fixture.pkg", Message: "m", Remedy: "r"},
	}

	violations := verifyCommentOnlyCodes(comments, literals, registry)
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %d: %v", len(violations), violations)
	}
}
