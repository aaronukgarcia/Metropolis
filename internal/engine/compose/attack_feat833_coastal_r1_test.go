package compose

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/coastal"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// FEAT-2326609833 destructive round r1 (independent attacker): the restore
// paths with a LIVE engine.coastal. Every pre-existing LoadAt / snapshot +
// tail-replay continuity test injects quietCoastal (zero arrivals), so the
// month==last replay guard, the posted counters and the citizen-id allocator
// position were never exercised across a restored clock with boats arriving.

// liveCoastal builds a busy coastal module (world conditions 1) for seed.
func liveCoastal(t *testing.T, seed uint64) *coastal.CoastalAPI {
	t.Helper()
	api, err := coastal.LoadDefault(seed, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("coastal.LoadDefault: %v", err)
	}
	if err := api.SetWorldConditions(1); err != nil {
		t.Fatalf("SetWorldConditions: %v", err)
	}
	return api
}

func liveCoastalComposition(t *testing.T, store persist.Store, city persist.CityKey) (*core.Engine, *Composition) {
	return liveCoastalCompositionMode(t, store, city, "unlimited")
}

func liveCoastalCompositionMode(t *testing.T, store persist.Store, city persist.CityKey, mode string) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	v := &atomic.Int64{}
	deps := &Deps{GameMode: mode, Coastal: liveCoastal(t, roundTripSeed), InvariantOpts: []invariant.HookOption{invariant.WithLogSink(func(*errs.E) { v.Add(1) })}}
	if store != nil {
		deps.PersistStore, deps.PersistCity = store, city
	}
	comp, err := Wire(e, deps)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	attackViolations[comp] = v
	return e, comp
}

// attackViolations counts invariant-suite (people/money conservation) reports
// per live-coastal composition.
var attackViolations = map[*Composition]*atomic.Int64{}

// assertConserved fails on any invariant report or ledger conservation violation.
func assertConserved(t *testing.T, label string, c *Composition) {
	t.Helper()
	if n := attackViolations[c].Load(); n != 0 {
		t.Errorf("%s: invariant suite reported %d people/money conservation violations", label, n)
	}
	if v := c.state.finance.FindConservationViolations(); len(v) != 0 {
		t.Errorf("%s: FindConservationViolations = %v", label, v)
	}
}

type coastalObs struct {
	digest  [32]byte
	posted  [4]int64
	last    int64
	nextID  uint64
	counts  coastal.Counts
	pop     int
	treas   int64
	hotelsL int64
}

func observeCoastal(c *Composition) coastalObs {
	st := c.state
	h, _ := sumCat(st.finance, finCatCoastalHotels)
	return coastalObs{
		digest: c.StateDigest(), posted: st.coastalW.posted, last: st.coastalW.lastMonth,
		nextID: st.nextCitizenID, counts: st.coastalW.api.Counts(),
		pop: st.citizens.TotalPopulation(st.cid), treas: ledgerBalance(st.finance, finance.AcctTreasury), hotelsL: h,
	}
}

func compareCoastalObs(t *testing.T, label string, ref, got coastalObs) {
	t.Helper()
	if ref.counts.People == 0 || ref.counts.Granted == 0 {
		t.Fatalf("%s: degenerate reference (counts %+v)", label, ref.counts)
	}
	if got.posted != ref.posted {
		t.Errorf("%s: posted counters %v != reference %v (double-post or lost post)", label, got.posted, ref.posted)
	}
	if got.last != ref.last {
		t.Errorf("%s: lastMonth %d != reference %d", label, got.last, ref.last)
	}
	if got.nextID != ref.nextID {
		t.Errorf("%s: nextCitizenID %d != reference %d (double-mint or lost allocator position)", label, got.nextID, ref.nextID)
	}
	if got.counts != ref.counts {
		t.Errorf("%s: coastal counts %+v != reference %+v", label, got.counts, ref.counts)
	}
	if got.pop != ref.pop {
		t.Errorf("%s: population %d != reference %d", label, got.pop, ref.pop)
	}
	if got.treas != ref.treas {
		t.Errorf("%s: treasury %d != reference %d", label, got.treas, ref.treas)
	}
	if got.digest != ref.digest {
		t.Errorf("%s: StateDigest diverged from the never-stopped reference", label)
	}
}

// TestAttackFEAT833_LoadAtLiveCoastalContinuity: Save at a mid-month tick and
// at an exact month-boundary tick, LoadAt into a fresh LIVE-coastal
// composition, continue across several month boundaries; every coastal
// observable plus the composition digest must equal a never-stopped reference.
func TestAttackFEAT833_LoadAtLiveCoastalContinuity(t *testing.T) {
	const total = int64(36 * core.DailyTicksPerMonth)
	eRef, compRef := liveCoastalComposition(t, nil, persist.CityKey{})
	advanceInChunks(t, eRef, total)
	ref := observeCoastal(compRef)
	assertConserved(t, "reference", compRef)

	for _, saveAt := range []int64{7*int64(core.DailyTicksPerMonth) + 13, 8 * int64(core.DailyTicksPerMonth), 20*int64(core.DailyTicksPerMonth) + 5} {
		eA, compA := liveCoastalComposition(t, nil, persist.CityKey{})
		advanceInChunks(t, eA, saveAt)
		dir := t.TempDir()
		if err := compA.Save(dir); err != nil {
			t.Fatalf("Save: %v", err)
		}
		eB, compB := liveCoastalComposition(t, nil, persist.CityKey{})
		if err := compB.LoadAt(dir, saveAt); err != nil {
			t.Fatalf("LoadAt(%d): %v", saveAt, err)
		}
		advanceInChunks(t, eB, total-saveAt)
		compareCoastalObs(t, "LoadAt@"+itoa(saveAt), ref, observeCoastal(compB))
		assertConserved(t, "LoadAt@"+itoa(saveAt), compB)
	}
}

