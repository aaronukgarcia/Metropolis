package projections

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ProjectedConsequence is the attached-at-confirmation-time payload
// A5's Slow-Fuse Principle requires for any decision whose FuseYears
// exceeds slowFuseThresholdYears — the "render its projected
// consequence in the confirmation UI at the moment of decision" the
// spec's own words describe. Description is the human-readable
// consequence text a confirmation dialog would show; Series is an
// optional rendered curve snippet (e.g. from a prior Curve call) a
// richer confirmation UI can chart. At least one of the two must be
// populated — see empty.
type ProjectedConsequence struct {
	Description string
	Series      []Point
}

// empty reports whether c counts as "no projected-consequence payload
// attached" for the Slow-Fuse gate's purposes (AC-5/AC-10): a nil
// pointer, or a non-nil pointer with neither prose nor a genuinely
// informative rendered series, is empty.
//
// LOWER-SEV fix (Destructive round, this build): the original check
// was `Description == "" && len(Series) == 0`, so a Series holding
// nothing but zero-value Points — e.g. []Point{{}} — counted as
// "non-empty" and satisfied the gate despite carrying zero real
// information (no month, no value, no confidence marker, nothing a
// confirmation UI could actually render). A Series is now only
// treated as present if it contains at least one Point that is not
// the exact zero value; an all-zero Series is, correctly, still empty.
func (c *ProjectedConsequence) empty() bool {
	if c == nil {
		return true
	}
	if c.Description != "" {
		return false
	}
	for _, pt := range c.Series {
		if pt != (Point{}) {
			return false
		}
	}
	return true
}

// slowFuseThresholdYears is A5's own spec-stated Slow-Fuse rule ("any
// decision whose principal effects land more than 5 game-years out")
// — a structural rule threshold quoted directly from the master doc's
// R5 adjudication, not a tunable balance number (GR#15's data-sourcing
// requirement targets game-balance magnitudes like consumption
// coefficients and market prices; A5's "5" is the design rule itself,
// the same distinction engine.season draws for its
// schoolIntakeGateThreshold — see that package's doc comment).
const slowFuseThresholdYears = 5

// Decision is a decision submitted through the confirmation path
// EnqueueDecision gates (US-3/US-4). Type is deliberately a free-form
// string, never a closed Go enum of A5's five named examples
// (education/planning/rehabilitation/BDI/debt) — AC-5's whole point is
// that slowFuseGate below must reject an unknown sixth Type exactly as
// readily as any of the five, since it operates only on FuseYears/
// Consequence, never on Type itself.
type Decision struct {
	// ID uniquely identifies this queued decision (for later
	// cancellation via CancelDecision).
	ID string

	// Type names the decision kind for logging/debugging only — the
	// Slow-Fuse gate below never branches on it (AC-5).
	Type string

	// CurveKey is the registered curve this decision's step affects
	// once queued (UI-SPEC §4's decision-marker idiom, AC-4). Empty if
	// this decision carries no curve-visible step (e.g. a policy
	// change with no single capacity metric).
	CurveKey string

	// CompletionMonth is the absolute month index (engine.core's
	// Clock.Month() convention) at which this decision's effect lands.
	CompletionMonth int64

	// Delta is the step Curve queries for CurveKey apply from
	// CompletionMonth onward, until the real system's own provider
	// catches up and this queued step is cancelled (AC-4).
	Delta float64

	// FuseYears is A5's own tag: this decision's principal effect
	// lands FuseYears game-years out. A value greater than
	// slowFuseThresholdYears requires Consequence to be non-empty.
	FuseYears float64

	// Consequence is the projected-consequence payload the Slow-Fuse
	// gate requires for FuseYears > slowFuseThresholdYears (AC-5).
	Consequence *ProjectedConsequence
}

// queuedDecision is the internal record EnqueueDecision stores —
// Curve's decisionStepsForKey reads only the fields it needs (curveKey/
// completionMonth/delta), keeping the public Decision shape free to
// grow without this internal bookkeeping type changing shape too.
type queuedDecision struct {
	curveKey        string
	completionMonth int64
	delta           float64
}

// slowFuseStep is what decisionStepsForKey returns to Curve — just
// enough to apply AC-4's step, independent of any other Decision field.
type slowFuseStep struct {
	completionMonth int64
	delta           float64
}

