package mining

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This test file is the full FEAT-049 regression suite. Test names are
// chosen to match the acceptance doc's own grep patterns (AC-2..AC-12).

func cid() string { return errs.NewCorrelationID() }

// realParams loads the committed data/deposits.json, proving the shipped
// file is well-formed and giving every test the real, data-sourced
// parameters (GR#15 — tests never hardcode balance numbers).
func realParams(t *testing.T) DepositParams {
	t.Helper()
	p, err := LoadDepositParams(realDepositDataPath(t), cid())
	if err != nil {
		t.Fatalf("load real data/deposits.json: %v", err)
	}
	return p
}

// realDepositDataPath walks upward from the test cwd to the repo root's
// data/deposits.json (the same resolution idea foundation/data uses, but
// self-contained so this package imports no unregistered edge).
func realDepositDataPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "data", "deposits.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("data/deposits.json not found walking upward from %s", dir)
		}
		dir = parent
	}
}

// writeMutatedParams loads the real data file, lets mutate edit its
// decoded JSON shape, and writes the result to a temp file whose path it
// returns. Used to prove a specific parameter is actually read (AC-6).
func writeMutatedParams(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	b, err := os.ReadFile(realDepositDataPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "deposits.json")
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// geologyFixtures buckets the expansion extent's tiles by real geology,
// discovered through WorldAPI itself (never by reimplementing world's
// geology derivation — that would be a mining-local geology copy, exactly
// what AC-10 forbids).
type geologyFixtures struct {
	chalkTiles  []world.TileCoord // pocket GeologyNone (pure chalk)
	clayTiles   []world.TileCoord
	gravelTiles []world.TileCoord
	coalTiles   []world.TileCoord // pocket GeologyDeepCoal
	seaTiles    []world.TileCoord // TileAt.OnLand == false
	landTiles   []world.TileCoord // TileAt.OnLand == true
}

func discover(w *world.WorldAPI) (geologyFixtures, error) {
	var f geologyFixtures
	// Enough of each kind for every statistical test below, discovered in
	// deterministic (y-major, x-minor) order. Early-terminating keeps the
	// full-extent synthetic-terrain generation (36M cells) out of the path —
	// each TileAt lazily generates a 40000-cell tile, so probing the whole
	// 30x30 grid would dominate the runtime.
	const (
		needChalk  = 8
		needGravel = 8
		needCoal   = 8
		needClay   = 4
		needSea    = 4
		needLand   = 4
	)
	done := func() bool {
		return len(f.chalkTiles) >= needChalk &&
			len(f.gravelTiles) >= needGravel &&
			len(f.coalTiles) >= needCoal &&
			len(f.clayTiles) >= needClay &&
			len(f.seaTiles) >= needSea &&
			len(f.landTiles) >= needLand
	}
	for y := 0; y < world.TilesPerSide; y++ {
		for x := 0; x < world.TilesPerSide; x++ {
			c := world.TileCoord{X: x, Y: y}
			if err := w.Prospect(c, cid()); err != nil {
				return geologyFixtures{}, err
			}
			pocket, err := w.PocketGeology(c, cid())
			if err != nil {
				return geologyFixtures{}, err
			}
			info, err := w.TileAt(c, cid())
			if err != nil {
				return geologyFixtures{}, err
			}
			if !info.OnLand {
				if len(f.seaTiles) < needSea {
					f.seaTiles = append(f.seaTiles, c)
				}
			} else {
				if len(f.landTiles) < needLand {
					f.landTiles = append(f.landTiles, c)
				}
				switch pocket {
				case world.GeologyNone:
					if len(f.chalkTiles) < needChalk {
						f.chalkTiles = append(f.chalkTiles, c)
					}
				case world.GeologyClay:
					if len(f.clayTiles) < needClay {
						f.clayTiles = append(f.clayTiles, c)
					}
				case world.GeologyGravel:
					if len(f.gravelTiles) < needGravel {
						f.gravelTiles = append(f.gravelTiles, c)
					}
				case world.GeologyDeepCoal:
					if len(f.coalTiles) < needCoal {
						f.coalTiles = append(f.coalTiles, c)
					}
				}
			}
			if done() {
				return f, nil
			}
		}
	}
	if len(f.coalTiles) == 0 || len(f.chalkTiles) == 0 || len(f.gravelTiles) == 0 || len(f.seaTiles) == 0 {
		return geologyFixtures{}, errors.New("discovery found an empty geology bucket — expected coal/chalk/gravel/sea tiles to all be non-empty")
	}
	return f, nil
}

// fixtureOnce caches the discovered geology fixture COORDINATES (pure
// TileCoord values, not world state) so the expensive full-extent terrain
// generation runs once per test binary, not once per test. Each test then
// prospects only the handful of tiles it actually shuffles on its own
// fresh world — the coordinates are valid everywhere because world's
// geology is a pure function of TileCoord.
var (
	fixtureOnce   sync.Once
	fixtureCoords geologyFixtures
	fixtureErr    error
)

func fixtures(t *testing.T) geologyFixtures {
	t.Helper()
	fixtureOnce.Do(func() {
		fixtureCoords, fixtureErr = discover(world.NewWorldAPI(world.TileCoord{X: 15, Y: 15}))
	})
	if fixtureErr != nil {
		t.Fatalf("fixture discovery: %v", fixtureErr)
	}
	return fixtureCoords
}

// prospectTiles prospects the named tiles on a fresh world (geology
// derivation, per AC-10) so the shuffle can read their pockets.
func prospectTiles(t *testing.T, w *world.WorldAPI, cs ...world.TileCoord) {
	t.Helper()
	for _, c := range cs {
		if err := w.Prospect(c, cid()); err != nil {
			t.Fatalf("Prospect(%v): %v", c, err)
		}
	}
}

// newWorld returns a fresh WorldAPI over synthetic (placeholder) terrain.
func newWorld(t *testing.T) *world.WorldAPI {
	t.Helper()
	return world.NewWorldAPI(world.TileCoord{X: 15, Y: 15})
}

// capTiles trims a tile slice to at most n entries (kept in discovery
// order, which is deterministic).
func capTiles(ts []world.TileCoord, n int) []world.TileCoord {
	if len(ts) <= n {
		return ts
	}
	return ts[:n]
}

func countByType(ds []LocatedDeposit) map[DepositType]int {
	c := make(map[DepositType]int)
	for _, d := range ds {
		c[d.Deposit.Type]++
	}
	return c
}

func assertErrCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected registry-sourced *errs.E, got %T", err)
	}
	if e.Code != want {
		t.Fatalf("expected error code %s, got %s", want, e.Code)
	}
}

