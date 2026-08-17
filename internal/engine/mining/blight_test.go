package mining

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// This test file is the engine.mining (MOD-046) general-blight-model
// regression suite: the elevation-based viewshed (AC-4), the distance-based
// noise (AC-5), stacking (AC-6), the real-earthwork mitigations (AC-7/AC-8),
// determinism (AC-12) and the concurrency hammer (AC-14). Test names match the
// acceptance doc's own grep patterns.

// testStartTile is the single imported ("real") start tile these fixtures
// build their synthetic ridge/flat heightmaps into.
var testStartTile = world.TileCoord{X: 15, Y: 15}

// realMiningPath walks upward to the repo root's data/mining.json (the same
// resolution idea as realDepositDataPath).
func realMiningPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "data", "mining.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("data/mining.json not found walking upward from %s", dir)
		}
		dir = parent
	}
}

// blightAPI builds a BlightAPI over w with the real data/mining.json config
// and the real data/minetypes.json catalogue wired (GR#15 — tests never
// hardcode balance numbers).
func blightAPI(t *testing.T, w *world.WorldAPI) *BlightAPI {
	t.Helper()
	cfg, err := LoadBlightConfig(realMiningPath(t), cid())
	if err != nil {
		t.Fatalf("load real data/mining.json: %v", err)
	}
	b, err := NewBlightAPI(w, cfg, cid())
	if err != nil {
		t.Fatalf("NewBlightAPI: %v", err)
	}
	if err := b.SetCatalogue(realCatalogue(t)); err != nil {
		t.Fatalf("SetCatalogue: %v", err)
	}
	return b
}

// importWorld builds a fresh WorldAPI and imports a synthetic 200x200
// heightmap (the same SourceGrid shape the world importer already accepts)
// whose elevation is elevFn(row, col), so the viewshed tests can place a real
// ridge in the one genuinely-imported start tile.
func importWorld(t *testing.T, elevFn func(row, col int) float64) *world.WorldAPI {
	t.Helper()
	const n = world.TileSizeCells
	elevs := make([]float64, n*n)
	for row := 0; row < n; row++ {
		for col := 0; col < n; col++ {
			elevs[row*n+col] = elevFn(row, col)
		}
	}
	src := &world.SourceGrid{
		Header:     world.AsciiGridHeader{NCols: n, NRows: n, XllCorner: 0, YllCorner: 0, CellSize: 50, NoDataValue: -9999},
		Elevations: elevs,
	}
	w := world.NewWorldAPI(testStartTile)
	if err := w.ImportAndPlaceStartTile(src, cid()); err != nil {
		t.Fatalf("ImportAndPlaceStartTile: %v", err)
	}
	return w
}

// flatWorld is a 200x200 flat (elevation 0) imported world.
func flatWorld(t *testing.T) *world.WorldAPI {
	return importWorld(t, func(row, col int) float64 { return 0 })
}

// ridgeElev returns an elevation function: flat 0 everywhere except a
// three-column north-south wall at columns 109..111 of height h. Varying only
// with column means compressV's north-south warp leaves it in place.
func ridgeElev(h float64) func(row, col int) float64 {
	return func(row, col int) float64 {
		if col >= 109 && col <= 111 {
			return h
		}
		return 0
	}
}

// ridgeAPI builds a BlightAPI over a ridge heightmap, with one blighting
// object at cell (100,100) of visual height 40 / magnitude 1.0 / noise radius
// 150, so home cell A (120,100) sits behind the ridge and home cell B (80,100)
// sits on flat ground at the identical 20-cell (200m) distance.
func ridgeAPI(t *testing.T, ridgeHeight float64) *BlightAPI {
	t.Helper()
	b := blightAPI(t, importWorld(t, ridgeElev(ridgeHeight)))
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key:             "obj",
		Class:           BlightSevere,
		Tile:            testStartTile,
		Local:           world.CellLocal{Row: 100, Col: 100},
		NoiseRadiusM:    150,
		VisualHeightM:   40,
		VisualMagnitude: 1.0,
	}); err != nil {
		t.Fatalf("PlaceBlightingObject: %v", err)
	}
	return b
}

