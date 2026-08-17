package airport

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- fixtures -------------------------------------------------------------

// repoDataDir resolves the repo's real data/ directory (airport.json is
// copied verbatim so the module loads against the committed, valid data).
func repoDataDir(t *testing.T) string {
	t.Helper()
	dir, err := data.ResolveDataDir("airport-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	return dir
}

// airportFixtureDir writes a temp data directory with the real
// data/airport.json mutated by mutate (or the committed one when mutate is
// nil).
func airportFixtureDir(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	repo := repoDataDir(t)
	dir := t.TempDir()
	b, err := os.ReadFile(filepath.Join(repo, "airport.json"))
	if err != nil {
		t.Fatalf("read airport.json: %v", err)
	}
	if mutate != nil {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal airport.json: %v", err)
		}
		mutate(m)
		b, err = json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatalf("marshal airport.json: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "airport.json"), b, 0o644); err != nil {
		t.Fatalf("write airport.json: %v", err)
	}
	return dir
}

// loadAirport loads an AirportAPI from a (mutated) fixture directory.
func loadAirport(t *testing.T, mutate func(map[string]any)) *AirportAPI {
	t.Helper()
	dir := airportFixtureDir(t, mutate)
	a, err := Load(dir, "airport-test-correlation")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return a
}

// loadAirportErr loads with an airport.json mutation and returns the
// (expected non-nil) load error.
func loadAirportErr(t *testing.T, mutate func(map[string]any)) error {
	t.Helper()
	dir := airportFixtureDir(t, mutate)
	_, err := Load(dir, "airport-test-correlation")
	return err
}

// mustWire fails the test if a Wire* seam call returns an error (SEC-118: the
// Wire* methods now return the copy-guard error instead of silently wiring a
// dead copy).
func mustWire(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wire: %v", err)
	}
}

// airportTier returns one tier entry from the decoded airport.json map.
func airportTier(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	tiers, ok := m["tiers"].([]any)
	if !ok {
		t.Fatalf("tiers is not a list")
	}
	for _, e := range tiers {
		tm, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if tm["key"] == key {
			return tm
		}
	}
	t.Fatalf("tier %q not found", key)
	return nil
}

// buildAirport wires full stub seams (permit granted, blight accepting,
// road+rail surface access present) and builds tierKey at a milestone and
// land budget that cover every tier in the committed data.
func buildAirport(t *testing.T, tierKey string) *AirportAPI {
	t.Helper()
	a := loadAirport(t, nil)
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(&stubBlight{}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))
	if err := a.Build(tierKey, 9, 10000); err != nil {
		t.Fatalf("Build(%q): %v", tierKey, err)
	}
	return a
}

// airportFromTier constructs an AirportAPI around a single, caller-controlled
// AirportTier (bypassing data loading) and builds it with full surface access.
// Used by AC-2 to hold every component fixed except the one under test.
func airportFromTier(t *testing.T, tier AirportTier) *AirportAPI {
	t.Helper()
	a := &AirportAPI{
		correlationID: "airport-test-correlation",
		cfg: airportConfig{
			tiers: []AirportTier{tier},
			byKey: map[string]AirportTier{tier.Key: tier},
		},
	}
	a.self.Store(a)
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(&stubBlight{}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))
	land := tier.LandFootprintHectares
	if land <= 0 {
		land = 1
	}
	if err := a.Build(tier.Key, tier.Milestone, land); err != nil {
		t.Fatalf("Build(%q): %v", tier.Key, err)
	}
	return a
}

// --- stub seams -----------------------------------------------------------

type stubPermit struct {
	grant bool
}

func (s *stubPermit) PermitGranted(tierKey string, milestone int) (bool, error) {
	return s.grant, nil
}

// blightContour is what the stub registrar stores for one object key: the
// class and radius of its current contour.
type blightContour struct {
	class  mining.BlightClass
	radius int64
}

