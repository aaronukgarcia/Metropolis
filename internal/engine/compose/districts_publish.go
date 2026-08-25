package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-022 (docs/planning/acceptance/ui.screen.districts.md): the sixth real
// UI delta-publishing vertical slice, "f8.districts" — the Districts &
// Policies screen (F8). It publishes the named-district roster (Districts,
// id + name) and the per-district tax-settings table (TaxSettings, the
// (district, instrument) join of engine.policies' district roster with
// engine.tax's instrument definitions and per-district multipliers), both
// derived live from the composed engine.policies / engine.tax modules.
//
// This file mirrors census_publish.go / projections_publish.go /
// finance_publish.go / services_publish.go / viewport_publish.go's
// one-file-per-integration convention exactly and, per the FEAT-208
// design's §3.3, builds compose's OWN copy of the wire schema — the same
// JSON tags as ui.screen.districts' wire.go, duplicated independently,
// NEVER importing internal/ui/screens/districts (GR#20's
// engine-never-imports-ui half of the seam).

// districtsWireSchemaVersion mirrors ui.screen.districts/wire.go's
// wireSchemaVersion constant VALUE (1), kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline the other five
// publish files' identical constants follow.
const districtsWireSchemaVersion = 1

// The wire structs below mirror ui.screen.districts/wire.go field for field
// (same JSON tags), duplicated here rather than imported so this package
// never depends on internal/ui (GR#20, SF-1). Both Districts and TaxSettings
// are ALWAYS emitted as non-nil pointers — even when the underlying roster
// is empty — because a non-nil field is the signal ui.screen.districts'
// ApplyDelta reads to flip haveDistrict/haveTaxSetting: "the engine serves
// this surface, and it currently holds zero districts" is a different,
// meaningfully-renderable state from "this surface was not sent" (which the
// screen decodes as unavailable / not present). With a non-nil-but-empty
// TaxSettings and no selected district, the screen renders its real header
// and "select a district to edit its tax settings" rather than the
// "unavailable" placeholder.

type districtsWireDistrict struct {
	DistrictID string `json:"districtId"`
	Name       string `json:"name"`
}

type districtsWireTaxSetting struct {
	DistrictID      string  `json:"districtId"`
	InstrumentID    string  `json:"instrumentId"`
	InstrumentLabel string  `json:"instrumentLabel"`
	Multiplier      float64 `json:"multiplier"`
	Rate            float64 `json:"rate"`          // citywide headline rate (percent), TaxAPI.InstrumentInfo.Rate
	RateMax         float64 `json:"rateMax"`       // TaxAPI.InstrumentInfo.RateMax; effective rate = Rate*Multiplier must stay <= RateMax
	EffectiveRate   float64 `json:"effectiveRate"` // Rate*Multiplier, engine-computed
}

type districtsWirePatch struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Districts     *[]districtsWireDistrict   `json:"districts,omitempty"`
	TaxSettings   *[]districtsWireTaxSetting `json:"taxSettings,omitempty"`
}

// buildDistrictsPatch reads the composed engine.policies roster and
// engine.tax instrument table and returns the "f8.districts" patch. It runs
// on the subscription pump goroutine (subscribe.go's ViewPatchFunc
// contract), CONCURRENTLY with the phase pipeline — safe only because every
// read goes through the module's own synchronization (PoliciesAPI.Districts
// and TaxAPI.Instruments/GetDistrictMultiplier each take their own
// sync.RWMutex internally), never a plain simState field read (the same
// discipline compose.go's simState doc comment spells out for the other
// publish files).
//
// The TaxSettings list is the (district, instrument) cross product: one row
// per district per instrument, carrying the instrument's data-loaded label,
// its citywide headline rate and rate bounds, and the district multiplier
// (1.0 when none has been set), with EffectiveRate = Rate × Multiplier —
// exactly the join the screen's wireDistrictTaxSetting documents. Both
// source APIs return their entries in sorted ID order (GR#21), so the
// nested loop is deterministic run over run.
//
// # Honest scope (no districts seeded at Wire time)
//
// engine.policies is constructed EMPTY via NewPoliciesAPI — baseline one
// seeds no districts, and a district exists only once a gameplay path calls
// CreateDistrict (ui.screen.districts' AC-2, PENDING BUILD under FEAT-210).
// The six tax instruments ARE present (data/tax_instruments.json), so both
// lists are genuinely empty at a fresh boot: the patch carries schemaVersion
// plus two non-nil-but-empty lists, and the screen renders "no districts
// yet" / "select a district to edit its tax settings" rather than fabricated
// rows. The moment any district is created, TaxSettings populates one row
// per (district, instrument) with no further change here.
func (st *simState) buildDistrictsPatch() (json.RawMessage, error) {
	dists := st.policies.Districts()
	instruments := st.tax.Instruments()

	districts := make([]districtsWireDistrict, 0, len(dists))
	for _, d := range dists {
		districts = append(districts, districtsWireDistrict{DistrictID: string(d.ID), Name: d.Name})
	}

	taxSettings := make([]districtsWireTaxSetting, 0, len(dists)*len(instruments))
	for _, d := range dists {
		for _, ins := range instruments {
			mult, err := st.tax.GetDistrictMultiplier(tax.DistrictID(d.ID), ins.ID)
			if err != nil {
				return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "tax", "accessor": "GetDistrictMultiplier", "district": string(d.ID), "instrument": ins.ID})
			}
			taxSettings = append(taxSettings, districtsWireTaxSetting{
				DistrictID:      string(d.ID),
				InstrumentID:    ins.ID,
				InstrumentLabel: ins.Name,
				Multiplier:      mult,
				Rate:            ins.Rate,
				RateMax:         ins.RateMax,
				EffectiveRate:   ins.Rate * mult,
			})
		}
	}

	patch := districtsWirePatch{
		SchemaVersion: districtsWireSchemaVersion,
		Districts:     &districts,
		TaxSettings:   &taxSettings,
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of ints/strings/floats cannot fail;
		// unreachable in practice — mirrored on the other five publish files'
		// identical "cannot fail" branches. Per GR#1, degrade loudly rather
		// than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "districts", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// districtsViewSubscriptionName mirrors internal/ui/screens/districts/wire.go's
// ViewSubscriptionName constant VALUE ("f8.districts") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/districts (GR#20's engine-never-imports-ui half of the
// seam; this file's own doc comment). Kept as its own named constant for the
// same reason censusViewSubscriptionName / projectionsViewSubscriptionName /
// financeViewSubscriptionName / servicesViewSubscriptionName /
// viewportViewSubscriptionName are.
const districtsViewSubscriptionName = "f8.districts"
