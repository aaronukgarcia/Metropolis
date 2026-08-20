package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-208 increment 1 (docs/planning — the FEAT-208 publish-path design
// proposal's §6 recommended first slice): the first real UI
// delta-publishing vertical slice, f4.services' capacityDemand sub-view
// ONLY (the remaining sliders/responseTimes/waitingLists/publicServicePie
// sub-shapes are documented fast-follows, strictly additive — every
// field on ui.screen.services' wirePatch is already `omitempty`, so no
// schemaVersion bump is needed when they land).
//
// This file mirrors traffic_wire.go/servicesfirms_wire.go's existing
// one-file-per-integration convention and, per the design's §3.3, builds
// compose's OWN copy of the wire schema — the same JSON tags as
// ui.screen.services' wire.go's wireCapacityDemand/wirePatch, duplicated
// independently, NEVER importing internal/ui/screens/services (GR#20's
// engine-never-imports-ui half of the seam, preserved here in reverse of
// how ui/screens/services/wire.go never imports engine.services).

// servicesWireSchemaVersion mirrors ui.screen.services/wire.go's
// wireSchemaVersion constant (kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline the whole file
// follows — a change to one side is not expected to auto-propagate to
// the other; a version mismatch is exactly what ui.screen.services'
// decodeWirePatch's schemaVersion check exists to catch).
const servicesWireSchemaVersion = 1

// servicesCapacityDemandWirePatch is compose's own copy of
// ui.screen.services/wire.go's wirePatch — only the CapacityDemand field
// is ever populated this increment; every other field is deliberately
// left nil (and therefore omitted, via wire.go's own `omitempty` tags on
// the UI side) rather than sent as an empty/zero value, so a future
// fast-follow sub-view can start sending its own field without this one
// changing shape.
type servicesCapacityDemandWirePatch struct {
	SchemaVersion  int                                `json:"schemaVersion"`
	CapacityDemand *[]servicesCapacityDemandWireEntry `json:"capacityDemand,omitempty"`
}

// servicesCapacityDemandWireEntry mirrors
// ui.screen.services/wire.go's wireCapacityDemand field-for-field.
type servicesCapacityDemandWireEntry struct {
	ServiceID     string  `json:"serviceId"`
	Label         string  `json:"label"`
	CapacityUnits float64 `json:"capacityUnits"`
	DemandUnits   float64 `json:"demandUnits"`
}

// buildServicesCapacityDemandPatch reads st.services' registered
// instance roster (ServiceIDs — engine.services/coverage.go's own
// deterministic, already-sorted read; this function never ranges a map
// itself, GR#21) and each instance's Capacity/Demand, and returns the
// "f4.services" capacityDemand-only patch (the design's §6 step 3).
//
// Baseline one wires no automatic engine.build -> engine.services
// registration bridge yet (the design survey's own §5 finding, and
// servicesfirms_wire.go's serviceCoverageTerm doc comment makes the same
// point for the attract-term reader of this same module) — so in an
// unmodified baseline-one run, ServiceIDs() is empty and this returns a
// patch with an empty (never nil — the JSON array renders as `[]`, the
// honest "zero services registered" state, not the "field absent"
// omitted state the other, not-yet-wired sub-shapes use) CapacityDemand
// slice. That is a legitimate baseline-one state, not an error: the
// end-to-end regression test (services_publish_test.go) drives it past
// that state by registering a real service instance first.
//
// Label is deliberately the bare ServiceID string: engine.services'
// ServicesAPI exposes no accessor mapping an already-registered
// instance's ID back to a human display name (KindDef.Label exists only
// per-KIND, and no accessor returns a registered instance's Kind either)
// — sourcing a real display label is documented, explicit fast-follow
// scope, not invented here as a silent guess.
func (st *simState) buildServicesCapacityDemandPatch() (json.RawMessage, error) {
	ids, err := st.services.ServiceIDs()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "services", "accessor": "ServiceIDs"})
	}

	entries := make([]servicesCapacityDemandWireEntry, 0, len(ids))
	for _, id := range ids {
		capacity, cErr := st.services.Capacity(id)
		if cErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, st.cid, cErr, map[string]any{"module": "services", "accessor": "Capacity", "serviceId": string(id)})
		}
		demand, dErr := st.services.Demand(id)
		if dErr != nil {
			return nil, errs.Wrap(ErrModuleFailed, st.cid, dErr, map[string]any{"module": "services", "accessor": "Demand", "serviceId": string(id)})
		}
		entries = append(entries, servicesCapacityDemandWireEntry{
			ServiceID:     string(id),
			Label:         string(id),
			CapacityUnits: capacity,
			DemandUnits:   demand,
		})
	}

	patch := servicesCapacityDemandWirePatch{
		SchemaVersion:  servicesWireSchemaVersion,
		CapacityDemand: &entries,
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of strings/float64s cannot fail;
		// unreachable in practice, mirrored on engine.core's own
		// EngineStatusView-adjacent "cannot fail" branches. Per GR#1,
		// degrade loudly rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "services", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// servicesViewSubscriptionName mirrors
// internal/ui/screens/services/wire.go's ViewSubscriptionName constant
// VALUE ("f4.services") — duplicated independently as compose's own
// string literal, never imported from internal/ui/screens/services
// (GR#20's engine-never-imports-ui half of the seam; this file's own
// doc comment). Kept as its own named constant (rather than a bare
// string literal inline in viewRegistrationOrder, compose.go) so a
// future compose test asserting the registered view-name set has a
// symbol to reference, the same shape RegistrationOrder()/
// BaselineOneHookCount() already give phase-hook names.
const servicesViewSubscriptionName = "f4.services"
