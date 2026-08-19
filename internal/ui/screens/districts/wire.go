package districts

import "encoding/json"

// ViewSubscriptionName is F8's int.protocol view subscription. code.json's
// ui.screen.districts inbound entry has name=null (no view registered yet —
// this is an undispatched screen, the same convention every other
// not-yet-built F-screen shows per this item's own BA "Escalations" note),
// so this constant is this dispatch's own naming choice, following the
// "f<N>.<package>" pattern every other built F-screen already uses
// (ui.screen.finance's "f2.finance", ui.screen.services' "f4.services").
const ViewSubscriptionName = "f8.districts"

const wireSchemaVersion = 1
const maxPatchWireBytes = 1 << 20 // 1 MiB

// wireDistrictTaxSetting is one (district, instrument) pair's per-district
// tax setting (AC-6 / engine.tax.md AC-6): engine.tax.TaxAPI.
// SetDistrictMultiplier(district, instrumentID, multiplier) is the live,
// merged-on-main command this mirrors exactly — Multiplier stacks with the
// instrument's citywide Rate (effective rate = Rate x Multiplier); RateMax
// is the same SEC-098 cap SetDistrictMultiplier enforces engine-side
// (internal/engine/tax/tax.go's SetDistrictMultiplier), reused here so the
// screen can validate/display against the identical bound rather than
// inventing its own.
type wireDistrictTaxSetting struct {
	DistrictID      string  `json:"districtId"`
	InstrumentID    string  `json:"instrumentId"`
	InstrumentLabel string  `json:"instrumentLabel"`
	Multiplier      float64 `json:"multiplier"`
	Rate            float64 `json:"rate"`          // citywide headline rate (percent), TaxAPI.InstrumentInfo.Rate
	RateMax         float64 `json:"rateMax"`       // TaxAPI.InstrumentInfo.RateMax; effective rate = Rate*Multiplier must stay <= RateMax
	EffectiveRate   float64 `json:"effectiveRate"` // Rate*Multiplier, engine-computed
}

// wireDistrict is one named district (AC-2's CreateDistrict result once
// engine.policies lands — see doc.go's BLOCKED note). Only DistrictID/Name
// are populated today, sourced from... nothing yet: engine.policies is not
// on main, so this screen has no live source for the district roster
// itself. The field exists purely so wireDistrictTaxSetting's DistrictID
// values have a human label to render against IF a caller ever supplies
// one; ApplyDelta accepts an empty/absent Districts list today (no engine
// sends it) and RenderTaxSettings falls back to the raw DistrictID.
type wireDistrict struct {
	DistrictID string `json:"districtId"`
	Name       string `json:"name"`
}

type wirePatch struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Districts     *[]wireDistrict           `json:"districts,omitempty"`
	TaxSettings   *[]wireDistrictTaxSetting `json:"taxSettings,omitempty"`
}

func decodeWirePatch(raw json.RawMessage) (wirePatch, error) {
	if len(raw) > maxPatchWireBytes {
		return wirePatch{}, errPatchTooLarge(len(raw), maxPatchWireBytes)
	}
	var p wirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return wirePatch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return wirePatch{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	return p, nil
}
