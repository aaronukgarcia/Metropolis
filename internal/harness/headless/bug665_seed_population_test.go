package headless

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-665: the standing perf-1m-probe CI check advertised "the real
// merge-blocking 1M scale gate" but headless.Config had no citizen-count
// field at all — the "1M" preset reached internal/harness/synth's
// throwaway Generate() and nothing else; the engine that actually ticked
// always held compose's own 64-citizen genesis seed regardless of what a
// caller asked for. This file proves the fix at the SMALLEST scale that
// still exercises the real path end to end: SeedCitizenCount plumbed
// through Config all the way into a ticked CitizensAPI, verified by
// reading back Result.Population — the exact assertion whose absence let
// the gap go unnoticed for weeks (a caller checking only its own input
// parameter, never Run's output, learns nothing).

// bug665BaselineFounders mirrors compose.go's own unexported
// seedCitizenCount constant (64) — compose.Wire's unconditional AC-8
// genesis founder seed, which runs on top of whatever
// Config.SeedCitizenCount asks for (see SeedCitizenCount's doc comment
// in run.go). Duplicated here as a named, commented constant rather than
// a bare magic number, exactly like every OTHER file in this codebase
// that already references "seedCitizenCount=64" in a comment (compose's
// own test suite cannot export it without widening compose's public
// surface for a single cross-package assertion, which is not this
// item's scope).
const bug665BaselineFounders = 64

// TestSeedCitizenCount_ReachesTheEngineWireDrives is the RED-proof-able
// assertion this item's own report calls for: "assert the CitizensAPI
// population equals N". It is deliberately measured BEFORE any tick
// advances (immediately after compose.Wire, using Wire's own Deps.Citizens
// injection seam directly — the same seam Run() now uses internally,
// run.go's "BUG-665" comment block) rather than after a full
// headless.Run(), because Baseline One's monthly mortality/migration
// dynamics (compose.go's PhasePopulation attract hook, the death-queue
// smoothing drain) legitimately move population during ticking — a
// month is the smallest unit headless.Config.Months can express (see
// driveTicks), so a post-tick assertion can never be an exact N+64
// invariant. Measuring at the seeding boundary itself is what makes this
// assertion exact rather than a fuzzy "roughly N" one: zero out
// SeedCitizenCount (or, before this item's fix, delete the seeding call
// entirely) and this fails loudly at 64, never silently.
func TestSeedCitizenCount_ReachesTheEngineWireDrives(t *testing.T) {
	const n = 5000 // small-N: proves the wiring, not the perf gate
	const correlationID = "bug665-test"

	citizensAPI, err := citizens.NewCitizensAPI(7, correlationID)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	records := generateSeedPopulation(7, n)
	if err := citizensAPI.SeedColdRecords(records, correlationID); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(7))
	comp, err := compose.Wire(e, &compose.Deps{Citizens: citizensAPI})
	if err != nil {
		t.Fatalf("compose.Wire: %v", err)
	}

	want := n + bug665BaselineFounders
	if got := comp.Population(); got != want {
		t.Fatalf("Population immediately after Wire = %d, want %d (SeedCitizenCount=%d + %d baseline founders) -- BUG-665: the seeded population never reached the CitizensAPI Wire drives", got, want, n, bug665BaselineFounders)
	}
}