// stubBlight models engine.mining's BlightAPI as an atomic idempotent UPSERT
// keyed by objectKey (SEC-141): RegisterBlightingObject replaces any existing
// contour under the same key rather than stacking a second one. err injects a
// register failure (a seam outage), mirroring a registrar that rejects the
// write.
type stubBlight struct {
	mu         sync.Mutex
	registered map[string]blightContour
	err        error
}

func (s *stubBlight) RegisterBlightingObject(objectKey string, class mining.BlightClass, contourRadiusM int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.registered == nil {
		s.registered = make(map[string]blightContour)
	}
	s.registered[objectKey] = blightContour{class: class, radius: contourRadiusM}
	return nil
}

type stubSurface struct {
	mu         sync.RWMutex
	road, rail bool
}

func (s *stubSurface) SurfaceAccess() (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.road, s.rail
}

func (s *stubSurface) set(road, rail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.road, s.rail = road, rail
}

type airCargoCall struct {
	inbound   bool
	commodity freight.Commodity
	tonnage   int64
}

type stubAirCargo struct {
	mu    sync.Mutex
	moved []airCargoCall
}

func (s *stubAirCargo) AirCargoMove(inbound bool, commodity freight.Commodity, tonnage int64) (freight.MovementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moved = append(s.moved, airCargoCall{inbound: inbound, commodity: commodity, tonnage: tonnage})
	return freight.MovementResult{Moved: tonnage}, nil
}

// --- AC-2: the node decomposes; no single capacity constant ---------------

func TestRunwayCountMovesThroughput(t *testing.T) {
	base := AirportTier{
		Key:                "runway_test",
		Name:               "Runway test",
		Milestone:          1,
		Runways:            1,
		PaxPerRunwayPerDay: 1000,
		TerminalGates:      1000,
		PaxPerGatePerDay:   1000, // terminal capacity 1,000,000 — never binding
		AccessTier:         AccessDomestic,
		ReachMultiplier:    1,
	}
	one := airportFromTier(t, base)
	base.Runways = 4
	four := airportFromTier(t, base)

	onePax, err := one.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (1 runway): %v", err)
	}
	fourPax, err := four.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (4 runways): %v", err)
	}
	if onePax == fourPax {
		t.Fatalf("runway count must move throughput: 1 runway = %d pax/day, 4 runways = %d pax/day", onePax, fourPax)
	}
}

func TestTerminalCapacityMovesThroughput(t *testing.T) {
	base := AirportTier{
		Key:                "terminal_test",
		Name:               "Terminal test",
		Milestone:          1,
		Runways:            1000,
		PaxPerRunwayPerDay: 1000, // runway capacity 1,000,000 — never binding
		TerminalGates:      10,
		PaxPerGatePerDay:   1000,
		AccessTier:         AccessDomestic,
		ReachMultiplier:    1,
	}
	ten := airportFromTier(t, base)
	base.TerminalGates = 20
	twenty := airportFromTier(t, base)

	tenPax, err := ten.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (10 gates): %v", err)
	}
	twentyPax, err := twenty.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (20 gates): %v", err)
	}
	if tenPax == twentyPax {
		t.Fatalf("terminal gate capacity must move throughput: 10 gates = %d pax/day, 20 gates = %d pax/day", tenPax, twentyPax)
	}
}

// --- AC-3: pax/day and component capacities are data-driven ----------------

