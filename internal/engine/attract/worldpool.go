package attract

// WorldPool is §4's "living outside world" seam: the source of the
// attractiveness comparison baseline A_world, and — in a future sprint — a
// finite/dynamic migrant pool. §11's monthly net migration is g(A −
// A_world), so A_world is the dial's zero point.
//
// v1 (this sprint) is a static, data-loaded baseline behind a
// [StaticWorldPool]. The seam exists so that a future finite/dynamic world
// pool (§4's explicit "future hook": the abstract pool migrants are pulled
// from, shared by the "living outside world" model) is an interface/data
// change, never a rewrite of the migration math (AC-8). Every read of
// A_world goes through this accessor rather than a bare float literal at
// each call site.
type WorldPool interface {
	// AWorld returns the comparison baseline attractiveness (the §11
	// "world average"). Net migration is driven by A − AWorld().
	AWorld() float64
}

// StaticWorldPool is the v1 WorldPool: a single static A_world value
// loaded from config data (GR#15) — §4's "infinite pool, static world".
// The zero StaticWorldPool reads A_world = 0, which is a genuine zero (a
// world with no attractiveness), not a "missing" sentinel: the missing
// case is represented by a nil WorldPool, which New rejects with
// ErrWorldPoolMissing (AC-11).
type StaticWorldPool struct {
	value float64
}

// NewStaticWorldPool returns a StaticWorldPool over the given baseline.
// The baseline is data-loaded (a Config/ParseConfig field), never a
// literal in this package's source (AC-8).
func NewStaticWorldPool(aworld float64) StaticWorldPool {
	return StaticWorldPool{value: aworld}
}

// AWorld implements WorldPool.
func (s StaticWorldPool) AWorld() float64 { return s.value }
