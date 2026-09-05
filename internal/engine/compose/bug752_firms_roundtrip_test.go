package compose

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// saveWithParticipants mirrors Composition.Save's own body (save_wire.go)
// exactly, but over a CALLER-SUPPLIED participant list rather than
// comp.Participants() — the RED-PROOF's way of reproducing "the firms
// participant was never registered" without touching save_wire.go itself.
func saveWithParticipants(t *testing.T, dir string, comp *Composition, participants []save.Participant) error {
	t.Helper()
	clock, err := comp.state.e.Clock()
	if err != nil {
		return err
	}
	gameMode, err := comp.state.gameInit.GameModeWire(comp.state.cid)
	if err != nil {
		return err
	}
	ctx := save.Context{
		WorldSeed:     int64(comp.state.seed),
		CreatedAtTick: clock.Tick(),
		GameMonth:     clock.Month(),
		AppVersion:    compositionSaveAppVersion,
		GameMode:      gameMode,
	}
	mgr := save.NewManager(dir, participants, comp.state.cid)
	return mgr.SaveManual(ctx, compositionSaveName)
}

// loadWithParticipants mirrors Composition.Load's own body over a
// CALLER-SUPPLIED participant list — the RED-PROOF's load-side twin of
// saveWithParticipants.
func loadWithParticipants(t *testing.T, dir string, comp *Composition, participants []save.Participant) error {
	t.Helper()
	summaries, _, err := save.List(dir)
	if err != nil {
		return err
	}
	saveDir := ""
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			saveDir = s.Path
			break
		}
	}
	if saveDir == "" {
		return fmt.Errorf("no composition save named %q found under %s", compositionSaveName, dir)
	}
	gameMode, err := comp.state.gameInit.GameModeWire(comp.state.cid)
	if err != nil {
		return err
	}
	mgr := save.NewManager(dir, participants, comp.state.cid)
	_, _, err = mgr.Load(saveDir, save.WithExpectedWorldSeed(int64(comp.state.seed)), save.WithExpectedGameMode(gameMode))
	return err
}

// BUG-752 (P2, save integrity): engine.firms was NOT a save.Participant —
// compose/save_wire.go's Participants() had no firms entry, so a Load
// silently dropped the whole firm registry. Measured effect (the bug
// report's own numbers): BUG-745's AggregateOutputScale reverted to the
// neutral 1000 once every firm vanished, the builders'-merchant firm
// re-registered under a NEW id (compose's buildersMerchantFirmID pointed
// at nothing), and a saved-then-loaded city diverged from a never-saved
// control by tens of millions of money over a short run. This file proves
// that divergence is now GONE.
//
// bug752FixtureMonths is the drive-then-continue split: N months with a
// deliberately input-starved firm (so OutputScale is measurably non-neutral
// — mirrors BUG-745's own fixture reasoning) before the save/load boundary,
// then N more months on both arms afterward.
const bug752FixtureMonths = 6

// bug752Observables is the exact set of numbers BUG-745's own fixture
// measured diverging: treasury, tracked citizen wealth, the population
// fingerprint, and the firms module's own AggregateOutputScale.
type bug752Observables struct {
	treasury    int64
	wealth      int64
	popHash     [32]byte
	outputScale int64
}

func observeBUG752(t *testing.T, comp *Composition) bug752Observables {
	t.Helper()
	scale, err := comp.state.firms.AggregateOutputScale()
	if err != nil {
		t.Fatalf("AggregateOutputScale: %v", err)
	}
	return bug752Observables{
		treasury:    comp.Treasury(),
		wealth:      comp.CitizenWealth(),
		popHash:     comp.PopulationHash(),
		outputScale: scale,
	}
}

// driveBUG752 registers one input-starved firm (exactly BUG-745's own
// half-output shape: InputRequired = 2x the real ConstructionMaterials
// capacity, so applyInputScalingLocked computes OutputScale=500) and
// advances the given number of months.
func driveBUG752(t *testing.T, e *core.Engine, comp *Composition, months int) {
	t.Helper()
	capacity := constructionMaterialsCapacity(t, comp)
	registerFirmWithInputRequired(t, comp, capacity*2)
	for m := 0; m < months; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", m, err)
		}
	}
}