func TestThroughputIsDataDriven(t *testing.T) {
	// Baseline throughputs from the committed data.
	baseHeathrowPax, err := buildAirport(t, "heathrow_class_international_airport").PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (heathrow): %v", err)
	}
	baseRegionalPax, err := buildAirport(t, "regional_airport").PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (regional): %v", err)
	}

	// Mutate ONLY the heathrow tier's per-runway rate in the loaded fixture.
	mutateHeathrow := func(m map[string]any) {
		airportTier(t, m, "heathrow_class_international_airport")["paxPerRunwayPerDay"] = json.Number("60000")
	}

	// Building heathrow from the mutated fixture must reflect the change.
	a := loadAirport(t, mutateHeathrow)
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(&stubBlight{}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))
	if err := a.Build("heathrow_class_international_airport", 9, 10000); err != nil {
		t.Fatalf("Build(heathrow): %v", err)
	}
	heathrowPax, err := a.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (mutated heathrow): %v", err)
	}
	if heathrowPax == baseHeathrowPax {
		t.Fatalf("mutating heathrow's per-runway rate must change heathrow throughput (got %d both times)", heathrowPax)
	}

	// Building the regional tier from the SAME mutated fixture must be unchanged.
	b := loadAirport(t, mutateHeathrow)
	mustWire(t, b.WirePermit(&stubPermit{grant: true}))
	mustWire(t, b.WireBlight(&stubBlight{}))
	mustWire(t, b.WireSurface(&stubSurface{road: true, rail: true}))
	if err := b.Build("regional_airport", 9, 10000); err != nil {
		t.Fatalf("Build(regional): %v", err)
	}
	regionalPax, err := b.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (regional): %v", err)
	}
	if regionalPax != baseRegionalPax {
		t.Fatalf("mutating heathrow's rate must not change regional throughput: baseline %d, got %d", baseRegionalPax, regionalPax)
	}
}

// --- AC-5: access-tier step-change reach ladder ----------------------------

func TestAccessTierReachLadder(t *testing.T) {
	// The no-airport floor is the lowest reach.
	none := loadAirport(t, nil)
	if none.AccessTier() != AccessNone {
		t.Fatalf("no airport: AccessTier = %q, want %q", none.AccessTier(), AccessNone)
	}
	if none.ReachMultiplier() != 0 {
		t.Fatalf("no airport: ReachMultiplier = %d, want 0", none.ReachMultiplier())
	}

	dom := buildAirport(t, "regional_airport")
	con := buildAirport(t, "continental_hub")
	glo := buildAirport(t, "heathrow_class_international_airport")

	if dom.AccessTier() != AccessDomestic {
		t.Fatalf("regional: AccessTier = %q, want domestic", dom.AccessTier())
	}
	if con.AccessTier() != AccessContinental {
		t.Fatalf("continental: AccessTier = %q, want continental", con.AccessTier())
	}
	if glo.AccessTier() != AccessGlobal {
		t.Fatalf("heathrow: AccessTier = %q, want global", glo.AccessTier())
	}

	// Monotonic non-decreasing with a strict increase between adjacent rungs.
	if dom.ReachMultiplier() >= con.ReachMultiplier() || con.ReachMultiplier() >= glo.ReachMultiplier() {
		t.Fatalf("reach ladder must step-change: domestic=%d, continental=%d, global=%d",
			dom.ReachMultiplier(), con.ReachMultiplier(), glo.ReachMultiplier())
	}
	if dom.ReachMultiplier() <= 0 {
		t.Fatalf("a domestic airport must out-reach no airport: domestic reach = %d", dom.ReachMultiplier())
	}
}

// --- AC-6: runway-access/adjacency query -----------------------------------

func TestRunwayAccess(t *testing.T) {
	none := loadAirport(t, nil)
	if none.RunwayAccess() {
		t.Fatal("no airport must not report runway access")
	}

	regional := buildAirport(t, "regional_airport") // domestic-only
	if regional.RunwayAccess() {
		t.Fatal("a regional (domestic-only) airport must not report international runway access")
	}

	international := buildAirport(t, "heathrow_class_international_airport") // global
	if !international.RunwayAccess() {
		t.Fatal("a built international airport must report runway access")
	}
}

// --- AC-8: surface access is a real prerequisite ---------------------------

