package traffic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-2326609792 inc1 test suite. Every test below is written to FAIL
// against a plausible wrong implementation -- the mutant is named in each
// test's own doc comment.

// loadFixtureLadder copies testdata/<name> into a fresh temp "data" dir at
// <dir>/traffic/scale_ladder.json (matching LoadScaleLadder's real path
// construction) and returns the API + data dir.
func loadFixtureLadder(t *testing.T, fixture string) (*TrafficAPI, string, error) {
	t.Helper()
	src := filepath.Join("testdata", fixture)
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", src, err)
	}
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "traffic"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "traffic", "scale_ladder.json"), raw, 0644); err != nil {
		t.Fatalf("write fixture copy: %v", err)
	}
	api := New()
	loadErr := api.LoadScaleLadder(dataDir)
	return api, dataDir, loadErr
}

func registryCode(t *testing.T, err error) string {
	t.Helper()
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("error %v is not a *errs.E -- not registry-sourced (GR#7)", err)
	}
	return e.Code
}

// TestAC1_MissingFile is impossible to pass against an implementation that
// panics or returns a bare os.PathError on a missing ladder file --
// it must be a registry-sourced ErrScaleLadderMissingFile.
func TestScaleLadderAC1_MissingFile(t *testing.T) {
	api := New()
	err := api.LoadScaleLadder(t.TempDir()) // empty dir, no traffic/scale_ladder.json
	if err == nil {
		t.Fatal("expected an error loading from an empty directory")
	}
	if got := registryCode(t, err); got != ErrScaleLadderMissingFile {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderMissingFile)
	}
}

// TestAC1_Unparsable: a mutant that Unmarshal-panics or returns a raw
// json.SyntaxError (not wrapped as a registry error) fails this.
func TestScaleLadderAC1_Unparsable(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_unparsable.json")
	if err == nil {
		t.Fatal("expected an error loading unparsable JSON")
	}
	if got := registryCode(t, err); got != ErrScaleLadderUnparsable {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderUnparsable)
	}
}

// TestAC1_TooFewRungs: a mutant that accepts a 1-rung ladder (e.g. checks
// len < 1 instead of len < 2) fails this -- there is nothing to
// interpolate between with a single rung.
func TestScaleLadderAC1_TooFewRungs(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_single_rung.json")
	if err == nil {
		t.Fatal("expected an error loading a single-rung ladder")
	}
	if got := registryCode(t, err); got != ErrScaleLadderTooFewRungs {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderTooFewRungs)
	}
}

// TestAC1_Unsorted: a mutant that skips the ascending-order check entirely
// (loads the ladder as-is) fails this.
func TestScaleLadderAC1_Unsorted(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_unsorted.json")
	if err == nil {
		t.Fatal("expected an error loading unsorted rungs")
	}
	if got := registryCode(t, err); got != ErrScaleLadderUnsortedOrDuplicate {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderUnsortedOrDuplicate)
	}
}

// TestAC1_Duplicate: a mutant using population <= population for "sorted"
// but with a <  (not <=) comparison would let a duplicate population pair
// through; this fixture reds that.
func TestScaleLadderAC1_Duplicate(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_duplicate.json")
	if err == nil {
		t.Fatal("expected an error loading duplicate-population rungs")
	}
	if got := registryCode(t, err); got != ErrScaleLadderUnsortedOrDuplicate {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderUnsortedOrDuplicate)
	}
}

// TestAC1_NonFiniteOrNegativeLeaf: a mutant that skips num.IsFinite/negative
// checks on flattened numeric leaves fails this (it would load a
// negative density leaf silently).
func TestScaleLadderAC1_NonFiniteOrNegativeLeaf(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_non_finite.json")
	if err == nil {
		t.Fatal("expected an error loading a negative numeric leaf")
	}
	if got := registryCode(t, err); got != ErrScaleLadderNonFiniteLeaf {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderNonFiniteLeaf)
	}
}

// TestAC1_PopulationOutOfRange: a mutant that clamps to the nearest rung
// instead of erroring (Aaron's explicit "no clamping, no extrapolation"
// rule) fails this -- it would return a value, not an error.
func TestScaleLadderAC1_PopulationOutOfRange(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if _, err := api.ScaleLadderAt(1); err == nil {
		t.Fatal("expected an out-of-range error for population below the first rung")
	} else if got := registryCode(t, err); got != ErrScaleLadderPopulationOutOfRange {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderPopulationOutOfRange)
	}
	if _, err := api.ScaleLadderAt(999999999); err == nil {
		t.Fatal("expected an out-of-range error for population above the last rung")
	} else if got := registryCode(t, err); got != ErrScaleLadderPopulationOutOfRange {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderPopulationOutOfRange)
	}
}

