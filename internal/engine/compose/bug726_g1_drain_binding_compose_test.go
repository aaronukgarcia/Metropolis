package compose

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug726_g1_drain_binding_compose_test.go — BUG-726: the BUG-689 G1
// "drain-binding" test (bug689_g1_drain_binding_test.go) proves the
// citizens.DeathQueue/deathservices contract in isolation, but it never
// goes through compose's own Wire() -- it builds a bare
// citizens.NewDeathQueue() and calls q.SetDrainCapacity(ds, cid) DIRECTLY.
// Mutating compose.go's real wiring call
// (deathServicesAPI.WireDrainCapacity(c, cid) -> ...WireDrainCapacity(nil,
// cid)) leaves that test green, because it never exercises compose.go at
// all -- only a source grep for "WireDrainCapacity" in compose.go would
// have caught the regression.
//
// This file closes that gap: it drives a REAL Composition built by the
// real Wire(), with a crematorium configured via Deps.DeathServiceCrematoria
// (the same Wire-time seam compose.go's own doc comment names), ticks real
// months with real citizens.CitizensAPI.AdvanceDayTick-driven deaths, and
// reads the realised release ONLY through public composition/citizens
// surfaces (citizens.CitizensAPI.DeathHandoffSince, deathservices.
// DeathServicesAPI.MonthlyDrainCapacity) -- never a source grep.
//
// Two data-file overrides make the module's live capacity the ACTUAL
// binding constraint on the citizens death queue (the BUG-689 backward-
// compatibility guarantee, TestBUG689_DrainCapacityNeverBindsForDefaultData,
// documents that for the SHIPPED data/mortality.json + data/deathservices.json
// figures the ordinary smoothing budget (25/month) is always the tighter
// term than the injected drain (>=300/month) -- so a compose-level test
// driven against the real, unmodified data files could never observe the
// drain actually binding, and could not tell a live wiring from a removed
// one). This file copies the real data/ directory to a temp dir and raises
// ONLY data/mortality.json's monthlyDeathBudget to a figure far above any
// crematorium-derived capacity this file uses, via METROPOLIS_DATA_DIR
// (t.Setenv, auto-restored) -- data/deathservices.json (hearse budget,
// cremation throughput) is copied UNCHANGED, so the module's own real
// crematorium/hearse arithmetic is exactly what MonthlyDrainCapacity would
// report against the shipped deathservices.json.

// bug726Seed is a fixed world seed dedicated to this file, distinct from
// every other compose test file's seed constant.
const bug726Seed = uint64(726)

// bug726AncientCitizenBase/bug726AncientCitizenCount seed a population far
// larger than any drain capacity this file configures, spread across many
// of citizens' 256 cold shards (sequential ids, det.ShardForEntity's own
// hash spreads them), so the amortised 1/30-shards-per-day cold pass
// (coldpass.go's ColdPassSchedule) selects effectively all of them for
// death within the FIRST month driven -- see bug726AncientBirthMonth's doc.
const (
	bug726AncientCitizenBase  = uint64(90_500_000)
	bug726AncientCitizenCount = 3000
	// bug726AncientBirthMonth is deep in the negative int16 range
	// ValidateColdRecord enforces (math.MinInt16..MaxInt16): age = month -
	// birthMonth is therefore ~2500 years at sim month 0/1, far past the
	// point citizens.MortalityHazard's Gompertz-Makeham curve saturates
	// and applyMonthly's own `if hazard > 1 { hazard = 1 }` clamp takes
	// over -- deterministic guaranteed selection, no probabilistic flake
	// risk (mirrors TestFEAT169_LiveDeaths_RealMortality's preAge trick,
	// but via a direct SeedColdRecords birth month rather than pre-
	// advancing the clock 2403 times).
	bug726AncientBirthMonth = int64(-30000)
	// bug726HugeMonthlyDeathBudget replaces data/mortality.json's shipped
	// 25/month figure for THIS test's copied data dir only, so the
	// ordinary smoothing budget never binds -- only the injected drain
	// capacity (crematoria + hearse) can be the tighter term.
	bug726HugeMonthlyDeathBudget = float64(1_000_000)
)

