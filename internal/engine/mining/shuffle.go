package mining

import (
	"errors"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the deterministic seeded deposit shuffle (FEAT-049 AC-5/
// AC-7/AC-8/AC-9/AC-10). It draws exclusively from a counter-based hash
// stream keyed by (worldSeed, tileID, cellID, purposeTag) — a pure
// function of those inputs with no shared/global RNG object and no wall
// clock (AC-13/AC-14). Same seed + same geology + same data
// file => bit-identical deposit records, in the same order, on any
// machine and any worker count (AC-8). The shuffle is single-threaded by
// construction (it never spawns goroutines), so shard/worker-count
// invariance holds trivially: there is no ordering that a shard split
// could perturb.
//
// Geology-awareness (AC-5) is a per-cell weighted type choice biased by
// the tile's real engine.world geology pocket (read via PocketGeology,
// AC-10's "never a mining-local geology copy" requirement): uranium's
// weight collapses in pure-chalk tiles, and gas/coal weights inflate in
// coal-measures tiles. The weight factors are all data-sourced (AC-6).
//
// Deposit placement runs over the full owned-and-unowned tile extent at
// world-gen time (AC-9): DepositAt and the shuffle read only cell
// terrain/surface and tile geology, never ownership, so a deposit's
// existence is never contingent on TileAt.Owned.

// salt constants tag the purpose of each per-cell draw (AC-13's
// purposeTag). They are hex bit-mix salts, not balance numbers.
const (
	saltPresence = 0x44503031 // "DP01" — does this cell hold a deposit?
	saltTypePick = 0x44503032 // "DP02" — which type wins the weighted pick
	saltSize     = 0x44503033 // "DP03" — size-curve draw
	saltDensity  = 0x44503034 // "DP04" — density-curve draw
	saltDepth    = 0x44503035 // "DP05" — depth-band draw
)

// depositKey is the in-memory cell key for a placed deposit.
type depositKey struct {
	tileX, tileY int
	row, col     int
}

// DepositMap is the queryable deposit-record surface (AC-1) plus the
// shuffle that fills it. It is built once for a (worldSeed, world,
// params) triple and then queried; it never mutates engine.world state
// beyond reading it (the Prospect call that derives geology is the
// caller's, per AC-10/AC-12).
type DepositMap struct {
	seed   uint64
	world  *world.WorldAPI
	params DepositParams

	mu     sync.RWMutex
	placed map[depositKey]LocatedDeposit

	// self holds the address NewDepositMap gave this DepositMap at
	// construction (self.Store(m), set once, at the end of NewDepositMap,
	// never stored to again). It is the SEC-020 copyguard, mirroring
	// internal/engine/world's World.self and internal/ui/diagrams'
	// Engine.self EXACTLY: `m2 := *m` is legal, unsafe-free, reflect-free
	// Go — every field of DepositMap is unexported, but that does not stop
	// a caller from dereferencing the *DepositMap NewDepositMap returned
	// and copying the struct value. mu is a plain value, so the copy m2
	// gets its OWN, independently-zeroed mu — but m2.placed (a map, a
	// reference type) still ALIASES m.placed, and m2.self still points at
	// the ORIGINAL m (copied by value, unchanged). That is exactly the
	// signal a copy cannot erase: checkNotCopied compares the receiver's
	// own address against self, and a copy's address can never equal the
	// original's.
	//
	// atomic.Pointer[DepositMap], not a plain *DepositMap, for the same
	// reason SEC-016 forced Engine.self's type: a plain, unsynchronized
	// field read done lock-free, concurrently with a struct copy that
	// touches the whole struct's memory as one operation, has no defined
	// result in the Go memory model unless the read itself is a properly
	// synchronized operation. Store happens exactly once, in
	// NewDepositMap, before any goroutine can have a reference to m to
	// race against; every subsequent Load is a single lock-free atomic
	// read requiring nothing else — not mu, nothing a copy could have
	// captured mid-lock.
	self atomic.Pointer[DepositMap]
}

// NewDepositMap constructs an empty deposit map for a fixed worldSeed.
// The world must be the same *world.WorldAPI whose tiles will be shuffled
// and queried; params must come from LoadDepositParams (AC-6).
func NewDepositMap(worldSeed uint64, w *world.WorldAPI, p DepositParams) *DepositMap {
	m := &DepositMap{
		seed:   worldSeed,
		world:  w,
		params: p,
		placed: make(map[depositKey]LocatedDeposit),
	}
	// Stored exactly once, here, before m is returned to any caller — no
	// goroutine can have a reference to m to race this Store against (see
	// self's doc comment above).
	m.self.Store(m)
	return m
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other DepositMap value (SEC-020 family, mirroring engine.world's
// World.checkNotCopied and internal/ui/diagrams' Engine.checkNotCopied).
// Deliberately lock-free — a single atomic.Pointer.Load, requiring nothing
// else, not m.mu — so it is safe and correct to call BEFORE m.mu is ever
// touched.
//
// Why this matters for DepositMap specifically: mu is a sync.RWMutex VALUE
// (a copy gets its own, independent lock) while placed
// (map[depositKey]LocatedDeposit) is a reference type a copy ALIASES. An
// unrejected copy is therefore a second lock domain that can read/mutate
// the SAME aliased map as the original — exactly SEC-020's "two locks, one
// referent" shape, and the -race data race Finding 1 proved.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can be
// byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`m2 := *m` while another goroutine has
// m.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's own
// mu in that state can block forever, since nothing will ever Unlock()
// that specific copy's address. A guard placed AFTER the lock can never run
// for that attack, because the attack IS acquiring the lock; rejecting the
// copy here, before Lock()/RLock() is ever called, means that hang path is
// never reached at all.
//
// A nil m.self.Load() (a DepositMap constructed as a bare DepositMap{} or
// new(DepositMap) rather than via NewDepositMap, so self was never stored)
// is treated the same as a mismatch and rejected the same way — every
// documented construction path is NewDepositMap, so an unset self is
// itself a misuse this same error correctly names.
func (m *DepositMap) checkNotCopied() error {
	if m.self.Load() != m {
		return errs.New(ErrDepositMapCopied, errs.NewCorrelationID(), nil)
	}
	return nil
}

// ShuffleTile lays deposits for one tile. It consumes engine.world's real
// geology query (AC-10): PocketGeology, which returns ErrGeologyNotProspected
// until the tile has been prospected — the "geology not yet derived" case
// is rejected as ErrGeologyNotDerived (AC-12) rather than proceeding
// against zero-value geology. Re-shuffling a tile with the same seed is
// idempotent (the draws are pure functions of the inputs).
func (m *DepositMap) ShuffleTile(t world.TileCoord, correlationID string) error {
	if err := m.checkNotCopied(); err != nil {
		return err
	}
	if !t.InExtent() {
		return errs.New(ErrDepositQueryOutOfBounds, correlationID, map[string]any{"tile": t})
	}

	pocket, err := m.world.PocketGeology(t, correlationID)
	if err != nil {
		if errors.Is(err, &errs.E{Code: world.ErrGeologyNotProspected}) {
			return errs.New(ErrGeologyNotDerived, correlationID, map[string]any{"tile": t})
		}
		return err
	}

	var placed []LocatedDeposit
	for row := 0; row < world.TileSizeCells; row++ {
		for col := 0; col < world.TileSizeCells; col++ {
			local := world.CellLocal{Row: row, Col: col}
			cell, err := m.world.CellAt(t, local, correlationID)
			if err != nil {
				return err
			}
			d, ok := placeOne(m.params, m.seed, t, local, cell.Surface == world.SurfaceWater, pocket)
			if ok {
				placed = append(placed, LocatedDeposit{Tile: t, Local: local, Deposit: d})
			}
		}
	}

	m.mu.Lock()
	for _, ld := range placed {
		m.placed[depositKey{t.X, t.Y, ld.Local.Row, ld.Local.Col}] = ld
	}
	m.mu.Unlock()
	return nil
}

// ShuffleAll lays deposits across the full expansion extent (AC-9) in
// deterministic tile order. It stops at the first unprospected tile with
// ErrGeologyNotDerived — the world-gen caller prospects tiles (derives
// geology) before invoking it, per AC-10.
func (m *DepositMap) ShuffleAll(correlationID string) error {
	if err := m.checkNotCopied(); err != nil {
		return err
	}
	for y := 0; y < world.TilesPerSide; y++ {
		for x := 0; x < world.TilesPerSide; x++ {
			if err := m.ShuffleTile(world.TileCoord{X: x, Y: y}, correlationID); err != nil {
				return err
			}
		}
	}
	return nil
}

// DepositAt returns the deposit at a cell, if one was placed (AC-1). A
// false return with nil error means "no deposit at this cell" (which
// includes the not-yet-shuffled state — a deposit map starts empty). An
// out-of-extent tile or out-of-bounds cell returns ErrDepositQueryOutOfBounds.
func (m *DepositMap) DepositAt(t world.TileCoord, local world.CellLocal) (Deposit, bool, error) {
	if err := m.checkNotCopied(); err != nil {
		return Deposit{}, false, err
	}
	if !t.InExtent() || !local.InBounds() {
		return Deposit{}, false, errs.New(ErrDepositQueryOutOfBounds, errs.NewCorrelationID(), map[string]any{
			"tile": t, "local": local,
		})
	}
	m.mu.RLock()
	d, ok := m.placed[depositKey{t.X, t.Y, local.Row, local.Col}]
	m.mu.RUnlock()
	return d.Deposit, ok, nil
}

// TileDeposits returns every deposit placed in tile t, sorted by
// (row, col) for determinism. Returns nil for an unshuffled tile, and nil
// (rejected) for a struct-copied DepositMap — there is no error channel on
// this AC-1 plain-read surface, so checkNotCopied rejects by returning the
// empty result before m.mu is touched rather than racing the copy's lock
// against the aliased map.
func (m *DepositMap) TileDeposits(t world.TileCoord) []LocatedDeposit {
	if err := m.checkNotCopied(); err != nil {
		return nil
	}
	m.mu.RLock()
	out := make([]LocatedDeposit, 0)
	for _, ld := range m.placed {
		if ld.Tile == t {
			out = append(out, ld)
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Local.Row != out[j].Local.Row {
			return out[i].Local.Row < out[j].Local.Row
		}
		return out[i].Local.Col < out[j].Local.Col
	})
	return out
}

// AllDeposits returns every placed deposit in a fixed, deterministic
// order (tile X, tile Y, row, col) — the order AC-8's deep-equality check
// relies on. Map iteration order is never exposed. A struct-copied
// DepositMap is rejected with nil (see TileDeposits for why this surface
// has no error channel).
func (m *DepositMap) AllDeposits() []LocatedDeposit {
	if err := m.checkNotCopied(); err != nil {
		return nil
	}
	m.mu.RLock()
	out := make([]LocatedDeposit, 0, len(m.placed))
	for _, ld := range m.placed {
		out = append(out, ld)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Tile.X != b.Tile.X {
			return a.Tile.X < b.Tile.X
		}
		if a.Tile.Y != b.Tile.Y {
			return a.Tile.Y < b.Tile.Y
		}
		if a.Local.Row != b.Local.Row {
			return a.Local.Row < b.Local.Row
		}
		return a.Local.Col < b.Local.Col
	})
	return out
}

// placeOne decides whether a cell holds a deposit and, if so, what it is.
// sea is the cell's surface classification (AC-3: offshore fields on sea
// cells only). All draws are pure functions of (seed, tile, local, salt).
//
// It and the draw helpers below are FREE FUNCTIONS over the immutable
// (params, seed) pair rather than *DepositMap methods, deliberately: they
// never touch m.mu or m.placed (they read only params/seed, both set once
// at construction and never mutated), so as methods they would still be
// flagged by astgate as unguarded copy-reachable entry points while having
// no copy hazard to guard. As free functions over immutable inputs they
// have no shared state to race at all — the same convention
// internal/ui/diagrams uses for its snapshot/blit helpers.
func placeOne(params DepositParams, seed uint64, t world.TileCoord, local world.CellLocal, sea bool, pocket world.GeologyKind) (Deposit, bool) {
	rate := params.DepositRate
	if sea {
		rate = params.OffshoreRate
	}
	if unitFloat(draw(seed, t, local, saltPresence)) >= rate {
		return Deposit{}, false
	}

	dt, ok := chooseType(params, seed, t, local, sea, pocket)
	if !ok {
		return Deposit{}, false
	}

	return Deposit{
		Type:    dt,
		Size:    drawCurve(params.SizeCurve, seed, t, local, saltSize),
		Density: drawCurve(params.DensityCurve, seed, t, local, saltDensity),
		Depth:   drawDepth(params, seed, dt, t, local),
	}, true
}

// chooseType picks the deposit type by a deterministic weighted choice
// over the canonical resource order (params.Resources), biased by geology
// (AC-5). Sea cells only consider offshore-capable resources (AC-3).
func chooseType(params DepositParams, seed uint64, t world.TileCoord, local world.CellLocal, sea bool, pocket world.GeologyKind) (DepositType, bool) {
	type candidate struct {
		dt DepositType
		w  float64
	}
	candidates := make([]candidate, 0, len(params.Resources))
	total := 0.0
	for _, rp := range params.Resources {
		if sea && !rp.Offshore {
			continue
		}
		w := rp.CountWeight * geologyFactor(params, rp.Type, pocket)
		if w <= 0 {
			continue
		}
		candidates = append(candidates, candidate{rp.Type, w})
		total += w
	}
	if len(candidates) == 0 || total <= 0 {
		return 0, false
	}

	pick := unitFloat(draw(seed, t, local, saltTypePick)) * total
	for _, c := range candidates {
		pick -= c.w
		if pick < 0 {
			return c.dt, true
		}
	}
	return candidates[len(candidates)-1].dt, true
}

// geologyFactor returns the data-sourced weight multiplier for dt in a
// tile whose geology pocket is pocket (AC-5/AC-6).
func geologyFactor(p DepositParams, dt DepositType, pocket world.GeologyKind) float64 {
	switch dt {
	case DepositUranium:
		if pocket == world.GeologyNone {
			return p.CoLocation.ChalkUraniumFactor
		}
	case DepositGas:
		if pocket == world.GeologyDeepCoal {
			return p.CoLocation.CoalGasFactor
		}
	case DepositCoal:
		if pocket == world.GeologyDeepCoal {
			return p.CoLocation.CoalCoalFactor * p.EastKentCoalfield.GenerosityMultiplier
		}
	}
	return 1.0
}

// drawCurve samples a value in [Min, Max) from a power-shaped curve. The
// shape is data-sourced; math.Pow is used purely to shape the draw and is
// correctly-rounded under IEEE-754, so it is bit-identical on every
// platform (the same determinism argument foundation/det documents for
// its own fixed-point draws).
func drawCurve(c CurveParams, seed uint64, t world.TileCoord, local world.CellLocal, salt uint64) float64 {
	u := unitFloat(draw(seed, t, local, salt))
	return c.Min + (c.Max-c.Min)*math.Pow(u, c.Shape)
}

// drawDepth samples a depth uniformly inside dt's data-sourced band.
func drawDepth(p DepositParams, seed uint64, dt DepositType, t world.TileCoord, local world.CellLocal) float64 {
	min, max, ok := p.DepthBand(dt)
	if !ok {
		min, max = 0, 0
	}
	return min + (max-min)*unitFloat(draw(seed, t, local, saltDepth))
}

// draw returns one position-independent draw for (seed, tile, cell,
// purposeTag). It is the AC-13 hash stream: a pure function of its
// inputs, no shared RNG object, no wall clock.
func draw(seed uint64, t world.TileCoord, local world.CellLocal, salt uint64) uint64 {
	return mixHash(seed, uint64(t.X), uint64(t.Y), uint64(local.Row), uint64(local.Col), salt)
}

// unitFloat converts a hash draw to a float64 in [0, 1) from the top 53
// bits — the same integer-only, platform-exact construction
// foundation/det.Stream.Float64 documents. No transcendentals, no
// division by a non-power-of-two.
func unitFloat(h uint64) float64 {
	return float64(h>>11) * (1.0 / (1 << 53))
}

// mixHash is a small splitmix64-style integer mixing hash over a seed and
// zero or more word inputs, modelled on engine.world's hashCoord
// (geology.go) for build-time procedural content — the same "pure function
// of (coords, salt), not gameplay RNG" convention. Bit-exact on every
// platform: integer ops only.
func mixHash(seed uint64, words ...uint64) uint64 {
	v := seed
	for _, w := range words {
		v ^= w
		v ^= v >> 33
		v *= 0xff51afd7ed558ccd
		v ^= v >> 33
		v *= 0xc4ceb9fe1a85ec53
		v ^= v >> 33
	}
	return v
}
