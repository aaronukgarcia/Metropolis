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

// metCodeInStringPattern matches a MET-<layer><NNN> code appearing
// anywhere inside the already-unquoted content of a Go string literal.
// It is deliberately applied only to string-literal content (see
// scanSourceCodes) and never to raw file text, so a code mentioned in a
// // comment or a doc string is not treated as "raised" — see the
// "known blind spots" note on scanSourceCodes.
var metCodeInStringPattern = regexp.MustCompile(`MET-[A-Z][0-9]{3}`)

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

// scanSourceCodes walks every non-test .go file under repoRoot/<dir>
// for each dir in dirs and collects every MET-<layer><NNN> code found
// inside a Go string literal (regular "..." or raw `...`).
//
// Known blind spots (documented rather than silently unhandled — a
// scanner with unadvertised false negatives is worse than no scanner):
//
//   - Comments are never scanned. A code mentioned only in a // or /*
//     */ comment (e.g. a range explanation like "claims MET-E000-
//     MET-E099") is NOT treated as raised. This is deliberate, not an
//     oversight: several packages' doc comments narrate a range using
//     its own boundary codes in prose, which are not actually
//     constructed anywhere — scanning comments would demand a
//     registry entry for text that is not a real error path. The
//     trade-off is that a code ONLY ever written in a comment (never
//     actually used in real code) is invisible to this check either
//     way — logged as ASM-* per the dispatch brief.
//   - String CONCATENATION defeats this scanner: `"MET-" + "E" +
//     "000"` or fmt.Sprintf-built codes are invisible, because each
//     Go string literal is inspected independently, exactly as the Go
//     parser tokenizes it. Every error code in this codebase today is
//     a single literal (checked by hand during this item), but this
//     is a real, permanent blind spot for whatever comes next.
//   - _test.go files are excluded outright. Several existing tests
//     (e.g. registry_test.go, log_test.go) deliberately use
//     unregistered fixture codes like MET-F900/F901/F999 to exercise
//     the unregistered-code fallback path; scanning them would demand
//     those fixtures be registered, defeating their purpose.
//   - Non-.go MEDIA are architecturally invisible: the WalkDir callback
//     below rejects any path not ending in ".go" before anything is
//     ever parsed, so a MET- code embedded via go:embed in a
//     template/config/i18n resource file and referenced dynamically at
//     runtime is never seen, no matter how it's written inside that
//     file. Distinct from the concatenation blind spot above (that one
//     is about how a Go string is built; this one is about a whole
//     other file type the walk never opens at all). Not live today
//     (zero go:embed directives and zero MET- codes in non-.go files
//     under cmd/ and internal/, checked by hand — ASM-019), but a real,
//     permanent gap for whatever comes next, same posture as
//     concatenation.
func scanSourceCodes(t testing.TB, repoRoot string, dirs ...string) []sourceCode {
	t.Helper()
	fset := token.NewFileSet()
	var out []sourceCode

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

			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				val, uqerr := strconv.Unquote(lit.Value)
				if uqerr != nil {
					return true
				}
				for _, code := range metCodeInStringPattern.FindAllString(val, -1) {
					pos := fset.Position(lit.Pos())
					out = append(out, sourceCode{Code: code, File: rel, Line: pos.Line})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", root, err)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
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

	found := scanSourceCodes(t, repoRoot, "cmd", "internal")
	if len(found) == 0 {
		t.Fatal("scanSourceCodes found zero MET- codes in cmd/ or internal/ — the scanner is almost certainly broken (repoRoot resolution, walk paths, or the literal pattern), not that the codebase stopped raising errors")
	}

	violations := verifySourceCodes(found, registry, ranges)
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

	found := scanSourceCodes(t, dir, "internal")

	seen := map[string]bool{}
	for _, sc := range found {
		seen[sc.Code] = true
	}
	if !seen["MET-Z100"] {
		t.Error("expected MET-Z100 (string literal in non-test file) to be found")
	}
	if seen["MET-Z999"] {
		t.Error("MET-Z999 only ever appears in a comment — it must NOT be reported as raised")
	}
	if seen["MET-Z200"] {
		t.Error("MET-Z200 only appears in a _test.go file — it must NOT be reported as raised")
	}
}