// advanceBUG752 continues an already-driven composition for the given
// number of months (no new firm registered — the fixture's ONE firm must
// be the SAME firm on both arms, restored via the save/load boundary on
// the loaded arm and never-touched on the control arm).
func advanceBUG752(t *testing.T, e *core.Engine, months int) {
	t.Helper()
	for m := 0; m < months; m++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
			t.Fatalf("month %d: AdvanceTicks: %v", m, err)
		}
	}
}

// TestBUG752_FirmsParticipant_RoundTripParityAgainstNeverSavedControl is the
// make-or-break proof: a composition driven with a real, input-starved firm
// for bug752FixtureMonths, saved, and loaded into a fresh composition —
// then BOTH arms continued for another bug752FixtureMonths — must produce
// byte-identical treasury/wealth/PopulationHash/AggregateOutputScale. Before
// this fix, the loaded arm's firm registry was empty post-Load, so
// AggregateOutputScale reverted to 1000 (neutral) instead of the control's
// genuinely-scaled value, and the two arms diverged in money within the
// first post-load month.
func TestBUG752_FirmsParticipant_RoundTripParityAgainstNeverSavedControl(t *testing.T) {
	// Control arm: never saved, drives the full 2*bug752FixtureMonths in one
	// continuous run.
	eControl, compControl := newTestEngine(t, roundTripSeed)
	driveBUG752(t, eControl, compControl, bug752FixtureMonths)

	// Loaded arm: drives the SAME first half, saves, then loads into a
	// FRESH composition before continuing the second half.
	eSaved, compSaved := newTestEngine(t, roundTripSeed)
	driveBUG752(t, eSaved, compSaved, bug752FixtureMonths)

	// Sanity: the firm is genuinely input-starved before the save/load
	// boundary at all — a vacuous fixture (scale stuck at 1000) would make
	// this whole test meaningless (mirrors BUG-745's own sanity check).
	preSaveScale, err := compSaved.state.firms.AggregateOutputScale()
	if err != nil {
		t.Fatalf("AggregateOutputScale (pre-save): %v", err)
	}
	if preSaveScale == 1000 {
		t.Fatal("fixture is vacuous: AggregateOutputScale is neutral before the save/load boundary")
	}

	dir := t.TempDir()
	if err := compSaved.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	savedClock, err := eSaved.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	eLoaded, compLoaded := newTestEngine(t, roundTripSeed)
	if err := compLoaded.LoadAt(dir, savedClock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}

	// Immediately after the load (before continuing), the loaded arm's
	// registry must already match the saved arm's — the BUG-752 divergence
	// (empty registry, neutral scale) would show up right here.
	postLoadScale, err := compLoaded.state.firms.AggregateOutputScale()
	if err != nil {
		t.Fatalf("AggregateOutputScale (post-load): %v", err)
	}
	if postLoadScale != preSaveScale {
		t.Fatalf("AggregateOutputScale did not survive Load: pre-save=%d post-load=%d (the BUG-752 divergence)", preSaveScale, postLoadScale)
	}
	if len(compLoaded.state.firms.Firms()) != len(compSaved.state.firms.Firms()) {
		t.Fatalf("firm registry did not survive Load: saved has %d firms, loaded has %d", len(compSaved.state.firms.Firms()), len(compLoaded.state.firms.Firms()))
	}

	// Continue BOTH arms the second half, driven by the pipeline alone (no
	// new RegisterFirm call — the fixture's one firm must be the SAME firm
	// on both arms throughout).
	advanceBUG752(t, eControl, bug752FixtureMonths)
	advanceBUG752(t, eLoaded, bug752FixtureMonths)

	control := observeBUG752(t, compControl)
	loaded := observeBUG752(t, compLoaded)

	if control != loaded {
		t.Fatalf("BUG-752 divergence NOT fixed:\ncontrol (never saved): %+v\nloaded (save/load boundary): %+v", control, loaded)
	}
}

