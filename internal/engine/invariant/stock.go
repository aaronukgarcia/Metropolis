package invariant

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TermFunc reports one named flow component's contribution for the current
// tick. A TermFunc is a pure, deterministic function of the already-committed
// per-tick state its closure captures — exactly the same contract as
// SnapshotProvider (hook.go): same tick, same value, never the wall clock
// (AC-15), and never mutated by this package across ticks. It is evaluated
// fresh each tick by the invariant it is registered against (never memoized),
// so it must not carry per-tick mutable state of its own.
type TermFunc func() int64

// RegisterStock registers a single-term conserved stock — the plan's
// RegisterStock(name string, snapshot func() int64) inbound contract
// (master-plan-v2.1.json engine.invariant.inbound, BUG-058) made real.
//
// snapshot returns the stock's net tracked change for the tick: the single,
// pre-summed ins-minus-outs scalar a single-term owning module already
// computes. The registered invariant verifies the BUG-058 conservation
// identity — Closing - Opening == snapshot() — reading the level delta from
// the stock's StockReading in the per-tick Snapshot (the same single per-tick
// input every invariant reads), with identical balance arithmetic to the four
// v1 stock invariants (conservation.go's stockCheck).
//
// This is exactly RegisterStockWithTerms with one implicit inflow term:
//
//	RegisterStock(reg, name, stock, snapshot)
//	== RegisterStockWithTerms(reg, name, stock,
//	       map[string]TermFunc{"tracked_delta": snapshot}, nil)
//
// so it degenerates to the single-term form of the multi-term identity
// Σ(ins) − Σ(outs) with one term. snapshot must be non-nil (ErrNilTermFunc
// otherwise).
func RegisterStock(reg *Registry, name string, stock StockName, snapshot func() int64) error {
	// Copy guard on the *Registry entry (SEC-014/SEC-016/SEC-020 shape —
	// see Registry.checkNotCopied). reg arrives as a caller-supplied
	// parameter, so it is guarded here exactly as Registry.Register guards
	// its own receiver, before any of reg's state is reached.
	if err := reg.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RegisterStock"}); err != nil {
		return err
	}
	return registerStockTerms(reg, name, stock, map[string]TermFunc{"tracked_delta": snapshot}, nil)
}

// RegisterStockWithTerms registers a multi-term conserved stock (BUG-067):
// the stock declares its inflow and outflow terms as separate named functions,
// and the per-tick check verifies the BUG-058 conservation identity —
//
//	Closing - Opening == Σ(ins) − Σ(outs)
//
// — reading the level delta (Δsnapshot = Closing - Opening) from the stock's
// StockReading in the per-tick Snapshot, and evaluating every ins/outs term
// fresh each tick. A multi-term stock (e.g. population = births − deaths +
// arrivals − departures; refuse mass = generated − collected − composted −
// landfilled) is expressed directly, not pre-summed into a single scalar.
//
// ins/outs use map[string]TermFunc (never a bare slice) so every term carries
// a stable name that survives into a Violation's per-term breakdown
// (Violation.Terms) — "composted" beats outs[2] at 2am. Every term func must
// be non-nil (ErrNilTermFunc otherwise). The maps are defensively copied at
// registration, so mutating them afterwards never changes what is checked.
//
// A term name present in both ins and outs is legal and nets to its signed
// difference (the same key in Violation.Terms carries ins − outs).
func RegisterStockWithTerms(reg *Registry, name string, stock StockName, ins, outs map[string]TermFunc) error {
	// Copy guard on the *Registry entry (SEC-014/SEC-016/SEC-020 shape —
	// see Registry.checkNotCopied), same as RegisterStock above.
	if err := reg.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RegisterStockWithTerms"}); err != nil {
		return err
	}
	return registerStockTerms(reg, name, stock, ins, outs)
}

// registerStockTerms is the shared implementation behind RegisterStock and
// RegisterStockWithTerms: validate every term func is non-nil (GR#7 — fail at
// registration, not panic in the tick loop), defensively copy the term maps,
// and register a multiTermCheck under name via the existing Registry.Register
// (which still owns duplicate-name rejection, ErrDuplicateInvariant).
func registerStockTerms(reg *Registry, name string, stock StockName, ins, outs map[string]TermFunc) error {
	// Copy guard on the *Registry entry (defence in depth — the two
	// exported RegisterStock* entry points above each check first, and
	// reg.Register checks again; this keeps registerStockTerms safe on its
	// own if ever reached by a future non-entry-point path).
	if err := reg.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "registerStockTerms"}); err != nil {
		return err
	}
	for termName, fn := range ins {
		if fn == nil {
			return errs.New(ErrNilTermFunc, errs.NewCorrelationID(), map[string]any{"stock": string(stock), "term": termName, "side": "ins"})
		}
	}
	for termName, fn := range outs {
		if fn == nil {
			return errs.New(ErrNilTermFunc, errs.NewCorrelationID(), map[string]any{"stock": string(stock), "term": termName, "side": "outs"})
		}
	}

	return reg.Register(multiTermCheck{
		name:  name,
		stock: stock,
		ins:   cloneTerms(ins),
		outs:  cloneTerms(outs),
	})
}