// --- AC-2: taxonomy + data-sourced depth bands ----------------------------

func TestDepositTypeTaxonomyDepthBands(t *testing.T) {
	p := realParams(t)

	names := map[DepositType]bool{}
	for dt := DepositCopper; dt <= DepositArcana; dt++ {
		if dt.String() == "unknown" {
			t.Fatalf("DepositType %d has no canonical name", dt)
		}
		names[dt] = true

		// Every enum value resolves to a data-sourced depth band — the
		// band comes from the loaded params (params.go), NOT a literal in
		// this package's enum file (AC-2/AC-6).
		min, max, ok := p.DepthBand(dt)
		if !ok {
			t.Fatalf("DepositType %s has no depth band in the loaded data", dt)
		}
		if min < 0 || max <= min {
			t.Fatalf("DepositType %s has inverted/non-realistic band [%v,%v)", dt, min, max)
		}
	}
	// All nine real + fictional slots present, distinct.
	want := []DepositType{
		DepositCopper, DepositTin, DepositIron, DepositUranium, DepositREM,
		DepositGas, DepositOil, DepositCoal, DepositArcana,
	}
	for _, dt := range want {
		if !names[dt] {
			t.Fatalf("taxonomy missing %s", dt)
		}
	}
	if len(names) != len(want) {
		t.Fatalf("taxonomy has %d entries, want %d", len(names), len(want))
	}
}

