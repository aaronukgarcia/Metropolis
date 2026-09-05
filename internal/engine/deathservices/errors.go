package deathservices

// Registry error codes for engine.deathservices (MOD-083). Range:
// G5440-G5479, claimed via `node tools/plan/add-error.js claim-range
// engine.deathservices --size 40 --layer G` (BUG-273's allocator) and
// added one-by-one via `node tools/plan/add-error.js add ...` (GR#7 -- no
// code below was hand-minted; every one IS registered in data/errors.json
// with real severity/module/message/remedy fields, guarded against drift
// by internal/foundation/errs' source-scan test).
const (
	// ErrDeathServicesCopied: a *DeathServicesAPI method was called on a
	// struct copy of the value NewDeathServicesAPI/Load returned (SEC-020
	// family, mirroring citizens.ErrAPICopied/ErrDeathQueueCopied). mu is a
	// sync.Mutex VALUE while every map/slice field is a reference type a
	// copy would alias -- an unrejected copy is a second, independent lock
	// racing the original over the same referents.
	ErrDeathServicesCopied = "MET-G5440"

	// ErrDeathServicesDataInvalid: data/deathservices.json is missing,
	// malformed, or fails its own schema validation. Rejected at load time
	// rather than falling back to a silently-invented default (GR#15) --
	// mirrors citizens.ErrMortalityDataInvalid.
	ErrDeathServicesDataInvalid = "MET-G5441"

	// ErrUnknownCemetery: a burial/reuse-recovery call named a cemetery ID
	// that was never registered via RegisterCemetery. Rejected rather than
	// silently fabricating a zero-capacity cemetery (AC-2/AC-17).
	ErrUnknownCemetery = "MET-G5442"

	// ErrUnknownCrematorium: a cremation call named a crematorium ID that
	// was never registered via RegisterCrematorium. Rejected rather than
	// silently fabricating an unbounded crematorium (AC-5/AC-17).
	ErrUnknownCrematorium = "MET-G5443"

	// ErrNoPlotAvailable: every plot in the named cemetery is occupied and
	// none has reached its reuse-eligibility horizon (AC-4's "fills
	// permanently -- land pressure" saturation triage). Bury never wraps
	// around or silently extends capacity; the caller is expected to route
	// the body to cremation or emergency dispensation instead.
	ErrNoPlotAvailable = "MET-G5444"

	// ErrUnknownBody: a call referenced a bodyID that intake never produced
	// (AC-14/AC-17). Never fabricates a phantom body record.
	ErrUnknownBody = "MET-G5445"

	// ErrBodyAlreadyHandled: a disposal call (Bury/Cremate/Dispense) was
	// made against a body that already reached a terminal state (AC-15 --
	// burial, cremation, and dispensation are mutually exclusive, a body is
	// handled exactly once). Rejected rather than silently re-disposing,
	// which would corrupt the AC-14 conservation identity by double-
	// counting one body across two terminal buckets.
	ErrBodyAlreadyHandled = "MET-G5446"

	// ErrDuplicateDeath: Intake observed a RealisedDeath for a citizenID
	// that already has a body record from a prior Intake call (AC-1 --
	// every death produces exactly one body, never a duplicate).
	ErrDuplicateDeath = "MET-G5447"

	// ErrMultiBodyOutsideDispensation: a caller attempted to move more than
	// one body in a single trip while dispensation is not active (AC-11/
	// AC-12 -- the multi-body lift is gated strictly on the dispensation
	// signal and reverts the instant the event ends).
	ErrMultiBodyOutsideDispensation = "MET-G5448"

	// ErrUnknownBuildingType: a RegisterCemetery/RegisterCrematorium-style
	// call named a building kind this module does not recognise.
	ErrUnknownBuildingType = "MET-G5449"

	// ErrPlotNotReusable: an internal invariant check found a plot that has
	// not yet reached its configured reuse horizon being treated as
	// allocatable (AC-3 -- defence in depth; the normal allocator path
	// never reaches this, since it filters ineligible plots before
	// selecting one).
	ErrPlotNotReusable = "MET-G5450"

	// ErrNegativeBudget: a non-positive throughput/transport budget was
	// supplied where a caller-programming error (not a normal zero-budget
	// month) is suspected. Logged as a diagnosability aid (GR#17), mirrors
	// citizens.ErrNegativeDrainCapacity's WARNING-not-fatal stance.
	ErrNegativeBudget = "MET-G5451"

	// ErrCorruptHandoffCursor (BUG-689 round follow-up F6, BUG-720 P2
	// follow-up, BUG-725): a decoded deathservices.meta save record carried
	// an impossible handoffCursor -- either NEGATIVE or OVER-LENGTH
	// (at-or-past the real citizens handoff stream's length, including
	// math.MaxInt64). No code in this codebase ever WRITES either shape
	// (snapshotForSave always mirrors d.handoffCursor, which only ever
	// advances by len(deaths) in IntakeFromHandoff) -- this can only be a
	// hand-edited or corrupt bundle, or a future format skew. This ONE code
	// covers BOTH directions, logged with "direction" ("negative" or
	// "over_length"), the original "handoffCursor" value, and the
	// "clampedTo" value it was corrected to, never fatal (GR#17
	// diagnosability aid, mirrors ErrNegativeBudget/
	// citizens.ErrNegativeDrainCapacity's stance):
	//
	//   - negative: applyLoadRecord (participant.go) clamps the installed
	//     value to 0 AT DECODE TIME (never installs the negative verbatim).
	//     A clamped cursor of 0 re-delivers the whole handoff stream once,
	//     which IntakeFromHandoff's own duplicate-death guard renders safe
	//     -- self-corrects within one month (the F6 finding this closed).
	//   - over-length: decode CANNOT correct this -- deathservices' decode
	//     step never holds a citizens reference (GR#20), so it cannot learn
	//     the real stream length "impossible relative to". Left verbatim at
	//     decode, it is instead detected and clamped to 0 at the FIRST
	//     intake call, in compose's intakeDeathServices (see that
	//     function's doc comment for the full argument), which then
	//     re-reads the full stream from 0 -- self-correcting in the SAME
	//     driven month rather than being permanently wedged (the over-length
	//     cursor otherwise makes DeathHandoffSince return empty forever, so
	//     IntakeFromHandoff is never called and the cursor never advances --
	//     the BUG-720/BUG-725 finding this direction closed).
	ErrCorruptHandoffCursor = "MET-G5452"
)
