package destination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/engine/parking"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// --- test config + helpers -------------------------------------------------

// testConfig is a self-contained in-memory data/destination.json (GR#15: the
// same magnitudes the real data file carries, held here so unit tests never
// depend on filesystem resolution). Test-file only — the spec's named figures
// in production code live solely in data/destination.json.
func testConfig() config {
	return config{
		Version: 1,
		Archetypes: map[string]archetypeConfig{
			"forestResort": {
				Name:              "Forest holiday resort",
				Jobs:              1500,
				MinFootprintHa:    100,
				YearRoundStaying:  true,
				MinShopFloorspace: 0,
				ParkingSpaces:     1200,
				BaseDrawFactor:    1.0,
				BDIHalfSaturation: 0.4,
				BDIMaxBoost:       0.6,
				BlightClass:       "none",
			},
			"megaMall": {
				Name:              "Mega-mall",
				Jobs:              7000,
				MinFootprintHa:    40,
				YearRoundStaying:  false,
				MinShopFloorspace: 300,
				ParkingSpaces:     8000,
				BaseDrawFactor:    1.0,
				BDIHalfSaturation: 0.4,
				BDIMaxBoost:       0.0,
				BlightClass:       "high",
				NoiseRadiusM:      1500,
				VisualHeightM:     30,
				VisualMagnitude:   0.9,
				ScreenWallHeightM: 12,
			},
		},
	}
}

func mustAPI(t *testing.T) *DestinationAPI {
	t.Helper()
	d, err := New(42, testConfig(), "test-correlation")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func placeResort(t *testing.T, d *DestinationAPI) DestinationID {
	t.Helper()
	id, err := d.Place(PlacementRequest{
		Kind:        ArchetypeForestResort,
		Tile:        world.TileCoord{X: 1, Y: 1},
		Local:       world.CellLocal{Row: 5, Col: 5},
		FootprintHa: 150,
	})
	if err != nil {
		t.Fatalf("place resort: %v", err)
	}
	return id
}

func mallRequest(screened bool) PlacementRequest {
	return PlacementRequest{
		Kind:        ArchetypeMegaMall,
		SiteKey:     "pit-1",
		Tile:        world.TileCoord{X: 2, Y: 2},
		Local:       world.CellLocal{Row: 10, Col: 10},
		FootprintHa: 60,
		Screened:    screened,
	}
}

func placeMall(t *testing.T, d *DestinationAPI, screened bool) DestinationID {
	t.Helper()
	id, err := d.Place(mallRequest(screened))
	if err != nil {
		t.Fatalf("place mall: %v", err)
	}
	return id
}

// errCode extracts the registry code a returned error carries. Because the
// MET-G46xx codes are claimed here but (until the registry sweep registers
// them) errs.New degrades an unregistered code to MET-F003 while preserving
// the requested code in Ctx["code"], this helper reads the intended code in
// either shape — matching the AC-10/AC-11 "the registry code matches" check
// without hard-coding which shape the registry currently resolves to.
func errCode(t *testing.T, err error) string {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected a registry-sourced *errs.E, got %T: %v", err, err)
	}
	if c, ok := e.Ctx["code"].(string); ok && c != "" {
		return c
	}
	return e.Code
}

// --- fakes (test doubles for the registered seams) ------------------------

// fakeTourism is a test double for the TourismDraw seam (AC-1).
type fakeTourism struct {
	score float64
	calls int
}

func (f *fakeTourism) PortfolioScore() (float64, error) {
	f.calls++
	return f.score, nil
}

// staticTourism is a stateless, race-free TourismDraw for concurrency tests.
type staticTourism struct{}

func (staticTourism) PortfolioScore() (float64, error) { return 1.0, nil }

// fakeMining is a test double for the MiningBlight seam (AC-3/AC-4). It
// models the registered blight model's viewshed occlusion: a screening bund
// (added via AddBund) reduces the SEEN component at every queried cell.
type fakeMining struct {
	sites      map[string]mining.ExtractionSite
	objects    []mining.BlightingObjectSpec
	bunds      int
	baseSeen   float64
	screenDrop float64
	addBundErr error // when non-nil, AddBund fails with it
}

func newFakeMining() *fakeMining {
	return &fakeMining{
		sites:      map[string]mining.ExtractionSite{},
		baseSeen:   0.5,
		screenDrop: 0.6,
	}
}

