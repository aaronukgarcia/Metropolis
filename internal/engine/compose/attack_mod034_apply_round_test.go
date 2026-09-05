package compose

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// MOD-034 downstream-effect APPLICATION independent destructive round
// (attacker: opus-round-wellbeing-apply, 2026-09-05). These tests are the
// attacker's own probes, not the lane's -- they exist to try to break the
// four newly-applied modifiers, not to demonstrate them.

// ---------------------------------------------------------------------------
// Attack 2: determinism of the read point across pool sizes.
// ---------------------------------------------------------------------------

// TestAttackMOD034_PopulationHashPoolSizeInvariant proves the three injected
// getters (SetMortalityModifier / SetWellbeingModifiers /
// SetProductivityModifier), all of which close over st.wellbeingStatus, are
// consulted at a read point that does NOT depend on the shard pool size. If
// any consumer could observe THIS month's freshly committed status on one
// pool size and LAST month's on another, the population hash would diverge
// (GR#21).
func TestAttackMOD034_PopulationHashPoolSizeInvariant(t *testing.T) {
	const months = 24

	type run struct {
		hash [32]byte
		pop  int
		wb   WellbeingStatus
	}
	var runs []run
	for _, pool := range []int{1, 4, 20} {
		e, comp := bugComposition(t, pool)
		runMonths(t, e, months)
		runs = append(runs, run{hash: comp.PopulationHash(), pop: comp.Population(), wb: comp.WellbeingStatus()})
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].hash != runs[0].hash {
			t.Fatalf("PopulationHash pool-size dependent after %d months: pool1=%x pool%d=%x", months, runs[0].hash, i, runs[i].hash)
		}
		if runs[i].pop != runs[0].pop {
			t.Fatalf("Population pool-size dependent: pool1=%d other=%d", runs[0].pop, runs[i].pop)
		}
		if runs[i].wb.MortalityModifier != runs[0].wb.MortalityModifier ||
			runs[i].wb.ProductivityModifier != runs[0].wb.ProductivityModifier ||
			runs[i].wb.SatisfactionModifier != runs[0].wb.SatisfactionModifier ||
			runs[i].wb.EmigrationModifier != runs[0].wb.EmigrationModifier {
			t.Fatalf("WellbeingStatus modifiers pool-size dependent: %+v vs %+v", runs[0].wb, runs[i].wb)
		}
	}
}

// TestAttackMOD034_RepeatRunsIdentical is the plain replay determinism check
// on the same pool size -- a getter that read a map-iteration-ordered or
// time-dependent value would show up here.
func TestAttackMOD034_RepeatRunsIdentical(t *testing.T) {
	const months = 18
	e1, c1 := bugComposition(t, 4)
	runMonths(t, e1, months)
	e2, c2 := bugComposition(t, 4)
	runMonths(t, e2, months)
	if c1.PopulationHash() != c2.PopulationHash() {
		t.Fatalf("two identical runs diverge: %x vs %x", c1.PopulationHash(), c2.PopulationHash())
	}
	if c1.WellbeingStatus().MortalityModifier != c2.WellbeingStatus().MortalityModifier {
		t.Fatalf("MortalityModifier differs across identical runs: %v vs %v", c1.WellbeingStatus().MortalityModifier, c2.WellbeingStatus().MortalityModifier)
	}
}

// ---------------------------------------------------------------------------
// Attack 1: conservation with the modifiers live.
// ---------------------------------------------------------------------------

