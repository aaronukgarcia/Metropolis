package synth

import (
	"encoding/json"
	"io"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// radialPullNumerator/radialPullDenominator define the fixed 3/4 pull
// NetworkRadial applies toward the grid's centre (placeCitizen below): a
// coarse, integer-only "denser near the core" approximation. Deliberately
// NOT built from math.Sin/Cos or any other transcendental function —
// foundation/det/rng.go's Float64 doc comment states this codebase's
// standing rule that anything on a path required to be bit-identical
// across platforms must stick to integer arithmetic and IEEE-754-exact
// operations (a correctly-rounded multiply/divide, unlike a series-
// approximated trig call, is guaranteed identical on every platform Go
// supports).
const (
	radialPullNumerator   = 3
	radialPullDenominator = 4
)

// synthMeta is the "synth" shard's first record: the generation
// parameters a reader needs to know how the citizen records that follow
// were laid out. Mirrors engine.core.persist.go's snapshotMeta pattern
// (one small metadata record first, then the bulk records) — the same
// house shape, not a second one (GR#3).
type synthMeta struct {
	CitizenCount int64        `json:"citizenCount"`
	Seed         uint64       `json:"seed"`
	Sprawl       float64      `json:"sprawl"`
	NetworkShape NetworkShape `json:"networkShape"`
	GridSide     int64        `json:"gridSide"`
}

// synthCitizen is one placeholder citizen record (AC-1b: this is what
// citizenCount's allocation/generation cost is actually spent producing).
// Sprint 1/2's engine.core is a walking skeleton with no real citizen
// data model yet (engine.citizens lands Sprint 3) — these fields are a
// deliberately small, deterministic stand-in scaled to exercise the
// SAME cost shape (O(citizenCount) allocation and work) a real citizen
// store will have, not a claim that this is what engine.citizens'
// eventual record shape will look like. Logged as an assumption against
// this item's BOW record, matching the acceptance doc's own "Assumption
// flagged" escalation about the 10M preset running against the Sprint-1
// skeleton.
type synthCitizen struct {
	ID     int64  `json:"id"`
	HomeX  int32  `json:"homeX"`
	HomeY  int32  `json:"homeY"`
	Wealth uint32 `json:"wealth"`
}

// gridSideFor derives the side length of the square cell grid a
// synthetic city's citizens are placed on, from citizenCount and sprawl.
// Coarse density model, documented purely for perf/allocation-scaling
// purposes (out of scope: a real cell/road simulation is engine.world's
// job, Sprint 3+ per the acceptance doc's "Out of scope" section).
// isqrt(citizenCount) is the smallest square grid that can hold
// citizenCount cells one-per-cell; sprawl linearly inflates that side up
// to 2x at Sprawl==MaxSprawl, so a maximally sprawling city occupies a
// sparser grid than a maximally dense one at the same citizen count —
// the qualitative behaviour AC-7b's "sprawl" parameter documents,
// without claiming any planning realism beyond that.
func gridSideFor(citizenCount int64, sprawl float64) int64 {
	base := isqrt(citizenCount)
	if base < 1 {
		base = 1
	}
	inflate := 1.0 + sprawl // 1.0..2.0 across MinSprawl..MaxSprawl
	side := int64(float64(base) * inflate)
	if side < 1 {
		side = 1
	}
	return side
}

// isqrt returns floor(sqrt(n)) for n >= 0, exact (not merely
// float64-approximate). math.Sqrt is used as the starting estimate —
// unlike math.Sin/Cos, IEEE 754 mandates a correctly-rounded sqrt, and
// Go's math.Sqrt is documented to meet that on every platform Go
// supports, so this is safe for GR#21 bit-exact determinism — and the
// following nudge loop corrects any remaining off-by-one from the
// float64 round-trip, so the result is always the exact integer square
// root, never an approximation.
func isqrt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	x := int64(math.Sqrt(float64(n)))
	for x > 0 && x*x > n {
		x--
	}
	for (x+1)*(x+1) <= n {
		x++
	}
	return x
}