// effectAt is the single query path every viewshed/noise test drives (AC-7's
// "identical viewshed function" requirement — there is no mitigation helper to
// call instead).
func effectAt(t *testing.T, b *BlightAPI, local world.CellLocal, year int64) BlightEffect {
	t.Helper()
	eff, err := b.EffectAt(testStartTile, local, year, cid())
	if err != nil {
		t.Fatalf("EffectAt(%v): %v", local, err)
	}
	return eff
}

// --- AC-4: the elevation-based viewshed — the equidistant ridge fixture ----

func TestViewshedRidgeOcclusion(t *testing.T) {
	b := ridgeAPI(t, 60)
	seenA := effectAt(t, b, world.CellLocal{Row: 100, Col: 120}, 0).Seen // behind the ridge
	seenB := effectAt(t, b, world.CellLocal{Row: 100, Col: 80}, 0).Seen  // flat, equidistant

	// A and B are equidistant from the source, so a radius-only model gives
	// them the same non-zero answer; a real-elevation viewshed must give A a
	// materially smaller seen hit (down to zero, as here).
	if seenB <= 0 {
		t.Fatalf("flat home cell B seen = %v, want > 0 (the fixture must start unobstructed)", seenB)
	}
	if seenA >= seenB/2 {
		t.Fatalf("occluded home cell A seen = %v, flat equidistant B seen = %v — a ridge must materially reduce the seen hit (real elevation, not radius)", seenA, seenB)
	}
}

func TestViewshedRidgeElevationMonotonic(t *testing.T) {
	// The second AC-4 assertion: raising the ridge (distance unchanged) must
	// strictly reduce A's seen effect further, lowering it must strictly
	// increase it — the result tracks elevation continuously, not a binary
	// "is there a ridge" flag.
	homeA := world.CellLocal{Row: 100, Col: 120}
	low := effectAt(t, ridgeAPI(t, 25), homeA, 0).Seen
	mid := effectAt(t, ridgeAPI(t, 30), homeA, 0).Seen
	high := effectAt(t, ridgeAPI(t, 35), homeA, 0).Seen
	if !(low > mid && mid > high) {
		t.Fatalf("seen effect must fall continuously as the ridge rises (low=%v mid=%v high=%v), got a non-monotonic or binary result", low, mid, high)
	}
}

// --- AC-5: the distance-only noise component (mechanically distinct) -------

func TestNoiseDistanceFalloff(t *testing.T) {
	b := ridgeAPI(t, 60)
	// A (behind ridge) and B (flat) are equidistant: the heard component must
	// be equal — noise does not care about line-of-sight, only distance.
	heardA := effectAt(t, b, world.CellLocal{Row: 100, Col: 120}, 0).Heard
	heardB := effectAt(t, b, world.CellLocal{Row: 100, Col: 80}, 0).Heard
	if heardA != heardB {
		t.Fatalf("equidistant home cells heard differently (A=%v B=%v) — noise must be distance-only, not elevation-gated", heardA, heardB)
	}
	// And the same fixture's SEEN component differs sharply at that distance.
	seenA := effectAt(t, b, world.CellLocal{Row: 100, Col: 120}, 0).Seen
	seenB := effectAt(t, b, world.CellLocal{Row: 100, Col: 80}, 0).Seen
	if seenA >= seenB {
		t.Fatalf("seen must differ sharply across the ridge (A=%v B=%v) while heard stays equal", seenA, seenB)
	}

	// Heard is monotonically non-increasing with distance (radius 150: 100m is
	// inside the contour, 200m/300m outside it).
	h100 := effectAt(t, b, world.CellLocal{Row: 100, Col: 110}, 0).Heard
	h200 := effectAt(t, b, world.CellLocal{Row: 100, Col: 120}, 0).Heard
	h300 := effectAt(t, b, world.CellLocal{Row: 100, Col: 130}, 0).Heard
	if !(h100 >= h200 && h200 >= h300) {
		t.Fatalf("heard must be monotonically non-increasing with distance (100m=%v 200m=%v 300m=%v)", h100, h200, h300)
	}
	if h100 <= h300 {
		t.Fatalf("heard at 100m (%v) not greater than at 300m (%v) — the dBA-falloff curve is not actually falling", h100, h300)
	}
}