// cloneTerms returns a shallow copy of src's entries so a registered invariant
// never aliases a caller's still-mutable map (weakness pattern #1: a late
// mutation of the registration map must not silently change what is checked).
// nil for an empty map — an invariant with no terms checks a zero-flow stock
// (Closing - Opening == 0).
func cloneTerms(src map[string]TermFunc) map[string]TermFunc {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]TermFunc, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// multiTermCheck is the Invariant RegisterStock/RegisterStockWithTerms
// register: it verifies one stock's balance as
// Closing - Opening == Σ(ins) − Σ(outs), reading Opening/Closing from the
// stock's StockReading (the same per-tick Snapshot every other invariant
// reads) and evaluating the registered term functions fresh each tick. It is
// the term-splitting sibling of stockCheck (conservation.go), which runs the
// identical arithmetic against a pre-summed StockReading.TrackedDelta.
type multiTermCheck struct {
	name  string
	stock StockName
	ins   map[string]TermFunc
	outs  map[string]TermFunc
}

// Name implements Invariant.
func (m multiTermCheck) Name() string { return m.name }

// Check implements Invariant: verifies m.stock's balance identity for
// state.Tick, or reports Ran: false if m.stock is not (yet) registered in
// state (AC-12) — the same skip semantics as stockCheck.Check.
func (m multiTermCheck) Check(state Snapshot) Result {
	reading, ok := state.Reading(m.stock)
	if !ok {
		return Result{Ran: false}
	}

	tracked, terms, overflowed := evalTerms(m.ins, m.outs)
	actual, actualOverflowed := satSub(reading.Closing, reading.Opening)
	if !overflowed && !actualOverflowed && actual == tracked {
		return Result{Ran: true}
	}

	entityIDs := append([]string(nil), reading.Suspects...) // defensive copy
	return Result{
		Ran:       true,
		Violation: newMultiTermViolation(m.name, state.Tick, tracked, actual, entityIDs, terms),
	}
}

// evalTerms evaluates every registered inflow/outflow term for the current
// tick and returns (Σ(ins) − Σ(outs), signed per-term breakdown, overflowed).
// The breakdown uses a sign convention — inflow positive, outflow negative —
// so Σ(Terms) == Σ(ins) − Σ(outs) == tracked. A name present in both ins and
// outs nets to ins − outs under that one key.
//
// Accumulation is overflow-safe (SEC-055): tracked is summed with saturating
// arithmetic, and overflowed reports whether saturation ever occurred, so a
// set of terms whose true sum exceeds int64 range can never wrap into a value
// that happens to equal Closing − Opening and reports "balanced" on a
// wildly-false identity — multiTermCheck.Check turns an overflowed sum into a
// Detected Violation instead of trusting the comparison. Because saturating
// arithmetic is NOT order-independent (unlike wrapping int64 addition), terms
// are evaluated in sorted-name order so the sum, the breakdown, and the
// overflowed flag are all deterministic across Go's randomised map iteration
// (AC-13). Term funcs must be pure (no side effects) so their evaluation
// order is irrelevant.
func evalTerms(ins, outs map[string]TermFunc) (int64, map[string]int64, bool) {
	terms := make(map[string]int64, len(ins)+len(outs))
	var tracked int64
	overflowed := false
	for _, name := range sortedTermNames(ins) {
		v := ins[name]()
		var o bool
		tracked, o = satAdd(tracked, v)
		overflowed = overflowed || o
		terms[name], _ = satAdd(terms[name], v)
	}
	for _, name := range sortedTermNames(outs) {
		v := outs[name]()
		var o bool
		tracked, o = satSub(tracked, v)
		overflowed = overflowed || o
		terms[name], _ = satSub(terms[name], v)
	}
	return tracked, terms, overflowed
}

// sortedTermNames returns m's keys in ascending order so accumulation over a
// map is deterministic. Go randomises map iteration order, which is harmless
// for wrapping int64 addition (order-independent) but not for saturating
// arithmetic (order-dependent), so evalTerms iterates in a fixed order to keep
// AC-13's determinism guarantee under the SEC-055 overflow fix.
func sortedTermNames(m map[string]TermFunc) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// newMultiTermViolation constructs a Detected Violation for a multi-term
// stock (multiTermCheck), identical to newViolation (same Message,
// expected/actual/entityIDs semantics) but additionally carrying the signed
// per-term breakdown (Terms) so a consumer can see which named flow is wrong,
// not just one opaque unexplained number. terms is passed through unchanged
// (never interpolated into Message — AC-11b).
func newMultiTermViolation(name string, tick, expected, actual int64, entityIDs []string, terms map[string]int64) Violation {
	v := newViolation(name, tick, expected, actual, entityIDs)
	v.Terms = terms
	return v
}