// --- AC-3: offshore fields on sea cells, never metallic ores ---------------

func TestOffshoreDepositsOnSeaCells(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	seaTiles := capTiles(fx.seaTiles, 1)
	prospectTiles(t, w, seaTiles...)
	seeds := []uint64{7, 99}

	offshoreCount := 0
	metalOnSea := 0
	for _, seed := range seeds {
		m := NewDepositMap(seed, w, p)
		for _, c := range seaTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatalf("ShuffleTile(sea %v): %v", c, err)
			}
			for _, d := range m.TileDeposits(c) {
				offshoreCount++
				if d.Deposit.Type.IsMetal() {
					metalOnSea++
				}
			}
		}
	}

	if offshoreCount == 0 {
		t.Fatal("no offshore deposit was placed on any sea cell across the seed sample")
	}
	if metalOnSea != 0 {
		t.Fatalf("placed %d metallic-ore deposits on sea cells — ores must never sit offshore", metalOnSea)
	}
}

// --- AC-4: size and density are independent first-class fields --------------

func TestSizeDensityIndependent(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	m := NewDepositMap(5, w, p)
	tile := fx.landTiles[0]
	prospectTiles(t, w, tile)
	if err := m.ShuffleTile(tile, cid()); err != nil {
		t.Fatal(err)
	}
	ds := m.TileDeposits(tile)
	if len(ds) < 50 {
		t.Fatalf("only %d deposits placed — too few to test size/density independence", len(ds))
	}

	sizes := make([]float64, len(ds))
	densities := make([]float64, len(ds))
	for i, d := range ds {
		sizes[i] = d.Deposit.Size
		densities[i] = d.Deposit.Density
	}
	// Also prove the fields actually vary (they are not constant), and
	// that depth stays inside its type's band.
	if maxF(sizes) == minF(sizes) {
		t.Fatal("size is constant across deposits — the size curve is not being sampled")
	}
	if maxF(densities) == minF(densities) {
		t.Fatal("density is constant across deposits — the density curve is not being sampled")
	}
	for _, d := range ds {
		min, max, ok := p.DepthBand(d.Deposit.Type)
		if !ok {
			t.Fatalf("no band for %s", d.Deposit.Type)
		}
		if d.Deposit.Depth < min || d.Deposit.Depth >= max {
			t.Fatalf("%s deposit depth %v outside band [%v,%v)", d.Deposit.Type, d.Deposit.Depth, min, max)
		}
	}

	r := pearson(sizes, densities)
	if math.IsNaN(r) || r >= 0.5 {
		t.Fatalf("size and density are correlated (r=%v) — they must be independently sampled curves", r)
	}
}

// --- AC-5: geology-aware co-location ---------------------------------------

