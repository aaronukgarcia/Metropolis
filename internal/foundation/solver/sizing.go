package solver

// Sizing constants transcribe the A4/A9 capacity-planning figures from the
// spec as typed Go values, so engine and F12-panel code reference one
// place instead of re-deriving or hardcoding these numbers at each call
// site (GR#15: validators derive from data, not hardcoded constants
// scattered across the codebase — this file IS that data for the solver
// seam).
//
//	Quantity                          Value                     Spec
//	---------------------------------------------------------------------
//	OD zone count, low end            1,000 (1e3)               A4
//	OD zone count, high end           10,000 (1e4)               A4
//	OD matrix worked example          5,000 zones ≈ "~100MB"     A4/R3
//	Road graph edges, low end         100,000 (1e5)              R3
//	Road graph edges, high end        1,000,000 (1e6)            R3
//	GPU sidecar VRAM envelope         4 GB (RTX 3050-class)      M0-ENG §1
//	Local-CPU citizen ceiling         20-30 million citizens     A9
//
// See EstimateODBytes for an OPEN QUESTION: this package computes OD
// bytes as zones² × 8 (float64 cells, per this BoW item's own payload
// spec), which does not reproduce R3's "~5,000 zones ⇒ ~100MB" figure
// (that math implies 4-byte cells). Flagged for freeze review rather than
// silently choosing a cell width to force a match — see
// docs/design/solver-contract.md.
const (
	// ODZoneCountMin/Max bound the zone-aggregated OD matrix size (A4:
	// "~10^3-10^4 zones on the road graph").
	ODZoneCountMin = 1_000
	ODZoneCountMax = 10_000

	// ReferenceODZoneCount is R3's worked example ("~5,000 zones ⇒
	// ~100MB of OD"), kept as a named constant for tests/docs to point at
	// even though EstimateODBytes does not reproduce the ~100MB figure
	// exactly under float64 cells (see the open question above).
	ReferenceODZoneCount = 5_000

	// RoadGraphEdgesMin/Max bound expected road graph size "even
	// late-game" (R3: "order 10^5-10^6 edges").
	RoadGraphEdgesMin = 100_000
	RoadGraphEdgesMax = 1_000_000

	// GPUVRAMEnvelopeBytes is the VRAM budget the GPU sidecar tier must
	// fit inside on the reference RTX 3050-class card (M0-ENG §1: "~4 GB
	// VRAM"). "Road graph + OD matrices + flow vectors ≪ 4 GB VRAM at
	// full 60x60 km" (M0-ENG §1.3) — the sidecar is expected to use a
	// small fraction of this, not fill it.
	GPUVRAMEnvelopeBytes int64 = 4 * 1024 * 1024 * 1024

	// LocalCitizenCeilingLow/High bound the population range beyond which
	// A9 says local CPU is no longer expected to cover the simulation
	// end-to-end, and cold-pass/assignment work should be considered for
	// the GPU sidecar or cloud tiers. "The player experience is identical
	// at every tier" (A9) — crossing this threshold changes only where
	// computation happens, never what the player sees.
	LocalCitizenCeilingLow  int64 = 20_000_000
	LocalCitizenCeilingHigh int64 = 30_000_000

	// odCellBytes is sizeof(float64): the traffic assignment payload
	// stub (problems.go) stores OD costs/flows as float64, so that is
	// the cell width used here. See the package doc comment above for
	// why this does not reproduce R3's "~100MB at 5,000 zones" example.
	odCellBytes int64 = 8

	// defaultRoadEdgeBytes is a conservative, placeholder per-edge
	// footprint (endpoints + weight + VDF params, packed) used by
	// EstimateRoadGraphBytes when the caller does not supply one.
	// TODO-SPEC: replace with the real engine.roads edge struct size
	// once that module lands.
	defaultRoadEdgeBytes int64 = 64
)

// EstimateODBytes returns the approximate byte size of a dense
// zones x zones origin-destination matrix of float64 cells (A4/R3).
// Returns 0 for zones <= 0.
//
// OPEN QUESTION (freeze review): at ReferenceODZoneCount (5,000),
// EstimateODBytes returns 200MB, not the spec's "~100MB" — see the file
// doc comment. Until resolved, treat this function's output as the
// conservative (2x) estimate.
func EstimateODBytes(zones int) int64 {
	if zones <= 0 {
		return 0
	}
	z := int64(zones)
	return z * z * odCellBytes
}

// EstimateRoadGraphBytes returns a rough byte-size estimate for a road
// graph of the given edge count. bytesPerEdge lets callers supply a
// measured per-edge footprint; passing <= 0 falls back to
// defaultRoadEdgeBytes, a placeholder pending the real engine.roads edge
// layout. Returns 0 for edges <= 0.
func EstimateRoadGraphBytes(edges int, bytesPerEdge int64) int64 {
	if edges <= 0 {
		return 0
	}
	if bytesPerEdge <= 0 {
		bytesPerEdge = defaultRoadEdgeBytes
	}
	return int64(edges) * bytesPerEdge
}

// FitsGPUEnvelope reports whether a workload of the given total byte size
// fits inside GPUVRAMEnvelopeBytes, per M0-ENG §1's "≪ 4 GB VRAM" sizing
// intent. It is a rough capacity check for the F12 panel and offload
// heuristics, not a guarantee: actual VRAM usage also depends on a
// backend's working-set overhead beyond the raw data size.
func FitsGPUEnvelope(totalBytes int64) bool {
	return totalBytes >= 0 && totalBytes < GPUVRAMEnvelopeBytes
}

// ExceedsLocalCPUCeiling reports whether citizenCount is at or beyond the
// A9 threshold where local CPU is no longer expected to carry the full
// simulation end-to-end, meaning the GPU sidecar or cloud tiers should be
// considered for offload-eligible work. It uses the conservative (low)
// end of the A9 range (20M) so the signal fires before players notice
// slowdown; ExceedsLocalCPUCeilingHigh below uses the top of the range.
func ExceedsLocalCPUCeiling(citizenCount int64) bool {
	return citizenCount >= LocalCitizenCeilingLow
}

// ExceedsLocalCPUCeilingHigh reports whether citizenCount is at or beyond
// the top of A9's range (30M), i.e. local CPU is now expected to be
// insufficient rather than merely approaching its limit.
func ExceedsLocalCPUCeilingHigh(citizenCount int64) bool {
	return citizenCount >= LocalCitizenCeilingHigh
}
