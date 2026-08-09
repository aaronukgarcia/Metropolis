package det

import (
	"encoding/binary"
	"hash/fnv"
	"math/bits"
)

// philoxRounds, philoxMul0, philoxWeyl0 parameterise the counter-based
// mixing function below: a 2-word (2x64), 10-round construction in the
// Philox family (Salmon, Moraes, Dror, Shaw, "Parallel Random Numbers: As
// Easy as 1, 2, 3", SC'11). Philox has no internal state beyond the
// (counter, key) input — the output is a pure function of its input words
// — which is exactly the "position-independent, order-free, no shared RNG
// object anywhere" property §1.2 point 3 requires: any goroutine can
// compute draw N for any stream at any time, with no sequencing
// dependency on any other draw, of this or any other stream.
//
// philoxMul0 and philoxWeyl0 are the standard Random123 constants for the
// 2x64 variant (a fixed odd 64-bit multiplier and the 64-bit golden-ratio
// Weyl increment used to perturb the key each round); 10 rounds is
// Philox's documented default round count for adequate statistical
// quality. All arithmetic is fixed-width, wraparound uint64 (math/bits
// only) — no floating point, no trigonometry, so the output is bit-exact
// on every platform Go supports.
const (
	philoxRounds = 10
	philoxMul0   = 0xD2B74407B1CE6E93
	philoxWeyl0  = 0x9E3779B97F4A7C15
)

// philox2x64 is the counter-mode mixing function: a pure, deterministic
// function of (ctr0, ctr1, key) with no external state. It is the sole
// primitive every Stream draw method below is built from.
func philox2x64(ctr0, ctr1, key uint64) uint64 {
	for r := 0; r < philoxRounds; r++ {
		hi, lo := bits.Mul64(ctr0, philoxMul0)
		ctr0, ctr1 = hi^key^ctr1, lo
		key += philoxWeyl0
	}
	return ctr0 ^ ctr1
}

// Stream is a counter-based (Philox-style) RNG stream keyed by
// (worldSeed, entityID, month, purposeTag), per §1.2 point 3. A Stream
// value holds only its own derived key and a local draw counter — it
// shares no memory with any other Stream, ever ("no shared RNG object
// anywhere"). It is safe to construct and draw from many Streams
// concurrently, from any number of goroutines, with no locking: each
// Stream's output depends only on its own key and its own counter/index
// argument, never on timing, scheduling, or any other Stream's state.
//
// The zero Stream is not valid — always construct via NewStream.
type Stream struct {
	key uint64
	n   uint64 // next sequential draw index for Uint64()
}

// NewStream derives a Stream's key from (worldSeed, entityID, month,
// purpose) via a fixed-width binary encoding (never string formatting,
// which could vary by Go version/locale) fed through FNV-1a 64
// (deterministic, stdlib, non-cryptographic — this key only needs to be
// well-mixed, not attacker-resistant). worldSeed of zero is a valid,
// fully-supported seed (AC-9): Philox's diffusion comes from the round
// function's multiply/XOR structure and the Weyl key schedule, not from
// any assumption that the key is non-zero, so a zero worldSeed produces a
// well-defined, non-degenerate stream exactly like any other seed — see
// rng_test.go's TestNewStream_ZeroSeedIsWellDefined.
func NewStream(worldSeed uint64, entityID uint64, month int64, purpose string) Stream {
	h := fnv.New64a()

	var buf [24]byte
	binary.BigEndian.PutUint64(buf[0:8], worldSeed)
	binary.BigEndian.PutUint64(buf[8:16], entityID)
	binary.BigEndian.PutUint64(buf[16:24], uint64(month))
	_, _ = h.Write(buf[:]) // hash.Hash.Write never errors (documented contract)
	_, _ = h.Write([]byte(purpose))

	return Stream{key: h.Sum64()}
}

// At returns the draw at position n directly, independent of any prior
// calls to At or Uint64 on this Stream (or any other Stream) — this is
// the "position-independent, order-free" property: drawing index 5
// without ever drawing 0-4 first produces the exact same value as
// sequential drawing would have produced at position 5.
func (s Stream) At(n uint64) uint64 {
	return philox2x64(n, 0, s.key)
}

// Uint64 returns the next value in the stream's sequential order (draw
// s.n, then advances s.n) — a convenience for callers that just want "the
// next draw" rather than addressing a specific position via At. It is
// exactly equivalent to s.At(n) for whatever n the stream has reached.
func (s *Stream) Uint64() uint64 {
	v := s.At(s.n)
	s.n++
	return v
}

// Float64 returns the next draw as a float64 in [0, 1), constructed from
// the top 53 bits of a Uint64 draw (the standard "use the mantissa's full
// precision, discard the low bits" technique) — never via math
// trigonometry or any transcendental function, so the result is
// bit-identical across platforms: it is built entirely from an integer
// right-shift and one IEEE-754 multiply by an exact power of two, both of
// which the Go spec guarantees are exactly reproducible.
func (s *Stream) Float64() float64 {
	return float64(s.Uint64()>>11) * (1.0 / (1 << 53))
}

// Int63 returns the next draw as a non-negative int64 (the top bit of the
// underlying Uint64 draw is cleared, matching the convention of the
// standard library's math/rand Int63).
func (s *Stream) Int63() int64 {
	return int64(s.Uint64() >> 1)
}

// IntN returns the next draw as a value in [0, n). n must be positive.
// Uses modulo reduction: for n that does not evenly divide 2^63 this has
// a documented, bounded, deterministic bias (never a fairness concern for
// this project's use — n is always tiny, e.g. a die roll or a small
// weighted-choice count, relative to 2^63), and is preferred here over a
// rejection-sampling loop specifically because a fixed-cost, single-draw
// implementation keeps every IntN call's cost (and its consumption of the
// stream's sequential counter) trivially predictable.
func (s *Stream) IntN(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return s.Int63() % n
}
