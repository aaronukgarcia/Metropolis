package compose

import (
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// MOD-034 (engine.wellbeing) composition-root wiring tests.
//
// IMPORTANT — read compose_wellbeing.go's package doc comment first: the
// four downstream-effect modifiers (MortalityModifier/ProductivityModifier/
// SatisfactionModifier/EmigrationModifier) are COMPUTED every month
// (WellbeingStatus) AND APPLIED to a real consumer at exactly one site each:
//
//   - MortalityModifier -> engine.citizens.CitizensAPI.SetMortalityModifier,
//     folded into ColdPassParams.MortalityMultiplier (coldParamsLocked).
//   - SatisfactionModifier/EmigrationModifier ->
//     engine.attract.AttractAPI.SetWellbeingModifiers, scaling the
//     attractiveness score A (ApplyMigration) and the per-resident
//     emigration hazard (applyEmigration) respectively.
//   - ProductivityModifier -> engine.firms.FirmsAPI.SetProductivityModifier,
//     folded into each firm's Financial.OutputScale
//     (applyInputScalingLocked). Filed as its own P2: OutputScale's only
//     current consumer (ResolveMonth's credit-failure check) does not
//     reach compose's money/population surfaces, so this application is
//     real and tested at the firms-package level but not yet observable
//     from a composed city's own money/population numbers the way the
//     other three modifiers are — a follow-on ticket threads OutputScale
//     into a real production/wage-bill consumer.
//
// So "the four modifiers are observably applied" below means what it says
// for mortality/satisfaction/emigration: a low-wellbeing city shows a
// measurably different mortality/emigration RATE and migration
// attractiveness in the live loop. For productivity, it means the
// arithmetic wiring into OutputScale is correct and tested, pending the
// P2 follow-on to give it a compose-visible consumer.

const wellbeingWireSeed = uint64(34034)

// TestWellbeingWire_ComposedWithLiveAndDegradedSeams proves Wire() actually
// constructs and wires MOD-034's WellbeingAPI (Composition.Wellbeing() is
// non-nil) and reports the documented seam split: engine.season/
// engine.traffic/engine.world.pollution live, engine.shopping/
// engine.services.healthcare/engine.world.neighbourhood degraded.
func TestWellbeingWire_ComposedWithLiveAndDegradedSeams(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp.Wellbeing() == nil {
		t.Fatal("Composition.Wellbeing() = nil, want a wired WellbeingAPI (MOD-034 built-but-not-wired regression)")
	}

	wantLive := map[string]bool{
		"engine.season":              true,
		"engine.traffic":             true,
		"engine.world.pollution":     true,
		"engine.shopping":            false,
		"engine.services.healthcare": false,
		"engine.world.neighbourhood": false,
	}
	status := comp.WellbeingStatus()
	if len(status.Seams) != len(wantLive) {
		t.Fatalf("WellbeingStatus().Seams has %d entries, want %d", len(status.Seams), len(wantLive))
	}
	seen := map[string]bool{}
	for _, s := range status.Seams {
		seen[s.Name] = true
		wantLiveState, ok := wantLive[s.Name]
		if !ok {
			t.Fatalf("unexpected seam %q reported", s.Name)
		}
		gotLive := s.State == "live"
		if gotLive != wantLiveState {
			t.Fatalf("seam %q state=%q, want live=%v", s.Name, s.State, wantLiveState)
		}
	}
	for name := range wantLive {
		if !seen[name] {
			t.Fatalf("expected seam %q missing from WellbeingStatus().Seams", name)
		}
	}
}

// TestWellbeingWire_ModifiersDirectional proves the wired WellbeingAPI's
// four downstream modifiers move in the §18-documented direction as the
// two tracks worsen, reached through the SAME instance
// (Composition.Wellbeing()) the monthly reconstruction hook drives — see
// this file's package doc comment for why this is the honest scope of
// "observably applied" while the modifiers have no real consumer yet.
func TestWellbeingWire_ModifiersDirectional(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	w := comp.Wellbeing()

	const (
		goodPhysical, goodMental = 100.0, 100.0
		badPhysical, badMental   = 0.0, 0.0
	)

	goodMortality := w.MortalityModifier(goodPhysical, goodMental)
	badMortality := w.MortalityModifier(badPhysical, badMental)
	if !(badMortality > goodMortality) {
		t.Fatalf("MortalityModifier not worse for a low-wellbeing cohort: good=%v bad=%v, want bad > good", goodMortality, badMortality)
	}

	goodEmigration := w.EmigrationModifier(goodPhysical, goodMental)
	badEmigration := w.EmigrationModifier(badPhysical, badMental)
	if !(badEmigration > goodEmigration) {
		t.Fatalf("EmigrationModifier not worse for a low-wellbeing cohort: good=%v bad=%v, want bad > good", goodEmigration, badEmigration)
	}

	goodProductivity := w.ProductivityModifier(goodPhysical, goodMental)
	badProductivity := w.ProductivityModifier(badPhysical, badMental)
	if !(badProductivity < goodProductivity) {
		t.Fatalf("ProductivityModifier not worse for a low-wellbeing cohort: good=%v bad=%v, want bad < good", goodProductivity, badProductivity)
	}

	goodSatisfaction := w.SatisfactionModifier(goodPhysical, goodMental)
	badSatisfaction := w.SatisfactionModifier(badPhysical, badMental)
	if !(badSatisfaction < goodSatisfaction) {
		t.Fatalf("SatisfactionModifier not worse for a low-wellbeing cohort: good=%v bad=%v, want bad < good", goodSatisfaction, badSatisfaction)
	}
}

// TestWellbeingWire_EverySeamDegradedStillTicks is the AC-14 liveness proof:
// even with every WellbeingAPI input seam left unwired but season (season is
// a hard AC-10 requirement — SetSeason(nil) is rejected by the package
// itself, so "every seam degraded" here means every OPTIONAL seam), the
// composed engine still ticks, the monthly reconstruction still runs
// without error, and WellbeingStatus() reports the degraded state rather
// than crashing or silently reporting a fabricated "live".
func TestWellbeingWire_EverySeamDegradedStillTicks(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))

	if got := comp.Population(); got <= 0 {
		t.Fatalf("Population() = %d after one driven month, want > 0 (engine must still tick with degraded wellbeing seams)", got)
	}
	status := comp.WellbeingStatus()
	if status.SampleSize <= 0 {
		t.Fatalf("WellbeingStatus().SampleSize = %d after one driven month, want > 0 (monthly reconstruction must have run)", status.SampleSize)
	}
	for _, s := range status.Seams {
		switch s.Name {
		case "engine.shopping", "engine.services.healthcare", "engine.world.neighbourhood":
			if s.State != "degraded" {
				t.Fatalf("seam %q reported %q, want degraded (AC-14 honesty)", s.Name, s.State)
			}
		}
	}
}

