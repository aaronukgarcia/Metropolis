package compose

// Registry error codes for feat.compositionroot (FEAT-082). Range:
// G800-G899, declared in data/errors.json's ranges.reserved table (the E
// layer was exhausted by the eleven earlier engine modules; G800-G899 is
// the next free engine block after attract's G700-G799). Every code below
// is registered there with real severity/module/message/remedy fields
// (GR#7). A compose error is always raised via errs.New/errs.Wrap — never
// a bare fmt.Errorf (AC-16).
const (
	// ErrAlreadyComposed: Wire was called on a *core.Engine that already
	// has PhaseHooks registered. The composition root is the only real
	// hook registrar, so a non-zero hook count on entry means the engine
	// was already composed (or externally tampered with). A second Wire
	// call is rejected rather than silently appending duplicate hooks
	// (AC-3).
	ErrAlreadyComposed = "MET-G800"

	// ErrModuleFailed: a required module's construction or wiring failed
	// (e.g. market.LoadDefault could not load data/market.json). The
	// ctx["module"] field names the failed module. Wire returns this
	// without leaving a partially-wired engine (AC-4).
	ErrModuleFailed = "MET-G801"

	// ErrRequiredModuleMissing: a required module was silently absent from
	// the wired set (registering N-1 of N required modules). Distinct from
	// ErrModuleFailed: this is the "quiet success" guard, raised when a
	// module neither constructed nor errored but simply is not present in
	// the composition (AC-4).
	ErrRequiredModuleMissing = "MET-G802"

	// ErrWiringAfterSeal: Wire was invoked after the Engine sealed its
	// hook set (the first AdvanceTicks already ran). Wraps
	// core.ErrEngineSealed (and, via the invariant path,
	// invariant.ErrWiringAfterSeal) so the original cause stays reachable
	// through errors.Unwrap (AC-6).
	ErrWiringAfterSeal = "MET-G803"

	// ErrGameplayRejectionPassthrough (BUG-267): a gameplay-command reject
	// that carries an already-rendered Display string from another
	// module's registry error (e.g. world.PurchaseTile's ErrorRef, whose
	// Code/Display were rendered against that module's OWN registry
	// template and ctx keys). Re-wrapping such a rejection under the
	// ORIGINAL module's code with a ctx keyed "display" left that code's
	// real template placeholders ({tile}/{cause} for MET-E404) unresolved
	// (the ctx key didn't match). This code's template is exactly
	// "{display}" — a deliberate pass-through, never re-rendered against a
	// foreign module's placeholders.
	ErrGameplayRejectionPassthrough = "MET-G804"

	// ErrCitizenIDNamespaceSeam (FEAT-169, corrected 2026-08-18 by
	// destructive-review REJECT): simState.nextCitizenID's sequential
	// migrant/seed counter reached attract.MigrantIDBase — the disjoint
	// high-bit namespace engine.attract mints admitted-migrant citizen ids
	// from (migration.go's migrantIDHighBit). This is compose's OWN range
	// guard, the first of three disjoint id ranges: compose [1, 2^62),
	// attract migrants [2^62, 2^63), citizens fertility children
	// [2^63, ...) (citizens.FertilityChildIDBase). ORIGINALLY bounded
	// against FertilityChildIDBase alone (2^62 at the time) — destructive
	// review found that would have let compose's counter silently drift
	// into attract's migrant range first without ever tripping this check,
	// since attract's migrantIDHighBit occupied the exact same 2^62 base.
	// The three id spaces are a documented, verified-disjoint CONTRACT, not
	// a shared allocator (the ICD's §12 open decision 2, amended): this
	// guard fires before compose ever mints a colliding id, cheaply, on
	// every mint (not gated behind a debug build — the check is a single
	// uint64 comparison).
	ErrCitizenIDNamespaceSeam = "MET-G805"

	// ErrIDNamespaceRangesOverlap (FEAT-169 destructive-review REJECT
	// finding): the Wire-time cross-check that citizens.FertilityChildIDBase
	// is at least 2x attract.MigrantIDBase — the boundary BETWEEN
	// engine.attract's migrant id range and engine.citizens' fertility
	// child id range, distinct from ErrCitizenIDNamespaceSeam (which
	// defends compose's OWN counter against the migrant boundary only).
	// Both sides are compile-time constants today; this should never fire
	// in production, but a future edit to either base breaking the
	// three-package disjoint id map now fails loudly at every Wire call
	// instead of silently overlapping (the original bug: attract's
	// migrantIDHighBit and citizens' fertilityChildIDBase both
	// independently started at 1<<62).
	ErrIDNamespaceRangesOverlap = "MET-G806"

	// ErrInvalidWireAmount (BUG-308): extcommute_wire.go's
	// extCommuteFinanceSeam.post rejected a negative amountMicropounds
	// from a FinanceSeam verb (RecordOffMapWage/RemoveOffMapWage/
	// RecordBusinessRates/RecordCorpShare/RecordWageLeakage). A negative
	// amount posted through post's debit/credit pair would reverse the
	// credit flow, silently breaking money conservation (GR#16) — this
	// code is raised instead of posting the sign-flipped transaction.
	ErrInvalidWireAmount = "MET-G807"
)