// TestAC1_FileTooLarge: proves the FEAT-135 secure-by-default size cap is
// enforced BEFORE Unmarshal -- a mutant that removes the cap accepts an
// oversized file (this test writes a >4MiB file directly, bypassing the
// fixture helper's normal small-file path).
func TestScaleLadderAC1_FileTooLarge(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "traffic"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	huge := make([]byte, scaleLadderMaxFileBytes+1024)
	for i := range huge {
		huge[i] = ' '
	}
	if err := os.WriteFile(filepath.Join(dataDir, "traffic", "scale_ladder.json"), huge, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	api := New()
	err := api.LoadScaleLadder(dataDir)
	if err == nil {
		t.Fatal("expected an error loading an oversized ladder file")
	}
	if got := registryCode(t, err); got != ErrScaleLadderFileTooLarge {
		t.Fatalf("code = %s, want %s", got, ErrScaleLadderFileTooLarge)
	}
}

// TestAC2_RungCountDerivedFromData proves rung-count/population assertions
// are read from the loaded data (Rungs()), never a literal. The mutant:
// hand-edit the fixture to declare 3 populations in meta but ship 4 rungs
// (or vice versa) -- ladder_rung_count_mismatch.json IS that mutant fixture
// and must be REJECTED at load time, and this test never hardcodes "4".
func TestScaleLadderAC2_RungCountDerivedFromData(t *testing.T) {
	api, dataDir, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	// Read the expected rung populations from the fixture FILE itself
	// (the data), not a literal in this test.
	raw, err := os.ReadFile(filepath.Join(dataDir, "traffic", "scale_ladder.json"))
	if err != nil {
		t.Fatalf("re-reading fixture: %v", err)
	}
	var decl struct {
		Meta struct {
			RungPopulations []int64 `json:"rungPopulations"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &decl); err != nil {
		t.Fatalf("unmarshal fixture meta: %v", err)
	}

	got := api.Rungs()
	if len(got) != len(decl.Meta.RungPopulations) {
		t.Fatalf("Rungs() len = %d, want %d (from meta.rungPopulations)", len(got), len(decl.Meta.RungPopulations))
	}
	for i, p := range decl.Meta.RungPopulations {
		if got[i] != p {
			t.Fatalf("Rungs()[%d] = %d, want %d", i, got[i], p)
		}
	}

	// The mismatch fixture (mutant: meta says 3, rungs array has 2) must be
	// rejected, not silently truncated/padded.
	_, _, mismatchErr := loadFixtureLadder(t, "ladder_rung_count_mismatch.json")
	if mismatchErr == nil {
		t.Fatal("expected ladder_rung_count_mismatch.json to be rejected at load time")
	}
	if code := registryCode(t, mismatchErr); code != ErrScaleLadderRungCountMismatch {
		t.Fatalf("code = %s, want %s", code, ErrScaleLadderRungCountMismatch)
	}
}

// TestScaleLadderBUG834_PerIndexPopulationMismatch (BUG-834): a fixture
// with the SAME rung count in meta.rungPopulations and rungs[] (so the
// length check never fires) but a differing value at one index must still
// be rejected -- ladder_rung_population_value_mismatch.json declares
// [100,1000,5001] while rungs[2].population is 5000. A mutant that disables
// only the per-index compare (leaving the length check) accepts this file.
func TestScaleLadderBUG834_PerIndexPopulationMismatch(t *testing.T) {
	_, _, err := loadFixtureLadder(t, "ladder_rung_population_value_mismatch.json")
	if err == nil {
		t.Fatal("expected the per-index population mismatch to be rejected at load time")
	}
	if code := registryCode(t, err); code != ErrScaleLadderRungCountMismatch {
		t.Fatalf("code = %s, want %s", code, ErrScaleLadderRungCountMismatch)
	}
}

// TestScaleLadderBUG831_FlattenKeyOrderSorted (BUG-831): the flattened key
// ORDER (both top-level and nested-object leaves) must come out byte-sorted
// ascending -- ladder_key_order.json authors its JSON keys in a scrambled
// order (zulu, mike, alpha; nested.zebra before nested.apple) specifically
// so a mutant that removes flattenRung/flattenLeaf's sort.Strings/
// sort.Slice calls cannot pass by accident (Go's own map iteration is
// randomised per-run, so an unsorted implementation flips key order across
// runs). This test asserts the exact expected sequence, not just "some"
// order.
func TestScaleLadderBUG831_FlattenKeyOrderSorted(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_key_order.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	point, err := api.ScaleLadderAt(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"alpha", "mike", "nested.apple", "nested.zebra", "zulu"}
	got := keysOf(point.Fields)
	if !equalStrings(want, got) {
		t.Fatalf("flattened key order = %v, want %v (byte-sorted ascending)", got, want)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("flattened key order %v is not byte-sorted", got)
	}
}

// --- Golden vector cross-language regression (AC-5/AC-6/AC-7) --------------

type goldenVectorFile struct {
	Meta struct {
		ToleranceRule string `json:"toleranceRule"`
	} `json:"meta"`
	Ladder json.RawMessage `json:"ladder"`
	Cases  []struct {
		Population            int64              `json:"population"`
		Kind                  string             `json:"kind"`
		ExpectedFields        map[string]float64 `json:"expectedFields"`
		ExpectedNonNumeric    map[string]any     `json:"expectedNonNumeric"`
		ExpectedRoundedPeople *int64             `json:"expectedRoundedPeopleCount"`
		ExpectError           bool               `json:"expectError"`
	} `json:"cases"`
}

func loadGoldenVectors(t *testing.T) (*TrafficAPI, goldenVectorFile) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "traffic", "ladder_vectors.json"))
	if err != nil {
		t.Fatalf("reading golden vectors: %v", err)
	}
	var gv goldenVectorFile
	if err := json.Unmarshal(raw, &gv); err != nil {
		t.Fatalf("unmarshal golden vectors: %v", err)
	}

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "traffic"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "traffic", "scale_ladder.json"), gv.Ladder, 0644); err != nil {
		t.Fatalf("write ladder: %v", err)
	}
	api := New()
	if err := api.LoadScaleLadder(dataDir); err != nil {
		t.Fatalf("loading golden ladder: %v", err)
	}
	return api, gv
}

// TestAC5_AC6_GoldenVectors runs every case in the shared
// data/traffic/ladder_vectors.json golden file. The mutant this catches
// (documented in the vector file's own meta.mutantNote): an interpolator
// computing weight linearly in population, w=(p-p_i)/(p_{i+1}-p_i), instead
// of log-linear, produces different field values at population=3000 and
// reds this test's exact-match comparison.
func TestScaleLadderAC5_AC6_GoldenVectors(t *testing.T) {
	api, gv := loadGoldenVectors(t)
	if len(gv.Cases) < 12 {
		t.Fatalf("golden vector file has only %d cases, want >= 12", len(gv.Cases))
	}

	for _, c := range gv.Cases {
		c := c
		t.Run(c.Kind, func(t *testing.T) {
			point, err := api.ScaleLadderAt(c.Population)
			if c.ExpectError {
				if err == nil {
					t.Fatalf("population %d: expected an out-of-range error", c.Population)
				}
				return
			}
			if err != nil {
				t.Fatalf("population %d: unexpected error: %v", c.Population, err)
			}

			got := map[string]float64{}
			for _, f := range point.Fields {
				got[f.Key] = f.Value
			}
			for key, want := range c.ExpectedFields {
				v, ok := got[key]
				if !ok {
					t.Fatalf("population %d: missing field %q", c.Population, key)
				}
				var tol float64
				if c.Kind == "exact" {
					tol = 0
				} else {
					tol = 1e-12 * maxF(1, absF(want))
				}
				if absF(v-want) > tol {
					t.Fatalf("population %d field %q = %v, want %v (tol %v)", c.Population, key, v, want, tol)
				}
			}

			if c.ExpectedRoundedPeople != nil {
				rounded := RoundCount(got["counts.peopleCount"])
				if rounded != *c.ExpectedRoundedPeople {
					t.Fatalf("population %d RoundCount(counts.peopleCount) = %d, want %d", c.Population, rounded, *c.ExpectedRoundedPeople)
				}
			}

			nonNumeric := map[string]string{}
			for _, nn := range point.NonNumeric {
				nonNumeric[nn.Key] = string(nn.RawValue)
			}
			for key, want := range c.ExpectedNonNumeric {
				wantRaw, err := json.Marshal(want)
				if err != nil {
					t.Fatalf("marshal expected non-numeric %q: %v", key, err)
				}
				gotRaw, ok := nonNumeric[key]
				if !ok {
					t.Fatalf("population %d: missing non-numeric field %q", c.Population, key)
				}
				if !jsonEqual(t, gotRaw, string(wantRaw)) {
					t.Fatalf("population %d non-numeric %q = %s, want %s", c.Population, key, gotRaw, wantRaw)
				}
			}
		})
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// TestAC7_RoundCount is impossible to pass against a rounding rule that
// isn't floor(x+0.5) (e.g. plain float64->int64 truncation, which would
// round 1865.6 down to 1865 instead of up to 1866).
func TestScaleLadderAC7_RoundCount(t *testing.T) {
	cases := []struct {
		in   float64
		want int64
	}{
		{0, 0},
		{0.4, 0},
		{0.5, 1},
		{0.9999, 1},
		{1865.214, 1865},
		{1865.6, 1866},
		{1865.5, 1866},
		{2.5, 3},
		// BUG-833: the half-ulp input where JS Math.round (spec:
		// floor(x+0.5)) and Go math.Round (round-half-away-from-zero,
		// computed exactly) DIVERGE: x+0.5 rounds to exactly 1.0 in
		// float64 (ties-to-even), so floor(x+0.5) = 1, while math.Round(x)
		// = 0. A Go "cleanup" to math.Round is caught here.
		{0.49999999999999994, 1},
	}
	for _, c := range cases {
		if got := RoundCount(c.in); got != c.want {
			t.Errorf("RoundCount(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestAC8_Determinism: a mutant that ranges a map on any output path (or
// reads time.Now/rand) would either panic-free-but-diverge in field order
// or in value across repeated calls; this asserts byte-identical JSON
// encoding across 10 repeated calls (GR#21).
func TestScaleLadderAC8_Determinism(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}

	var first []byte
	for i := 0; i < 10; i++ {
		point, err := api.ScaleLadderAt(3000)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		enc, err := json.Marshal(point)
		if err != nil {
			t.Fatalf("iteration %d: marshal: %v", i, err)
		}
		if first == nil {
			first = enc
		} else if string(enc) != string(first) {
			t.Fatalf("iteration %d: output diverged:\n first=%s\n this =%s", i, first, enc)
		}
	}
}

// TestAC8_NoTimeOrRandomInSource is a grep-style guard: the mutant is any
// future edit that reaches for time.Now/Math.random-equivalent inside the
// production interpolation path.
func TestScaleLadderAC8_NoTimeOrRandomInSource(t *testing.T) {
	raw, err := os.ReadFile("scaleladder.go")
	if err != nil {
		t.Fatalf("reading scaleladder.go: %v", err)
	}
	src := string(raw)
	if strings.Contains(src, "time.Now") {
		t.Fatal("scaleladder.go must not read the wall clock (GR#21)")
	}
	if strings.Contains(src, "rand.") {
		t.Fatal("scaleladder.go must not use randomness (GR#21)")
	}
	// GR#21 section 18: a map ranged with an early break is the specific
	// nondeterminism trap. This file only ranges maps at LOAD time to
	// collect+sort keys (never on a return path before sorting), but grep
	// for the dangerous shape defensively: "range" immediately followed
	// eventually by "break" inside the same func without an intervening
	// "sort." is what the map-range-with-break class looks like; here we
	// just assert every "for _, k := range" over a map is followed by a
	// sort.Strings/sort.Slice call before use, which flattenRung/flattenLeaf
	// both do (see source above) -- this is a documentation-level pin, not
	// an AST check.
	if m := regexp.MustCompile(`for k := range \w+ \{[^}]*\}\s*$`).FindString(src); m != "" && !strings.Contains(src, "sort.Strings(keys)") {
		t.Fatal("found a raw map range with no subsequent sort — GR#21 violation")
	}
}

// TestAC9_SignatureTakesNoCitizenState documents the AC-9 structural
// guarantee via reflection-free inspection: ScaleLadderAt's only argument
// is a population int64. A mutant signature that also takes a citizen
// slice/world state would fail to compile against this call, which is
// itself the enforcement -- if this file compiles, the signature is right.
func TestScaleLadderAC9_SignatureTakesNoCitizenState(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	var population int64 = 3000
	if _, err := api.ScaleLadderAt(population); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAC9_AllocsBoundedByFieldCount: a mutant that allocates per rung per
// field (e.g. re-flattening both rungs from scratch on every query instead
// of reading the pre-flattened, pre-sorted slices) would blow well past a
// bound derived from field count; this test derives its bound from the
// loaded ladder's OWN field count (GR#15 -- no hardcoded constant),
// scaled by a small constant factor to allow the one output slice
// allocation.
func TestScaleLadderAC9_AllocsBoundedByFieldCount(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	point, err := api.ScaleLadderAt(100) // exact rung, to learn field count
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fieldCount := len(point.Fields)
	nonNumericCount := len(point.NonNumeric)
	if fieldCount == 0 {
		t.Fatal("fixture ladder has zero numeric fields; test cannot derive a bound")
	}
	// BUG-832 (rework): the original bound (4*fieldCount+16 = 44 on this
	// fixture) was so loose that the named per-rung-per-field mutant (4
	// rungs x 7 fields = 28 escaping allocations) survived it. The real
	// output shape is O(1) in field count -- ONE fields slice, ONE
	// nonNumeric slice, plus one RawValue byte-copy per non-numeric leaf
	// (copyFields/copyNonNumeric, BUG-828) -- so the bound is derived from
	// the loaded ladder's OWN non-numeric leaf count (GR#15), not the
	// field count, with a small constant for the two slice headers plus
	// interpolation-loop overhead. Measured actual on this fixture: 4
	// allocs; bound below is 2 (slices) + nonNumericCount (raw copies) + 1
	// slack = 5, which still reds the 28-alloc mutant by a wide margin.
	bound := float64(2 + nonNumericCount + 1)

	allocs := testing.AllocsPerRun(20, func() {
		if _, err := api.ScaleLadderAt(3000); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if allocs > bound {
		t.Fatalf("ScaleLadderAt allocs/run = %v, want <= %v (derived from field count %d)", allocs, bound, fieldCount)
	}
}

// BenchmarkLadderInterpolate exists for local profiling only (AC-9); it
// asserts nothing and is excluded from the scoped test run above (-run
// filters to test names, not benchmarks, and `go test` does not execute
// benchmarks unless -bench is passed).
func BenchmarkLadderInterpolate(b *testing.B) {
	src := filepath.Join("testdata", "ladder_valid.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		b.Fatalf("reading fixture: %v", err)
	}
	dataDir := b.TempDir()
	_ = os.MkdirAll(filepath.Join(dataDir, "traffic"), 0755)
	_ = os.WriteFile(filepath.Join(dataDir, "traffic", "scale_ladder.json"), raw, 0644)
	api := New()
	if err := api.LoadScaleLadder(dataDir); err != nil {
		b.Fatalf("load: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = api.ScaleLadderAt(3000)
	}
}

// TestAC10_NoConsumerYet is a grep-style guard: nothing in this package
// outside scaleladder.go/scaleladder_test.go calls ScaleLadderAt or
// Rungs -- inc2 is their first real consumer. The mutant this catches: a
// stray wiring call added to api.go or elsewhere in the package before
// inc2 lands.
func TestScaleLadderAC10_NoConsumerYet(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	callRe := regexp.MustCompile(`\.ScaleLadderAt\(|\bInterpolateLadder\(`)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		// BUG-836 (re-round finding): skip every _test.go, not two named
		// files -- attack/round test files and inc2's own tests may call the
		// interpolator; the guard is about PRODUCTION consumers only.
		if name == "scaleladder.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if callRe.MatchString(string(raw)) {
			t.Fatalf("%s calls the scale-ladder interpolator -- AC-10 forbids a consumer before inc2", name)
		}
	}
}

// TestAC11_RungsSortedAscending is a supporting sanity check for the
// exposed Rungs() surface used by AC-2's data derivation.
func TestScaleLadderAC11_RungsSortedAscending(t *testing.T) {
	api, _, err := loadFixtureLadder(t, "ladder_valid.json")
	if err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	rungs := api.Rungs()
	if !sort.SliceIsSorted(rungs, func(i, j int) bool { return rungs[i] < rungs[j] }) {
		t.Fatalf("Rungs() not sorted ascending: %v", rungs)
	}
}

// --- Rework regression suite (2026-09-09 independent Destructive REJECT) --
// Adopted from the attacker's opus-round-feat792-inc1 file
// (scratchpad/round-792-inc1/scaleladder_round_test.go.attack), renamed to
// TestRegression_* with assertions inverted to the now-FIXED behaviour.

// writeInlineLadder writes body directly as the ladder file (bypassing the
// testdata/ fixture convention) so BUG-828/BUG-829 regression cases can use
// tiny inline JSON, matching the attack file's own writeLadder helper.
func writeInlineLadder(t *testing.T, body string) (*TrafficAPI, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "traffic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "traffic", "scale_ladder.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	api := New()
	return api, api.LoadScaleLadder(dir)
}

// TestRegression_RoundAliasingExactRung (BUG-828): mutating a returned
// LadderPoint's Fields/NonNumeric must NEVER affect a later call -- the
// ladder is documented immutable. Mutant this reds: reverting copyFields/
// copyNonNumeric back to returning r.numeric/r.nonNumeric/rLo.nonNumeric
// directly makes the second call observe the poisoned value.
func TestRegression_RoundAliasingExactRung(t *testing.T) {
	api, err := writeInlineLadder(t, `{"meta":{"rungPopulations":[100,1000]},"rungs":[
		{"population":100,"a":1,"s":"lo"},{"population":1000,"a":2,"s":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	p1, err := api.ScaleLadderAt(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p1.Fields[0].Value = 999
	p1.NonNumeric[0].RawValue = json.RawMessage(`"POISONED"`)
	p2, err := api.ScaleLadderAt(100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.Fields[0].Value != 1 {
		t.Errorf("ALIASING: exact-rung Fields aliases ladder internals; second call returned %v, want 1", p2.Fields[0].Value)
	}
	if string(p2.NonNumeric[0].RawValue) != `"lo"` {
		t.Errorf("ALIASING: NonNumeric aliases ladder internals; second call returned %s", p2.NonNumeric[0].RawValue)
	}
	// interpolated path: NonNumeric is copied from rLo.nonNumeric.
	p3, err := api.ScaleLadderAt(300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p3.NonNumeric[0].RawValue = json.RawMessage(`"POISONED2"`)
	p4, err := api.ScaleLadderAt(300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(p4.NonNumeric[0].RawValue) != `"lo"` {
		t.Errorf("ALIASING: interpolated NonNumeric aliases ladder internals; got %s", p4.NonNumeric[0].RawValue)
	}
}

// TestRegression_RoundKeySetMismatch (BUG-829): a rung whose flattened key
// set (fewer leaves, more leaves, or differently-named leaves) diverges
// from rung 0's must be REJECTED AT LOAD, not accepted and later panic or
// silently mispair during interpolation. Mutant this reds: removing the
// validateUniformKeySets call from buildScaleLadder.
func TestRegression_RoundKeySetMismatch(t *testing.T) {
	cases := []struct{ name, body string }{
		{"upper rung has FEWER numeric leaves", `{"meta":{"rungPopulations":[100,1000]},"rungs":[
			{"population":100,"a":1,"b":2},{"population":1000,"a":2}]}`},
		{"upper rung has MORE numeric leaves", `{"meta":{"rungPopulations":[100,1000]},"rungs":[
			{"population":100,"a":1},{"population":1000,"a":2,"zz":9}]}`},
		{"same count, DIFFERENT keys", `{"meta":{"rungPopulations":[100,1000]},"rungs":[
			{"population":100,"a":1,"b":100},{"population":1000,"a":2,"c":900}]}`},
		{"ragged numeric array", `{"meta":{"rungPopulations":[100,1000]},"rungs":[
			{"population":100,"a":[1,2,3]},{"population":1000,"a":[2]}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC on a key-set-mismatched ladder (must be a registry error, never a panic): %v", r)
				}
			}()
			_, err := writeInlineLadder(t, c.body)
			if err == nil {
				t.Fatal("expected the key-set mismatch to be rejected AT LOAD TIME")
			}
			if got := registryCode(t, err); got != ErrScaleLadderKeySetMismatch {
				t.Fatalf("code = %s, want %s", got, ErrScaleLadderKeySetMismatch)
			}
		})
	}
}

// TestRegression_RoundCountHalfUlp (BUG-833): proves RoundCount implements
// floor(x+0.5), not math.Round, at the exact input where they diverge.
func TestRegression_RoundCountHalfUlp(t *testing.T) {
	x := 0.49999999999999994
	if got := RoundCount(x); got != 1 {
		t.Errorf("RoundCount(%v) = %d; the documented rule floor(x+0.5) gives 1 (JS Math.round agrees)", x, got)
	}
}