func TestGeologyCoLocation(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	chalkTiles := capTiles(fx.chalkTiles, 2)
	nonChalkTiles := capTiles(fx.gravelTiles, 2)
	coalTiles := capTiles(fx.coalTiles, 2)
	nonCoalTiles := capTiles(fx.gravelTiles, 2)
	prospectTiles(t, w, chalkTiles...)
	prospectTiles(t, w, nonChalkTiles...)
	prospectTiles(t, w, coalTiles...)

	seeds := []uint64{1, 42}
	var chalkUranium, nonChalkUranium, coalGas, nonCoalGas int
	for _, seed := range seeds {
		m := NewDepositMap(seed, w, p)
		for _, c := range chalkTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatal(err)
			}
			chalkUranium += countByType(m.TileDeposits(c))[DepositUranium]
		}
		for _, c := range nonChalkTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatal(err)
			}
			nonChalkUranium += countByType(m.TileDeposits(c))[DepositUranium]
		}
		for _, c := range coalTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatal(err)
			}
			coalGas += countByType(m.TileDeposits(c))[DepositGas]
		}
		for _, c := range nonCoalTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatal(err)
			}
			nonCoalGas += countByType(m.TileDeposits(c))[DepositGas]
		}
	}

	chalkU := float64(chalkUranium) / float64(len(chalkTiles)*len(seeds))
	nonChalkU := float64(nonChalkUranium) / float64(len(nonChalkTiles)*len(seeds))
	coalG := float64(coalGas) / float64(len(coalTiles)*len(seeds))
	nonCoalG := float64(nonCoalGas) / float64(len(nonCoalTiles)*len(seeds))

	// (a) uranium density in chalk far lower than in non-chalk.
	if nonChalkU == 0 {
		t.Fatal("no uranium placed in non-chalk tiles at all — the sample cannot establish the correlation")
	}
	if chalkU >= nonChalkU*0.25 {
		t.Errorf("uranium in chalk tiles (%0.2f/tile) not far below non-chalk (%0.2f/tile) — expected a strong chalk/uranium exclusion",
			chalkU, nonChalkU)
	}

	// (b) gas co-location: elevated in coal-measures tiles.
	if coalG <= nonCoalG*2.0 {
		t.Errorf("gas in coal-measures tiles (%0.2f/tile) not elevated vs non-coal (%0.2f/tile) — expected coal-measures-implies-gas correlation",
			coalG, nonCoalG)
	}
}

// --- AC-6: every tunable number is data-sourced ----------------------------

func TestDataDrivenGenerosity(t *testing.T) {
	// Two param sets differing ONLY in the coalfield generosity multiplier,
	// same seed, same coal tile — the shuffle's output must change, proving
	// the multiplier is actually read, not merely present in a file nobody
	// loads (AC-6).
	lowPath := writeMutatedParams(t, func(m map[string]any) {
		m["eastKentCoalfield"].(map[string]any)["generosityMultiplier"] = 2.0
	})
	highPath := writeMutatedParams(t, func(m map[string]any) {
		m["eastKentCoalfield"].(map[string]any)["generosityMultiplier"] = 100.0
	})

	low, err := LoadDepositParams(lowPath, cid())
	if err != nil {
		t.Fatal(err)
	}
	high, err := LoadDepositParams(highPath, cid())
	if err != nil {
		t.Fatal(err)
	}

	w := newWorld(t)
	fx := fixtures(t)
	coalTile := fx.coalTiles[0]
	prospectTiles(t, w, coalTile)

	const seed = 12345
	ml := NewDepositMap(seed, w, low)
	if err := ml.ShuffleTile(coalTile, cid()); err != nil {
		t.Fatal(err)
	}
	mh := NewDepositMap(seed, w, high)
	if err := mh.ShuffleTile(coalTile, cid()); err != nil {
		t.Fatal(err)
	}

	coalLow := countByType(ml.TileDeposits(coalTile))[DepositCoal]
	coalHigh := countByType(mh.TileDeposits(coalTile))[DepositCoal]
	if coalHigh <= coalLow {
		t.Errorf("raising generosityMultiplier 2.0 -> 100.0 did not increase coal deposits (low=%d high=%d) — the parameter is not being read",
			coalLow, coalHigh)
	}
}

// --- AC-7: coalfield generosity is a checkable floor ------------------------

func TestCoalfieldGenerosityNotStingy(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	coalTiles := capTiles(fx.coalTiles, 4)
	prospectTiles(t, w, coalTiles...)
	seeds := []uint64{11, 22}

	floor := p.EastKentCoalfield.CoverageFloor
	for _, seed := range seeds {
		m := NewDepositMap(seed, w, p)
		withCoal := 0
		for _, c := range coalTiles {
			if err := m.ShuffleTile(c, cid()); err != nil {
				t.Fatal(err)
			}
			if countByType(m.TileDeposits(c))[DepositCoal] > 0 {
				withCoal++
			}
		}
		coverage := float64(withCoal) / float64(len(coalTiles))
		if coverage < floor {
			t.Errorf("seed %d: coal coverage %0.2f below data-file floor %0.2f — the East Kent coalfield is being stingy",
				seed, coverage, floor)
		}
	}
}