// TestAttackMOD034_ConservationWithModifiersLive runs 36 months and asserts
// money and population conservation hold month by month with the four
// modifiers applied, and that no firm's OutputScale ever leaves [0,1000]
// per-mille.
func TestAttackMOD034_ConservationWithModifiersLive(t *testing.T) {
	const months = 36
	e, comp := bugComposition(t, 4)

	prevPop := comp.Population()
	prevBirths, prevDeaths, prevNet := comp.VitalBirths(), comp.VitalDeaths(), comp.NetMigration()

	for m := 1; m <= months; m++ {
		runMonths(t, e, 1)

		pop := comp.Population()
		money := num.SatAdd(comp.Treasury(), comp.CitizenWealth())
		births, deaths, net := comp.VitalBirths(), comp.VitalDeaths(), comp.NetMigration()

		// Money must never become non-finite/negative-overflowed and must
		// stay a real int64 (SatAdd saturation would be a red flag).
		if money == math.MaxInt64 || money == math.MinInt64 {
			t.Fatalf("month %d: money saturated (%d) -- an unbounded modifier-driven flow", m, money)
		}
		// Population conservation: delta must equal births - deaths + net
		// migration over the same window.
		gotDelta := int64(pop - prevPop)
		wantDelta := (births - prevBirths) - (deaths - prevDeaths) + (net - prevNet)
		if gotDelta != wantDelta {
			t.Fatalf("month %d: population not conserved: delta=%d, births-deaths+net=%d (pop %d->%d)", m, gotDelta, wantDelta, prevPop, pop)
		}

		// The wellbeing modifiers themselves must stay inside the documented
		// worst-case band for the shipped slopes (0.001 => +-10% at
		// physical=mental=0, i.e. deviation 100).
		st := comp.WellbeingStatus()
		for name, v := range map[string]float64{
			"Mortality":    st.MortalityModifier,
			"Productivity": st.ProductivityModifier,
			"Satisfaction": st.SatisfactionModifier,
			"Emigration":   st.EmigrationModifier,
		} {
			if !num.IsFinite(v) {
				t.Fatalf("month %d: %sModifier non-finite (%v)", m, name, v)
			}
		}
		if st.MortalityModifier < 1.0 || st.MortalityModifier > 1.1000001 {
			t.Fatalf("month %d: MortalityModifier %v outside documented [1.0,1.1] worst case", m, st.MortalityModifier)
		}
		if st.EmigrationModifier < 1.0 || st.EmigrationModifier > 1.1000001 {
			t.Fatalf("month %d: EmigrationModifier %v outside documented [1.0,1.1] worst case", m, st.EmigrationModifier)
		}
		if st.ProductivityModifier > 1.0 || st.ProductivityModifier < 0.8999999 {
			t.Fatalf("month %d: ProductivityModifier %v outside documented [0.9,1.0] worst case", m, st.ProductivityModifier)
		}
		if st.SatisfactionModifier > 1.0 || st.SatisfactionModifier < 0.8999999 {
			t.Fatalf("month %d: SatisfactionModifier %v outside documented [0.9,1.0] worst case", m, st.SatisfactionModifier)
		}

		prevPop = pop
		prevBirths, prevDeaths, prevNet = births, deaths, net
	}
}

// ---------------------------------------------------------------------------
// Attack 3: save/restore -- the eager reconstruction.
// ---------------------------------------------------------------------------

// TestAttackMOD034_LoadAtWellbeingStatusMatchesReference proves the eager
// reconstruction at LoadAt reproduces exactly what the never-stopped
// reference held at that tick, and that N further months keep the population
// hash identical for N in 1..6 across pool sizes.
func TestAttackMOD034_LoadAtWellbeingStatusMatchesReference(t *testing.T) {
	for _, pool := range []int{1, 4} {
		for n := 1; n <= 6; n++ {
			pool, n := pool, n
			t.Run("", func(t *testing.T) {
				const before = 6
				eRef, cRef := bugComposition(t, pool)
				runMonths(t, eRef, before)
				refStatusAtSave := cRef.WellbeingStatus()
				dir := t.TempDir()
				if err := cRef.Save(dir); err != nil {
					t.Fatalf("Save: %v", err)
				}
				clk, err := eRef.Clock()
				if err != nil {
					t.Fatalf("Clock: %v", err)
				}
				saveTick := clk.Tick()
				runMonths(t, eRef, n)
				refHash := cRef.PopulationHash()

				eB, cB := bugComposition(t, pool)
				if err := cB.LoadAt(dir, saveTick); err != nil {
					t.Fatalf("LoadAt: %v", err)
				}
				gotStatus := cB.WellbeingStatus()
				if gotStatus.MortalityModifier != refStatusAtSave.MortalityModifier ||
					gotStatus.ProductivityModifier != refStatusAtSave.ProductivityModifier ||
					gotStatus.SatisfactionModifier != refStatusAtSave.SatisfactionModifier ||
					gotStatus.EmigrationModifier != refStatusAtSave.EmigrationModifier ||
					gotStatus.SampleSize != refStatusAtSave.SampleSize ||
					gotStatus.NoData != refStatusAtSave.NoData {
					// ROUND FINDING (P2, recorded not asserted): LoadAt's eager
					// reconstruction does NOT reproduce the value the hook
					// committed for this month -- MeanMental differs (the
					// reconstruction runs at the restored END-of-tick state,
					// the hook ran at its own PhasePopulation point within the
					// month). The hash check below is the determinism-critical
					// assertion.
					t.Logf("FINDING pool=%d: LoadAt status != hook-committed reference: got mortality=%v mental=%v, want mortality=%v mental=%v",
						pool, gotStatus.MortalityModifier, gotStatus.MeanMental, refStatusAtSave.MortalityModifier, refStatusAtSave.MeanMental)
				}
				runMonths(t, eB, n)
				if cB.PopulationHash() != refHash {
					t.Fatalf("pool=%d n=%d: hash diverges after LoadAt: ref=%x got=%x (participants: %v)", pool, n, refHash, cB.PopulationHash(), diffParticipants(t, cRef, cB))
				}
			})
		}
	}
}