func TestSurfaceAccessGatesThroughput(t *testing.T) {
	a := loadAirport(t, nil)
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(&stubBlight{}))
	surface := &stubSurface{road: false, rail: false} // no surface link
	mustWire(t, a.WireSurface(surface))
	if err := a.Build("heathrow_class_international_airport", 9, 10000); err != nil {
		t.Fatalf("Build: %v", err)
	}

	degraded, err := a.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (degraded): %v", err)
	}
	st, err := a.SurfaceAccessStatus()
	if err != nil {
		t.Fatalf("SurfaceAccessStatus: %v", err)
	}
	if st.Full {
		t.Fatal("with no road/rail link the airport must not report full surface access")
	}

	// Add the road and rail spurs — throughput rises to the full figure.
	surface.set(true, true)
	full, err := a.PassengerThroughput()
	if err != nil {
		t.Fatalf("PassengerThroughput (full): %v", err)
	}
	if degraded >= full {
		t.Fatalf("surface access must gate throughput: degraded=%d, full=%d", degraded, full)
	}
}

// --- AC-9/AC-10: permit + land gate, no mutation on failure ----------------

func TestBuildNoPermitRejected(t *testing.T) {
	a := loadAirport(t, nil)
	mustWire(t, a.WireBlight(&stubBlight{}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))
	// No permit authority wired.
	err := a.Build("regional_airport", 9, 10000)
	if err == nil || !errors.Is(err, &errs.E{Code: ErrAirportBuildRejected}) {
		t.Fatalf("want ErrAirportBuildRejected (no permit authority), got %v", err)
	}
	if a.ActiveTier() != "" {
		t.Fatalf("a refused build must not set activeTier (got %q)", a.ActiveTier())
	}
	if _, err := a.RunwayCount(); err == nil || !errors.Is(err, &errs.E{Code: ErrUnknownAirport}) {
		t.Fatalf("a refused build must leave no component to query: RunwayCount err = %v", err)
	}

	// Permit wired but not granted.
	a2 := loadAirport(t, nil)
	blight2 := &stubBlight{}
	mustWire(t, a2.WirePermit(&stubPermit{grant: false}))
	mustWire(t, a2.WireBlight(blight2))
	mustWire(t, a2.WireSurface(&stubSurface{road: true, rail: true}))
	if err := a2.Build("regional_airport", 9, 10000); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportBuildRejected}) {
		t.Fatalf("want ErrAirportBuildRejected (permit not granted), got %v", err)
	}
	if len(blight2.registered) != 0 {
		t.Fatalf("a permit-refused build must not register a blighting object: %v", blight2.registered)
	}
}

func TestBuildRequiresLand(t *testing.T) {
	a := loadAirport(t, nil)
	blight := &stubBlight{}
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(blight))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))
	// heathrow requires 1200 ha (data); give it 1 ha.
	if err := a.Build("heathrow_class_international_airport", 9, 1); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportBuildRejected}) {
		t.Fatalf("want ErrAirportBuildRejected (insufficient land), got %v", err)
	}
	if len(blight.registered) != 0 {
		t.Fatalf("a land-refused build must not register a blighting object: %v", blight.registered)
	}
	if a.ActiveTier() != "" {
		t.Fatalf("a land-refused build must not set activeTier (got %q)", a.ActiveTier())
	}
}

func TestQueryUnknownAirport(t *testing.T) {
	a := loadAirport(t, nil)
	if _, err := a.RunwayCount(); err == nil || !errors.Is(err, &errs.E{Code: ErrUnknownAirport}) {
		t.Fatalf("RunwayCount on unbuilt airport: want ErrUnknownAirport, got %v", err)
	}
	if _, err := a.Tier("no_such_tier"); err == nil || !errors.Is(err, &errs.E{Code: ErrUnknownAirport}) {
		t.Fatalf("Tier(unknown): want ErrUnknownAirport, got %v", err)
	}
}

// --- AC-11: malformed data rejected, no silent default ---------------------

func TestMalformedAirportDataRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing per-runway rate", func(m map[string]any) {
			delete(airportTier(t, m, "heathrow_class_international_airport"), "paxPerRunwayPerDay")
		}},
		{"negative capacity", func(m map[string]any) {
			airportTier(t, m, "regional_airport")["runways"] = json.Number("-3")
		}},
		{"unrecognised access tier", func(m map[string]any) {
			airportTier(t, m, "continental_hub")["accessTier"] = "interplanetary"
		}},
		{"unrecognised blight class", func(m map[string]any) {
			airportTier(t, m, "heathrow_class_international_airport")["blightClass"] = "apocalyptic"
		}},
		{"inverted reach ladder", func(m map[string]any) {
			// continental (9) out-ranking global (4) inverts the §44 ladder.
			airportTier(t, m, "continental_hub")["reachMultiplier"] = json.Number("9")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadAirportErr(t, tc.mutate)
			if err == nil || !errors.Is(err, &errs.E{Code: ErrAirportDataInvalid}) {
				t.Fatalf("want ErrAirportDataInvalid, got %v", err)
			}
		})
	}
}

// --- AC-12: determinism ----------------------------------------------------

type airportSnapshot struct {
	tick       int64
	throughput int64
	runways    int64
	reach      int64
	accessTier AccessTier
}

func TestDeterminism(t *testing.T) {
	run := func() airportSnapshot {
		a := buildAirport(t, "heathrow_class_international_airport")
		mustWire(t, a.WireFreight(&stubAirCargo{}))
		if _, err := a.AirCargo(true, "machinery", 500); err != nil {
			t.Fatalf("AirCargo: %v", err)
		}
		if err := a.AdvanceTick(); err != nil {
			t.Fatalf("AdvanceTick: %v", err)
		}
		tp, err := a.PassengerThroughput()
		if err != nil {
			t.Fatalf("PassengerThroughput: %v", err)
		}
		rc, err := a.RunwayCount()
		if err != nil {
			t.Fatalf("RunwayCount: %v", err)
		}
		return airportSnapshot{
			tick:       a.Tick(),
			throughput: tp,
			runways:    rc,
			reach:      a.ReachMultiplier(),
			accessTier: a.AccessTier(),
		}
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(first, got) {
			t.Fatalf("determinism broken: run 0 = %+v, run %d = %+v", first, i+1, got)
		}
	}
}

// --- AC-13: concurrency (run under -race) ----------------------------------

func TestConcurrentComponentQueries(t *testing.T) {
	a := buildAirport(t, "heathrow_class_international_airport")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = a.RunwayCount()
				_, _ = a.TerminalCapacity()
				_, _ = a.PassengerThroughput()
				_ = a.AccessTier()
				_ = a.ReachMultiplier()
				_ = a.RunwayAccess()
				_, _ = a.SurfaceAccessStatus()
				_, _ = a.Tier("heathrow_class_international_airport")
			}
		}()
	}
	for i := 0; i < 1000; i++ {
		if err := a.AdvanceTick(); err != nil {
			t.Fatalf("AdvanceTick: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

// --- SEC-020 copy guard ----------------------------------------------------

// airportByteCopy performs SEC-020's attack — a plain AirportAPI struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, so go vet's copylocks
// check (which VERIFY runs) does not flag the deliberate attack copy.
func airportByteCopy(a *AirportAPI) *AirportAPI {
	cp := new(AirportAPI)
	*(*[unsafe.Sizeof(AirportAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(AirportAPI{})]byte)(unsafe.Pointer(a))
	return cp
}

func TestAirportCopyGuard(t *testing.T) {
	a := loadAirport(t, nil)
	cp := airportByteCopy(a)
	if _, err := cp.RunwayCount(); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportCopiedValue}) {
		t.Fatalf("struct-copied AirportAPI: want ErrAirportCopiedValue, got %v", err)
	}
}

// --- AC-4: air cargo handed to engine.freight, no local tonnage ledger ------

func TestAirCargoHandsOffToFreight(t *testing.T) {
	a := buildAirport(t, "heathrow_class_international_airport")
	cargo := &stubAirCargo{}
	mustWire(t, a.WireFreight(cargo))

	res, err := a.AirCargo(true, "machinery", 500)
	if err != nil {
		t.Fatalf("AirCargo: %v", err)
	}
	if res.Moved != 500 {
		t.Fatalf("AirCargo moved = %d, want 500", res.Moved)
	}
	if len(cargo.moved) != 1 || !cargo.moved[0].inbound || cargo.moved[0].commodity != "machinery" || cargo.moved[0].tonnage != 500 {
		t.Fatalf("air cargo was not handed to the freight seam: %+v", cargo.moved)
	}

	// An unwired freight seam must reject, never silently drop.
	a2 := buildAirport(t, "regional_airport")
	if _, err := a2.AirCargo(true, "machinery", 500); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportBuildRejected}) {
		t.Fatalf("unwired air-cargo arm: want ErrAirportBuildRejected, got %v", err)
	}
}