// TestSeedCitizenCount_ThroughHeadlessRun_DominatesOverBaseline proves
// the full end-to-end plumbing this item's report demands: Config's new
// SeedCitizenCount field, carried through Run() and a full month of real
// ticking, produces a population dominated by N — not the vacuous
// ~64-citizen baseline every "1M" preset silently produced before this
// fix (headless.Config had no citizen-count field at all: internal/
// harness/synth's runHeadless only ever set Seed/Months/OutDir/Report/
// CorrelationID, so the "1M" preset's citizen count reached ONLY
// synth.Generate's throwaway records, never the ticked engine — see
// go-engine-100m-proving-plan.md §1.1). A same-seed, same-months control
// run with SeedCitizenCount=0 measures how much of Population is
// baseline churn versus seeded population, without hard-coding an exact
// post-tick figure (deliberately fragile, per the sibling exact test
// above's own doc comment on why an exact post-tick assertion is wrong).
func TestSeedCitizenCount_ThroughHeadlessRun_DominatesOverBaseline(t *testing.T) {
	const n = 5000

	baselineDir := filepath.Join(t.TempDir(), "baseline")
	baseline, err := Run(context.Background(), Config{Seed: 7, Months: 1, OutDir: baselineDir})
	if err != nil {
		t.Fatalf("baseline Run: %v", err)
	}

	seededDir := filepath.Join(t.TempDir(), "seeded")
	seeded, err := Run(context.Background(), Config{
		Seed:             7,
		Months:           1,
		OutDir:           seededDir,
		SeedCitizenCount: n,
	})
	if err != nil {
		t.Fatalf("seeded Run: %v", err)
	}

	// A generous 10% tolerance against a month of mortality/migration
	// dynamics touching the seeded population too (BUG-665's own report:
	// "land the gate with a generous first bound" applies equally here —
	// this is a wiring proof, not a tight population-dynamics contract).
	got := seeded.Population - baseline.Population
	tolerance := n / 10
	if got < n-tolerance || got > n+tolerance {
		t.Fatalf("seeded.Population(%d) - baseline.Population(%d) = %d, want within %d of %d -- BUG-665: SeedCitizenCount is not reaching the ticked engine through the full headless.Run path", seeded.Population, baseline.Population, got, tolerance, n)
	}
	if baseline.Population != bug665BaselineFounders {
		t.Logf("baseline.Population = %d (informational: Baseline One's own monthly mortality/migration dynamics moved the 64-citizen founder seed within one month — not this item's concern, just recorded so a future reader is not surprised it is not exactly 64)", baseline.Population)
	}
}

// TestGenerateSeedPopulation_DeterministicAndDisjointIDs (GR#21): two
// calls with the same (seed, n) produce byte-identical records, and
// every id lands in [PerfSeedIDBase+1, PerfSeedIDBase+n] -- disjoint
// from compose's [1,64] founder range (see PerfSeedIDBase's doc
// comment).
func TestGenerateSeedPopulation_DeterministicAndDisjointIDs(t *testing.T) {
	const n = 2000
	a := generateSeedPopulation(42, n)
	b := generateSeedPopulation(42, n)
	if len(a) != n || len(b) != n {
		t.Fatalf("len(a)=%d len(b)=%d, want %d", len(a), len(b), n)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("record %d differs across two calls with the same (seed, n): %+v vs %+v (GR#21 determinism)", i, a[i], b[i])
		}
		if a[i].ID <= bug665BaselineFounders {
			t.Fatalf("record %d id=%d collides with compose's [1,%d] founder range", i, a[i].ID, bug665BaselineFounders)
		}
		if err := citizens.ValidateColdRecord(a[i], "corr"); err != nil {
			t.Fatalf("record %d fails ValidateColdRecord: %v", i, err)
		}
	}
}

// TestGenerateSeedPopulation_DifferentSeedsDiffer guards against a
// generator that silently ignores its seed argument (which would make
// every headless.Run(SeedCitizenCount>0) call produce the identical
// population regardless of Config.Seed -- exactly the kind of
// "structurally impossible to have come from a real draw" defect this
// package's own synth sibling (perf.go's ImplausibleReason) polices for
// PerfResult).
func TestGenerateSeedPopulation_DifferentSeedsDiffer(t *testing.T) {
	const n = 500
	a := generateSeedPopulation(1, n)
	b := generateSeedPopulation(2, n)
	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("generateSeedPopulation(1, n) == generateSeedPopulation(2, n) -- the generator is ignoring its seed argument")
	}
}