// --- AC-6: heard and seen both stack ---------------------------------------

func TestBlightStacking(t *testing.T) {
	b := blightAPI(t, flatWorld(t))
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key:             "obj",
		Class:           BlightSevere,
		Tile:            testStartTile,
		Local:           world.CellLocal{Row: 100, Col: 100},
		NoiseRadiusM:    150,
		VisualHeightM:   40,
		VisualMagnitude: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	// 100m out: within the noise radius AND on flat, unobstructed ground, so
	// both components are positive.
	eff := effectAt(t, b, world.CellLocal{Row: 100, Col: 110}, 0)
	if eff.Heard <= 0 || eff.Seen <= 0 {
		t.Fatalf("fixture must produce both components (heard=%v seen=%v), got a zero", eff.Heard, eff.Seen)
	}
	if eff.Combined() <= eff.Heard || eff.Combined() <= eff.Seen {
		t.Fatalf("combined %v must exceed each individual component (heard=%v seen=%v) — both stack, never max()", eff.Combined(), eff.Heard, eff.Seen)
	}
}

// --- AC-7: bund and tree-belt mitigation through the SAME viewshed path ----

func TestBundReducesViewshed(t *testing.T) {
	b := blightAPI(t, flatWorld(t))
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "obj", Class: BlightSevere, Tile: testStartTile,
		Local: world.CellLocal{Row: 100, Col: 100}, NoiseRadiusM: 150, VisualHeightM: 40, VisualMagnitude: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	homeB := world.CellLocal{Row: 100, Col: 80}
	before := effectAt(t, b, homeB, 0).Seen
	if before <= 0 {
		t.Fatal("flat home cell must start unobstructed for the bund test")
	}
	// A real earthwork between object and home, read by the same losOcclusion
	// path the ridge exercises — not a percentage multiplier.
	if err := b.AddBund(testStartTile, world.CellLocal{Row: 100, Col: 90}, 60, cid()); err != nil {
		t.Fatal(err)
	}
	after := effectAt(t, b, homeB, 0).Seen
	if after >= before/2 {
		t.Fatalf("bund reduced seen from %v to %v — must be a material drop through the same viewshed path", before, after)
	}
}

func TestTreeBeltGrowIn(t *testing.T) {
	b := blightAPI(t, flatWorld(t))
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "obj", Class: BlightSevere, Tile: testStartTile,
		Local: world.CellLocal{Row: 100, Col: 100}, NoiseRadiusM: 150, VisualHeightM: 40, VisualMagnitude: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	homeB := world.CellLocal{Row: 100, Col: 80}
	if err := b.AddTreeBelt(testStartTile, world.CellLocal{Row: 100, Col: 90}, 60, 0, cid()); err != nil {
		t.Fatal(err)
	}
	fresh := effectAt(t, b, homeB, 0).Seen  // planted this year: ~no occlusion
	mature := effectAt(t, b, homeB, 5).Seen // five simulated years: full occlusion
	if fresh <= 0 {
		t.Fatal("a freshly-planted tree belt must not occlude yet (grow-in)")
	}
	if mature >= fresh/2 {
		t.Fatalf("tree belt after 5 years seen=%v is not materially weaker than fresh seen=%v — grow-in must matter", mature, fresh)
	}
}

// --- AC-8: enclosure and night-ban reduce the noise component specifically -