// TestWellbeingWire_DeterministicAcrossPoolSizes mirrors
// TestBUG689_DeterministicAcrossPoolSizes exactly: the reconstructed cohort
// mean tracks and the four modifier values must be byte-identical across
// pool sizes 1, 4, and 20 (GR#21) — the reconstruction reads only the
// citizen population (already proven deterministic across pool sizes by
// citizens' own suite) and the fixed residentIDs() order, never anything
// pool-size-dependent.
func TestWellbeingWire_DeterministicAcrossPoolSizes(t *testing.T) {
	const months = 2

	run := func(poolSize int) WellbeingStatus {
		e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(poolSize))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire (pool %d): %v", poolSize, err)
		}
		for i := 0; i < months; i++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		}
		return comp.WellbeingStatus()
	}

	s1 := run(1)
	s4 := run(4)
	s20 := run(20)

	if s1.SampleSize == 0 {
		t.Fatal("SampleSize = 0 at pool size 1 — this determinism proof needs a real reconstructed cohort")
	}
	if s1.SampleSize != s4.SampleSize || s1.SampleSize != s20.SampleSize {
		t.Fatalf("SampleSize differs across pool sizes: 1=%d 4=%d 20=%d", s1.SampleSize, s4.SampleSize, s20.SampleSize)
	}
	if s1.MeanPhysical != s4.MeanPhysical || s1.MeanPhysical != s20.MeanPhysical {
		t.Fatalf("MeanPhysical differs across pool sizes: 1=%v 4=%v 20=%v", s1.MeanPhysical, s4.MeanPhysical, s20.MeanPhysical)
	}
	if s1.MeanMental != s4.MeanMental || s1.MeanMental != s20.MeanMental {
		t.Fatalf("MeanMental differs across pool sizes: 1=%v 4=%v 20=%v", s1.MeanMental, s4.MeanMental, s20.MeanMental)
	}
	if s1.MortalityModifier != s4.MortalityModifier || s1.MortalityModifier != s20.MortalityModifier {
		t.Fatalf("MortalityModifier differs across pool sizes: 1=%v 4=%v 20=%v", s1.MortalityModifier, s4.MortalityModifier, s20.MortalityModifier)
	}
	if s1.ProductivityModifier != s4.ProductivityModifier || s1.ProductivityModifier != s20.ProductivityModifier {
		t.Fatalf("ProductivityModifier differs across pool sizes: 1=%v 4=%v 20=%v", s1.ProductivityModifier, s4.ProductivityModifier, s20.ProductivityModifier)
	}
	if s1.SatisfactionModifier != s4.SatisfactionModifier || s1.SatisfactionModifier != s20.SatisfactionModifier {
		t.Fatalf("SatisfactionModifier differs across pool sizes: 1=%v 4=%v 20=%v", s1.SatisfactionModifier, s4.SatisfactionModifier, s20.SatisfactionModifier)
	}
	if s1.EmigrationModifier != s4.EmigrationModifier || s1.EmigrationModifier != s20.EmigrationModifier {
		t.Fatalf("EmigrationModifier differs across pool sizes: 1=%v 4=%v 20=%v", s1.EmigrationModifier, s4.EmigrationModifier, s20.EmigrationModifier)
	}
}

