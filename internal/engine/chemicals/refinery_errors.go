package chemicals

// Registry error codes for feat.refinery, which shares the
// internal/engine/chemicals package with engine.chemicals. Each code below is
// registered in data/errors.json with real severity/module/message/remedy
// fields (GR#7); the numeric range is claimed there too — see doc.go for the
// range-claim narrative and this file's package doc for the ASM scope split.
const (
	// ErrRefineryDataInvalid: data/refinery.json could not be loaded or failed
	// schema validation — a missing/zero throughput, a negative utility draw,
	// an unrecognised facility/stage name, a missing disclosure, a non-positive
	// import margin. The catalogue never falls back to a partial or
	// default-substituted set (AC-9, GR#15).
	ErrRefineryDataInvalid = "MET-G2600"

	// ErrUnknownRefineryFacility: a facility resolve named a key that is not
	// one of the two modelled facilities (refinery / petrochemical_works).
	// Rejected rather than silently default-substituted (AC-9).
	ErrUnknownRefineryFacility = "MET-G2601"

	// ErrRefineryNotBuilt: operating the refinery or feeding its fuel output
	// was requested before it is built. Rejected — never a silently-emitted
	// zero fuel, no stage registered, no incident reported (AC-9).
	ErrRefineryNotBuilt = "MET-G2602"

	// ErrUnregisteredStage: routing feedstock to, or querying, a chain stage
	// that is not registered against ChemAPI (AC-9).
	ErrUnregisteredStage = "MET-G2603"

	// ErrRefineryNotWired: an operation required an outbound edge (freight,
	// fuel, dispatch, permit, decommission) that is not wired (GR#20).
	ErrRefineryNotWired = "MET-G2604"

	// ErrRefineryCopied: a method was called on a struct-copied Refinery or
	// ChemAPI value rather than the pointer its constructor returned.
	ErrRefineryCopied = "MET-G2605"

	// ErrRefineryBuildRejected: the refinery build was refused — either the
	// permit authority did not grant it, or it is already built.
	ErrRefineryBuildRejected = "MET-G2606"

	// ErrRefineryNegativeCrude: Operate was called with negative or zero crude
	// tonnes (AC-9). A negative crude request is rejected at the API boundary
	// rather than passed downstream to the freight seam, which would report a
	// nonsensical negative landing (SEC-165).
	ErrRefineryNegativeCrude = "MET-G2607"
)