func (f *fakeMining) SiteInfo(key, corr string) (mining.ExtractionSite, error) {
	s, ok := f.sites[key]
	if !ok {
		return mining.ExtractionSite{}, fmt.Errorf("unknown site %q", key)
	}
	return s, nil
}

func (f *fakeMining) PlaceBlightingObject(spec mining.BlightingObjectSpec) error {
	f.objects = append(f.objects, spec)
	return nil
}

func (f *fakeMining) AddBund(tile world.TileCoord, local world.CellLocal, heightM float64, corr string) error {
	if f.addBundErr != nil {
		return f.addBundErr
	}
	f.bunds++
	return nil
}

func (f *fakeMining) EffectAt(tile world.TileCoord, local world.CellLocal, year int64, corr string) (mining.BlightEffect, error) {
	seen := f.baseSeen
	if f.bunds > 0 {
		seen = f.baseSeen * (1 - f.screenDrop)
	}
	return mining.BlightEffect{Heard: 0.1, Seen: seen}, nil
}

// fakeParking is a test double for the ParkingSink seam (AC-9).
type fakeParking struct {
	registered []struct {
		spaces int
		kind   parking.InstrumentType
	}
	failRegister error // when non-nil, RegisterFacility fails with it
}

func (f *fakeParking) RegisterFacility(id uint64, tile world.TileCoord, local world.CellLocal, spaces int, instType parking.InstrumentType, district uint16) error {
	if f.failRegister != nil {
		return f.failRegister
	}
	f.registered = append(f.registered, struct {
		spaces int
		kind   parking.InstrumentType
	}{spaces, instType})
	return nil
}

// --- AC-2: two distinct archetypes with data-driven named characteristics ----

func TestArchetypeDistinct(t *testing.T) {
	d := mustAPI(t)
	resort, err := d.Archetype(ArchetypeForestResort)
	if err != nil {
		t.Fatalf("Archetype(forestResort): %v", err)
	}
	mall, err := d.Archetype(ArchetypeMegaMall)
	if err != nil {
		t.Fatalf("Archetype(megaMall): %v", err)
	}

	if resort.Kind == mall.Kind {
		t.Fatal("resort and mall must be distinct archetype identifiers")
	}
	if resort.Jobs == mall.Jobs {
		t.Fatalf("resort and mall job counts must differ, both %d", resort.Jobs)
	}
	if resort.Jobs < 1000 || resort.Jobs >= 3000 {
		t.Fatalf("resort jobs %d outside the documented ~1.5k order of magnitude", resort.Jobs)
	}
	if mall.Jobs < 5000 || mall.Jobs >= 10000 {
		t.Fatalf("mall jobs %d outside the documented ~7k order of magnitude", mall.Jobs)
	}
	if resort.MinFootprintHa < 100 {
		t.Fatalf("resort min footprint %v below the documented 100+ ha", resort.MinFootprintHa)
	}
	if mall.MinShopFloorspace < 300 {
		t.Fatalf("mall min shop floorspace %d below the documented 300+ shops", mall.MinShopFloorspace)
	}
	if resort.MinShopFloorspace != 0 {
		t.Fatalf("resort must carry no shop-floorspace floor, got %d", resort.MinShopFloorspace)
	}
	if !resort.YearRoundStaying {
		t.Fatal("resort must carry the year-round staying-visitor draw shape")
	}
	if mall.YearRoundStaying {
		t.Fatal("mall must not carry the year-round staying-visitor draw shape")
	}
}

// --- AC-1: regional draw reaches TourismAPI, never a duplicate scoring ----

func TestSharedDrawMachineryReachesTourismAPI(t *testing.T) {
	d := mustAPI(t)
	ft := &fakeTourism{score: 7.5}
	if err := d.SetTourism(ft); err != nil {
		t.Fatalf("SetTourism: %v", err)
	}
	id := placeResort(t, d)

	if _, err := d.RegionalDraw(id, 0.5); err != nil {
		t.Fatalf("RegionalDraw: %v", err)
	}
	if ft.calls == 0 {
		t.Fatal("RegionalDraw never reached the TourismDraw seam — the draw is a destination-local parallel formula, not shared machinery")
	}
}