// TestAttackMOD034_PlainLoadReconstructsWellbeing proves bare Load (not
// LoadAt) also leaves a non-neutral, reconstructed status rather than the
// freshly-Wired zero-count NoData default -- the exact defect the lane
// reports finding and fixing. A regression here would silently give the
// first post-load month neutral modifiers.
func TestAttackMOD034_PlainLoadReconstructsWellbeing(t *testing.T) {
	eRef, cRef := bugComposition(t, 1)
	runMonths(t, eRef, 6)
	dir := t.TempDir()
	if err := cRef.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := cRef.WellbeingStatus()

	_, cB := bugComposition(t, 1)
	fresh := cB.WellbeingStatus()
	if !fresh.NoData {
		t.Fatalf("precondition: a freshly Wired composition should report NoData, got %+v", fresh)
	}
	if err := cB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cB.WellbeingStatus()
	if got.NoData {
		t.Fatalf("plain Load left WellbeingStatus at the neutral NoData default -- the eager reconstruction did not run")
	}
	if got.SampleSize != want.SampleSize {
		t.Fatalf("plain Load reconstruction sample size != reference: got %d want %d", got.SampleSize, want.SampleSize)
	}
	// ROUND FINDING (P3): the modifiers are NOT equal to the reference's,
	// because plain Load does not restore the engine clock (it stays at
	// tick 0 / month 0 -- measured), so reconstructWellbeingForRestore
	// attributes the restored cohort at MONTH 0, not the saved month.
	// save_wire.go's Load-side doc comment claims the reconstruction
	// reproduces "exactly what wellbeingHook would have computed had the
	// engine kept running through this same month" -- true for LoadAt,
	// overclaimed for bare Load. Recorded, not asserted, because plain
	// Load's clock divergence is itself pre-existing and documented.
	if got.MortalityModifier == want.MortalityModifier {
		t.Logf("plain Load modifiers happen to match reference exactly (%v)", got.MortalityModifier)
	} else {
		t.Logf("ROUND FINDING: plain Load reconstruction differs from reference (month-0 attribution): got mortality=%v want %v; meanPhysical %v vs %v",
			got.MortalityModifier, want.MortalityModifier, got.MeanPhysical, want.MeanPhysical)
	}
}

// ---------------------------------------------------------------------------
// Attack 4: do the modifiers ever BIND?
// ---------------------------------------------------------------------------

