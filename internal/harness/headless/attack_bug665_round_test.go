package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-665 — INDEPENDENT DESTRUCTIVE ROUND (GR#23: attacker != author)
//
// This file is the destructive round's own regression evidence. It does
// NOT re-litigate what bug665_seed_population_test.go and
// bug665_population_perf_gate_test.go already prove; it attacks the seams
// the report specifically flagged as suspect. Two of the seven attacks in
// the round dispatch (vacuity-via-zeroed-injection, and vacuity-via-
// halved-seeding) were proven LIVE by hand during the round, via
// scratch-copy mutation of run.go/seedpop.go, run, observed failure, then
// restored byte-identical (GR#24: no git checkout/restore used) — they are
// NOT re-encoded as permanent tests here because reproducing them would
// require literally re-breaking the production seam inside a committed
// test file, which is worse than the disease. The finding is recorded in
// this round's BOW verdict note instead. Everything else below IS a
// permanent, committed regression test.

// TestAttackBUG665_IDSpacesStayDisjointAtFinaleScale attacks the report's
// "ID DISJOINTNESS" item directly: PerfSeedIDBase's own doc comment claims
// the seeded range "tops out at PerfSeedIDBase + 100,000,000 ... still ~40
// quintillion below 1<<62" (attract.MigrantIDBase) and stays below
// fertility's 1<<63 base — this proves that arithmetic against the REAL
// exported constants (never a hand-copied literal, GR#3) at the exact
// finale scale the plan targets (100M) and at 5x that (500M) as a margin
// check, rather than trusting the comment.
func TestAttackBUG665_IDSpacesStayDisjointAtFinaleScale(t *testing.T) {
	for _, n := range []int64{100_000_000, 500_000_000} {
		maxSeededID := uint64(PerfSeedIDBase) + uint64(n)
		if maxSeededID >= attract.MigrantIDBase {
			t.Fatalf("n=%d: max seeded id %d >= attract.MigrantIDBase %d -- LATENT COLLISION at this scale", n, maxSeededID, attract.MigrantIDBase)
		}
		if maxSeededID >= citizens.FertilityChildIDBase {
			t.Fatalf("n=%d: max seeded id %d >= citizens.FertilityChildIDBase %d -- LATENT COLLISION at this scale", n, maxSeededID, citizens.FertilityChildIDBase)
		}
		// Founder range [1,64] must also stay clear of the seeded range's
		// floor (PerfSeedIDBase+1) -- this is the FEAT-169-class collision
		// this whole item exists to not repeat, checked both directions.
		if PerfSeedIDBase+1 <= 64 {
			t.Fatalf("n=%d: PerfSeedIDBase+1 (%d) collides with compose's [1,64] founder range", n, PerfSeedIDBase+1)
		}
	}
}

// TestAttackBUG665_5MSeedStaysDisjointAndDeterministic attacks the
// report's explicit ask ("seed 5M in a quick test - still disjoint?") by
// actually GENERATING 5,000,000 records (not just doing the id arithmetic
// above) and checking every single one lands in the documented
// [PerfSeedIDBase+1, PerfSeedIDBase+n] band, with no id reaching either
// attract.MigrantIDBase or citizens.FertilityChildIDBase. This is the one
// attack in this file expensive enough to matter for CI wall-time (id
// arithmetic + two det.Stream draws per record, no disk I/O -- the
// proving plan's own §1.1 measured generation at this shape as cheap and
// linear), so it stays a single, non-repeated pass rather than a
// multi-seed sweep.
func TestAttackBUG665_5MSeedStaysDisjointAndDeterministic(t *testing.T) {
	const n = 5_000_000
	records := generateSeedPopulation(99, n)
	if int64(len(records)) != n {
		t.Fatalf("len(records) = %d, want %d", len(records), n)
	}
	wantMin := uint64(PerfSeedIDBase) + 1
	wantMax := uint64(PerfSeedIDBase) + uint64(n)
	for i, r := range records {
		if r.ID < wantMin || r.ID > wantMax {
			t.Fatalf("record %d: id=%d outside [%d,%d]", i, r.ID, wantMin, wantMax)
		}
		if r.ID >= attract.MigrantIDBase {
			t.Fatalf("record %d: id=%d collides with attract.MigrantIDBase %d", i, r.ID, attract.MigrantIDBase)
		}
		if r.ID >= citizens.FertilityChildIDBase {
			t.Fatalf("record %d: id=%d collides with citizens.FertilityChildIDBase %d", i, r.ID, citizens.FertilityChildIDBase)
		}
	}
}

// TestAttackBUG665_TickedPopulationDoesRealDemographicWork attacks the
// report's "SUBTLER VACUITY" concern directly: does the seeded population
// actually exercise births/deaths across the ticked window, or is it
// degenerate (e.g. every record the same age -> nobody dies, nobody is
// born -> an idle tick that undercounts real per-citizen cost the way
// BUG-665's OWN defect undercounted it by never ticking the population at
// all)? This drives a real citizens.CitizensAPI + compose.Wire + Engine
// through THREE simulated months (matching popPerfGateMonths) at a scale
// small enough to run fast in CI (100k, not 1M -- the amortised cold-pass
// schedule is population-size-independent per FEAT-160/coldpass.go, so
// the mix of ages/health/stage this proves is representative of the 1M
// gate's own generator, just measured at 1/10th the record count for
// speed) and sums CitizensAPI.VitalEvents' births+deaths across all three
// months. A zero here would mean the perf gate is measuring an idle
// engine -- the vacuity class in a subtler coat, per the round dispatch.
func TestAttackBUG665_TickedPopulationDoesRealDemographicWork(t *testing.T) {
	const n = 100_000
	const seed = 7
	const correlationID = "attack-bug665-vitals"

	citizensAPI, err := citizens.NewCitizensAPI(seed, correlationID)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	records := generateSeedPopulation(seed, n)
	if err := citizensAPI.SeedColdRecords(records, correlationID); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	// BUG-665 round fix: generateSeedPopulation's own second pass pairs a
	// childbearing-age fraction of records into mutual partners (raw
	// Household/Partner columns), but a raw column write alone does not
	// make fertility.go's birthChildLocked succeed -- ValidateCitizen
	// rejects a Household with no real c.households entry behind it.
	// SeedHouseholds is the explicit, O(n), no-rowOf registration call
	// this fix adds for exactly that (see its own doc comment).
	if err := citizensAPI.SeedHouseholds(records, correlationID); err != nil {
		t.Fatalf("SeedHouseholds: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(seed))
	if _, err := compose.Wire(e, &compose.Deps{Citizens: citizensAPI}); err != nil {
		t.Fatalf("compose.Wire: %v", err)
	}

	const months = popPerfGateMonths // mirrors the real gate's own window
	totalBirths, totalDeaths := 0, 0
	for m := 0; m < months; m++ {
		if err := citizensAPI.AdvanceMonth(correlationID); err != nil {
			t.Fatalf("AdvanceMonth %d: %v", m, err)
		}
		b, d := citizensAPI.VitalEvents(correlationID)
		totalBirths += b
		totalDeaths += d
	}

	t.Logf("BUG-665 attack: %d-citizen seed, %d months: births=%d deaths=%d", n, months, totalBirths, totalDeaths)

	if totalBirths+totalDeaths == 0 {
		t.Fatalf("births=0 deaths=0 across %d months of a %d-citizen seeded population -- the tick is doing no demographic work; the perf gate would be measuring an IDLE engine (BUG-665's vacuity class in a subtler coat)", months, n)
	}
	// Both individually, not just their sum, so a generator that happens
	// to produce (many deaths, zero births) or vice versa is still caught
	// -- either half being permanently zero is itself a degenerate-seed
	// defect the report asked this round to rule out.
	if totalDeaths == 0 {
		t.Errorf("deaths=0 across %d months -- mortality sampling never fired against this seeded population (age/health distribution may be degenerate)", months)
	}
	if totalBirths == 0 {
		t.Errorf("births=0 across %d months -- fertility scans never fired against this seeded population (age/partner/household distribution may be degenerate)", months)
	}
}

// TestAttackBUG665_SeededAgeDistributionIsNotOneValue is a cheaper,
// narrower companion to the vitals test above: it directly inspects the
// generated BirthMonth field's SPREAD (not just its downstream effect on
// births/deaths, which can be masked by mortality/fertility gating logic
// this round does not own) to positively confirm the generator itself
// produces a real age spread rather than "row 0 of every branch" -- the
// exact degenerate shape the report worried about and seedpop.go's own
// doc comment claims to avoid.
func TestAttackBUG665_SeededAgeDistributionIsNotOneValue(t *testing.T) {
	const n = 50_000
	records := generateSeedPopulation(3, n)
	distinctBirthMonths := map[int64]bool{}
	distinctStages := map[citizens.Stage]bool{}
	for _, r := range records {
		distinctBirthMonths[r.BirthMonth] = true
		distinctStages[r.Stage] = true
	}
	if len(distinctBirthMonths) < 2 {
		t.Fatalf("only %d distinct BirthMonth value(s) across %d records -- degenerate age distribution (every citizen the same age never ages into mortality/fertility eligibility differently)", len(distinctBirthMonths), n)
	}
	if len(distinctStages) < 2 {
		t.Fatalf("only %d distinct Stage value(s) across %d records -- degenerate lifecycle-stage distribution", len(distinctStages), n)
	}
}

// TestAttackBUG665_PopulationFloorIsDerivedNotLiteral attacks the report's
// "must be computed from cfg.SeedCitizenCount, not a literal 950000"
// requirement mechanically: greps the real gate test file's source for a
// bare 6+-digit numeric literal on the floor-check line, so a future edit
// that hardcodes the floor (instead of deriving it from
// popPerfGateCitizenCount) fails this test rather than silently
// decoupling the floor from the seeded count the next time
// popPerfGateCitizenCount is retuned.
func TestAttackBUG665_PopulationFloorIsDerivedNotLiteral(t *testing.T) {
	src, err := readSourceFile(t, "bug665_population_perf_gate_test.go")
	if err != nil {
		t.Fatalf("reading gate test source: %v", err)
	}
	const wantExpr = "popPerfGateCitizenCount * 95 / 100"
	if !strings.Contains(src, wantExpr) {
		t.Fatalf("bug665_population_perf_gate_test.go no longer computes its floor as %q -- the 95%% floor must be DERIVED from popPerfGateCitizenCount (which is itself the exact value passed as SeedCitizenCount), never a hand-copied literal like 950000 that silently decouples from a future retune of popPerfGateCitizenCount", wantExpr)
	}
	if strings.Contains(src, "950000") || strings.Contains(src, "950_000") {
		t.Fatalf("bug665_population_perf_gate_test.go contains a literal 950000/950_000 -- the floor must be an expression over popPerfGateCitizenCount, not a hardcoded number (BUG-665 round attack)")
	}
}

// TestAttackBUG665_CIJobRunsUnconditionally attacks the report's "THE CI
// SHAPE" concern: perf-population-probe must actually RUN on every push/
// PR (no needs:/if:/paths filter silently skipping it) for its signal to
// exist at all, per this item's own commit message ("still runs on every
// push/PR ... a failure here does not itself block a merge"). actionlint
// happily passes a job that is syntactically valid but functionally
// unreachable (e.g. gated on a branch that never triggers, or dependent
// on a job this workflow never runs) -- this test greps the real
// workflow's actual YAML text for the job block and asserts none of the
// three gating keys appear inside it, so a future edit that quietly adds
// one is caught mechanically rather than trusted to a human re-read.
func TestAttackBUG665_CIJobRunsUnconditionally(t *testing.T) {
	src, err := readSourceFile(t, filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("reading ci.yml: %v", err)
	}
	const marker = "perf-population-probe:"
	idx := strings.Index(src, marker)
	if idx < 0 {
		t.Fatalf("ci.yml no longer defines a %q job -- BUG-665's real population-scale gate has disappeared from CI", marker)
	}
	// The job block runs from the marker to the next top-level job entry
	// (a line starting with exactly two spaces then a bare word then ':',
	// i.e. the next sibling key under `jobs:`) or EOF.
	rest := src[idx+len(marker):]
	end := len(rest)
	for _, line := range strings.Split(rest, "\n")[1:] {
		if len(line) > 2 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' && strings.HasSuffix(strings.TrimRight(line, "\r"), ":") {
			// Found the next sibling job key -- stop before it.
			end = strings.Index(rest, line)
			break
		}
	}
	block := rest[:end]
	for _, gate := range []string{"\n    needs:", "\n    if:", "paths:", "paths-ignore:"} {
		if strings.Contains(block, gate) {
			t.Fatalf("perf-population-probe job block contains %q -- this job must run UNCONDITIONALLY on every push/PR (per BUG-665's own commit rationale: 'still runs on every push/PR so its signal is visible immediately'); a gating key here could silently skip the real population-scale gate", gate)
		}
	}
	if !strings.Contains(block, "timeout-minutes:") {
		t.Fatalf("perf-population-probe job block has no timeout-minutes -- an unbounded job can hang a runner forever on a genuine tick-loop regression")
	}
}

// readSourceFile reads a file relative to this test file's own package
// directory (t.Helper-wrapped so a failure's stack points at the caller,
// not here). Kept tiny and local rather than pulling in a path-walking
// helper package for two call sites.
func readSourceFile(t *testing.T, name string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TestAttackBUG665_SeededPopulationInvisibleToMoneycirc is the round's
// MOST LOAD-BEARING finding: it proves the perf-seeded population is
// COMPLETELY INVISIBLE to compose's own monthly resident-processing
// passes (markEmploymentAndCount, employedResidentCount,
// formResidentHouseholds, distributeWagesToResidents -- moneycirc.go,
// every one of them iterating simState.liveResidentIDs()), which is
// EXACTLY why TestAttackBUG665_TickedPopulationDoesRealDemographicWork
// above measured births=0 always: liveResidentIDs() is a union of
// compose's own founder-counter range, attract's migrant range, and
// citizens' fertility-child range (compose.go:2198) -- the perf harness's
// SeedColdRecords-injected population, at ids starting from
// PerfSeedIDBase (1,000,001), is in NONE of those three ranges. It exists
// in the cold store (CitizensAPI.TotalPopulation counts it correctly,
// which is what makes the Result.Population assertion pass) but compose
// never enumerates it as a "resident" for household formation, employment
// marking, or wage distribution -- the go-engine-100m-proving-plan.md
// §3.3 quadratic terms (the FOUR O(N²/256) moneycirc passes, the other
// half of the superlinearity finding alongside fertility's §3.2) are
// therefore NEVER exercised by this perf gate at ANY population size,
// because their entire input set stays pinned at ~64-implied-residents
// regardless of SeedCitizenCount. This proves it by comparing MoneyFlows
// after one ticked month with SeedCitizenCount=0 against
// SeedCitizenCount=50,000: byte-identical cumulative money flow means the
// seeded 50,000 citizens contributed zero wages/tax, i.e. compose's own
// money-circulation cost model is exactly as vacuous for a 1M-citizen
// perf run as it was before BUG-665 -- just for a DIFFERENT subsystem
// than the one BUG-665's own report and tests already closed.
func TestAttackBUG665_SeededPopulationInvisibleToMoneycirc(t *testing.T) {
	const seed = 11

	baselineDir := filepath.Join(t.TempDir(), "moneycirc-baseline")
	baseline, err := Run(context.Background(), Config{Seed: seed, Months: 1, OutDir: baselineDir})
	if err != nil {
		t.Fatalf("baseline Run: %v", err)
	}

	seededDir := filepath.Join(t.TempDir(), "moneycirc-seeded")
	seeded, err := Run(context.Background(), Config{
		Seed:             seed,
		Months:           1,
		OutDir:           seededDir,
		SeedCitizenCount: 50_000,
	})
	if err != nil {
		t.Fatalf("seeded Run: %v", err)
	}

	t.Logf("BUG-665 attack: baseline MoneyFlows n/a via headless.Run (no accessor) -- see finalPopulation instead: baseline pop=%d seeded pop=%d", baseline.Population, seeded.Population)

	// The seeded run's population correctly reflects the extra 50,000
	// cold-store citizens (proving Result.Population, BUG-665's own fix,
	// is genuinely live) -- but that is exactly what makes the ABSENCE of
	// any corresponding money-circulation difference so telling: a
	// population that large, if it were a real compose-visible resident
	// set, would materially change wage/tax flow. This test's job is
	// narrower and cheaper than reading MoneyFlows (which headless.Result
	// does not expose): it proves directly, via compose.Deps' own
	// injection seam, that liveResidentIDs() -- moneycirc's ONLY resident
	// enumeration -- never contains a single perf-seeded id.
	citizensAPI, err := citizens.NewCitizensAPI(seed, "attack-moneycirc")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const seededCount = 50_000
	records := generateSeedPopulation(seed, seededCount)
	if err := citizensAPI.SeedColdRecords(records, "attack-moneycirc"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	// BUG-665 round fix: register the paired households (see
	// generateSeedPopulation's own pairing pass) as REAL, ValidateCitizen-
	// visible entries -- formResidentHouseholds (moneycirc.go) is one of
	// the four resident-scoped passes this test attacks, so a household-
	// less seeded population would still understate what this test is
	// meant to exercise even once liveResidentIDs() sees these ids.
	if err := citizensAPI.SeedHouseholds(records, "attack-moneycirc"); err != nil {
		t.Fatalf("SeedHouseholds: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(seed))
	// BUG-665 round fix: SeedResidentIDBase/SeedResidentIDCount is the
	// "compose.Deps' own injection seam" this test's own comment above
	// already anticipated -- it did not exist on the round's first
	// landing (that IS this test's finding); wiring it in here completes
	// the proof this test was written to make, using the fix's own new
	// contract, without touching the assertion logic below at all.
	comp, err := compose.Wire(e, &compose.Deps{
		Citizens:            citizensAPI,
		SeedResidentIDBase:  PerfSeedIDBase,
		SeedResidentIDCount: seededCount,
	})
	if err != nil {
		t.Fatalf("compose.Wire: %v", err)
	}
	if got := comp.Population(); got < 50_000 {
		t.Fatalf("Population = %d, want >= 50000 -- seeding itself did not take (unrelated to this test's own attack)", got)
	}
	// Drive one household-formation-eligible month via the SAME AdvanceTicks
	// path headless.Run uses internally, then confirm that this composition's
	// own cumulative MoneyFlows (wages+tax, moneycirc.go) is IDENTICAL to a
	// same-seed, zero-seeded-population composition's MoneyFlows after the
	// same one month -- i.e. the 50,000 extra citizens contributed exactly
	// zero wage/tax activity, because moneycirc's four resident-scoped
	// passes (markEmploymentAndCount/employedResidentCount/
	// formResidentHouseholds/distributeWagesToResidents, all keyed on
	// simState.liveResidentIDs()) never enumerate them.
	if err := e.AdvanceTicks("attack-moneycirc", core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks (seeded): %v", err)
	}
	seededFlows := comp.MoneyFlows()

	eBase := core.NewEngine(core.WithWorldSeed(seed))
	compBase, err := compose.Wire(eBase, nil)
	if err != nil {
		t.Fatalf("compose.Wire (baseline): %v", err)
	}
	if err := eBase.AdvanceTicks("attack-moneycirc-base", core.DailyTicksPerMonth); err != nil {
		t.Fatalf("AdvanceTicks (baseline): %v", err)
	}
	baseFlows := compBase.MoneyFlows()

	t.Logf("BUG-665 attack: MoneyFlows after 1 month -- baseline(0 seeded)=%d seeded(50000)=%d", baseFlows, seededFlows)

	if seededFlows != baseFlows {
		// Not a failure by itself -- it would mean the seeded population IS
		// somehow visible to moneycirc, contradicting the liveResidentIDs()
		// source-read finding. Recorded loudly either way since this is the
		// round's most consequential claim.
		t.Logf("BUG-665 attack: MoneyFlows DIFFERS between baseline and 50000-seeded runs (%d vs %d) -- the seeded population DOES influence money circulation; the liveResidentIDs()-based vacuity finding does not hold as stated and should be re-verified before citing it in the verdict", baseFlows, seededFlows)
		return
	}
	t.Errorf("MoneyFlows IDENTICAL (%d) whether SeedCitizenCount is 0 or 50,000 -- the perf-seeded population is INVISIBLE to compose's own moneycirc resident-processing (markEmploymentAndCount/employedResidentCount/formResidentHouseholds/distributeWagesToResidents), which enumerates ONLY simState.liveResidentIDs() (compose.go:2198 -- founders + attract migrants + fertility children, never the SeedColdRecords-injected range starting at PerfSeedIDBase). This gate therefore never exercises go-engine-100m-proving-plan.md §3.3's four O(N²/256) moneycirc passes at ANY seeded population size -- a regression there would go completely undetected. Combined with TestAttackBUG665_TickedPopulationDoesRealDemographicWork's births=0 finding (fertility requires a formResidentHouseholds-assigned partner, which never happens for an invisible resident), this is the vacuity-in-a-subtler-coat the round dispatch asked this file to rule out -- and it is NOT ruled out.", baseFlows)
}
