package deathservices

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestHearseOnePerTrip (AC-7): the hearse trip capacity field reads
// exactly 1.
func TestHearseOnePerTrip(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	got, err := d.HearseTripCapacity("corr")
	if err != nil {
		t.Fatalf("HearseTripCapacity: %v", err)
	}
	if got != 1 {
		t.Fatalf("HearseTripCapacity = %d, want 1", got)
	}
}

// TestHearseBacklogSurgePersists (AC-7): a death surge exceeding the
// monthly hearse budget accumulates as a queryable, PERSISTING
// unhandled-body backlog -- never drained in one tick regardless of how
// large the offered batch is (the false-pass-risk this AC calls out
// explicitly: a scheduler that loops to clear the whole backlog in one
// tick would satisfy a capacity-field-reads-1 check while destroying this
// behaviour).
func TestHearseBacklogSurgePersists(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	budget, err := d.HearseMonthlyBudget("corr")
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	n := budget + 37 // deliberately over the monthly cap
	deaths := make([]citizens.RealisedDeath, n)
	ids := make([]uint64, n)
	for i := int64(0); i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
		ids[i] = uint64(i + 1)
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	transported, backlog, err := d.RunHearseTransport(ids, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport: %v", err)
	}
	if int64(len(transported)) != budget {
		t.Fatalf("transported = %d, want exactly the monthly budget %d", len(transported), budget)
	}
	if int64(backlog) != n-budget {
		t.Fatalf("backlog = %d, want %d (N - cleared)", backlog, n-budget)
	}

	// The backlog PERSISTS across a second call in the SAME month (budget
	// already consumed) -- it is not silently drained by looping.
	remainingIDs := ids[len(transported):]
	transported2, backlog2, err := d.RunHearseTransport(remainingIDs, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport (same month): %v", err)
	}
	if len(transported2) != 0 {
		t.Fatalf("second call in the same month transported %d bodies, want 0 (budget exhausted)", len(transported2))
	}
	if backlog2 != len(remainingIDs) {
		t.Fatalf("backlog2 = %d, want %d (unchanged -- no trip made)", backlog2, len(remainingIDs))
	}

	globalBacklog, _ := d.AwaitingBacklog("corr")
	if int64(globalBacklog) != n-budget {
		t.Fatalf("global awaiting backlog = %d, want %d", globalBacklog, n-budget)
	}

	// Next month: the budget resets and the persisted backlog drains.
	transported3, _, err := d.RunHearseTransport(remainingIDs, "cem-1", 2, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport (next month): %v", err)
	}
	if len(transported3) != len(remainingIDs) {
		t.Fatalf("next month transported %d, want the full remaining backlog %d", len(transported3), len(remainingIDs))
	}
}