// TestWellbeingWire_SaveRestoreContinuation proves save/restore never
// carries wellbeing state across the boundary (nothing is a save
// participant — see compose_wellbeing.go's reconstructWellbeing doc
// comment: everything is derived) yet the monthly reconstruction resumes
// cleanly afterward.
//
// This test does NOT assert the restored run's reconstructed mean tracks
// equal a directly-continued (never-saved) run's: a debug probe run during
// this lane's own verification found Composition.Population() AND
// Composition.PopulationHash() already agree exactly immediately after
// Load() (proving Save/Load itself round-trips the citizen population
// byte-for-byte), but PopulationHash DIVERGES between the two paths after
// one further ticked month even though the raw population COUNT still
// matches — a pre-existing compose-level save/restore divergence in
// something that influences a subsequent tick's outcome (candidates:
// engine.attract's reputation-momentum state, a migrant/fertility-child id
// counter, or similar tick-affecting state not itself population), NOT
// anything wellbeing's read-only reconstruction touches or introduces.
// Flagged for the architect as an out-of-scope P2 finding rather than
// silently loosened away; this test instead proves the honest, in-scope
// claim: reload does not crash or starve the reconstruction.
func TestWellbeingWire_SaveRestoreContinuation(t *testing.T) {
	const monthsBeforeSave = 2
	const monthsAfterSave = 1

	// --- A: drive monthsBeforeSave, save, then keep ticking directly (no reload).
	eA := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	compA, err := Wire(eA, nil)
	if err != nil {
		t.Fatalf("Wire A: %v", err)
	}
	for i := 0; i < monthsBeforeSave; i++ {
		advanceInChunks(t, eA, int64(core.DailyTicksPerMonth))
	}
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	popPreSave := compA.Population()
	hashPreSave := compA.PopulationHash()
	for i := 0; i < monthsAfterSave; i++ {
		advanceInChunks(t, eA, int64(core.DailyTicksPerMonth))
	}
	direct := compA.WellbeingStatus()
	if direct.SampleSize <= 0 {
		t.Fatal("WellbeingStatus().SampleSize = 0 on the direct (never-saved) run — fixture must produce a real cohort")
	}

	// --- B: fresh composition, load A's save, drive the same remaining months.
	eB := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, nil)
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Confirm Load() itself round-trips the citizen population exactly
	// (isolates this test's remaining claim to "the monthly reconstruction
	// resumes cleanly", not "Load() is lossy" — it is not, at this point).
	if got := compB.Population(); got != popPreSave {
		t.Fatalf("Population() immediately after Load() = %d, want %d (pre-save)", got, popPreSave)
	}
	if got := compB.PopulationHash(); got != hashPreSave {
		t.Fatalf("PopulationHash() immediately after Load() = %x, want %x (pre-save)", got, hashPreSave)
	}
	for i := 0; i < monthsAfterSave; i++ {
		advanceInChunks(t, eB, int64(core.DailyTicksPerMonth))
	}
	restored := compB.WellbeingStatus()

	if restored.SampleSize <= 0 {
		t.Fatalf("WellbeingStatus().SampleSize = %d after restore + one more month, want > 0", restored.SampleSize)
	}
}

