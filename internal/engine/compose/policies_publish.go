package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/policies"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-2326609832: "f7.policies" - the engine.policies view. It carries the
// policy library, active enactments, conflict warnings, raised PreviewDrift
// events, the series Enact STORED per enactment (Computed at enact time),
// districts and the combined coefficient state. The pre-enact preview is NOT
// served here (a ViewPatchFunc takes no params and PreviewImpact mutates the
// shared projections clock, which would race Enact; see policies_wire.go).

// policiesViewSubscriptionName is the view name a policies screen subscribes
// to. Compose's own copy (GR#20), like the other *_publish.go files.
const policiesViewSubscriptionName = "f7.policies"

const policiesWireSchemaVersion = 1

// policiesDisclosure states, on the wire, what this wiring does not do.
const policiesDisclosure = "tax-moving policies enactable with no revenue effect; engine.tax not the revenue poster"

type policiesWirePatch struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Disclosure    string                           `json:"disclosure"`
	Library       []policiesWireLibraryEntry       `json:"library"`
	Enactments    []policies.EnactmentState        `json:"enactments"`
	Conflicts     []policies.ConflictWarning       `json:"conflicts"`
	DriftEvents   []policies.PreviewDriftEvent     `json:"driftEvents"`
	Previews      []policies.PreviewState          `json:"previews"`
	Districts     []policies.DistrictState         `json:"districts"`
	Coefficients  []policies.CoefficientStateEntry `json:"coefficients"`
}

type policiesWireLibraryEntry struct {
	ID                     string                      `json:"id"`
	Name                   string                      `json:"name"`
	Category               string                      `json:"category"`
	Scope                  string                      `json:"scope"`
	EnactmentMicroPounds   int64                       `json:"enactmentMicroPounds"`
	OpexMonthlyMicroPounds int64                       `json:"opexMonthlyMicroPounds"`
	Mechanism              []policies.CoefficientDelta `json:"mechanism"`
	Conflicts              []string                    `json:"conflicts"`
	Disclosure             string                      `json:"disclosure"`
}

// buildPoliciesPatch implements core.ViewPatchFunc for "f7.policies".
func (st *simState) buildPoliciesPatch() (json.RawMessage, error) {
	if st.pol == nil {
		return nil, errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "policies", "cause": "policies not wired"})
	}
	api := st.pol.api
	lib := api.Policies()
	patch := policiesWirePatch{
		SchemaVersion: policiesWireSchemaVersion,
		Disclosure:    policiesDisclosure,
		Library:       make([]policiesWireLibraryEntry, 0, len(lib)),
		Conflicts:     api.Conflicts(),
		DriftEvents:   api.PreviewDriftEvents(),
		Coefficients:  api.CoefficientState(),
	}
	for _, p := range lib {
		conf := make([]string, 0, len(p.Conflicts))
		for _, c := range p.Conflicts {
			conf = append(conf, string(c))
		}
		patch.Library = append(patch.Library, policiesWireLibraryEntry{
			ID: string(p.ID), Name: p.Name, Category: p.Category, Scope: string(p.Scope),
			EnactmentMicroPounds: p.Cost.EnactmentMicroPounds, OpexMonthlyMicroPounds: p.Cost.OpexMonthlyMicroPounds,
			Mechanism: p.Mechanism, Conflicts: conf, Disclosure: p.Disclosure,
		})
	}
	snap := api.ExportState()
	patch.Enactments = snap.Enactments
	patch.Previews = snap.Previews
	patch.Districts = snap.Districts
	if patch.Enactments == nil {
		patch.Enactments = []policies.EnactmentState{}
	}
	if patch.Conflicts == nil {
		patch.Conflicts = []policies.ConflictWarning{}
	}
	if patch.DriftEvents == nil {
		patch.DriftEvents = []policies.PreviewDriftEvent{}
	}
	if patch.Previews == nil {
		patch.Previews = []policies.PreviewState{}
	}
	if patch.Districts == nil {
		patch.Districts = []policies.DistrictState{}
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "policies", "accessor": "json.Marshal"})
	}
	return raw, nil
}
