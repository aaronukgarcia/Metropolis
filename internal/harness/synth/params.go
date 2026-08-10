package synth

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// NetworkShape names one of the closed set of abstract road-network
// topologies Generate can lay a synthetic city's cells out along (AC-7b:
// "one of {grid, radial, organic} — an explicit enum, not a free
// string"). This is a procedural approximation only, for perf/scale
// shape variation — not a terrain/geology-derived network, which is
// engine.world's job (Sprint 3+, see doc.go's "Out of scope").
type NetworkShape string

// The closed NetworkShape enum (AC-7b). No other value is ever accepted.
const (
	NetworkGrid    NetworkShape = "grid"
	NetworkRadial  NetworkShape = "radial"
	NetworkOrganic NetworkShape = "organic"
)

// validNetworkShapes is the closed set ValidateParams checks NetworkShape
// against, declared once so the domain is authoritative in exactly one
// place (GR#3) rather than re-listed at every call site.
var validNetworkShapes = map[NetworkShape]bool{
	NetworkGrid:    true,
	NetworkRadial:  true,
	NetworkOrganic: true,
}

// Params is everything Generate needs to produce a synthetic world
// (AC-1): a target citizen count, a seed, and the sprawl/network-shape
// parameters that steer its layout.
//
// Every field's allowed domain, stated positively and exhaustively
// (AC-7b — never merely "valid"):
//   - CitizenCount: MinSyntheticCitizens..MaxSyntheticCitizens inclusive
//     (limits.go).
//   - Seed: any uint64 value, including zero — det.NewStream's zero seed
//     is fully well-defined (see foundation/det/rng.go's doc comment),
//     so this package places no restriction beyond the type itself.
//   - Sprawl: MinSprawl..MaxSprawl inclusive (limits.go).
//   - NetworkShape: exactly one of NetworkGrid, NetworkRadial,
//     NetworkOrganic.
type Params struct {
	CitizenCount int64
	Seed         uint64
	Sprawl       float64
	NetworkShape NetworkShape
}

// ValidateParams checks p against every documented domain boundary
// above (AC-7b) and rejects — never clamps (AC-1b(c)) — the first
// violation it finds with a registry-sourced error naming the requested
// value and the boundary it failed. This is the single choke point both
// Generate and RunPerf call before doing any work, so no caller can
// reach generation/allocation through a path that skips validation
// (AC-1b(b)).
func ValidateParams(correlationID string, p Params) error {
	if p.CitizenCount < MinSyntheticCitizens || p.CitizenCount > MaxSyntheticCitizens {
		return errs.New(codeCitizenCountOutOfRange, correlationID, map[string]any{
			"citizenCount": p.CitizenCount,
			"min":          MinSyntheticCitizens,
			"max":          MaxSyntheticCitizens,
		})
	}
	if p.Sprawl < MinSprawl || p.Sprawl > MaxSprawl {
		return errs.New(codeSprawlOutOfRange, correlationID, map[string]any{
			"sprawl": p.Sprawl,
			"min":    MinSprawl,
			"max":    MaxSprawl,
		})
	}
	if !validNetworkShapes[p.NetworkShape] {
		return errs.New(codeInvalidNetworkShape, correlationID, map[string]any{
			"networkShape": string(p.NetworkShape),
		})
	}
	return nil
}