func TestNoDuplicateScoring(t *testing.T) {
	d := mustAPI(t)
	ft := &fakeTourism{score: 4.0}
	if err := d.SetTourism(ft); err != nil {
		t.Fatalf("SetTourism: %v", err)
	}
	id := placeResort(t, d)

	// At bdi 0 the resort factor is exactly its baseDrawFactor; the draw must
	// be the seam's portfolio score times that factor, not a local constant.
	got, err := d.RegionalDraw(id, 0.0)
	if err != nil {
		t.Fatalf("RegionalDraw: %v", err)
	}
	if want := 4.0 * 1.0; got != want {
		t.Fatalf("RegionalDraw = %v, want %v (portfolio score × baseDrawFactor)", got, want)
	}

	// Raising the seam's portfolio score raises the draw by exactly the same
	// factor — the number is read live from TourismAPI, never hardcoded here.
	ft.score = 8.0
	got2, err := d.RegionalDraw(id, 0.0)
	if err != nil {
		t.Fatalf("RegionalDraw: %v", err)
	}
	if got2 != 8.0 {
		t.Fatalf("RegionalDraw after seam change = %v, want 8.0", got2)
	}
}

// --- AC-3: mega-mall reclamation-site eligibility --------------------------

func TestReclamationGate(t *testing.T) {
	t.Run("rejected on non-exhausted site", func(t *testing.T) {
		d := mustAPI(t)
		m := newFakeMining()
		m.sites["pit-live"] = mining.ExtractionSite{Key: "pit-live", Exhausted: false}
		p := &fakeParking{}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(p); err != nil {
			t.Fatal(err)
		}

		_, err := d.Place(PlacementRequest{
			Kind: ArchetypeMegaMall, SiteKey: "pit-live",
			Tile: world.TileCoord{X: 2, Y: 2}, Local: world.CellLocal{Row: 10, Col: 10},
			FootprintHa: 60,
		})
		if err == nil {
			t.Fatal("mega-mall on a non-reclamation site: want error, got nil")
		}
		if got := errCode(t, err); got != ErrNotReclamationSite {
			t.Fatalf("code = %s, want %s", got, ErrNotReclamationSite)
		}
		if got := len(d.DestinationIDs()); got != 0 {
			t.Fatalf("rejected placement was recorded: %d destinations", got)
		}
	})

	t.Run("accepted on exhausted reclaimable pit", func(t *testing.T) {
		d := mustAPI(t)
		m := newFakeMining()
		m.sites["pit-done"] = mining.ExtractionSite{Key: "pit-done", Exhausted: true}
		p := &fakeParking{}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(p); err != nil {
			t.Fatal(err)
		}

		id, err := d.Place(PlacementRequest{
			Kind: ArchetypeMegaMall, SiteKey: "pit-done",
			Tile: world.TileCoord{X: 2, Y: 2}, Local: world.CellLocal{Row: 10, Col: 10},
			FootprintHa: 60,
		})
		if err != nil {
			t.Fatalf("mega-mall on an exhausted pit: %v", err)
		}
		if id == 0 {
			t.Fatal("placement returned a zero destination id")
		}
	})
}

// --- AC-4: viewshed screening lowers the SEEN contribution -----------------

func TestViewshedScreening(t *testing.T) {
	neighbour := world.CellLocal{Row: 11, Col: 11}

	unscreened := mustAPI(t)
	mu := newFakeMining()
	mu.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
	if err := unscreened.SetMining(mu); err != nil {
		t.Fatal(err)
	}
	if err := unscreened.SetParking(&fakeParking{}); err != nil {
		t.Fatal(err)
	}
	uid := placeMall(t, unscreened, false)

	screened := mustAPI(t)
	ms := newFakeMining()
	ms.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
	if err := screened.SetMining(ms); err != nil {
		t.Fatal(err)
	}
	if err := screened.SetParking(&fakeParking{}); err != nil {
		t.Fatal(err)
	}
	sid := placeMall(t, screened, true)

	seenU, err := unscreened.ViewshedBlightAt(uid, world.TileCoord{X: 2, Y: 2}, neighbour, 2030)
	if err != nil {
		t.Fatalf("ViewshedBlightAt(unscreened): %v", err)
	}
	seenS, err := screened.ViewshedBlightAt(sid, world.TileCoord{X: 2, Y: 2}, neighbour, 2030)
	if err != nil {
		t.Fatalf("ViewshedBlightAt(screened): %v", err)
	}
	if seenS >= seenU {
		t.Fatalf("screened viewshed blight %v not strictly lower than unscreened %v", seenS, seenU)
	}
	// The screening was wired through the mining seam as a wall bund, never
	// applied as a destination-local reduction coefficient.
	if ms.bunds != 1 {
		t.Fatalf("screened placement should add exactly one screening bund, got %d", ms.bunds)
	}
	if mu.bunds != 0 {
		t.Fatalf("unscreened placement should add no screening bund, got %d", mu.bunds)
	}
}