// --- AC-15 (behavioural): the committed data loads and every tier carries a
// non-empty disclosure, plus the reach ladder is ordered. -------------------

func TestCommittedDataLoads(t *testing.T) {
	a := loadAirport(t, nil)
	tiers := a.Tiers()
	if len(tiers) != 3 {
		t.Fatalf("want 3 airport tiers in the committed data, got %d", len(tiers))
	}
	for _, tier := range tiers {
		if tier.Disclosure == "" {
			t.Fatalf("tier %q carries no disclosure", tier.Key)
		}
	}
	// Reach ladder is monotonic non-decreasing across the ordered tiers.
	prev := int64(0)
	for _, tier := range tiers {
		if tier.ReachMultiplier < prev {
			t.Fatalf("reach ladder not monotonic: %q reach %d < previous %d", tier.Key, tier.ReachMultiplier, prev)
		}
		prev = tier.ReachMultiplier
	}
}

// --- SEC-116/SEC-141: one stable contour, atomically replaced ---------------

func TestBuildUpgradeReplacesContourAtomically(t *testing.T) {
	a := loadAirport(t, nil)
	blight := &stubBlight{}
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(blight))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))

	for _, k := range []string{"regional_airport", "continental_hub", "heathrow_class_international_airport"} {
		if err := a.Build(k, 9, 10000); err != nil {
			t.Fatalf("Build(%q): %v", k, err)
		}
	}

	// SEC-116/SEC-141: the airport is ONE blighting object under a stable key.
	// Each strict upgrade re-registers (atomically replaces) that key's contour
	// in a single call, so the registrar never stacks one entry per tier, and
	// there is no deregister-then-register window that could leave partial
	// state. After three builds the registrar holds exactly one contour: the
	// final tier's.
	if len(blight.registered) != 1 {
		t.Fatalf("want exactly one registered contour, got %d: %v", len(blight.registered), blight.registered)
	}
	final, err := a.Tier("heathrow_class_international_airport")
	if err != nil {
		t.Fatalf("Tier(heathrow): %v", err)
	}
	got, ok := blight.registered[blightObjectKey]
	if !ok {
		t.Fatalf("the stable key %q must hold the final contour, but the registrar holds %v", blightObjectKey, blight.registered)
	}
	if got.class != final.BlightClass || got.radius != final.ContourRadiusM {
		t.Fatalf("the stable key must hold the final tier's contour: got class=%v radius=%d, want class=%v radius=%d",
			got.class, got.radius, final.BlightClass, final.ContourRadiusM)
	}
	if a.ActiveTier() != "heathrow_class_international_airport" {
		t.Fatalf("ActiveTier = %q, want heathrow_class_international_airport", a.ActiveTier())
	}
}

