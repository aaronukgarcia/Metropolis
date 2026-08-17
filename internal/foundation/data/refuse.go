package data

import (
	"encoding/json"
	"path/filepath"
	"sort"
)

// This file defines data/refuse.json's typed schema (engine.refuse,
// MOD-039), routed through the SAME generic [Load] every other module-
// owned config file uses — the identical split engine.market
// (LoadMarketFile) and engine.logistics (LoadLogisticsFile) already draw,
// not a self-contained loader. engine.refuse.Load performs any key-
// specific completeness checks (which land-use / stream keys this consumer
// requires) itself, the same way engine.logistics.Load checks all nine §6
// commodities are present after calling the shared loader.

// FileRefuse is data/refuse.json's filename, relative to the resolved data
// directory (see ResolveDataDir). Added here per the same MOD-020-ruling-1
// precedent that gave market.json its FileMarket constant rather than
// growing load.go's §24 constant block, which is written specifically about
// the eight files LoadAll aggregates.
const FileRefuse = "refuse.json"

// RefuseBinCapacityRecord is one data/refuse.json "binCapacities" entry:
// the documented per-land-use bin stock capacity (§25's wheelie/trade/skip
// distinction, AC-2). CapacityKg is the whole-kilogram bin capacity; the
// exact figures are unpinned balance data (see data/refuse.json's
// $comment), never Go literals in engine.refuse.
type RefuseBinCapacityRecord struct {
	Label      string `json:"label"`
	CapacityKg int64  `json:"capacityKg"`
	Comment    string `json:"comment,omitempty"`
}

// RefuseWasteRateRecord is one data/refuse.json "wasteRates" entry: the
// per-driver, per-tick waste generation rate for one land use. PerDriverPerTickKg
// is the placeholder standing in for §17's consumption-coefficient-derived
// waste rate (the engine.consumption edge is not yet registered — see
// engine.refuse.md Escalations), so it is read from data, never a Go
// literal (GR#15).
type RefuseWasteRateRecord struct {
	PerDriverPerTickKg float64 `json:"perDriverPerTickKg"`
	Comment            string  `json:"comment,omitempty"`
}

// RefuseStreamMix is data/refuse.json's "streamMix": the fraction of
// generated waste that lands in the recycling and food §25 streams.
// Fractions are in [0,1]. There is deliberately NO "general" fraction: the
// general stream is the exact remainder of the recycling/food split in
// engine.refuse's integer split, so the three per-tick allocations always
// sum exactly to the generated whole-kilogram figure (the AC-11 identity's
// per-tick source term). A separately-configured general fraction would be
// dead data — engine.refuse never reads it.
type RefuseStreamMix struct {
	Recycling float64 `json:"recycling"`
	Food      float64 `json:"food"`
}

// RefuseContamination is data/refuse.json's "contamination" block: the
// per-kg recycling resale baseline (in micro-pounds) and the fractional
// penalty per unit of contamination (AC-3's contamination reduces resale).
type RefuseContamination struct {
	ResaleValuePerKgMicropounds float64 `json:"resaleValuePerKgMicropounds"`
	PenaltyPerContamination     float64 `json:"penaltyPerContamination"`
}

// RefuseCompost is data/refuse.json's "compost" block: the food-waste →
// compost conversion ratio (AC-10, the data-sourced GR#15 ratio).
type RefuseCompost struct {
	ConversionRatio float64 `json:"conversionRatio"`
}

// RefuseVermin is data/refuse.json's "vermin" block: the overflow→vermin
// accumulation rate and the per-vermin land-value / fire-risk consequence
// rates (AC-7's causal spine, all directional placeholders).
type RefuseVermin struct {
	PerKgOverflowPerTick      float64 `json:"perKgOverflowPerTick"`
	LandValuePenaltyPerVermin float64 `json:"landValuePenaltyPerVermin"`
	FireRiskPerVermin         float64 `json:"fireRiskPerVermin"`
}

// RefuseIncineration is data/refuse.json's "incineration" block: the
// per-kg energy output and airshed-pollution cost of incineration (AC-9's
// trade-off — incineration is never strictly dominant because the airshed
// term has no landfill-side equivalent).
type RefuseIncineration struct {
	EnergyPerKg           float64 `json:"energyPerKg"`
	AirshedPollutionPerKg float64 `json:"airshedPollutionPerKg"`
}

// RefuseFunding is data/refuse.json's "funding" block: the refuse-service
// funding fraction below which a depot is treated as underfunded (AC-6's
// depot-underfunding miss cause, gated through engine.services' generic
// funding→quality path, US-4).
type RefuseFunding struct {
	FundingThreshold float64 `json:"fundingThreshold"`
}

// RefuseTrucks is data/refuse.json's "trucks" block: the per-truck
// collection capacity and the crews-per-truck conversion that turns
// refuse-crew staffing (engine.services' Public Service Pie refuseCrews
// benchmark, US-4) into a truck count (AC-6's truck-shortage miss cause).
type RefuseTrucks struct {
	TruckCapacityKg int64   `json:"truckCapacityKg"`
	CrewsPerTruck   float64 `json:"crewsPerTruck"`
}