// placeCitizen computes citizen i's (x, y) home-cell coordinates on a
// gridSide x gridSide grid, according to shape. stream supplies the
// draws NetworkOrganic consumes for its jitter — always drawn in the
// same fixed order for a given shape, which is what keeps Generate's
// output byte-identical across repeated runs (AC-9).
func placeCitizen(i, gridSide int64, shape NetworkShape, stream *det.Stream) (x, y int32) {
	gx, gy := i%gridSide, i/gridSide
	switch shape {
	case NetworkRadial:
		cx, cy := gridSide/2, gridSide/2
		x = int32(cx + (gx-cx)*radialPullNumerator/radialPullDenominator)
		y = int32(cy + (gy-cy)*radialPullNumerator/radialPullDenominator)
	case NetworkOrganic:
		jx := stream.IntN(3) - 1 // -1..1
		jy := stream.IntN(3) - 1
		x = int32(gx + jx)
		y = int32(gy + jy)
	default: // NetworkGrid
		x = int32(gx)
		y = int32(gy)
	}
	return x, y
}

// Generate produces a synthetic world (AC-1) as a single "synth" NDJSON
// shard written to w, returning the serialize.Header a caller writes
// alongside it (matching engine.core.Snapshot's own Header+shard
// pattern, AC-2 — no second save/bundle shape invented here). The first
// record is a synthMeta describing the generation parameters; every
// subsequent record is one synthCitizen, written in ID order.
//
// p is validated (AC-7b's positive domains) BEFORE any allocation or
// generation work begins (AC-1b(b)/(c)) — a request outside
// MinSyntheticCitizens..MaxSyntheticCitizens, MinSprawl..MaxSprawl, or
// the NetworkShape enum is rejected outright, never clamped.
//
// Determinism (GR#21, AC-9): citizen records are produced by streaming
// through det.NewStream(p.Seed, 0, 0, "synth-citizen") in a fixed,
// sequential draw order per citizen (see placeCitizen) — the same
// (Seed, CitizenCount, Sprawl, NetworkShape) tuple always produces the
// same draw sequence, and serialize.NDJSONSerializer.WriteShard's own
// determinism guarantee (pinned gzip header, records written in next()'s
// exact order) carries that through to byte-identical output. Generation
// here is single-threaded/sequential by design — no goroutines, no
// worker pool — so there is no worker-pool-size dimension for this
// specific function to vary across (AC-9's "and across worker-pool
// sizes" clause is a property of engine.core's phase pipeline, which
// this package does not reimplement; determinism_test.go still exercises
// Generate at different GOMAXPROCS settings as a defence-in-depth check
// that no incidental parallelism ever gets introduced here later without
// a test catching it).
func Generate(correlationID string, p Params, w io.Writer) (serialize.Header, error) {
	if err := ValidateParams(correlationID, p); err != nil {
		return serialize.Header{}, err
	}

	gridSide := gridSideFor(p.CitizenCount, p.Sprawl)
	meta := synthMeta{
		CitizenCount: p.CitizenCount,
		Seed:         p.Seed,
		Sprawl:       p.Sprawl,
		NetworkShape: p.NetworkShape,
		GridSide:     gridSide,
	}

	header := serialize.NewHeader(int64(p.Seed), 0, 0, buildinfo.Version)
	stream := det.NewStream(p.Seed, 0, 0, "synth-citizen")

	metaWritten := false
	var i int64
	next := func() (serialize.Record, bool, error) {
		if !metaWritten {
			metaWritten = true
			data, err := json.Marshal(meta)
			if err != nil {
				return serialize.Record{}, false, err
			}
			return serialize.Record{Kind: "synth-meta", Data: data}, true, nil
		}
		if i >= p.CitizenCount {
			return serialize.Record{}, false, nil
		}
		x, y := placeCitizen(i, gridSide, p.NetworkShape, &stream)
		wealth := uint32(stream.Uint64() % 100000)
		rec := synthCitizen{ID: i, HomeX: x, HomeY: y, Wealth: wealth}
		i++
		data, err := json.Marshal(rec)
		if err != nil {
			return serialize.Record{}, false, err
		}
		return serialize.Record{Kind: "synth-citizen", Data: data}, true, nil
	}

	shardMeta, err := (serialize.NDJSONSerializer{}).WriteShard(w, serialize.ShardMeta{Name: "synth", Kind: "synth"}, next)
	if err != nil {
		return serialize.Header{}, errs.Wrap(codeGenerationIOFailed, correlationID, err, map[string]any{
			"citizenCount": p.CitizenCount,
		})
	}
	header.ShardIndex = []serialize.ShardMeta{shardMeta}
	return header, nil
}
