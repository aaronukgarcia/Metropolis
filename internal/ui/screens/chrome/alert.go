package chrome

import (
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// validTarget reports whether t is a usable navigation target: a
// dash.DrillTarget whose ViewName is non-empty after trimming surrounding
// whitespace. This is the alert-construction gate (AC-3/AC-11). dash's
// own DrillTarget.Valid() is the lenient "has any view name" check;
// chrome additionally rejects a whitespace-only ViewName here, so a
// hand-built target of spaces is refused the same way NewAlert has always
// refused it.
func validTarget(t dash.DrillTarget) bool {
	return strings.TrimSpace(t.ViewName) != ""
}

// Tier is the priority tier of an alert, ordered so that higher values
// rank higher in the stack (AC-5's tier-driven ordering). It is a closed
// set and is deliberately NOT derived from the crisis flag (AC-6): a
// crisis is a tagged subset of alerts, independent of its display tier.
type Tier int

const (
	// TierInfo is a low-stakes reminder (the kind that must never bury a
	// real emergency, US-2).
	TierInfo Tier = iota
	// TierWarning is an approaching-threshold state.
	TierWarning
	// TierCritical is an urgent, already-breach state.
	TierCritical
)

// Token maps a Tier to the ui.widgets semantic colour token that paints
// the alert's line (AC-2's colour-coding). The mapping is this package's
// own default (see doc.go and the ASM logged for it): critical reads as
// danger red, warning as warning amber, info as the neutral selection
// base colour. Colour always comes from the injected widgets.Palette —
// never a hardcoded tcell.Color (GR#3, GR#15).
func (t Tier) Token() widgets.Token {
	switch t {
	case TierCritical:
		return widgets.TokenDanger
	case TierWarning:
		return widgets.TokenWarning
	default:
		return widgets.TokenSelection
	}
}

// Alert is one entry in the bottom alert stack. The target field holds the
// canonical dash.DrillTarget (MOD-038, AC-3's jump destination) and is
// deliberately unexported — the only way to obtain a populated Alert is
// NewAlert, which rejects a targetless value, so a targetless alert can
// never reach the stack (AC-3's structural guarantee, mirroring
// ui.dash.md AC-4's unexported DrillTarget discipline — chrome consumes
// dash's type rather than defining its own, GR#3). An Alert is immutable
// once constructed: the stack never mutates it in place, it only sorts
// and drops copies (so a rendered line can never tear, AC-16).
type Alert struct {
	// ID is the alert's stable identifier. For a crisis-tagged alert it is
	// the stable per-instance crisis identity the engine emitter mints once
	// per underlying crisis instance (FEAT-042 AC-25b); AC-8's
	// edge-triggered dedupe is keyed on it. Non-empty always.
	ID string

	// Text is the human-readable alert line (e.g. "Water deficit in 3
	// months"). Display text only — never used as a key, identifier, or
	// path component (weakness pattern #4).
	Text string

	// Tier is the alert's priority tier (see Tier's doc comment).
	Tier Tier

	// Crisis is the explicit crisis tag (AC-6): true only for an alert
	// tagged crisis, independent of Tier. It is NOT derived from Tier and
	// Tier is NOT derived from it — a P0/TierCritical "Loan payment due"
	// is urgent but not a crisis, while a crisis-tagged alert auto-pauses
	// regardless of its display tier.
	Crisis bool

	// Tick is the simulation tick the alert was raised at (protocol.Tick,
	// never the wall clock — AC-15). It is the "oldest-first" tie-break
	// key within a tier (AC-5).
	Tick protocol.Tick

	target dash.DrillTarget
}

// NewAlert constructs an Alert, rejecting an empty target
// (ErrAlertMissingTarget, AC-3/AC-11) or an empty ID (ErrAlertMissingID —
// the resolution and crisis-dedupe key, AC-8/AC-12). On rejection it
// returns (zero, error); the caller must not use the zero value.
//
// GR#1/GR#7: the rejection is a registry-sourced *errs.E, never a bare
// errors.New/fmt.Errorf — the caller can errors.Is it against the
// ErrAlertMissingTarget code. The correlation ID is minted here because a
// construction-time validation has no causal chain of its own yet.
func NewAlert(id, text string, tier Tier, crisis bool, target dash.DrillTarget, tick protocol.Tick) (Alert, error) {
	if !validTarget(target) {
		return Alert{}, errs.New(ErrAlertMissingTarget, errs.NewCorrelationID(), map[string]any{
			"id": id, "text": text, "crisis": crisis,
		})
	}
	if strings.TrimSpace(id) == "" {
		return Alert{}, errs.New(ErrAlertMissingID, errs.NewCorrelationID(), map[string]any{
			"text": text, "crisis": crisis,
		})
	}
	return Alert{
		ID:     id,
		Text:   text,
		Tier:   tier,
		Crisis: crisis,
		Tick:   tick,
		target: target,
	}, nil
}

// validate re-checks the two construction invariants NewAlert enforces, so
// AddAlert (the stack boundary) can reject a hand-built Alert (e.g. from
// an internal white-box test) that bypassed NewAlert. Defense in depth:
// the constructor makes the invalid state unconstructible, this makes the
// stack boundary the second, unforgeable gate. Returns the registry error
// for the first violated invariant.
func (a Alert) validate(correlationID string) error {
	if !validTarget(a.target) {
		return errs.New(ErrAlertMissingTarget, correlationID, map[string]any{
			"id": a.ID, "text": a.Text, "crisis": a.Crisis,
		})
	}
	if strings.TrimSpace(a.ID) == "" {
		return errs.New(ErrAlertMissingID, correlationID, map[string]any{
			"text": a.Text, "crisis": a.Crisis,
		})
	}
	return nil
}