// --- defect fix: forest resort contributes zero viewshed blight (AC-4) ------

func TestResortViewshedBlightIsZero(t *testing.T) {
	d := mustAPI(t)
	m := newFakeMining()
	m.baseSeen = 0.5 // a non-zero aggregate cell blight the seam would report
	if err := d.SetMining(m); err != nil {
		t.Fatal(err)
	}
	id := placeResort(t, d)

	got, err := d.ViewshedBlightAt(id, world.TileCoord{X: 1, Y: 1}, world.CellLocal{Row: 6, Col: 6}, 2030)
	if err != nil {
		t.Fatalf("ViewshedBlightAt(resort): %v", err)
	}
	if got != 0 {
		t.Fatalf("forest resort must contribute zero viewshed blight, got %v (aggregate cell blight leaked through)", got)
	}
}

// --- defect fix: Place is atomic across seams (AC-10) ------------------------

func TestFailedPlacementLeavesNoPartialState(t *testing.T) {
	t.Run("AddBund failure registers nothing and consumes no id", func(t *testing.T) {
		d := mustAPI(t)
		m := newFakeMining()
		m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
		m.addBundErr = errors.New("bund refused")
		p := &fakeParking{}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(p); err != nil {
			t.Fatal(err)
		}

		if _, err := d.Place(mallRequest(true)); err == nil {
			t.Fatal("screened placement with a failing AddBund: want error, got nil")
		}

		if len(m.objects) != 0 {
			t.Fatalf("failed placement left %d mining blight objects registered", len(m.objects))
		}
		if len(p.registered) != 0 {
			t.Fatalf("failed placement left %d parking facilities registered", len(p.registered))
		}
		if got := len(d.DestinationIDs()); got != 0 {
			t.Fatalf("failed placement was recorded: %d destinations", got)
		}

		// The id was not consumed: the next successful placement takes id 1.
		m.addBundErr = nil
		id, err := d.Place(mallRequest(false))
		if err != nil {
			t.Fatalf("recovery placement after failed AddBund: %v", err)
		}
		if id != 1 {
			t.Fatalf("failed placement consumed an id: next id = %d, want 1", id)
		}
	})

	t.Run("RegisterFacility failure registers no blight object", func(t *testing.T) {
		d := mustAPI(t)
		m := newFakeMining()
		m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
		p := &fakeParking{failRegister: errors.New("parking refused")}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(p); err != nil {
			t.Fatal(err)
		}

		if _, err := d.Place(mallRequest(false)); err == nil {
			t.Fatal("placement with a failing RegisterFacility: want error, got nil")
		}

		if len(m.objects) != 0 {
			t.Fatalf("failed placement left %d mining blight objects registered", len(m.objects))
		}
		if got := len(d.DestinationIDs()); got != 0 {
			t.Fatalf("failed placement was recorded: %d destinations", got)
		}
	})

	t.Run("unknown blightClass consumed no id", func(t *testing.T) {
		cfg := testConfig()
		mall := cfg.Archetypes["megaMall"]
		mall.BlightClass = "bogus"
		cfg.Archetypes["megaMall"] = mall

		d, err := New(42, cfg, "test-correlation")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		m := newFakeMining()
		m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(&fakeParking{}); err != nil {
			t.Fatal(err)
		}

		if _, err := d.Place(mallRequest(false)); err == nil {
			t.Fatal("placement with an unrecognised blightClass: want error, got nil")
		} else if got := errCode(t, err); got != ErrInvalidSite {
			t.Fatalf("code = %s, want %s", got, ErrInvalidSite)
		}
		if len(m.objects) != 0 {
			t.Fatalf("failed placement left %d mining blight objects registered", len(m.objects))
		}
		if got := len(d.DestinationIDs()); got != 0 {
			t.Fatalf("failed placement was recorded: %d destinations", got)
		}

		// The id was not burned before blightClass resolution: a subsequent
		// valid placement takes id 1.
		if id := placeResort(t, d); id != 1 {
			t.Fatalf("failed placement consumed an id: next id = %d, want 1", id)
		}
	})
}

