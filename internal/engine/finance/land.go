package finance

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Land pricing (§7, AC-4): every cell is continuously priced as
//
//	base(terrain) × access(junction, roads) × amenity(services, coast
//	view, pollution⁻¹) × scarcity(city size)
//
// as four independently-testable component functions, not one opaque
// calculation. Factors are fixed-point "per mille" values (factorScale
// below): 1000 is 1.0×, 1500 is 1.5×, 500 is 0.5×. LandPrice multiplies
// the four together and divides by factorScale three times, so the
// result stays inside int64 (GR#16 — no float, no precision loss).
//
// Every base price and factor magnitude in this file is a PLACEHOLDER
// pending Aaron's balance pass (the balance-number regime): the tests
// only assert monotonic direction, never a specific figure.

// factorScale is the fixed-point denominator for the access/amenity/
// scarcity factors: 1000 = 1.0×.
const factorScale int64 = 1000

// TerrainKind is finance's own terrain classification for base land
// pricing. It is deliberately a self-contained enum (not engine.world's
// Surface): the composition root maps world cells onto these kinds, so
// this package never reaches into engine.world's internals (GR#20).
type TerrainKind uint8

const (
	TerrainGrass TerrainKind = iota
	TerrainWoodland
	TerrainShingle
	TerrainRock
	TerrainUrban
	TerrainWater
)

// LandCell is the input to LandPrice: everything the formula needs, in
// finance's own vocabulary. The composition root populates it from
// engine.world (terrain), engine.roads (junction/road count),
// engine.services (service coverage), and engine.citizens (city size).
type LandCell struct {
	Terrain   TerrainKind
	Junction  bool
	Roads     int
	Services  int
	CoastView bool
	Pollution int64
	CitySize  int64
}

// baseTerrainPrices is the placeholder base-price table (micro-pounds
// per cell), keyed by TerrainKind. PLACEHOLDER pending Aaron's balance
// pass — directional only.
var baseTerrainPrices = [...]int64{
	TerrainGrass:    50_000 * micropoundsScale,
	TerrainWoodland: 40_000 * micropoundsScale,
	TerrainShingle:  15_000 * micropoundsScale,
	TerrainRock:     8_000 * micropoundsScale,
	TerrainUrban:    80_000 * micropoundsScale,
	TerrainWater:    0, // water is not buildable land — no price
}

// BaseTerrainPrice returns t's base price in micro-pounds (AC-4's
// base(terrain) component).
func BaseTerrainPrice(t TerrainKind) Money {
	if int(t) >= len(baseTerrainPrices) || int(t) < 0 {
		return 0
	}
	return Money(baseTerrainPrices[t])
}

// AccessFactor is AC-4's access(junction, roads) component: a per-mille
// factor that rises with a junction and with each connecting road.
// PLACEHOLDER magnitudes, monotonic direction only. The roads multiplier
// uses num.SafeMul + saturating addition so an extreme roads count saturates
// instead of wrapping below the 1.0× baseline (GR#16).
func AccessFactor(junction bool, roads int) int64 {
	if roads < 0 {
		roads = 0
	}
	inc, overflowed := num.SafeMul(int64(roads), 50)
	if overflowed {
		inc = math.MaxInt64
	}
	f := num.SatAdd(factorScale, inc)
	if junction {
		f = num.SatAdd(f, 200)
	}
	return f
}

// AmenityFactor is AC-4's amenity(services, coast view, pollution⁻¹)
// component: a per-mille factor that rises with service coverage and a
// coast view, and falls as pollution rises (clamped at a positive
// floor so a heavily polluted cell still prices above zero). The
// services multiplier uses num.SafeMul + saturating arithmetic so an extreme
// count saturates instead of wrapping below baseline (GR#16).
func AmenityFactor(services int, coastView bool, pollution int64) int64 {
	if services < 0 {
		services = 0
	}
	if pollution < 0 {
		pollution = 0
	}
	inc, overflowed := num.SafeMul(int64(services), 100)
	if overflowed {
		inc = math.MaxInt64
	}
	f := num.SatAdd(factorScale, inc)
	if coastView {
		f = num.SatAdd(f, 200)
	}
	f = num.SatSub(f, minI64(pollution, 90)*10)
	if f < 100 {
		f = 100 // floor: 0.1×
	}
	return f
}

// ScarcityFactor is AC-4's scarcity(city size) component: a per-mille
// factor that rises monotonically with city size (more demand for a
// fixed land supply ⇒ higher price).
func ScarcityFactor(citySize int64) int64 {
	if citySize < 0 {
		citySize = 0
	}
	// +1 per-mille point per 100 citizens-equivalent, capped.
	return num.SatAdd(factorScale, minI64(citySize/100, 9000))
}

// LandPrice implements §7's land-price formula by composing the four
// component functions. All arithmetic is fixed-point int64.
func LandPrice(cell LandCell) Money {
	p := int64(BaseTerrainPrice(cell.Terrain))
	p = mulDiv(p, AccessFactor(cell.Junction, cell.Roads), factorScale)
	p = mulDiv(p, AmenityFactor(cell.Services, cell.CoastView, cell.Pollution), factorScale)
	p = mulDiv(p, ScarcityFactor(cell.CitySize), factorScale)
	return Money(p)
}