// TestAttackFEAT833_SnapshotTailReplayLiveCoastal: the snapshot + journal-tail
// restore with a LIVE coastal, the tail crossing three month boundaries, plus
// the genesis-replay fallback; both must reproduce the reference exactly.
func TestAttackFEAT833_SnapshotTailReplayLiveCoastal(t *testing.T) {
	ctx := context.Background()
	eRef, compRef := liveCoastalCompositionMode(t, nil, persist.CityKey{}, "")
	advanceViaCommand(t, eRef, 160)
	advanceViaCommand(t, eRef, 200)
	advanceViaCommand(t, eRef, 50)
	advanceViaCommand(t, eRef, 45)
	ref := observeCoastal(compRef)

	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "coastal-r1"}
	eLive, compLive := liveCoastalCompositionMode(t, store, city, "")
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200)
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, city); err != nil || !ok {
		t.Fatalf("MaybeSnapshot at 360: ok=%v err=%v", ok, err)
	}
	advanceViaCommand(t, eLive, 50)
	advanceViaCommand(t, eLive, 45)
	compareCoastalObs(t, "live-precondition", ref, observeCoastal(compLive))

	eR, compR := liveCoastalCompositionMode(t, nil, persist.CityKey{}, "")
	used, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, city)
	if err != nil || !used || tick != 455 {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: used=%v tick=%d err=%v", used, tick, err)
	}
	compareCoastalObs(t, "snapshot+tail", ref, observeCoastal(compR))
	assertConserved(t, "snapshot+tail", compR)

	// Genesis fallback: same journal, no snapshot.
	store2 := persist.NewMemStore()
	city2 := persist.CityKey{TenantID: "t", CityID: "coastal-r1-genesis"}
	eG, _ := liveCoastalCompositionMode(t, store2, city2, "")
	advanceViaCommand(t, eG, 160)
	advanceViaCommand(t, eG, 200)
	advanceViaCommand(t, eG, 50)
	advanceViaCommand(t, eG, 45)
	eR2, compR2 := liveCoastalCompositionMode(t, nil, persist.CityKey{}, "")
	used, tick, err = RestoreLatestSnapshotOrGenesis(ctx, eR2, compR2, store2, city2)
	if err != nil || used || tick != 455 {
		t.Fatalf("genesis restore: used=%v tick=%d err=%v", used, tick, err)
	}
	compareCoastalObs(t, "genesis-replay", ref, observeCoastal(compR2))
	assertConserved(t, "genesis-replay", compR2)
}

// TestAttackFEAT833_SaveWithoutCoastalRecordThenGrants: a save written WITHOUT the
// coastal participant (the shape of every save made before this wiring) is
// loaded into a live-coastal composition and run on; granted coastal citizens
// must never duplicate an existing citizen id, and people conservation holds.
func TestAttackFEAT833_SaveWithoutCoastalRecordThenGrants(t *testing.T) {
	eA, compA := liveCoastalComposition(t, nil, persist.CityKey{})
	saveAt := int64(6 * core.DailyTicksPerMonth)
	advanceInChunks(t, eA, saveAt)
	st := compA.state
	var parts []save.Participant
	for _, p := range compA.Participants() {
		if p.Kind() != kindComposeCoastal {
			parts = append(parts, p)
		}
	}
	clock, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	gm, err := st.gameInit.GameModeWire(st.cid)
	if err != nil {
		t.Fatalf("GameModeWire: %v", err)
	}
	dir := t.TempDir()
	mgr := save.NewManager(dir, parts, st.cid)
	if err := mgr.SaveManual(save.Context{WorldSeed: int64(st.seed), CreatedAtTick: clock.Tick(), GameMonth: clock.Month(), AppVersion: compositionSaveAppVersion, GameMode: gm}, compositionSaveName); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	eB, compB := liveCoastalComposition(t, nil, persist.CityKey{})
	if err := compB.LoadAt(dir, saveAt); err != nil {
		t.Logf("pre-coastal save refused by LoadAt (fail-closed, acceptable): %v", err)
		return
	}
	stB := compB.state
	popAtLoad := stB.citizens.TotalPopulation(stB.cid)
	advanceInChunks(t, eB, int64(18*core.DailyTicksPerMonth))
	// A collision with a citizen minted before the save makes the birth fail
	// loudly (the month is lost under MET-G5482, the id burned) -- never an
	// overwrite: every granted case resolves to a stored citizen and the
	// people/money conservation suite stays clean.
	assertConserved(t, "lost-coastal-record load", compB)
	granted := stB.coastalW.api.Counts().Granted
	t.Logf("lost-coastal-record load: pop@load=%d pop now=%d granted=%d nextID=%d failedMonths=%d", popAtLoad, stB.citizens.TotalPopulation(stB.cid), granted, stB.nextCitizenID, stB.coastalW.failedMonths)
	if granted == 0 {
		t.Fatalf("degenerate: no grants after the pre-coastal load")
	}
	for _, k := range stB.coastalW.api.Snapshot().Cases {
		if k.Stage == coastal.CaseGranted {
			if _, ok := stB.citizens.CitizenAt(k.CitizenID, stB.cid); !ok {
				t.Fatalf("granted case %d citizen %d missing from the store", k.ID, k.CitizenID)
			}
		}
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
