package projections

// Registry error codes for engine.projections (MOD-031).
//
// engine.projections owns the G100-G199 sub-range (data/errors.json
// ranges.reserved; the E-layer is fully exhausted, and engine.citizens
// holds the first overflow block G000-G099 — this is the second G-layer
// engine claim). BUG-233 registered ten codes at G100-G109 and repointed
// the constants below off the codes they had temporarily borrowed from
// other modules while data/errors.json was claimed by another window
// (the tenth, MET-G109, is ErrCopiedValue's own copy-guard code — the
// BUG-233 addendum residual, resolved here).
const (
	// ErrUnknownCurveKey: Curve()/Threshold() was queried with a curve
	// or threshold key no provider has registered (AC-9). No zero-value
	// curve is returned.
	ErrUnknownCurveKey = "MET-G100"

	// ErrNegativeMonthQuery: a month-query argument is out of bounds — a
	// month index before the world's epoch (< 0), an inverted range
	// (fromMonth > toMonth), or a range wider than maxCurveQueryMonths
	// (projections.go). All three are the same contract violation ("the
	// month arguments to a curve query are not usable"); Ctx always
	// carries "monthIndex", the specific value that made the query
	// invalid. The wide/inverted-range cases are the BREAK-3 fix from the
	// MOD-031 Destructive round (BUG-214 bounds guard) — rejected before
	// the result buffer is allocated, so an oversized range errors rather
	// than overflowing make().
	ErrNegativeMonthQuery = "MET-G101"

	// ErrInvalidFuseYears: EnqueueDecision was called with a non-finite
	// (NaN/+Inf/-Inf) or negative Decision.FuseYears (BREAK-1, Destructive
	// round). A5's Slow-Fuse gate (decisions.go's slowFuseGate) compares
	// FuseYears against slowFuseThresholdYears with a bare ">"; Go's
	// IEEE-754 rules make every ordering comparison against NaN false, so
	// an uncaught NaN silently reads as "under threshold" and sails
	// through ungated — defeating A5 for exactly the long-fuse decision it
	// exists to catch. Rejected before the threshold test runs. Negative
	// FuseYears is rejected too: a decision cannot land a negative number
	// of years out, so a negative value signals corrupted input upstream
	// (it would only ever have made the gate less strict, so this closes a
	// latent gap rather than changing observed behaviour). Ctx carries the
	// offending value.
	ErrInvalidFuseYears = "MET-G102"

	// ErrSlowFuseMissingPayload: a decision carrying FuseYears > 5 was
	// submitted through EnqueueDecision without a non-nil/non-empty
	// ProjectedConsequence payload (AC-10, A5's Slow-Fuse gate). Ctx names
	// both the decision id and the missing payload.
	ErrSlowFuseMissingPayload = "MET-G103"

	// ErrUnknownDecision: CancelDecision was called with an id no
	// EnqueueDecision call registered.
	ErrUnknownDecision = "MET-G104"

	// ErrNilCurveProvider: RegisterCurveProvider was called with a nil
	// provider.
	ErrNilCurveProvider = "MET-G105"

	// ErrDuplicateCurveProvider: RegisterCurveProvider was called twice
	// with the same key — rejected, never a silent overwrite (GR#7/GR#15:
	// a silently-overwritten curve provider would be as dangerous as a
	// silently-substituted default).
	ErrDuplicateCurveProvider = "MET-G106"

	// ErrEmbeddedConfigInvalid: this package's own embedded config
	// (horizon.json / deathwarnings.json, both go:embed'd at build time —
	// see config.go) failed to unmarshal. Should be unreachable in a built
	// binary (the embedded bytes are fixed at compile time and covered by
	// this package's loader tests), but every query method that depends on
	// the loaded config returns this registry-sourced error rather than
	// panicking if it somehow is reached — the "should be unreachable,
	// fail loudly anyway" convention engine.season/engine.market also use.
	ErrEmbeddedConfigInvalid = "MET-G107"

	// ErrGhostCityProviderShape: the provider registered under the
	// reserved MarginToGhostCity curve key does not additionally implement
	// GhostCityPeakProvider (HistoricPeak), so AC-18's dual-threshold check
	// cannot be evaluated.
	ErrGhostCityProviderShape = "MET-G108"

	// ErrCopiedValue: a *ProjectionsAPI method was called on a
	// struct-copied value, not the one NewProjectionsAPI constructed
	// (SEC-020-class hazard: a copy gets its own, independently-zeroed mu
	// but ALIASES the original's providers/thresholds/decisions maps and
	// the ledger pointer). Owns MET-G109, this module's dedicated
	// copy-guard code (BUG-233 addendum) — formerly borrowed from
	// engine.invariant's MET-E302, whose message named the wrong
	// constructor.
	ErrCopiedValue = "MET-G109"
)
