package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-019 (docs/planning/acceptance/ui.screen.proj.md): the fifth real
// UI delta-publishing vertical slice, "f7.projections" — the Projections
// screen (F7). It publishes the forecast-horizon N plus the aggregate
// curve/crossing/rate-outlook feed, derived live from the composed
// engine.projections observer (constructed in compose.go's Wire).
//
// This file mirrors census_publish.go / finance_publish.go /
// services_publish.go / viewport_publish.go's one-file-per-integration
// convention exactly and, per the FEAT-208 design's §3.3, builds
// compose's OWN copy of the wire schema — the same JSON tags as
// ui.screen.proj's wire.go, duplicated independently, NEVER importing
// internal/ui/screens/proj (GR#20's engine-never-imports-ui half of the
// seam).

// projectionsWireSchemaVersion mirrors ui.screen.proj/wire.go's
// wireSchemaVersion constant VALUE (1), kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline the other four
// publish files' identical constants follow.
const projectionsWireSchemaVersion = 1

// The wire structs below mirror ui.screen.proj/wire.go field for field
// (same JSON tags), duplicated here rather than imported so this package
// never depends on internal/ui (GR#20, SF-1). The full schema is kept in
// lockstep even though buildProjectionsPatch today emits only
// schemaVersion + horizonMonths: no producer module that registers a
// curve provider is composed into simState yet (see the honest-scope note
// in compose.go's projections field doc comment), so curves/crossings/
// rateOutlook are genuinely empty and omitted from the wire patch (they
// decode as "no curves" on the screen side). The structs are typed here
// rather than dropped so the schema stays the single contract this
// package and ui.screen.proj share, and the fields pop into existence the
// moment a producer is composed — nothing else changes.

type projectionsWireThreshold struct {
	Value float64 `json:"value"`
	Label string  `json:"label,omitempty"`
}

type projectionsWireMarker struct {
	MonthOffset int    `json:"monthOffset"`
	Label       string `json:"label,omitempty"`
}

type projectionsWireCurve struct {
	Key               string                     `json:"key"`
	Label             string                     `json:"label"`
	Status            string                     `json:"status"`
	UnavailableReason string                     `json:"unavailableReason,omitempty"`
	History           []float64                  `json:"history,omitempty"`
	Projection        []float64                  `json:"projection,omitempty"`
	ConfidenceUpper   []float64                  `json:"confidenceUpper,omitempty"`
	ConfidenceLower   []float64                  `json:"confidenceLower,omitempty"`
	Thresholds        []projectionsWireThreshold `json:"thresholds,omitempty"`
	Markers           []projectionsWireMarker    `json:"markers,omitempty"`
}

type projectionsWireCrossing struct {
	Key                string    `json:"key"`
	Label              string    `json:"label"`
	Status             string    `json:"status"`
	UnavailableReason  string    `json:"unavailableReason,omitempty"`
	InternalDemand     []float64 `json:"internalDemand,omitempty"`
	ContractedCapacity []float64 `json:"contractedCapacity,omitempty"`
	CrossingMonth      int       `json:"crossingMonth"`
}

type projectionsWireRateOutlook struct {
	Status            string    `json:"status"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
	History           []float64 `json:"history,omitempty"`
	Projection        []float64 `json:"projection,omitempty"`
}

type projectionsWirePatch struct {
	SchemaVersion int                         `json:"schemaVersion"`
	HorizonMonths int                         `json:"horizonMonths"`
	Curves        []projectionsWireCurve      `json:"curves,omitempty"`
	Crossings     []projectionsWireCrossing   `json:"crossings,omitempty"`
	RateOutlook   *projectionsWireRateOutlook `json:"rateOutlook,omitempty"`
}

// buildProjectionsPatch reads the composed engine.projections observer's
// live horizon and returns the "f7.projections" patch. It runs on the
// subscription pump goroutine (subscribe.go's ViewPatchFunc contract),
// CONCURRENTLY with the phase pipeline's citizen mutations — safe because
// the only read (ProjectionsAPI.HorizonMonths) goes through the module's
// own synchronization (horizonMonths is a construction-time-fixed func
// field; nothing mutates it after Wire), never a plain simState field
// read (the same discipline compose.go's simState doc comment spells out
// for the other publish files).
//
// # Honest scope (no curve providers composed yet)
//
// engine.projections is a curve-provider REGISTRY: systems register curve
// providers via RegisterCurveProvider, and the UI subscribes to the
// aggregated view. As of this wiring, no producer module that owns a
// RegisterCurveProvider seam is composed into simState — engine.capexport
// (RegisterContractCurves), engine.education / engine.social (their
// funding curves), engine.spiral (CurveKeyGhostCityPopulation) and
// engine.policies all hold SetProjections + RegisterCurveProvider edges,
// but none is constructed in Wire — and ProjectionsAPI exposes no
// key-enumeration surface for compose to query "whatever is registered"
// generically. The result is that curves/crossings/rateOutlook are
// genuinely empty today, NOT fabricated: the patch carries the real,
// data-sourced horizon (engine.projections' embedded horizon.json, 72
// months) and nothing else, and the screen renders its header with that
// horizon and no curve panes. When a producer module is composed, its
// Wire-time RegisterCurveProvider call(s) plus a compose-owned key list
// here are what turn those fields on — nothing in this file is a silent
// placeholder.
func (st *simState) buildProjectionsPatch() (json.RawMessage, error) {
	horizon, err := st.projections.HorizonMonths()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "projections", "accessor": "HorizonMonths"})
	}

	patch := projectionsWirePatch{
		SchemaVersion: projectionsWireSchemaVersion,
		HorizonMonths: int(horizon),
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of an int cannot fail; unreachable in
		// practice — mirrored on the other four publish files' identical
		// "cannot fail" branches. Per GR#1, degrade loudly rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "projections", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// projectionsViewSubscriptionName mirrors internal/ui/screens/proj/wire.go's
// ViewSubscriptionName constant VALUE ("f7.projections") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/proj (GR#20's engine-never-imports-ui half of the
// seam; this file's own doc comment). Kept as its own named constant for
// the same reason censusViewSubscriptionName / financeViewSubscriptionName
// / servicesViewSubscriptionName / viewportViewSubscriptionName are.
const projectionsViewSubscriptionName = "f7.projections"