// --- AC-5: forest-resort BDI synergy ---------------------------------------

func TestBDISynergy(t *testing.T) {
	d := mustAPI(t)
	if err := d.SetTourism(&fakeTourism{score: 10.0}); err != nil {
		t.Fatal(err)
	}
	id := placeResort(t, d)

	low, err := d.RegionalDraw(id, 0.2)
	if err != nil {
		t.Fatalf("RegionalDraw(low BDI): %v", err)
	}
	high, err := d.RegionalDraw(id, 0.8)
	if err != nil {
		t.Fatalf("RegionalDraw(high BDI): %v", err)
	}
	if high <= low {
		t.Fatalf("raising BDI did not raise the resort draw: low=%v high=%v", low, high)
	}

	// The mall, by contrast, is BDI-insensitive (its draw is retail spend).
	m := newFakeMining()
	m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
	if err := d.SetMining(m); err != nil {
		t.Fatal(err)
	}
	if err := d.SetParking(&fakeParking{}); err != nil {
		t.Fatal(err)
	}
	mallID := placeMall(t, d, false)
	mallLow, err := d.RegionalDraw(mallID, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	mallHigh, err := d.RegionalDraw(mallID, 0.8)
	if err != nil {
		t.Fatal(err)
	}
	if mallLow != mallHigh {
		t.Fatalf("mall draw must be BDI-insensitive: low=%v high=%v", mallLow, mallHigh)
	}
}

// --- AC-9: colossal parking demand through engine.parking ------------------

func TestParkingDemand(t *testing.T) {
	d := mustAPI(t)
	m := newFakeMining()
	m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
	p := &fakeParking{}
	if err := d.SetMining(m); err != nil {
		t.Fatal(err)
	}
	if err := d.SetParking(p); err != nil {
		t.Fatal(err)
	}
	id := placeMall(t, d, false)

	if len(p.registered) != 1 {
		t.Fatalf("expected one parking-facility registration, got %d", len(p.registered))
	}
	if p.registered[0].spaces <= 0 {
		t.Fatalf("parking demand must be non-zero, got %d", p.registered[0].spaces)
	}
	pd, err := d.ParkingDemand(id)
	if err != nil {
		t.Fatalf("ParkingDemand: %v", err)
	}
	if pd != int64(p.registered[0].spaces) {
		t.Fatalf("ParkingDemand(%d) = %d, want the pushed %d", id, pd, p.registered[0].spaces)
	}
}

// --- AC-10: invalid site / unknown destination -----------------------------

func TestInvalidSite(t *testing.T) {
	d := mustAPI(t)

	if _, err := d.Place(PlacementRequest{Kind: ArchetypeForestResort, FootprintHa: 50}); err == nil {
		t.Fatal("under-minimum footprint: want error, got nil")
	} else if got := errCode(t, err); got != ErrInvalidSite {
		t.Fatalf("code = %s, want %s", got, ErrInvalidSite)
	}

	if _, err := d.Place(PlacementRequest{Kind: ArchetypeKind(99), FootprintHa: 200}); err == nil {
		t.Fatal("unrecognised archetype kind: want error, got nil")
	} else if got := errCode(t, err); got != ErrInvalidSite {
		t.Fatalf("code = %s, want %s", got, ErrInvalidSite)
	}

	// Neither rejected command was silently applied as a placement.
	if got := len(d.DestinationIDs()); got != 0 {
		t.Fatalf("rejected placements were recorded: %d destinations", got)
	}
}

func TestUnknownDestination(t *testing.T) {
	d := mustAPI(t)
	if err := d.SetTourism(&fakeTourism{score: 1.0}); err != nil {
		t.Fatal(err)
	}

	if _, err := d.RegionalDraw(999, 0.5); err == nil {
		t.Fatal("RegionalDraw on unknown id: want error, got nil")
	} else if got := errCode(t, err); got != ErrUnknownDestination {
		t.Fatalf("code = %s, want %s", got, ErrUnknownDestination)
	}
	if _, err := d.ViewshedBlightAt(999, world.TileCoord{}, world.CellLocal{}, 2030); err == nil {
		t.Fatal("ViewshedBlightAt on unknown id: want error, got nil")
	} else if got := errCode(t, err); got != ErrUnknownDestination {
		t.Fatalf("code = %s, want %s", got, ErrUnknownDestination)
	}
	if _, err := d.ParkingDemand(999); err == nil {
		t.Fatal("ParkingDemand on unknown id: want error, got nil")
	} else if got := errCode(t, err); got != ErrUnknownDestination {
		t.Fatalf("code = %s, want %s", got, ErrUnknownDestination)
	}
}

// --- AC-11: malformed archetype config -------------------------------------

func TestMalformedArchetypeConfig(t *testing.T) {
	t.Run("missing job count", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, fileDestination), `{
			"version": 1,
			"archetypes": {
				"forestResort": {"name": "resort", "minFootprintHa": 100},
				"megaMall": {"name": "mall", "jobs": 7000, "minFootprintHa": 40, "minShopFloorspace": 300, "parkingSpaces": 8000, "baseDrawFactor": 1, "bdiHalfSaturation": 0.4, "bdiMaxBoost": 0, "blightClass": "high", "noiseRadiusM": 1500, "visualHeightM": 30, "visualMagnitude": 0.9, "screenWallHeightM": 12}
			}
		}`)

		d, err := Load(dir, "corr")
		if err == nil {
			t.Fatal("Load of a job-count-less config: want error, got nil")
		}
		if got := errCode(t, err); got != ErrMalformedConfig {
			t.Fatalf("code = %s, want %s", got, ErrMalformedConfig)
		}
		if d != nil {
			t.Fatalf("Load returned a non-nil DestAPI alongside the error: %v", d)
		}
	})

	t.Run("negative footprint", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, fileDestination), `{
			"version": 1,
			"archetypes": {
				"forestResort": {"name": "resort", "jobs": 1500, "minFootprintHa": -5, "parkingSpaces": 1200, "baseDrawFactor": 1, "bdiHalfSaturation": 0.4, "bdiMaxBoost": 0.6, "blightClass": "none", "noiseRadiusM": 0, "visualHeightM": 0, "visualMagnitude": 0, "screenWallHeightM": 0},
				"megaMall": {"name": "mall", "jobs": 7000, "minFootprintHa": 40, "minShopFloorspace": 300, "parkingSpaces": 8000, "baseDrawFactor": 1, "bdiHalfSaturation": 0.4, "bdiMaxBoost": 0, "blightClass": "high", "noiseRadiusM": 1500, "visualHeightM": 30, "visualMagnitude": 0.9, "screenWallHeightM": 12}
			}
		}`)

		d, err := Load(dir, "corr")
		if err == nil {
			t.Fatal("Load of a negative-footprint config: want error, got nil")
		}
		if got := errCode(t, err); got != ErrMalformedConfig {
			t.Fatalf("code = %s, want %s", got, ErrMalformedConfig)
		}
		if d != nil {
			t.Fatalf("Load returned a non-nil DestAPI alongside the error: %v", d)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- AC-13: determinism ----------------------------------------------------

func TestDeterminism(t *testing.T) {
	run := func() [4]float64 {
		d := mustAPI(t)
		m := newFakeMining()
		m.sites["pit-1"] = mining.ExtractionSite{Key: "pit-1", Exhausted: true}
		if err := d.SetMining(m); err != nil {
			t.Fatal(err)
		}
		if err := d.SetParking(&fakeParking{}); err != nil {
			t.Fatal(err)
		}
		if err := d.SetTourism(&fakeTourism{score: 3.0}); err != nil {
			t.Fatal(err)
		}
		mallID := placeMall(t, d, true)
		resortID := placeResort(t, d)

		drawMall, err := d.RegionalDraw(mallID, 0.5)
		if err != nil {
			t.Fatal(err)
		}
		drawResort, err := d.RegionalDraw(resortID, 0.5)
		if err != nil {
			t.Fatal(err)
		}
		seen, err := d.ViewshedBlightAt(mallID, world.TileCoord{X: 2, Y: 2}, world.CellLocal{Row: 11, Col: 11}, 2030)
		if err != nil {
			t.Fatal(err)
		}
		pd, err := d.ParkingDemand(mallID)
		if err != nil {
			t.Fatal(err)
		}
		return [4]float64{drawMall, drawResort, seen, float64(pd)}
	}

	a := run()
	b := run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("output %d differs across identical runs: %v vs %v", i, a[i], b[i])
		}
	}
}