// TestWellbeingWire_ConservationUntouched proves wiring MOD-034's monthly
// reconstruction alongside the existing money/people invariants raises zero
// violations — the reconstruction reads citizen records and calls no
// mutating method on any other module (it applies no modifier to any
// consumer, per this file's package doc comment), so it cannot possibly
// perturb conservation.
func TestWellbeingWire_ConservationUntouched(t *testing.T) {
	var violations atomic.Int64
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		InvariantOpts: []invariant.HookOption{
			invariant.WithLogSink(func(*errs.E) { violations.Add(1) }),
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	const months = 3
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	if comp.WellbeingStatus().SampleSize <= 0 {
		t.Fatal("WellbeingStatus().SampleSize = 0 after 3 driven months — reconstruction must have run to make this conservation proof meaningful")
	}
	if got := violations.Load(); got != 0 {
		t.Fatalf("conservation suite reported %d violations with MOD-034 wired, want 0", got)
	}
}

// --- Round-2 findings (P1-a sample coverage, P1-b empty-sample severity,
// P2 Seams aliasing) ---

// TestWellbeingWire_SampleGrowsWithPopulationUnderCap is the P1-a
// regression proof: the round found reconstructWellbeing sampling
// residentIDs() (the CLOSED seed range) pinned SampleSize at the seed
// population forever (measured live: pop 46 -> 595 over 36 months,
// SampleSize stuck at 46) because migrants/fertility children are minted
// outside that range. With the liveResidentIDs() fix, SampleSize must track
// population growth up to wellbeingSampleCap, then plateau AT the cap once
// population exceeds it — never regress back toward the seed-only count.
func TestWellbeingWire_SampleGrowsWithPopulationUnderCap(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	const seedPopulation = seedCitizenCount
	var lastSample int
	grewPastSeed := false
	const months = 36
	for i := 0; i < months; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		status := comp.WellbeingStatus()
		pop := comp.Population()

		// SampleSize must never exceed the live population, nor the cap.
		if status.SampleSize > pop {
			t.Fatalf("month %d: SampleSize=%d > Population()=%d", i+1, status.SampleSize, pop)
		}
		if status.SampleSize > wellbeingSampleCap {
			t.Fatalf("month %d: SampleSize=%d exceeds wellbeingSampleCap=%d", i+1, status.SampleSize, wellbeingSampleCap)
		}
		// Below the cap, SampleSize should track population closely (a small
		// gap is expected: liveResidentIDs() enumerates every id EVER born/
		// admitted, including ones that have since died within the same
		// month CitizenAt is queried, which the !ok skip in
		// reconstructWellbeing correctly excludes from the sample — this is
		// not the P1-a bug, which pinned SampleSize at the CLOSED seed count
		// regardless of how large the population grew). Widened 20 -> 30
		// (MOD-034 downstream-effect application lane): once
		// SetMortalityModifier/SetWellbeingModifiers/SetProductivityModifier
		// are actually wired to real consumers, the mortality/emigration
		// modifiers have a real (mild) accelerating effect on the oldest
		// sampled ids, so a slightly larger share of the FIRST
		// wellbeingSampleCap ids (ascending order, i.e. the oldest ever
		// born/admitted) have died by month 36 than under the
		// observability-only baseline this tolerance was originally set
		// against — this is the expected downstream consequence of MOD-034
		// finally being load-bearing, not a sampling regression.
		if pop <= wellbeingSampleCap && status.SampleSize < pop-30 {
			t.Fatalf("month %d: SampleSize=%d, want close to Population()=%d (pop below cap)", i+1, status.SampleSize, pop)
		}
		if status.SampleSize > seedPopulation {
			grewPastSeed = true
		}
		lastSample = status.SampleSize
	}

	if !grewPastSeed {
		t.Fatalf("SampleSize never exceeded the seed population (%d) over %d months (last=%d) — the P1-a fix did not take effect (still sampling the closed seed range)", seedPopulation, months, lastSample)
	}
	// Once population exceeds the cap, SampleSize must never exceed it
	// (checked every month in the loop above) and should sit reasonably
	// close to it — some of the first wellbeingSampleCap ids (the OLDEST
	// ever-born/admitted ids) may have since died, which the CitizenAt !ok
	// skip correctly excludes, so exact equality is not required.
	if pop := comp.Population(); pop > wellbeingSampleCap && lastSample < wellbeingSampleCap*9/10 {
		t.Fatalf("final population=%d exceeds the cap but SampleSize=%d is far below wellbeingSampleCap=%d", pop, lastSample, wellbeingSampleCap)
	}
}