// bug726CopyDataDirWithHugeMortalityBudget copies the real data/ directory
// (found via the same data.ResolveDataDir every production loader uses) to
// a fresh t.TempDir(), then rewrites ONLY the copied mortality.json's
// monthlyDeathBudget figure -- every other file (including
// deathservices.json's hearse/cremation figures) is byte-identical to the
// shipped data. Returns the temp directory path; the caller still needs to
// t.Setenv("METROPOLIS_DATA_DIR", ...) it before calling Wire.
func bug726CopyDataDirWithHugeMortalityBudget(t *testing.T) string {
	t.Helper()
	cid := errs.NewCorrelationID()
	realDir, err := data.ResolveDataDir(cid)
	if err != nil {
		t.Fatalf("data.ResolveDataDir: %v", err)
	}

	dst := t.TempDir()
	if err := filepath.WalkDir(realDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(realDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return bug726CopyFile(path, target)
	}); err != nil {
		t.Fatalf("copy data dir: %v", err)
	}

	mortCfg, err := citizens.LoadMortalityConfig(dst, cid)
	if err != nil {
		t.Fatalf("LoadMortalityConfig (copied dir, pre-edit): %v", err)
	}
	mortCfg.Params.MonthlyDeathBudget.Value = bug726HugeMonthlyDeathBudget
	b, err := json.MarshalIndent(mortCfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent mortality config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, citizens.FileMortality), b, 0o644); err != nil {
		t.Fatalf("WriteFile mortality.json: %v", err)
	}

	// Confirm the rewritten file still loads and validates (LoadMortalityConfig
	// runs the same validate() every production Wire() call path runs) and
	// actually carries the new figure, before the test relies on it.
	reloaded, err := citizens.LoadMortalityConfig(dst, cid)
	if err != nil {
		t.Fatalf("LoadMortalityConfig (copied dir, post-edit): %v", err)
	}
	if got := reloaded.MonthlyDeathBudget(); got != int(bug726HugeMonthlyDeathBudget) {
		t.Fatalf("rewritten mortality.json MonthlyDeathBudget() = %d, want %d", got, int(bug726HugeMonthlyDeathBudget))
	}
	return dst
}

func bug726CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// bug726SeedAncientCitizens bulk-injects bug726AncientCitizenCount
// guaranteed-to-die citizens directly into the composition's live
// citizens.CitizensAPI cold store via the same public SeedColdRecords bulk
// seam this package's own mkFertilityColdRecord/buildFertilityCoupleAPI
// helpers use (registry.go's own doc: "exported for exactly this").
func bug726SeedAncientCitizens(t *testing.T, api *citizens.CitizensAPI, cid string) {
	t.Helper()
	records := make([]citizens.ColdRecord, bug726AncientCitizenCount)
	for i := range records {
		records[i] = citizens.ColdRecord{
			ID:         bug726AncientCitizenBase + uint64(i) + 1,
			BirthMonth: bug726AncientBirthMonth,
		}
	}
	if err := api.SeedColdRecords(records, cid); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
}