// --- AC-8: determinism ------------------------------------------------------

func TestDeterministicDepositShuffle(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	tiles := append(append([]world.TileCoord{},
		fx.coalTiles[:1]...), fx.seaTiles[:1]...)
	prospectTiles(t, w, tiles...)
	const seed = 777777

	first := NewDepositMap(seed, w, p)
	for _, c := range tiles {
		if err := first.ShuffleTile(c, cid()); err != nil {
			t.Fatal(err)
		}
	}

	second := NewDepositMap(seed, w, p)
	for _, c := range tiles {
		if err := second.ShuffleTile(c, cid()); err != nil {
			t.Fatal(err)
		}
	}

	a, b := first.AllDeposits(), second.AllDeposits()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed produced different deposit records: %d vs %d deposits (determinism violated)",
			len(a), len(b))
	}
	if len(a) == 0 {
		t.Fatal("shuffle placed zero deposits — determinism test is vacuous")
	}
}

func TestDifferentSeedDifferentDeposits(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)
	tile := fx.landTiles[0]
	prospectTiles(t, w, tile)

	m1 := NewDepositMap(1, w, p)
	if err := m1.ShuffleTile(tile, cid()); err != nil {
		t.Fatal(err)
	}
	m2 := NewDepositMap(2, w, p)
	if err := m2.ShuffleTile(tile, cid()); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(m1.AllDeposits(), m2.AllDeposits()) {
		t.Fatal("different seeds produced an identical deposit set — the seed is not being consumed")
	}
}

// --- AC-9: deposits exist in unowned tiles, and purchase does not alter them

func TestUnownedTileDepositBeforePurchase(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	unowned := fx.landTiles[0]
	owned := fx.landTiles[1]
	prospectTiles(t, w, unowned, owned)

	// Shuffle both before either is purchased. The unowned tile must get
	// deposits identical in kind/shape to a purchased one (it is never
	// contingent on ownership — the shuffle reads only geology/surface).
	seed := uint64(314159)
	m := NewDepositMap(seed, w, p)
	for _, c := range []world.TileCoord{unowned, owned} {
		if err := m.ShuffleTile(c, cid()); err != nil {
			t.Fatal(err)
		}
	}
	unownedBefore := m.TileDeposits(unowned)
	if len(unownedBefore) == 0 {
		t.Fatal("no deposits placed under a never-purchased tile — deposits must exist in unowned tiles (a reason to buy them)")
	}

	// Purchasing afterward must not alter the already-placed record
	// (purchase reveals, it does not create).
	if res := w.PurchaseTile(world.PurchaseCommand{CorrelationID: cid(), Tile: unowned, BuyerID: 1}); !res.Accepted {
		t.Fatalf("PurchaseTile rejected: %v", res.Error)
	}
	unownedAfter := m.TileDeposits(unowned)
	if !reflect.DeepEqual(unownedBefore, unownedAfter) {
		t.Fatal("purchasing the tile changed the already-placed deposit records")
	}
}

// --- AC-11: malformed/missing data file ------------------------------------

func TestMissingDepositData(t *testing.T) {
	_, err := LoadDepositParams(filepath.Join(t.TempDir(), "nope.json"), cid())
	assertErrCode(t, err, ErrDepositDataInvalid)
}

