package fdi

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the STUB of engine.fdi's (MOD-059) competitive-bid surface —
// the machinery feat.pharmacampus supplies its education term INTO. MOD-059
// is open; when it lands it owns prospect generation, the full
// competitive-bid comparison against the off-map region, incentive-package
// composition, and anchor-registration-as-firm. This stub exists only so the
// feat.pharmacampus compounding loop has a real, directional win/lose
// outcome to prove (AC-3's "higher education output → strictly better bid;
// the lose branch is reachable") without reimplementing MOD-059's machinery.
//
// The stub is deliberately thin: a bid-quality term (base + education
// contribution, computed by feat.pharmacampus) compared against a
// data-sourced off-map competing floor, with a seeded jitter draw so the
// seed is genuinely consumed (AC-9). No prospect generation, no incentive
// package, no anchor registration — those are MOD-059's.

// BidOutcome is the result of resolving a pharma prospect bid: whether the
// city won, and the bid's quality score (for "wins on strictly better
// terms" assertions).
type BidOutcome struct {
	Won     bool
	Quality int64
}

// ResolvePharmaBid compares a bid-quality term against the off-map competing
// region's floor, both derived from data, with a deterministic seeded jitter
// draw, and returns the win/lose outcome. It is a pure function of
// (term, competingFloor, jitterMax, seed) — never wall clock, never a shared
// RNG (GR#21).
func ResolvePharmaBid(term, competingFloor, jitterMax int64, seed uint64) BidOutcome {
	quality := num.SatAdd(term, pharmaBidJitter(seed, jitterMax))
	return BidOutcome{Won: quality >= competingFloor, Quality: quality}
}

// pharmaBidJitter returns a deterministic symmetric jitter in
// [-jitterMax, +jitterMax] drawn from the counter-based hash stream keyed on
// the world seed (GR#21). The purpose tag disambiguates this stream from any
// other consumer of the same seed.
func pharmaBidJitter(seed uint64, jitterMax int64) int64 {
	if jitterMax <= 0 {
		return 0
	}
	stream := det.NewStream(seed, 0, 0, "pharma-bid")
	n := num.SatAdd(num.SatAdd(jitterMax, jitterMax), 1)
	return stream.IntN(n) - jitterMax
}