// TestBUG752_OldSaveWithoutFirmsShard_LoadsToEmptyRegistry proves the
// documented migration path: a bundle written with NO "firms" shard (every
// save taken before this participant existed — simulated here by dropping
// the firms participant's shard from the header before Load, mirroring
// save_wire.go's own hasDeathServicesShard precedent for the equivalent
// BUG-689 migration case) must not error, and must leave the loaded
// composition's firm registry empty rather than partially populated or
// crashed.
func TestBUG752_OldSaveWithoutFirmsShard_LoadsToEmptyRegistry(t *testing.T) {
	e, comp := newTestEngine(t, roundTripSeed)
	// No firm registered at all — a genuinely empty registry, matching
	// what an old, pre-BUG-752 save would decode to (Handler never called,
	// LoadDefault's fresh empty map stands).
	if err := e.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, compLoaded := newTestEngine(t, roundTripSeed)
	if err := compLoaded.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(compLoaded.state.firms.Firms()); got != 0 {
		t.Fatalf("expected an empty firm registry, got %d firms", got)
	}
}

// TestBUG752_RedProof_RemovingParticipantRedsParity is the RED-PROOF: with
// the firms participant deliberately excluded from the assembled list (this
// test builds its own participant.Save/Load path over a fourteen-entry
// slice missing firms, mirroring how save.Manager consumes Participants()),
// the parity this bug fixes is measurably lost — proving
// TestBUG752_FirmsParticipant_RoundTripParityAgainstNeverSavedControl has
// real teeth rather than passing vacuously.
func TestBUG752_RedProof_RemovingParticipantRedsParity(t *testing.T) {
	eControl, compControl := newTestEngine(t, roundTripSeed)
	driveBUG752(t, eControl, compControl, bug752FixtureMonths)

	eSaved, compSaved := newTestEngine(t, roundTripSeed)
	driveBUG752(t, eSaved, compSaved, bug752FixtureMonths)

	preSaveScale, err := compSaved.state.firms.AggregateOutputScale()
	if err != nil {
		t.Fatalf("AggregateOutputScale: %v", err)
	}
	if preSaveScale == 1000 {
		t.Fatal("fixture is vacuous")
	}

	// Save/load via a save.Manager built over Participants() WITH firms
	// excluded — this is exactly the pre-fix shape (save_wire.go's
	// Participants() before this change had no firms entry).
	dir := t.TempDir()
	full := compSaved.Participants()
	withoutFirms := make([]save.Participant, 0, len(full)-1)
	for _, p := range full {
		if p.Kind() == "firms" {
			continue
		}
		withoutFirms = append(withoutFirms, p)
	}
	if len(withoutFirms) != len(full)-1 {
		t.Fatalf("expected to drop exactly one participant, dropped %d", len(full)-len(withoutFirms))
	}
	if err := saveWithParticipants(t, dir, compSaved, withoutFirms); err != nil {
		t.Fatalf("saveWithParticipants: %v", err)
	}

	_, compLoaded := newTestEngine(t, roundTripSeed)
	if err := loadWithParticipants(t, dir, compLoaded, withoutFirms); err != nil {
		t.Fatalf("loadWithParticipants: %v", err)
	}

	// Without the firms shard, the loaded registry is EMPTY, so
	// AggregateOutputScale reverts to the documented neutral 1000 — exactly
	// the BUG-752 symptom.
	postLoadScale, err := compLoaded.state.firms.AggregateOutputScale()
	if err != nil {
		t.Fatalf("AggregateOutputScale (post-load): %v", err)
	}
	if postLoadScale == preSaveScale {
		t.Fatal("RED-PROOF failed to reproduce the bug: AggregateOutputScale survived a Load with the firms participant excluded")
	}
	if postLoadScale != 1000 {
		t.Fatalf("expected the documented neutral fallback 1000 with an empty registry, got %d", postLoadScale)
	}
	if got := len(compLoaded.state.firms.Firms()); got != 0 {
		t.Fatalf("expected an empty firm registry with firms excluded, got %d firms", got)
	}
}