// TestBUG726_ComposeLevelDrainBindingThroughRealWire is the non-vacuous
// compose-level replacement/extension for BUG-689 G1: it proves compose.go's
// deathServicesAPI.WireDrainCapacity(c, cid) call ACTUALLY reaches the real,
// Wire()'d citizens.DeathQueue -- through the public composition surfaces
// only (DeathHandoffSince, MonthlyDrainCapacity), never a bare DeathQueue
// and never a source grep.
//
// Assertions:
//  1. Month 0's realised release count EXACTLY equals the crematorium-
//     configured module's own live MonthlyDrainCapacity(0) -- read BEFORE
//     any tick runs, so no deathservices disposal activity for month 0 has
//     happened yet on either side of the comparison (the module's
//     MonthlyDrainCapacity doc: hearse usage is tracked per monthIndex, so
//     a pre-tick read for month 0 and the real RealiseDrained call's own
//     internal read for month 0 both see zero hearse consumption).
//  2. Building a SECOND crematorium mid-run (deathservices.
//     DeathServicesAPI.RegisterCrematorium, the same production entry
//     point compose.go itself and the BUG-743 bridge call) makes the
//     module's own live MonthlyDrainCapacity STRICTLY RISE.
//  3. The very next month's realised release count also STRICTLY RISES
//     over month 0's -- proving the capacity increase actually reaches the
//     live queue's observable release, not just a standalone number.
//  4. The risen release never exceeds the risen capacity ceiling (sanity
//     upper bound).
func TestBUG726_ComposeLevelDrainBindingThroughRealWire(t *testing.T) {
	cid := errs.NewCorrelationID()

	dataDir := bug726CopyDataDirWithHugeMortalityBudget(t)
	t.Setenv("METROPOLIS_DATA_DIR", dataDir)

	api, err := citizens.NewCitizensAPI(bug726Seed, cid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(bug726Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{
		CorrelationID:          cid,
		Citizens:               api,
		DeathServiceCrematoria: []string{"bug726-crem-1"},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	ds := comp.state.deathServices
	if ds == nil {
		t.Fatal("comp.state.deathServices == nil after Wire with DeathServiceCrematoria configured")
	}

	// --- Warm up 3 months BEFORE seeding the ancient cohort (mirrors
	// TestFEAT169_LiveDeaths_RealMortality's own "not month %12==0"
	// reasoning): month 0 is January under data/mortality.json's
	// weatherEmergency thresholds against data/seasonal.json's winter
	// curve, and RealiseDrained's own doc is explicit that a declared
	// weather emergency makes the injected drain capacity IGNORED
	// ENTIRELY (min(emergency budget, queued) alone) -- exactly the
	// behaviour this test must NOT accidentally exercise, since it would
	// release the whole queue regardless of whether WireDrainCapacity is
	// wired at all, making the test vacuous in a different way. Three
	// warm-up months (no ancient citizens seeded yet, only the 64
	// ordinary founders) land on month index 3 == April, an ordinary
	// (non-emergency) month under those same thresholds.
	advanceInChunks(t, e, 3*int64(core.DailyTicksPerMonth))
	monthA, err := comp.state.currentMonth()
	if err != nil {
		t.Fatalf("currentMonth (post warm-up): %v", err)
	}
	if monthA%12 == 11 || monthA%12 == 0 || monthA%12 == 1 {
		t.Fatalf("test setup invalid: warm-up landed on month index %d (month-of-year %d), a winter month under data/mortality.json's weatherEmergency thresholds -- this test's drain-binding measurement requires an ordinary, non-emergency month", monthA, monthA%12)
	}
	cursorBase, err := api.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff (post warm-up baseline): %v", err)
	}
	baseline := len(cursorBase)

	bug726SeedAncientCitizens(t, api, cid)

	// --- Month A: capacity read BEFORE the tick, no deathservices activity
	// for month A has occurred on either side yet.
	capBeforeMonthA := ds.MonthlyDrainCapacity(monthA)
	if capBeforeMonthA <= 0 {
		t.Fatalf("test setup invalid: MonthlyDrainCapacity(%d) with one crematorium = %d, want > 0", monthA, capBeforeMonthA)
	}
	if capBeforeMonthA >= bug726AncientCitizenCount {
		t.Fatalf("test setup invalid: capacity %d >= seeded population %d -- the drain would never bind against the queue size, defeating this test's whole point", capBeforeMonthA, bug726AncientCitizenCount)
	}

	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))

	handoffA, err := api.DeathHandoffSince(baseline, cid)
	if err != nil {
		t.Fatalf("DeathHandoffSince(%d): %v", baseline, err)
	}
	releasedA := len(handoffA)
	if releasedA != capBeforeMonthA {
		t.Fatalf("month %d realised release = %d, want exactly the crematorium-configured module's live capacity %d -- compose's WireDrainCapacity wiring is not binding the real citizens.DeathQueue (may have been removed or passed nil)", monthA, releasedA, capBeforeMonthA)
	}

	// --- Build a second crematorium mid-run via the real production entry
	// point (the same one compose.go's Deps-time loop and the BUG-743
	// bridge both call) -- never a Deps mutation, never a source seam.
	if err := ds.RegisterCrematorium("bug726-crem-2", cid); err != nil {
		t.Fatalf("RegisterCrematorium (second): %v", err)
	}

	monthB := monthA + 1
	capBeforeMonthB := ds.MonthlyDrainCapacity(monthB)
	if capBeforeMonthB <= capBeforeMonthA {
		t.Fatalf("MonthlyDrainCapacity did not RISE after registering a second crematorium: month%d=%d month%d=%d", monthA, capBeforeMonthA, monthB, capBeforeMonthB)
	}
	if capBeforeMonthB >= bug726AncientCitizenCount-releasedA {
		t.Fatalf("test setup invalid: remaining seeded population (%d) too small to exercise the risen capacity %d", bug726AncientCitizenCount-releasedA, capBeforeMonthB)
	}

	baselineB := baseline + releasedA
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))

	handoffB, err := api.DeathHandoffSince(baselineB, cid)
	if err != nil {
		t.Fatalf("DeathHandoffSince(%d): %v", baselineB, err)
	}
	releasedB := len(handoffB)
	if releasedB <= releasedA {
		t.Fatalf("month %d realised release (%d) did not rise over month %d's (%d) after registering a second crematorium -- the risen module capacity never reached the live citizens.DeathQueue", monthB, releasedB, monthA, releasedA)
	}
	if releasedB > capBeforeMonthB {
		t.Fatalf("month %d realised release (%d) exceeded the module's own risen capacity ceiling (%d)", monthB, releasedB, capBeforeMonthB)
	}
}