// --- AC-14: concurrency (run with -race) -----------------------------------

func TestConcurrency(t *testing.T) {
	d := mustAPI(t)
	if err := d.SetTourism(staticTourism{}); err != nil {
		t.Fatal(err)
	}

	const n = 32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := d.Place(PlacementRequest{
				Kind:        ArchetypeForestResort,
				Tile:        world.TileCoord{X: i % 30, Y: 0},
				Local:       world.CellLocal{Row: i, Col: 0},
				FootprintHa: 150,
			})
			if err != nil {
				t.Errorf("Place: %v", err)
				return
			}
			if _, err := d.RegionalDraw(id, 0.5); err != nil {
				t.Errorf("RegionalDraw: %v", err)
			}
			if _, err := d.Archetype(ArchetypeForestResort); err != nil {
				t.Errorf("Archetype: %v", err)
			}
			_ = d.DestinationIDs()
		}()
	}
	wg.Wait()

	if got := len(d.DestinationIDs()); got != n {
		t.Fatalf("placed %d destinations, want %d", got, n)
	}
}

// --- SEC-020: copy guard ---------------------------------------------------

func TestDestAPICopyGuard(t *testing.T) {
	orig := mustAPI(t)
	cp := destAPICopy(orig)

	if _, err := cp.Archetype(ArchetypeForestResort); errCode(t, err) != ErrCopiedValue {
		t.Fatalf("Archetype on a copied value: code = %s, want %s", errCode(t, err), ErrCopiedValue)
	}
	if err := cp.SetTourism(staticTourism{}); errCode(t, err) != ErrCopiedValue {
		t.Fatalf("SetTourism on a copied value: code = %s, want %s", errCode(t, err), ErrCopiedValue)
	}
	if _, err := cp.Place(PlacementRequest{Kind: ArchetypeForestResort, FootprintHa: 150}); errCode(t, err) != ErrCopiedValue {
		t.Fatalf("Place on a copied value: code = %s, want %s", errCode(t, err), ErrCopiedValue)
	}

	// The original is unaffected and still usable.
	if _, err := orig.Archetype(ArchetypeForestResort); err != nil {
		t.Fatalf("original API corrupted by the copy-guard test: %v", err)
	}
}