func TestEnclosureNightBanReduceNoise(t *testing.T) {
	b := blightAPI(t, flatWorld(t))
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "obj", Class: BlightSevere, Tile: testStartTile,
		Local: world.CellLocal{Row: 100, Col: 100}, NoiseRadiusM: 150, VisualHeightM: 40, VisualMagnitude: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	home := world.CellLocal{Row: 100, Col: 110}
	base := effectAt(t, b, home, 0)
	if base.Heard <= 0 || base.Seen <= 0 {
		t.Fatalf("fixture must produce both components (heard=%v seen=%v)", base.Heard, base.Seen)
	}

	if err := b.SetEnclosure("obj", true); err != nil {
		t.Fatal(err)
	}
	enclosed := effectAt(t, b, home, 0)
	if enclosed.Heard >= base.Heard {
		t.Fatalf("enclosure did not reduce heard (%v -> %v)", base.Heard, enclosed.Heard)
	}
	if enclosed.Seen != base.Seen {
		t.Fatalf("enclosure changed seen (%v -> %v) — it must reduce the noise component only", base.Seen, enclosed.Seen)
	}

	if err := b.SetNightBan("obj", true); err != nil {
		t.Fatal(err)
	}
	banned := effectAt(t, b, home, 0)
	if banned.Heard >= enclosed.Heard {
		t.Fatalf("night ban did not further reduce heard (%v -> %v)", enclosed.Heard, banned.Heard)
	}
	if banned.Seen != base.Seen {
		t.Fatalf("night ban changed seen (%v -> %v) — it must reduce the noise component only", base.Seen, banned.Seen)
	}
}

// --- AC-12: the viewshed float path is byte-identical across instances -----

func TestViewshedDeterminism(t *testing.T) {
	b1 := ridgeAPI(t, 60)
	b2 := ridgeAPI(t, 60)
	for _, local := range []world.CellLocal{
		{Row: 100, Col: 110}, {Row: 100, Col: 120}, {Row: 100, Col: 80},
	} {
		e1 := effectAt(t, b1, local, 0)
		e2 := effectAt(t, b2, local, 0)
		if e1.Heard != e2.Heard || e1.Seen != e2.Seen {
			t.Fatalf("viewshed diverged across two identical instances at %v (heard %v/%v seen %v/%v) — not byte-identical", local, e1.Heard, e2.Heard, e1.Seen, e2.Seen)
		}
	}
}

// --- AC-14: concurrent use is race-free ------------------------------------

func TestBlightConcurrentNoRace(t *testing.T) {
	w := importWorld(t, ridgeElev(30))
	b := blightAPI(t, w)
	if err := b.PlaceBlightingObject(BlightingObjectSpec{
		Key: "obj", Class: BlightModerate, Tile: testStartTile,
		Local: world.CellLocal{Row: 100, Col: 100}, NoiseRadiusM: 200, VisualHeightM: 20, VisualMagnitude: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				local := world.CellLocal{Row: 80 + (i*3+j)%40, Col: 60 + (j*5)%60}
				if _, err := b.EffectAt(testStartTile, local, int64(j), cid()); err != nil {
					t.Errorf("EffectAt: %v", err)
					return
				}
				if err := b.AddBund(testStartTile, world.CellLocal{Row: 150, Col: 150}, float64(10+j%50), cid()); err != nil {
					t.Errorf("AddBund: %v", err)
					return
				}
				if err := b.RegisterBlightingObject("tmp", BlightModerate, 100+int64(j)); err != nil {
					t.Errorf("RegisterBlightingObject: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// --- constructor guard (SEC-208 class): a hand-built hostile config --------

func TestNewBlightAPIRejectsHostileConfig(t *testing.T) {
	base, err := LoadBlightConfig(realMiningPath(t), cid())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*BlightConfig)
	}{
		{"occlusion-scale-zero", func(c *BlightConfig) { c.Viewshed.OcclusionScaleM = 0 }},
		{"seen-falloff-nan", func(c *BlightConfig) { c.Viewshed.SeenFalloffM = math.NaN() }},
		{"min-distance-zero", func(c *BlightConfig) { c.Noise.MinDistanceM = 0 }},
		{"grow-in-zero", func(c *BlightConfig) { c.TreeBelt.GrowInYears = 0 }},
		{"capacity-days-inf", func(c *BlightConfig) { c.Extraction.CapacityDays = math.Inf(1) }},
		{"class-magnitude-nan", func(c *BlightConfig) {
			e := c.ClassProfile[BlightSevere]
			e.Magnitude = math.NaN()
			c.ClassProfile[BlightSevere] = e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mutate(&cfg)
			b, err := NewBlightAPI(newWorld(t), cfg, cid())
			assertErrCode(t, err, ErrBlightDataInvalid)
			if b != nil {
				t.Fatalf("NewBlightAPI returned a non-nil API alongside a rejection — fail-closed means nil API")
			}
		})
	}
}
