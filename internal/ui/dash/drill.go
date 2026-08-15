package dash

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// DrillTarget is the source identity a tile's (or element's) displayed
// value points at: the thing Enter navigates to. It is the drill-through
// rule's carrier (AC-4/AC-5), a (ViewName, EntityID) pair as described
// in the package doc.
//
// DrillTarget is deliberately a plain, exported value type (so it can
// round-trip profile JSON and be compared with ==), but it is never
// valid as its zero value: ViewName must be a non-empty, grammar-valid
// protocol view name. Callers reach it through NewDrillTarget, which
// validates, or through a New<Kind>Tile constructor, which validates it
// again on the way into a Tile (AC-4).
type DrillTarget struct {
	// ViewName is an int.protocol view name (protocol.ValidateViewName).
	// Whole-entity targets use the entity-scoped grammar
	// (e.g. "junction.14.approaches"); a screen-scoped dashboard uses an
	// F-screen key (e.g. "f1.viewport").
	ViewName string `json:"viewName"`

	// EntityID optionally names a sub-entity or row within ViewName's
	// view (a ledger line, a diagram arrow). Empty means "the whole
	// view". It is opaque and engine-defined; this package does not
	// parse it beyond non-emptiness.
	EntityID string `json:"entityId,omitempty"`
}

// NewDrillTarget constructs a DrillTarget, validating viewName against
// int.protocol's view-name grammar. entityID is optional (empty means
// "whole view"); a non-empty entityID is carried through unchanged. A
// zero/empty viewName, or one that fails grammar validation, returns a
// registry-sourced error (MET-U602 / MET-U603) rather than a silently
// unusable target.
func NewDrillTarget(viewName, entityID string) (DrillTarget, error) {
	d := DrillTarget{ViewName: viewName, EntityID: entityID}
	if err := requireDrill(d, nil); err != nil {
		return DrillTarget{}, err
	}
	return d, nil
}

// Valid reports whether t is a resolvable drill target: it has a
// non-empty ViewName. This is the lenient "is there something to resolve
// at all" check AuditDrillCoverage uses — grammar strictness is
// NewDrillTarget's/New<Kind>Tile's job at construction time, while a
// zero-value target (e.g. one that slipped in via a corrupt profile
// decode) is exactly the dead end the audit exists to surface.
func (t DrillTarget) Valid() bool { return t.ViewName != "" }

// requireDrill is the shared construction-time validation: a DrillTarget
// must name a real view. Empty view name -> MET-U602; grammar failure ->
// MET-U603. ctx carries whatever caller-specific context (tile ID, row
// index) the raising site can provide.
func requireDrill(d DrillTarget, ctx map[string]any) error {
	if d.ViewName == "" {
		if ctx == nil {
			ctx = map[string]any{}
		}
		ctx["reason"] = "empty DrillTarget view name"
		return errs.New(codeTileNeedsDrill, corr(), ctx)
	}
	if err := protocol.ValidateViewName(d.ViewName); err != nil {
		if ctx == nil {
			ctx = map[string]any{}
		}
		ctx["cause"] = err.Error()
		return errs.New(codeInvalidViewName, corr(), ctx)
	}
	return nil
}