// TestWellbeingWire_EmptySampleYieldsNeutral is the P1-b regression proof:
// computeWellbeingStatus at count == 0 must report the documented NEUTRAL
// modifiers (1.0 each) with NoData=true — never derive
// MortalityModifier/EmigrationModifier == 2.0 and
// ProductivityModifier/SatisfactionModifier == 0.0 from a fabricated (0, 0)
// mean, which would be byte-identical to a genuinely catastrophic cohort
// (GR#17). Exercises computeWellbeingStatus directly (it is a pure function
// of its arguments, independent of simState) so the empty-sample path is
// tested without needing to engineer a full zero-population composed city.
func TestWellbeingWire_EmptySampleYieldsNeutral(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	status := computeWellbeingStatus(comp.Wellbeing(), nil, 0, 0, 0)

	if !status.NoData {
		t.Fatal("NoData = false at count 0, want true")
	}
	if status.SampleSize != 0 {
		t.Fatalf("SampleSize = %d at count 0, want 0", status.SampleSize)
	}
	if status.MeanPhysical != 0 || status.MeanMental != 0 {
		t.Fatalf("mean tracks at count 0 = (%v, %v), want (0, 0)", status.MeanPhysical, status.MeanMental)
	}
	for name, got := range map[string]float64{
		"MortalityModifier":    status.MortalityModifier,
		"ProductivityModifier": status.ProductivityModifier,
		"SatisfactionModifier": status.SatisfactionModifier,
		"EmigrationModifier":   status.EmigrationModifier,
	} {
		if got != wellbeingNeutralModifier {
			t.Fatalf("%s at count 0 = %v, want the neutral value %v (NOT the catastrophic-cohort value a fabricated (0,0) mean would produce)", name, got, wellbeingNeutralModifier)
		}
	}

	// Sanity: a REAL catastrophic (0, 0) cohort (count > 0, tracks genuinely
	// bottomed out) is NOT neutral — proves this test would have caught the
	// bug (count==0 silently computing the same numbers a real (0,0) cohort
	// would) rather than passing vacuously.
	catastrophic := computeWellbeingStatus(comp.Wellbeing(), nil, 0, 0, 1)
	if catastrophic.MortalityModifier == wellbeingNeutralModifier {
		t.Fatal("a real 1-citizen (0,0) mean produced the neutral MortalityModifier — count==0 vs count==1-at-zero-mean must differ")
	}
}

// TestWellbeingWire_SeamsCloneIsolatesMutation is the P2 regression proof:
// WellbeingStatus() must return a defensively-cloned Seams slice — mutating
// one call's result must never perturb a later call's result or the
// composition's own internal report.
func TestWellbeingWire_SeamsCloneIsolatesMutation(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(wellbeingWireSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	first := comp.WellbeingStatus()
	if len(first.Seams) == 0 {
		t.Fatal("WellbeingStatus().Seams is empty — nothing to prove isolation over")
	}
	originalName := first.Seams[0].Name
	originalState := first.Seams[0].State

	// Mutate the returned copy's backing array.
	first.Seams[0].Name = "MUTATED"
	first.Seams[0].State = "MUTATED"

	second := comp.WellbeingStatus()
	if second.Seams[0].Name != originalName || second.Seams[0].State != originalState {
		t.Fatalf("mutating one WellbeingStatus() result perturbed a later call: got (%q, %q), want (%q, %q) — Seams is aliasing the composition's backing array",
			second.Seams[0].Name, second.Seams[0].State, originalName, originalState)
	}

	// Driving a tick (which recomputes wellbeingStatus) must also be
	// unaffected by the earlier mutation.
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	third := comp.WellbeingStatus()
	if third.Seams[0].Name != originalName || third.Seams[0].State != originalState {
		t.Fatalf("mutation survived into the next reconstructed status: got (%q, %q), want (%q, %q)",
			third.Seams[0].Name, third.Seams[0].State, originalName, originalState)
	}
}
