package solver

// ProblemKind enumerates the offload slots the solver contract currently
// knows about. §15/A4 name four: traffic equilibrium, cold-pass batch,
// deep projections, and batch life-writing ("hybrid solver offload slots
// (traffic equilibrium, deep projections, batch life-writing) as
// stateless request/response with local-fallback"; cold-pass batch sizing
// is folded into the same seam by A4). EchoProblem is not a real offload
// slot — it exists purely so cpu.go has one trivial, fully-implemented
// path, letting registry/fallback/determinism plumbing be tested before
// any real engine module lands.
type ProblemKind int

const (
	// EchoProblem is the plumbing-proof kind: CPUBackend implements it for
	// real (see cpu.go) and the test suite exercises registration,
	// fallback, and determinism against it. Never used by engine code.
	EchoProblem ProblemKind = iota

	// TrafficAssignment is the daily capacity-restrained stochastic user
	// equilibrium solve (§19, §21, §51; A4 zone-aggregated OD sizing):
	// iterative link-time / volume-delay assignment over the road graph
	// and an aggregated origin-destination matrix. See
	// TrafficAssignmentRequestV1.
	TrafficAssignment

	// ColdPassBatch is the batch update for citizens outside the HOT
	// simulation sample: monthly-batch mathematics fitted from the hot
	// sample, applied to the cold population in bulk (§17-§18; A4/A9
	// sizing). TODO-SPEC: payload schema firms up with engine.citizens.
	ColdPassBatch

	// DeepProjection is the multi-decade forward projection used for
	// long-horizon planning views and the Azure Batch balance-tuning
	// workflow (§15 cloud path: "Azure Batch headless balance-tuning
	// during development — thousands of parameter runs, centuries per
	// run"). TODO-SPEC: payload schema firms up with the projection
	// engine module.
	DeepProjection

	// LifeWriting is the batch generation/update of citizen life-history
	// narrative content (education drift, biography events) at
	// population scale (§5, §17). TODO-SPEC: payload schema firms up with
	// engine.citizens.
	LifeWriting
)

// String renders a human-readable ProblemKind name, for logs and the F12
// panel.
func (p ProblemKind) String() string {
	switch p {
	case EchoProblem:
		return "EchoProblem"
	case TrafficAssignment:
		return "TrafficAssignment"
	case ColdPassBatch:
		return "ColdPassBatch"
	case DeepProjection:
		return "DeepProjection"
	case LifeWriting:
		return "LifeWriting"
	default:
		return "ProblemKind(unknown)"
	}
}

// TrafficAssignmentRequestV1 is the versioned payload schema for
// TrafficAssignment requests (Request.SchemaVersion == 1). Per A4
// discipline, routing never touches cells: it runs on the road graph
// (~10^5-10^6 edges, R3) with zone-aggregated OD matrices (~10^3-10^4
// zones, A4) — see sizing.go for the byte-size tables this implies.
//
// The reference fields below are handles (e.g. shard IDs, snapshot
// generation numbers), not embedded data: a solver backend is expected to
// already have, or fetch, the road graph and OD matrix by reference,
// keeping the request payload itself small even though the referenced
// data is large. The exact reference encoding is still open — see
// docs/design/solver-contract.md's open questions.
type TrafficAssignmentRequestV1 struct {
	// ZoneCount is the number of OD zones (A4: ~10^3-10^4).
	ZoneCount int

	// GraphRef identifies the road graph snapshot to assign against
	// (~10^5-10^6 edges, R3). Opaque reference, format TBD.
	GraphRef string

	// ODMatrixRef identifies the zone x zone OD matrix to assign against
	// (R3 worked example: ~5,000 zones ⇒ ~100MB of OD, see sizing.go).
	ODMatrixRef string

	// VDFParams are the volume-delay function parameters (BPR-style)
	// applied per link during assignment (§19).
	VDFParams VDFParamsV1

	// MaxIterations bounds the capacity-restrained equilibrium inner loop
	// (§19: "converges in a few iterations on cached warm starts").
	MaxIterations int

	// ConvergenceEpsilon is the relative-gap (or equivalent) stopping
	// threshold for the equilibrium loop.
	ConvergenceEpsilon float64
}

// VDFParamsV1 is the volume-delay function parameter set used by
// TrafficAssignmentRequestV1, BPR-style:
//
//	travelTime = freeFlowTime * (1 + Alpha*(volume/capacity)^Beta)
//
// TODO-SPEC: firms up alongside engine.traffic and engine.roads — may
// become per-link-class rather than one global parameter set.
type VDFParamsV1 struct {
	Alpha float64
	Beta  float64
}

// ColdPassBatchRequestV1 is a minimal placeholder payload schema for
// ColdPassBatch. TODO-SPEC: firms up with engine.citizens — expected to
// carry a cold-shard reference set and the fitted batch-update parameters
// measured from the hot sample (§17-§18).
type ColdPassBatchRequestV1 struct {
	ShardRefs []string
	Month     int
}

// DeepProjectionRequestV1 is a minimal placeholder payload schema for
// DeepProjection. TODO-SPEC: firms up with the projection engine module —
// expected to carry a world-snapshot reference, a projection horizon, and
// the parameter sweep for Azure Batch balance-tuning (§15).
type DeepProjectionRequestV1 struct {
	WorldSnapshotRef string
	HorizonMonths    int
}

// LifeWritingRequestV1 is a minimal placeholder payload schema for
// LifeWriting. TODO-SPEC: firms up with engine.citizens — expected to
// carry a citizen-id batch reference and the biography-event trigger set.
type LifeWritingRequestV1 struct {
	CitizenShardRefs []string
	Month            int
}