func TestMalformedDepositData(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"syntax-error", func(m map[string]any) { /* handled below via raw write */ }},
		{"negative-count", func(m map[string]any) {
			m["resources"].(map[string]any)["coal"].(map[string]any)["countWeight"] = -1.0
		}},
		{"inverted-depth-band", func(m map[string]any) {
			m["resources"].(map[string]any)["coal"].(map[string]any)["depthMin"] = 2000.0
		}},
		{"out-of-taxonomy-key", func(m map[string]any) {
			m["resources"].(map[string]any)["vibranium"] = map[string]any{
				"class": "ore", "countWeight": 1, "depthMin": 0, "depthMax": 100,
			}
		}},
		{"negative-coLocation", func(m map[string]any) {
			m["coLocation"].(map[string]any)["coalGasFactor"] = -1.0
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.name == "syntax-error" {
				path = filepath.Join(t.TempDir(), "bad.json")
				if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				path = writeMutatedParams(t, tc.mutate)
			}
			// The failed load must return the registry code AND yield no
			// usable (partial) params — all-or-nothing, never a partial map
			// silently continuing (AC-11).
			p, err := LoadDepositParams(path, cid())
			assertErrCode(t, err, ErrDepositDataInvalid)
			if len(p.Resources) != 0 {
				t.Fatalf("failed load returned %d resource entries — expected an all-or-nothing (zero) result", len(p.Resources))
			}
		})
	}
}

// --- Finding 2: metal marked offshore is rejected at load time (AC-3 as a
// schema invariant, not only a runtime chooseType filter) -------------------

func TestMetalOffshoreDataRejected(t *testing.T) {
	// A data file marking a metallic ore offshore-capable would load clean
	// pre-fix and place ore on sea cells (the AC-3 "ores never offshore"
	// invariant existed only in chooseType's sea filtering). The loader must
	// reject it fail-closed, all-or-nothing (GR#15: the data file is the
	// no-rebuild surface, so the validator must catch it).
	path := writeMutatedParams(t, func(m map[string]any) {
		m["resources"].(map[string]any)["copper"].(map[string]any)["offshore"] = true
	})
	p, err := LoadDepositParams(path, cid())
	assertErrCode(t, err, ErrDepositDataInvalid)
	if len(p.Resources) != 0 {
		t.Fatalf("metal-offshore load returned %d resources — expected all-or-nothing (zero)", len(p.Resources))
	}
}

// --- Finding 3: overflow magnitudes are rejected at load time ---------------

func TestDepositDataOverflowRejected(t *testing.T) {
	// countWeight and curve min/max have no upper bound pre-fix, so ~1e308
	// (a valid float64) survives validation and overflows to +Inf/NaN once
	// shuffle arithmetic multiplies/subtracts it. The loader must reject a
	// magnitude above 1e12 fail-closed rather than silently degenerate.
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"count-weight-overflow", func(m map[string]any) {
			m["resources"].(map[string]any)["coal"].(map[string]any)["countWeight"] = 1e308
		}},
		{"size-curve-max-overflow", func(m map[string]any) {
			m["sizeCurve"].(map[string]any)["max"] = 1e308
		}},
		{"size-curve-min-overflow", func(m map[string]any) {
			m["sizeCurve"].(map[string]any)["min"] = 1e13
			m["sizeCurve"].(map[string]any)["max"] = 2e13
		}},
		{"density-curve-max-overflow", func(m map[string]any) {
			m["densityCurve"].(map[string]any)["max"] = 1e13
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedParams(t, tc.mutate)
			p, err := LoadDepositParams(path, cid())
			assertErrCode(t, err, ErrDepositDataInvalid)
			if len(p.Resources) != 0 {
				t.Fatalf("overflow load returned %d resources — expected all-or-nothing (zero)", len(p.Resources))
			}
		})
	}
}

// --- Finding 3 (class): co-location factors and coalfield generosity get an
// upper bound, not only a lower one -----------------------------------------

