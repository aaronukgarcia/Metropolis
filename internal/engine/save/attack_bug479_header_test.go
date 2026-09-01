package save

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-479 independent destructive round (Opus r1, 2026-09-01).
//
// The seed check added in load.go reads header.WorldSeed, which is decoded
// straight out of an UNTRUSTED header.json (a bundle can arrive from a
// shared save file or a bug report — the same threat model SEC-001 states
// for ShardMeta.Name in serialize/savebundle.go). These tests attack that
// field with every malformed shape the JSON decoder can be handed and
// assert the SAME two properties for each: the failure is a registry-
// sourced *errs.E (GR#7), and it is never a panic and never a silent
// accept into a differently-seeded composition.
//
// They are permanent regressions, not scratch: the seed comparison is a
// fail-closed security-shaped check, and a future refactor that made a
// hostile header decode to a convenient zero (or crash the loader) would
// reintroduce exactly the silent-accept BUG-479 closed.

var worldSeedFieldRE = regexp.MustCompile(`"worldSeed"\s*:\s*-?[0-9]+`)

// rewriteWorldSeedLiteral replaces the header.json "worldSeed" member with
// the raw literal text given (which need not be a valid JSON number — that
// is the point), returning nothing. It operates on the raw bytes rather
// than decode/re-encode so shapes Go's own encoder could never produce
// (a string, a duplicate key, an out-of-range integer) can be injected.
func rewriteWorldSeedLiteral(t *testing.T, dir, literal string) {
	t.Helper()
	path := filepath.Join(dir, "header.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading header.json: %v", err)
	}
	if !worldSeedFieldRE.Match(raw) {
		t.Fatalf("fixture header.json has no numeric worldSeed member to rewrite: %s", string(raw))
	}
	out := worldSeedFieldRE.ReplaceAll(raw, []byte(`"worldSeed": `+literal))
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("writing header.json: %v", err)
	}
}

// loadNoPanic runs Load and converts any panic into a test failure naming
// the input that caused it — a panic on hostile bundle data is a defect,
// not an acceptable rejection.
func loadNoPanic(t *testing.T, mgr *Manager, dir, what string) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Load PANICKED on %s: %v", what, r)
		}
	}()
	_, _, err = mgr.Load(dir, WithExpectedWorldSeed(42))
	return err
}

// wantRegistryError asserts err is a registry-sourced *errs.E carrying a
// MET- code (GR#7) rather than a bare fmt error escaping the package.
func wantRegistryError(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: Load SUCCEEDED, want a refusal", what)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("%s: Load error %v (%T) is not a registry *errs.E (GR#7)", what, err, err)
	}
	if !strings.HasPrefix(e.Code, "MET-") {
		t.Fatalf("%s: Load error code %q is not a MET- registry code", what, e.Code)
	}
}