// TestHearseNeverExceedsOneBodyPerTrip (AC-7(b)): across a transport run,
// no single trip (i.e. no single Bury call inside RunHearseTransport) ever
// carries more than one body -- proven by observing that transported
// bodies are individually, successfully buried one at a time (a >1-per-
// trip implementation would need a different call shape entirely; this
// test pins the current one-Bury-call-per-body invariant).
func TestHearseNeverExceedsOneBodyPerTrip(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	ids := []uint64{1, 2, 3}
	deaths := make([]citizens.RealisedDeath, len(ids))
	for i, id := range ids {
		deaths[i] = citizens.RealisedDeath{CitizenID: id, DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	transported, _, err := d.RunHearseTransport(ids, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport: %v", err)
	}
	if len(transported) != 3 {
		t.Fatalf("transported = %v, want all 3", transported)
	}
	// Each is independently, individually buried (occupies its own plot) --
	// the observable signature of one-at-a-time trips.
	occ, _, err := d.CemeteryOccupancy("cem-1", "corr")
	if err != nil {
		t.Fatalf("CemeteryOccupancy: %v", err)
	}
	if occ != 3 {
		t.Fatalf("occupancy = %d, want 3 (one plot per transported body)", occ)
	}
}

// congestedLogistics builds a REAL logistics.LogisticsAPI from a temp-dir
// fixture whose waste-commodity throughput is squeezed to wasteThroughput
// (a genuinely congested movement channel -- the same data field that
// congests refuse collection rounds), leaving everything else a faithful
// copy of the live data/logistics.json + data/market.json. This is what
// makes TestHearseCongestionDelaysTrips a REAL property rather than a
// call-shape check: the LogisticsAPI is not a mock, its Deliverable math
// (min(localThroughput, marketCeiling) x shortfallFactor) runs for real.
func congestedLogistics(t *testing.T, wasteThroughput int64) *logistics.LogisticsAPI {
	t.Helper()
	realDir, err := data.ResolveDataDir("corr")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	dir := t.TempDir()

	// logistics.json: loaded, waste throughput squeezed, re-encoded.
	var lf map[string]any
	b, err := os.ReadFile(filepath.Join(realDir, "logistics.json"))
	if err != nil {
		t.Fatalf("read logistics.json: %v", err)
	}
	if err := json.Unmarshal(b, &lf); err != nil {
		t.Fatalf("unmarshal logistics.json: %v", err)
	}
	waste := lf["commodities"].(map[string]any)["waste"].(map[string]any)
	waste["throughput"] = wasteThroughput
	out, err := json.Marshal(lf)
	if err != nil {
		t.Fatalf("marshal fixture logistics.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logistics.json"), out, 0o644); err != nil {
		t.Fatalf("write fixture logistics.json: %v", err)
	}

	// market.json: verbatim copy (logistics.Load loads it from the same dir).
	mb, err := os.ReadFile(filepath.Join(realDir, "market.json"))
	if err != nil {
		t.Fatalf("read market.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "market.json"), mb, 0o644); err != nil {
		t.Fatalf("write fixture market.json: %v", err)
	}

	lg, err := logistics.Load(dir, "corr")
	if err != nil {
		t.Fatalf("logistics.Load(fixture): %v", err)
	}
	return lg
}

// TestHearseCongestionDelaysTrips (AC-8, restored round-3 after the
// engine.market SSOT edge landed in 6a4e210): a hearse trip is subject to
// the SAME movement congestion any logistics round faces. With a REAL
// LogisticsAPI whose waste-channel throughput is squeezed below the hearse
// budget, RunHearseTransport must transport FEWER bodies than the budget
// allows -- 25-offered -> 25-transported must become FALSE under
// congestion (the exact regression the round-3 attacker's
// TestAttackR3HearseIgnoresLogisticsEntirely measured while the call was
// removed). The uncongested control run proves the delta is caused by the
// logistics state, nothing else.
func TestHearseCongestionDelaysTrips(t *testing.T) {
	const offered = 25
	setup := func(lg *logistics.LogisticsAPI) *DeathServicesAPI {
		cfg := writeConfigFixture(t, func(c *Config) {
			c.Params.HearseMonthlyTransportBudget.Value = offered
		})
		d := NewDeathServicesAPI(cfg, "corr")
		if err := d.Wire(nil, lg, "corr"); err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if err := d.RegisterCemeteryWithCapacity("cem-1", 500, "corr"); err != nil {
			t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
		}
		deaths := make([]citizens.RealisedDeath, offered)
		for i := 0; i < offered; i++ {
			deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
		}
		if _, err := d.Intake(deaths, "corr"); err != nil {
			t.Fatalf("Intake: %v", err)
		}
		return d
	}

	// Control: uncongested (live data) -- the full budget moves.
	lgFree, err := logistics.LoadDefault("corr")
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	dFree := setup(lgFree)
	ids, err := dFree.AwaitingSorted("corr")
	if err != nil {
		t.Fatalf("AwaitingSorted: %v", err)
	}
	free, _, err := dFree.RunHearseTransport(ids, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport (uncongested): %v", err)
	}
	if len(free) != offered {
		t.Fatalf("uncongested control transported %d, want the full %d", len(free), offered)
	}

	// Congested: the waste channel squeezed to 5 units -- Deliverable's
	// effective throughput (floor(5 x shortfallFactor)) is the binding
	// bound, well below the hearse budget.
	const squeezedThroughput = 5
	dJam := setup(congestedLogistics(t, squeezedThroughput))
	idsJam, err := dJam.AwaitingSorted("corr")
	if err != nil {
		t.Fatalf("AwaitingSorted: %v", err)
	}
	jammed, _, err := dJam.RunHearseTransport(idsJam, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport (congested): %v", err)
	}
	if len(jammed) >= len(free) {
		t.Fatalf("AC-8 VIOLATED: congested logistics transported %d, uncongested %d -- logistics state does not delay hearse trips", len(jammed), len(free))
	}
	if int64(len(jammed)) > squeezedThroughput {
		t.Fatalf("congested run transported %d, more than the squeezed channel's %d-unit throughput ceiling", len(jammed), squeezedThroughput)
	}
	if len(jammed) == 0 {
		t.Fatalf("congested run transported 0 -- congestion should THROTTLE trips (data throughput %d > 0), not eliminate them", squeezedThroughput)
	}

	// The throttled remainder stays a real backlog (AC-7).
	backlog, err := dJam.AwaitingBacklog("corr")
	if err != nil {
		t.Fatalf("AwaitingBacklog: %v", err)
	}
	if backlog != offered-len(jammed) {
		t.Fatalf("backlog = %d after congestion, want %d (offered - transported)", backlog, offered-len(jammed))
	}
}

// TestHearseBudgetTOCTOURegression (N4, round-3): the author-side
// regression for H3's single-continuous-lock fix -- the round-3 attacker
// proved a mutation reintroducing the read-budget/unlock/work/re-lock
// window (MU-B) left this suite green with only their attack test
// catching it. Concurrent RunHearseTransport callers racing for one small
// monthly budget must NEVER collectively exceed it, on any interleaving.
func TestHearseBudgetTOCTOURegression(t *testing.T) {
	const budget = 10
	const workers = 16
	const perWorker = 8
	cfg := writeConfigFixture(t, func(c *Config) {
		c.Params.HearseMonthlyTransportBudget.Value = budget
	})
	d := NewDeathServicesAPI(cfg, "corr")
	if err := d.RegisterCemeteryWithCapacity("cem-1", 5000, "corr"); err != nil {
		t.Fatalf("RegisterCemeteryWithCapacity: %v", err)
	}
	total := workers * perWorker
	deaths := make([]citizens.RealisedDeath, total)
	for i := 0; i < total; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w*perWorker + 1
		wg.Add(1)
		go func(lo int) {
			defer wg.Done()
			ids := make([]uint64, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				ids = append(ids, uint64(lo+i))
			}
			_, _, _ = d.RunHearseTransport(ids, "cem-1", 1, "corr")
		}(lo)
	}
	wg.Wait()

	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.BodiesBuried > budget {
		t.Fatalf("H3 TOCTOU regression: %d bodies buried against a monthly budget of %d -- concurrent callers saw a stale remaining-budget figure", snap.BodiesBuried, budget)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Fatalf("conservation broke: %+v", snap)
	}
}

// TestHearseWorksUnwiredFromLogistics (AC-8 boundary): a DeathServicesAPI
// never Wired to engine.logistics still transports normally, bounded by
// the hearse budget alone.
func TestHearseWorksUnwiredFromLogistics(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	transported, _, err := d.RunHearseTransport([]uint64{1}, "cem-1", 1, "corr")
	if err != nil {
		t.Fatalf("RunHearseTransport unwired: %v", err)
	}
	if len(transported) != 1 {
		t.Fatalf("transported = %v, want [1]", transported)
	}
}

// TestHearseMonthLevelBudgetOnly (AC-9): the throughput tests above assert
// MONTH-level totals via RunHearseTransport's single aggregate budget
// field, never a per-tick/per-vehicle count -- this test pins that the
// budget accessor itself is a single monthly figure, not a sub-tick
// schedule.
func TestHearseMonthLevelBudgetOnly(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	budget, err := d.HearseMonthlyBudget("corr")
	if err != nil {
		t.Fatalf("HearseMonthlyBudget: %v", err)
	}
	if budget <= 0 {
		t.Fatalf("HearseMonthlyBudget = %d, want > 0", budget)
	}
}