func TestCoLocationGenerosityOverflowRejected(t *testing.T) {
	// chalkUraniumFactor, coalGasFactor, coalCoalFactor and
	// generosityMultiplier each feed chooseType's `w := countWeight *
	// geologyFactor(...)`. Pre-fix they carried lower-bound-only checks, so a
	// hostile 1e308 (a valid float64) survived validation and overflowed to
	// +Inf when multiplied — the weight total then went +Inf/NaN and every
	// coal-measures/chalk draw collapsed to the last candidate (arcana).
	// Each must be rejected above 1e12 (maxDataMagnitude), all-or-nothing.
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"chalk-uranium-factor-1e13", func(m map[string]any) {
			m["coLocation"].(map[string]any)["chalkUraniumFactor"] = 1e13
		}},
		{"chalk-uranium-factor-1e308", func(m map[string]any) {
			m["coLocation"].(map[string]any)["chalkUraniumFactor"] = 1e308
		}},
		{"coal-gas-factor-1e13", func(m map[string]any) {
			m["coLocation"].(map[string]any)["coalGasFactor"] = 1e13
		}},
		{"coal-gas-factor-1e308", func(m map[string]any) {
			m["coLocation"].(map[string]any)["coalGasFactor"] = 1e308
		}},
		{"coal-coal-factor-1e13", func(m map[string]any) {
			m["coLocation"].(map[string]any)["coalCoalFactor"] = 1e13
		}},
		{"coal-coal-factor-1e308", func(m map[string]any) {
			m["coLocation"].(map[string]any)["coalCoalFactor"] = 1e308
		}},
		{"generosity-multiplier-1e13", func(m map[string]any) {
			m["eastKentCoalfield"].(map[string]any)["generosityMultiplier"] = 1e13
		}},
		{"generosity-multiplier-1e308", func(m map[string]any) {
			m["eastKentCoalfield"].(map[string]any)["generosityMultiplier"] = 1e308
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMutatedParams(t, tc.mutate)
			p, err := LoadDepositParams(path, cid())
			assertErrCode(t, err, ErrDepositDataInvalid)
			if len(p.Resources) != 0 {
				t.Fatalf("co-location/generosity overflow load returned %d resources — expected all-or-nothing (zero)", len(p.Resources))
			}
		})
	}
}

// --- AC-12: geology-not-yet-derived ----------------------------------------

func TestGeologyNotDerived(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)

	// A tile that has never been prospected: its pocket geology is not
	// derived (revealed), so the shuffle must refuse rather than place
	// deposits against zero-value geology.
	tile := world.TileCoord{X: 15, Y: 15}
	m := NewDepositMap(1, w, p)
	err := m.ShuffleTile(tile, cid())
	assertErrCode(t, err, ErrGeologyNotDerived)

	// Distinct from the data-file error, and zero deposits were placed.
	if got := m.TileDeposits(tile); len(got) != 0 {
		t.Fatalf("geology-not-derived shuffle placed %d deposits — expected zero", len(got))
	}
}

// --- AC-15: concurrent queries race-free -----------------------------------

func TestDepositConcurrentQueriesNoRace(t *testing.T) {
	p := realParams(t)
	w := newWorld(t)
	fx := fixtures(t)

	m := NewDepositMap(1, w, p)
	tile := fx.coalTiles[0]
	prospectTiles(t, w, tile)
	if err := m.ShuffleTile(tile, cid()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < world.TileSizeCells; r += 7 {
				for c := 0; c < world.TileSizeCells; c += 5 {
					_, _, _ = m.DepositAt(tile, world.CellLocal{Row: r, Col: c})
				}
			}
		}()
	}
	wg.Wait()
}

// --- helpers ----------------------------------------------------------------

func pearson(xs, ys []float64) float64 {
	n := len(xs)
	var sx, sy, sxx, syy, sxy float64
	for i := 0; i < n; i++ {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		syy += ys[i] * ys[i]
		sxy += xs[i] * ys[i]
	}
	num := float64(n)*sxy - sx*sy
	den := math.Sqrt((float64(n)*sxx - sx*sx) * (float64(n)*syy - sy*sy))
	if den == 0 {
		return 1 // degenerate: constant column, treated as perfectly correlated
	}
	return num / den
}

func minF(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func maxF(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}