// TestAttackBUG479_HostileWorldSeedShapes feeds Load a bundle whose
// header.json worldSeed member has been replaced with each malformed
// shape in turn. Every one must be refused with a registry error and no
// panic — never accepted into a composition expecting seed 42.
func TestAttackBUG479_HostileWorldSeedShapes(t *testing.T) {
	cases := []struct {
		name    string
		literal string
	}{
		{"string", `"42"`},
		{"nonIntegralFloat", `42.5`},
		{"exponentFloat", `4.2e1`},
		{"aboveInt64Max", `9223372036854775808`},
		{"belowInt64Min", `-9223372036854775809`},
		{"null", `null`},
		{"boolean", `true`},
		{"object", `{"seed":42}`},
		{"array", `[42]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildAndSaveWidgetBundle(t, 7) // deliberately NOT 42
			rewriteWorldSeedLiteral(t, dir, tc.literal)

			loadWidgets := newWidgetParticipant()
			mgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "attack-bug479-"+tc.name)
			err := loadNoPanic(t, mgr, dir, "worldSeed="+tc.literal)
			wantRegistryError(t, err, "worldSeed="+tc.literal)

			if got := loadWidgets.State(); len(got) != 0 {
				t.Fatalf("participant state was applied despite a refused hostile header: %+v", got)
			}
		})
	}
}

// TestAttackBUG479_DuplicateWorldSeedKey injects a header.json carrying
// the worldSeed member TWICE, the classic JSON-parser-disagreement
// smuggle: a validator reading the first occurrence and a loader reading
// the second would disagree about which seed the bundle claims. Go's
// encoding/json takes the LAST occurrence, so a bundle whose first
// worldSeed is the expected 42 and whose second is 43 must still be
// REFUSED — fail closed, never "the first one looked fine".
func TestAttackBUG479_DuplicateWorldSeedKey(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)
	rewriteWorldSeedLiteral(t, dir, `42, "worldSeed": 43`)

	loadWidgets := newWidgetParticipant()
	mgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "attack-bug479-duplicate-key-test")
	err := loadNoPanic(t, mgr, dir, "duplicate worldSeed 42 then 43")
	if err == nil {
		t.Fatal("a header.json with worldSeed 42 AND worldSeed 43 loaded into a seed-42 composition; the duplicate-key smuggle must fail closed")
	}
	if !errors.Is(err, &errs.E{Code: ErrSaveSeedMismatch}) {
		wantRegistryError(t, err, "duplicate worldSeed key")
	}
	if got := loadWidgets.State(); len(got) != 0 {
		t.Fatalf("participant state was applied despite a refused duplicate-key header: %+v", got)
	}
}

// TestAttackBUG479_NegativeSeedIsCompared covers the sign-handling half
// of the comparison: a negative seed is a perfectly legal int64 world
// seed (compose casts a uint64 with the top bit set straight to int64 —
// save_wire.go), so the check must compare it exactly, neither treating
// it as an absolute value nor as "unset".
func TestAttackBUG479_NegativeSeedIsCompared(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, -42)

	// Same magnitude, wrong sign: must be refused (an abs()-style or
	// unsigned comparison would wrongly accept this).
	refuseWidgets := newWidgetParticipant()
	refuseMgr := NewManager(t.TempDir(), []Participant{refuseWidgets}, "attack-bug479-negrefuse")
	if _, _, err := refuseMgr.Load(dir, WithExpectedWorldSeed(42)); err == nil {
		t.Fatal("a seed -42 bundle loaded into a seed +42 composition; the comparison must be sign-exact")
	} else if !errors.Is(err, &errs.E{Code: ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want %s", err, ErrSaveSeedMismatch)
	}

	// Exact negative match: must succeed (the check must not treat a
	// negative seed as missing/invalid and refuse unconditionally).
	acceptWidgets := newWidgetParticipant()
	acceptMgr := NewManager(t.TempDir(), []Participant{acceptWidgets}, "attack-bug479-negaccept")
	if _, _, err := acceptMgr.Load(dir, WithExpectedWorldSeed(-42)); err != nil {
		t.Fatalf("a seed -42 bundle refused by a seed -42 composition: %v", err)
	}
	if got := acceptWidgets.State(); len(got) != 1 {
		t.Fatalf("exact negative-seed match did not load the shard: %+v", got)
	}
}

// TestAttackBUG479_HeaderAbsentEntirely removes header.json outright: the
// loader must refuse with a registry error before any participant sees a
// record, not panic on a nil/zero header and not treat the resulting zero
// WorldSeed as an accepted match for a zero-seeded composition.
func TestAttackBUG479_HeaderAbsentEntirely(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)
	if err := os.Remove(filepath.Join(dir, "header.json")); err != nil {
		t.Fatalf("removing header.json: %v", err)
	}

	loadWidgets := newWidgetParticipant()
	mgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "attack-bug479-noheader")
	err := loadNoPanic(t, mgr, dir, "header.json absent")
	wantRegistryError(t, err, "header.json absent")

	// And the zero-seed composition case: an absent header must not
	// become "seed 0 matches my seed 0, load away".
	zeroWidgets := newWidgetParticipant()
	zeroMgr := NewManager(t.TempDir(), []Participant{zeroWidgets}, "attack-bug479-noheader-zero")
	if _, _, err := zeroMgr.Load(dir, WithExpectedWorldSeed(0)); err == nil {
		t.Fatal("a bundle with NO header.json loaded successfully into a seed-0 composition")
	}
	if got := zeroWidgets.State(); len(got) != 0 {
		t.Fatalf("participant state was applied from a headerless bundle: %+v", got)
	}
}
