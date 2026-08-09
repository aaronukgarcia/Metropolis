package stub

import (
	"errors"
	"time"
)

// This file implements StubEngine's chaos knobs (AC-7): a delayed-delta
// mode (artificial latency before a Delta is pushed) and a burst-delta
// mode (several Deltas pushed in a tight cluster). Both are independently
// togglable and deterministic given a seed — the seeded splitmix64
// generator below (splitMix64) is pure arithmetic, never math/rand
// (global or otherwise) and never time-derived (AC-11, AC-12): only the
// actual time.Sleep wall-clock wait in applyDelay below touches real
// time, and that duration itself is chosen deterministically from the
// seed, so two runs with the same seed request the same delays even
// though OS scheduling jitter means the observed wall-clock gap is never
// byte-for-byte comparable (that's the one thing AC-11 permits to vary).

// DelayConfig configures the delayed-delta chaos knob: each pushed Delta
// waits a pseudo-random duration in [MinDelay, MaxDelay] (deterministic
// given ChaosConfig.Seed) before being sent.
type DelayConfig struct {
	Enabled  bool
	MinDelay time.Duration
	MaxDelay time.Duration
}

// BurstConfig configures the burst-delta chaos knob: instead of pushing
// one Delta per scripted-stream advance, Size Deltas are pushed back to
// back with no artificial gap between them.
type BurstConfig struct {
	Enabled bool
	// Size is how many Deltas one scripted-stream advance pushes in the
	// cluster. Must be >= 2 when Enabled (1 is just the non-burst
	// behaviour and is rejected as a config mistake, not silently
	// accepted as a no-op — AC-10).
	Size int
}

// ChaosConfig bundles both knobs plus the seed they share.
type ChaosConfig struct {
	// Seed drives the deterministic delay-jitter generator. Two
	// StubEngines constructed with the same Seed and driven with the
	// same command sequence request byte-identical delay durations (not
	// necessarily identical observed wall-clock gaps — see the file doc
	// above).
	Seed          uint64
	DelayedDeltas DelayConfig
	BurstDeltas   BurstConfig
}

// ErrInvalidChaosConfig is the sentinel wrapped by validation failures
// returned from ChaosConfig.Validate / NewStubEngine's WithChaos option
// (AC-10: invalid chaos config must fail loudly, never be silently
// clamped).
var ErrInvalidChaosConfig = errors.New("stub: invalid chaos configuration")

// Validate reports whether c is well-formed. A negative delay, an
// inverted [Min, Max] range, or a burst size below 2 while enabled are
// all rejected explicitly rather than clamped.
func (c ChaosConfig) Validate() error {
	if c.DelayedDeltas.Enabled {
		if c.DelayedDeltas.MinDelay < 0 {
			return errors.New("stub: DelayConfig.MinDelay must not be negative: " + c.DelayedDeltas.MinDelay.String())
		}
		if c.DelayedDeltas.MaxDelay < c.DelayedDeltas.MinDelay {
			return errors.New("stub: DelayConfig.MaxDelay must be >= MinDelay")
		}
	}
	if c.BurstDeltas.Enabled && c.BurstDeltas.Size < 2 {
		return errors.New("stub: BurstConfig.Size must be >= 2 when enabled")
	}
	return nil
}

// validateChaos wraps ChaosConfig.Validate's error, if any, with
// ErrInvalidChaosConfig so callers can errors.Is against a stable
// sentinel regardless of which specific field failed.
func validateChaos(c ChaosConfig) error {
	if err := c.Validate(); err != nil {
		return errors.Join(ErrInvalidChaosConfig, err)
	}
	return nil
}

// splitMix64 is a small, dependency-free deterministic PRNG (the same
// avalanche construction as fixture.go's foldSeed, here kept stateful
// across calls) used only to pick chaos delay durations. Never
// math/rand, never time-seeded.
type splitMix64 struct {
	state uint64
}

func newSplitMix64(seed uint64) *splitMix64 {
	return &splitMix64{state: seed}
}

func (g *splitMix64) next() uint64 {
	g.state += 0x9E3779B97F4A7C15
	z := g.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// nextDelay returns a deterministic duration in [min, max]. min == max
// returns min directly without consuming randomness (a fixed delay is a
// valid, still-deterministic configuration).
func (g *splitMix64) nextDelay(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	span := uint64(max - min)
	return min + time.Duration(g.next()%span)
}
