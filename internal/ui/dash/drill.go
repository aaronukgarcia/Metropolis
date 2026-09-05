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
// DrillTarget is a type ALIAS for protocol.TargetRef (FEAT-231 V2 /
// architect ruling 2026-09-05): the SSOT carrier for the
// (ViewName, EntityID) drill-through pair lives at the protocol layer
// now, since FEAT-042 needed the same shape on the wire and a second,
// structurally-identical carrier would fragment the one-DrillTarget-type
// doctrine (viewgate's TestNoSecondDrillTargetType). An alias is the
// exact same type as protocol.TargetRef — not a copy, not a convertible
// sibling — so every existing dash consumer (New<Kind>Tile, Dashboard.Drill,
// profile JSON round-trip) keeps working unchanged, and the two names can
// never drift apart. See drill_alias_test.go for the compile-time /
// reflect-identity proof nobody can quietly re-fork this.
//
// DrillTarget is deliberately a plain, exported value type (so it can
// round-trip profile JSON and be compared with ==), but it is never
// valid as its zero value: ViewName must be a non-empty, grammar-valid
// protocol view name. Callers reach it through NewDrillTarget, which
// validates, or through a New<Kind>Tile constructor, which validates it
// again on the way into a Tile (AC-4).
//
// Field docs (see protocol.TargetRef for the canonical declaration):
//   - ViewName is an int.protocol view name (protocol.ValidateViewName).
//     Whole-entity targets use the entity-scoped grammar
//     (e.g. "junction.14.approaches"); a screen-scoped dashboard uses an
//     F-screen key (e.g. "f1.viewport").
//   - EntityID optionally names a sub-entity or row within ViewName's
//     view (a ledger line, a diagram arrow). Empty means "the whole
//     view". It is a protocol.EntityID (opaque, engine-defined) rather
//     than a plain string; this package validates it via
//     protocol.ValidateEntityID when non-empty (AC-20 closeout).
type DrillTarget = protocol.TargetRef

// NewDrillTarget constructs a DrillTarget, validating viewName against
// int.protocol's view-name grammar and, when non-empty, entityID against
// int.protocol's EntityID grammar (protocol.ValidateEntityID — FEAT-042
// AC-20 closeout: a hostile/malformed entityID is rejected at
// construction time rather than silently carried through). entityID is
// optional (empty means "whole view"). A zero/empty viewName, or one
// that fails grammar validation, returns a registry-sourced error
// (MET-U602 / MET-U603); a non-empty entityID that fails
// protocol.ValidateEntityID returns MET-P003 (protocol.ErrInvalidEntityID).
func NewDrillTarget(viewName, entityID string) (DrillTarget, error) {
	d := DrillTarget{ViewName: viewName, EntityID: protocol.EntityID(entityID)}
	if err := requireDrill(d, nil); err != nil {
		return DrillTarget{}, err
	}
	if entityID != "" {
		if err := protocol.ValidateEntityID(d.EntityID); err != nil {
			return DrillTarget{}, err
		}
	}
	return d, nil
}

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