// TestBuildUpgradeRegisterFailureKeepsPriorContour is the SEC-141 regression: a
// strict upgrade whose RegisterBlightingObject call fails must leave the prior
// contour registered and activeTier untouched — a built airport must never lose
// its contour to a failed upgrade. It is key-agnostic on purpose: the invariant
// is that the registrar is UNCHANGED, whatever key the airport registers under.
func TestBuildUpgradeRegisterFailureKeepsPriorContour(t *testing.T) {
	a := loadAirport(t, nil)
	blight := &stubBlight{}
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(blight))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))

	if err := a.Build("regional_airport", 9, 10000); err != nil {
		t.Fatalf("Build(regional): %v", err)
	}
	regional, err := a.Tier("regional_airport")
	if err != nil {
		t.Fatalf("Tier(regional): %v", err)
	}

	// The next register fails (seam outage). Pre-SEC-141, Build deregistered
	// the prior contour FIRST, so this failure left the airport built at
	// regional_airport with no contour registered at all.
	blight.err = errors.New("blight registry unavailable")
	if err := a.Build("continental_hub", 9, 10000); err == nil {
		t.Fatal("want the register error to surface, got nil")
	}
	if a.ActiveTier() != "regional_airport" {
		t.Fatalf("a failed upgrade must not move activeTier (got %q)", a.ActiveTier())
	}
	// SEC-141: the prior contour must still be registered, untouched.
	if len(blight.registered) != 1 {
		t.Fatalf("a failed upgrade must leave the registrar unchanged: %v", blight.registered)
	}
	var got blightContour
	for _, c := range blight.registered {
		got = c
	}
	if got.class != regional.BlightClass || got.radius != regional.ContourRadiusM {
		t.Fatalf("the prior contour must be intact after a failed upgrade: got class=%v radius=%d, want class=%v radius=%d",
			got.class, got.radius, regional.BlightClass, regional.ContourRadiusM)
	}
}

// TestBuildFirstRegisterFailureRegistersNothing covers the first-build failure
// order of the same class: the one external mutation fails on the very first
// build, so the registrar must be untouched and activeTier empty.
func TestBuildFirstRegisterFailureRegistersNothing(t *testing.T) {
	a := loadAirport(t, nil)
	blight := &stubBlight{err: errors.New("blight registry unavailable")}
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(blight))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))

	if err := a.Build("regional_airport", 9, 10000); err == nil {
		t.Fatal("want the register error to surface, got nil")
	}
	if len(blight.registered) != 0 {
		t.Fatalf("a first-build register failure must register nothing: %v", blight.registered)
	}
	if a.ActiveTier() != "" {
		t.Fatalf("a first-build register failure must leave activeTier empty (got %q)", a.ActiveTier())
	}
}

// --- SEC-117: capacity figures are bounded / saturate, never silent 0 --------

func TestLoadRejectsOverflowingCapacity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"runway capacity overflow", func(m map[string]any) {
			airportTier(t, m, "regional_airport")["runways"] = json.Number("1000000000000")
			airportTier(t, m, "regional_airport")["paxPerRunwayPerDay"] = json.Number("1000000000000")
		}},
		{"terminal capacity overflow", func(m map[string]any) {
			airportTier(t, m, "regional_airport")["terminalGates"] = json.Number("1000000000000")
			airportTier(t, m, "regional_airport")["paxPerGatePerDay"] = json.Number("1000000000000")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadAirportErr(t, tc.mutate)
			if err == nil || !errors.Is(err, &errs.E{Code: ErrAirportDataInvalid}) {
				t.Fatalf("want ErrAirportDataInvalid for overflowing capacity, got %v", err)
			}
		})
	}
}

func TestSafeMulNonNegSaturates(t *testing.T) {
	// SEC-117: an overflowing positive product must saturate to MaxInt64,
	// never collapse to a silent 0.
	if got := safeMulNonNeg(1_000_000_000_000, 1_000_000_000_000); got != math.MaxInt64 {
		t.Fatalf("safeMulNonNeg(1e12, 1e12) = %d, want math.MaxInt64 (saturated, never 0)", got)
	}
	if got := safeMulNonNeg(4, 1_000_000_000); got != 4_000_000_000 {
		t.Fatalf("safeMulNonNeg(4, 1e9) = %d, want 4_000_000_000 (exact)", got)
	}
}

func TestApplySurfaceFactorExact(t *testing.T) {
	// SEC-117: full*pct/percentFull must be exact even when the naive
	// intermediate full*pct would overflow — never a saturated wrong answer
	// and never a silent 0.
	full := int64(1_000_000_000_000_000_000) // 1e18
	want := int64(990_000_000_000_000_000)   // 1e18 * 99 / 100
	if got := applySurfaceFactor(full, 99); got != want {
		t.Fatalf("applySurfaceFactor(1e18, 99) = %d, want %d (exact)", got, want)
	}
	if got := applySurfaceFactor(full, 99); got <= math.MaxInt64/100 {
		t.Fatalf("applySurfaceFactor(1e18, 99) = %d must exceed MaxInt64/100 (a saturated naive path)", got)
	}
}