// RefuseFile is data/refuse.json's top-level schema (§25/§31, MOD-039).
type RefuseFile struct {
	Version       int                                `json:"version"`
	Meta          json.RawMessage                    `json:"meta,omitempty"`
	BinCapacities map[string]RefuseBinCapacityRecord `json:"binCapacities"`
	WasteRates    map[string]RefuseWasteRateRecord   `json:"wasteRates"`
	StreamMix     RefuseStreamMix                    `json:"streamMix"`
	Contamination RefuseContamination                `json:"contamination"`
	Compost       RefuseCompost                      `json:"compost"`
	Vermin        RefuseVermin                       `json:"vermin"`
	Incineration  RefuseIncineration                 `json:"incineration"`
	Funding       RefuseFunding                      `json:"funding"`
	Trucks        RefuseTrucks                       `json:"trucks"`
}

// Validate implements Validator. Deterministic (sorted) iteration over the
// two maps so a refuse.json with MULTIPLE simultaneously-violating entries
// blames the same entry on every run against the byte-identical file
// (GR#21, BUG-098's lesson, mirrored from LogisticsFile.Validate).
func (r *RefuseFile) Validate() error {
	if err := requireVersion(r.Version); err != nil {
		return err
	}

	landUses := make([]string, 0, len(r.BinCapacities))
	for k := range r.BinCapacities {
		landUses = append(landUses, k)
	}
	sort.Strings(landUses)
	for _, k := range landUses {
		rec := r.BinCapacities[k]
		if rec.CapacityKg <= 0 {
			return fieldErr("binCapacities["+k+"].capacityKg", "must be > 0")
		}
	}

	rateNames := make([]string, 0, len(r.WasteRates))
	for k := range r.WasteRates {
		rateNames = append(rateNames, k)
	}
	sort.Strings(rateNames)
	for _, k := range rateNames {
		rec := r.WasteRates[k]
		if rec.PerDriverPerTickKg < 0 {
			return fieldErr("wasteRates["+k+"].perDriverPerTickKg", "must be >= 0")
		}
	}

	for _, f := range []struct {
		name string
		v    float64
	}{
		{"streamMix.recycling", r.StreamMix.Recycling},
		{"streamMix.food", r.StreamMix.Food},
	} {
		if f.v < 0 || f.v > 1 {
			return fieldErr(f.name, "must be in [0,1]")
		}
	}

	if r.Contamination.ResaleValuePerKgMicropounds < 0 {
		return fieldErr("contamination.resaleValuePerKgMicropounds", "must be >= 0")
	}
	if r.Contamination.PenaltyPerContamination < 0 || r.Contamination.PenaltyPerContamination > 1 {
		return fieldErr("contamination.penaltyPerContamination", "must be in [0,1]")
	}
	if r.Compost.ConversionRatio <= 0 || r.Compost.ConversionRatio > 1 {
		return fieldErr("compost.conversionRatio", "must be in (0,1]")
	}
	if r.Vermin.PerKgOverflowPerTick < 0 {
		return fieldErr("vermin.perKgOverflowPerTick", "must be >= 0")
	}
	if r.Vermin.LandValuePenaltyPerVermin < 0 {
		return fieldErr("vermin.landValuePenaltyPerVermin", "must be >= 0")
	}
	if r.Vermin.FireRiskPerVermin < 0 {
		return fieldErr("vermin.fireRiskPerVermin", "must be >= 0")
	}
	if r.Incineration.EnergyPerKg < 0 {
		return fieldErr("incineration.energyPerKg", "must be >= 0")
	}
	if r.Incineration.AirshedPollutionPerKg < 0 {
		return fieldErr("incineration.airshedPollutionPerKg", "must be >= 0")
	}
	if r.Funding.FundingThreshold < 0 || r.Funding.FundingThreshold > 1 {
		return fieldErr("funding.fundingThreshold", "must be in [0,1]")
	}
	if r.Trucks.TruckCapacityKg <= 0 {
		return fieldErr("trucks.truckCapacityKg", "must be > 0")
	}
	if r.Trucks.CrewsPerTruck <= 0 {
		return fieldErr("trucks.crewsPerTruck", "must be > 0")
	}
	return nil
}

// LoadRefuseFile loads and validates refuse.json from dir
// (engine.refuse, MOD-045's module-owned data — see this file's package-
// level doc comment). Not part of the eight-file §24 set LoadAll
// aggregates; engine.refuse.Load calls this directly, matching
// engine.market/engine.logistics' precedent for a module-owned loader
// built on this shared generic Load.
func LoadRefuseFile(dir, correlationID string) (RefuseFile, error) {
	return Load[RefuseFile, *RefuseFile](filepath.Join(dir, FileRefuse), correlationID)
}