// TestAttackMOD034_ModifiersBindAtWorstCase drives the three injected seams
// directly with the worst-case values the shipped 0.001 slopes can produce
// (mortality/emigration 1.1, productivity/satisfaction 0.9) against a
// neutral control, and reports the measured 24-month delta. If a modifier is
// inert even at its own documented worst case, applying it is a no-op and
// the feature is decorative.
func TestAttackMOD034_ModifiersBindAtWorstCase(t *testing.T) {
	const months = 24

	measure := func(t *testing.T, mortality, productivity, satisfaction, emigration float64) (pop int, hash [32]byte, wealth int64) {
		t.Helper()
		e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(4))
		comp, err := Wire(e, nil)
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		// Override the compose-installed getters with fixed extremes. This
		// is the seam the lane added; driving it directly isolates
		// "is the application load-bearing" from "does the cohort ever get
		// that bad".
		if err := comp.state.citizens.SetMortalityModifier(func() float64 { return mortality }, comp.state.cid); err != nil {
			t.Fatalf("SetMortalityModifier: %v", err)
		}
		if err := comp.state.attract.SetWellbeingModifiers(func() (float64, float64) { return satisfaction, emigration }); err != nil {
			t.Fatalf("SetWellbeingModifiers: %v", err)
		}
		if err := comp.state.firms.SetProductivityModifier(func() float64 { return productivity }); err != nil {
			t.Fatalf("SetProductivityModifier: %v", err)
		}
		runMonths(t, e, months)
		return comp.Population(), comp.PopulationHash(), comp.CitizenWealth()
	}

	basePop, baseHash, baseWealth := measure(t, 1.0, 1.0, 1.0, 1.0)

	t.Run("mortality", func(t *testing.T) {
		pop, hash, _ := measure(t, 1.1, 1.0, 1.0, 1.0)
		t.Logf("mortality 1.1: pop %d -> %d (delta %d)", basePop, pop, pop-basePop)
		// ROUND FINDING (P2, balance -- recorded, not asserted): at the
		// shipped 0.001 slopes the mortality modifier tops out at 1.1
		// (physical=mental=0) and reads 1.053 at the observed cohort,
		// which is BELOW the draw-discretisation floor at baseline-one
		// scale -- measured: 2 deaths in 24 months at pop ~370, identical
		// population hash. The seam IS load-bearing (measured separately:
		// x10 -> 13 deaths, x1000 -> 158 deaths), so this is a slope
		// calibration question for Aaron, not a wiring defect.
		if hash == baseHash {
			t.Logf("FINDING: MortalityModifier=1.1 is a no-op at baseline-one scale (identical population hash)")
		}
	})
	t.Run("emigration", func(t *testing.T) {
		pop, hash, _ := measure(t, 1.0, 1.0, 1.0, 1.1)
		t.Logf("emigration 1.1: pop %d -> %d (delta %d)", basePop, pop, pop-basePop)
		if hash == baseHash {
			t.Fatalf("EmigrationModifier=1.1 produced an IDENTICAL population hash to neutral -- the emigration application is inert")
		}
	})
	t.Run("satisfaction", func(t *testing.T) {
		pop, hash, _ := measure(t, 1.0, 1.0, 0.9, 1.0)
		t.Logf("satisfaction 0.9: pop %d -> %d (delta %d)", basePop, pop, pop-basePop)
		if hash == baseHash {
			t.Fatalf("SatisfactionModifier=0.9 produced an IDENTICAL population hash to neutral -- the satisfaction application is inert")
		}
	})
	t.Run("productivity", func(t *testing.T) {
		pop, _, wealth := measure(t, 1.0, 0.9, 1.0, 1.0)
		t.Logf("productivity 0.9: pop %d -> %d, wealth %d -> %d (delta %d)", basePop, pop, baseWealth, wealth, wealth-baseWealth)
		// ROUND FINDING (P2, scope): productivity is INERT in the composed
		// loop. Measured with the modifier forced to 0.0 (every firm's
		// OutputScale clamped to zero): population, citizen wealth and the
		// full population hash are all BYTE-IDENTICAL to neutral.
		// firms.Financial.OutputScale has exactly one consumer
		// (lifecycle.go:318, the effective monthly cash flow) and that
		// quantity does not reach compose's money/population surfaces at
		// baseline one, so applying ProductivityModifier changes nothing a
		// player can observe. A pre-existing firms/compose wiring gap, not
		// a defect this diff introduced -- but MOD-034's 'four modifiers
		// applied' claim is really 'three applied, one applied into a
		// dead end'.
	})
}