// --- SEC-118: the copy-guard is applied to every wire path -------------------

func TestWireCopyGuard(t *testing.T) {
	a := loadAirport(t, nil)
	cp := airportByteCopy(a)

	// Every Wire* method must reject a struct-copied value and wire nothing.
	cases := []struct {
		name string
		wire func() error
	}{
		{"WirePermit", func() error { return cp.WirePermit(&stubPermit{grant: true}) }},
		{"WireBlight", func() error { return cp.WireBlight(&stubBlight{}) }},
		{"WireSurface", func() error { return cp.WireSurface(&stubSurface{road: true, rail: true}) }},
		{"WireFreight", func() error { return cp.WireFreight(&stubAirCargo{}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.wire(); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportCopiedValue}) {
				t.Fatalf("%s on a copied value: want ErrAirportCopiedValue, got %v", tc.name, err)
			}
		})
	}

	// Wiring the copy must not have wired the original: a later Build on the
	// original still reports the missing permit authority (SEC-118's
	// config-defeat — the error signal now surfaces at the Wire call itself).
	if err := a.Build("regional_airport", 9, 10000); err == nil || !errors.Is(err, &errs.E{Code: ErrAirportBuildRejected}) {
		t.Fatalf("wiring a copy must not wire the original: Build on the unwired original = %v", err)
	}
}

// --- SEC-119: Build must not hold the write lock across a seam callback ------

// reentrantPermit re-enters the airport from inside PermitGranted. A Build
// that held the write lock across the seam would deadlock here, because
// sync.RWMutex is not re-entrant.
type reentrantPermit struct{ a *AirportAPI }

func (p *reentrantPermit) PermitGranted(tierKey string, milestone int) (bool, error) {
	_ = p.a.Tick() // re-entrant read: must not deadlock the write lock
	return true, nil
}

// reentrantBlight re-enters the airport from inside the blight registrar.
type reentrantBlight struct{ a *AirportAPI }

func (b *reentrantBlight) RegisterBlightingObject(objectKey string, class mining.BlightClass, contourRadiusM int64) error {
	_ = b.a.Tick()
	return nil
}

// The re-entrant deadlock is deterministic (a non-reentrant RWMutex blocks a
// nested RLock 100% of the time), never scheduling-dependent — the time.After
// is a fail-fast guard that converts a would-be infinite hang into a test
// failure, not the assertion. The assertion is that Build returns and commits.
func TestBuildReentrantPermitDoesNotDeadlock(t *testing.T) {
	a := loadAirport(t, nil)
	mustWire(t, a.WirePermit(&reentrantPermit{a: a}))
	mustWire(t, a.WireBlight(&stubBlight{}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))

	done := make(chan error, 1)
	go func() { done <- a.Build("regional_airport", 9, 10000) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Build held the write lock across PermitGranted — re-entrant Tick() deadlocked")
	}
	if a.ActiveTier() != "regional_airport" {
		t.Fatalf("ActiveTier = %q, want regional_airport", a.ActiveTier())
	}
}

func TestBuildReentrantBlightDoesNotDeadlock(t *testing.T) {
	a := loadAirport(t, nil)
	mustWire(t, a.WirePermit(&stubPermit{grant: true}))
	mustWire(t, a.WireBlight(&reentrantBlight{a: a}))
	mustWire(t, a.WireSurface(&stubSurface{road: true, rail: true}))

	done := make(chan error, 1)
	go func() { done <- a.Build("regional_airport", 9, 10000) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Build held the write lock across RegisterBlightingObject — re-entrant Tick() deadlocked")
	}
	if a.ActiveTier() != "regional_airport" {
		t.Fatalf("ActiveTier = %q, want regional_airport", a.ActiveTier())
	}
}