// destAPICopy takes a byte-for-byte struct copy (go vet's copylocks check
// would flag a literal `cp := *d`), mirroring engine.services'/world's
// servicesCopy/w2Copy convention.
func destAPICopy(d *DestinationAPI) *DestinationAPI {
	c := new(DestAPI)
	*(*[unsafe.Sizeof(DestinationAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(DestinationAPI{})]byte)(unsafe.Pointer(d))
	return c
}

// --- real data smoke test --------------------------------------------------

func TestLoadDefaultRealData(t *testing.T) {
	d, err := LoadDefault("corr")
	if err != nil {
		t.Fatalf("LoadDefault (real data/destination.json): %v", err)
	}
	resort, err := d.Archetype(ArchetypeForestResort)
	if err != nil {
		t.Fatal(err)
	}
	mall, err := d.Archetype(ArchetypeMegaMall)
	if err != nil {
		t.Fatal(err)
	}
	if resort.Jobs <= 0 || mall.Jobs <= 0 {
		t.Fatalf("real data must carry positive job counts: resort=%d mall=%d", resort.Jobs, mall.Jobs)
	}
	if resort.MinFootprintHa < 100 {
		t.Fatalf("real resort min footprint %v below 100+ ha", resort.MinFootprintHa)
	}
	if mall.MinShopFloorspace < 300 {
		t.Fatalf("real mall min floorspace %d below 300+ shops", mall.MinShopFloorspace)
	}
}