// slowFuseGate is A5's single, decision-type-agnostic enforcement
// point (AC-5): it inspects only fuseYears/consequence, never a
// decision's Type, so a brand-new decision type this module has never
// heard of is gated exactly as strictly as any of A5's five named
// examples. Returns a non-nil error (ErrSlowFuseMissingPayload) when
// fuseYears exceeds the threshold and consequence is empty; nil
// otherwise (including every fuseYears <= threshold decision, which
// A5 does not require a payload for at all).
//
// BREAK-1 fix (Destructive round, this build — CORE SAFETY). The
// original code went straight to `fuseYears > slowFuseThresholdYears`.
// Go's IEEE-754 float rules make every ordering comparison against NaN
// evaluate to false, so `math.NaN() > 5` is false — a decision
// submitted with a corrupted/degenerate FuseYears (NaN, or +Inf/-Inf,
// which compare in ways just as unreliable for this purpose) read as
// "under threshold" and sailed through with NO payload required,
// which is precisely the ambush A5 exists to prevent: a real decision
// whose fuse is corrupted in transit is the WORST case to let through
// silently, not a safe default. validateFuseYears now runs BEFORE the
// threshold comparison and rejects any non-finite value outright, so a
// degenerate tag can never be misread as "safe" by float comparison
// semantics. Negative FuseYears is rejected too (this build's explicit
// decision, documented on ErrInvalidFuseYears in errors.go): it is not
// a meaningful "under threshold" reading, it is invalid input that
// happened to compare as harmless purely by coincidence of sign.
func slowFuseGate(correlationID string, decisionID string, fuseYears float64, consequence *ProjectedConsequence) error {
	if err := validateFuseYears(correlationID, decisionID, fuseYears); err != nil {
		return err
	}
	if fuseYears > slowFuseThresholdYears && consequence.empty() {
		return errs.New(ErrSlowFuseMissingPayload, correlationID, map[string]any{
			"field": "decision \"" + decisionID + "\": ProjectedConsequence payload (FuseYears " +
				formatFuseYears(fuseYears) + " exceeds the Slow-Fuse threshold)",
		})
	}
	return nil
}

// validateFuseYears rejects a FuseYears value the Slow-Fuse gate's
// ">" comparison cannot be trusted to reason about safely: NaN, +Inf,
// -Inf, or negative (BREAK-1). Called before slowFuseGate's own
// threshold test, never after — the whole point is that a degenerate
// value must never reach that comparison at all.
func validateFuseYears(correlationID, decisionID string, fuseYears float64) error {
	if math.IsNaN(fuseYears) || math.IsInf(fuseYears, 0) || fuseYears < 0 {
		return errs.New(ErrInvalidFuseYears, correlationID, map[string]any{
			"id":    "decision \"" + decisionID + "\"",
			"axis":  "FuseYears",
			"value": formatFuseYears(fuseYears),
		})
	}
	return nil
}

// formatFuseYears renders fuseYears for error-message context values
// without pulling in strconv/fmt just for this one call site's float
// formatting; kept trivially simple since it only ever renders a
// human-readable number inside an error message, never a computed
// value anything downstream branches on.
//
// BREAK-1 fix: NaN/+Inf/-Inf are special-cased to their literal names
// rather than falling into the int64(fuseYears) conversion below —
// that conversion does not panic (the Go spec defines it as producing
// an implementation-dependent, but not undefined, value for an
// out-of-range/NaN float), but the resulting integer is meaningless
// noise in a message specifically about a degenerate value being
// rejected; the reader needs to see "NaN", not some arbitrary digits.
func formatFuseYears(fuseYears float64) string {
	switch {
	case math.IsNaN(fuseYears):
		return "NaN"
	case math.IsInf(fuseYears, 1):
		return "+Inf"
	case math.IsInf(fuseYears, -1):
		return "-Inf"
	}
	whole := int64(fuseYears)
	if float64(whole) == fuseYears {
		return itoa64(whole)
	}
	return itoa64(whole) + "+"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// EnqueueDecision submits d through the confirmation path: the
// Slow-Fuse gate (slowFuseGate) runs first (AC-5/AC-10), then — if d
// carries a CurveKey — a step is queued so Curve's future-month
// queries reflect d's effect before it actually lands (AC-4). A
// duplicate ID overwrites the previous queued step for that ID (a
// re-confirmed/edited decision replacing its own prior queued step),
// never silently ignored (there is no separate "reject duplicate ID"
// requirement in this item's ACs, unlike RegisterCurveProvider's).
func (p *ProjectionsAPI) EnqueueDecision(d Decision) error {
	if err := p.checkNotCopied(map[string]any{"method": "EnqueueDecision"}); err != nil {
		return err
	}
	if err := slowFuseGate(p.correlationID, d.ID, d.FuseYears, d.Consequence); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNotCopied(map[string]any{"method": "EnqueueDecision", "id": d.ID}); err != nil {
		return err
	}
	p.decisions[d.ID] = &queuedDecision{
		curveKey:        d.CurveKey,
		completionMonth: d.CompletionMonth,
		delta:           d.Delta,
	}
	return nil
}

// CancelDecision removes a previously-enqueued decision's step, so
// Curve's future-month queries for its CurveKey revert to the
// registered provider's own value (AC-4's "removing/cancelling the
// queued decision removes the step"). Rejects an unknown id
// (ErrUnknownDecision) rather than silently no-op-ing.
func (p *ProjectionsAPI) CancelDecision(id string) error {
	if err := p.checkNotCopied(map[string]any{"method": "CancelDecision"}); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNotCopied(map[string]any{"method": "CancelDecision", "id": id}); err != nil {
		return err
	}
	if _, ok := p.decisions[id]; !ok {
		return errs.New(ErrUnknownDecision, p.correlationID, map[string]any{"actionID": id})
	}
	delete(p.decisions, id)
	return nil
}

// decisionStepsForKey returns every queued decision step affecting
// curveKey, in no particular order (Curve applies each independently
// and their sum is order-independent — GR#21 determinism holds
// regardless of map iteration order here).
func decisionStepsForKey(decisions map[string]*queuedDecision, curveKey string) []slowFuseStep {
	var steps []slowFuseStep
	for _, d := range decisions {
		if d.curveKey == curveKey {
			steps = append(steps, slowFuseStep{completionMonth: d.completionMonth, delta: d.delta})
		}
	}
	return steps
}
