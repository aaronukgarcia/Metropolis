package fuel

import (
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileFuel is data/fuel.json's filename, relative to the resolved data
// directory (see data.ResolveDataDir).
const fileFuel = "fuel.json"

// fuelData is the decoded data/fuel.json surface (GR#15: every balance figure
// fuel consumes lives here, never as a Go literal in *.go). It is a thin
// mirror of the JSON schema used only by [Load]; once validated, the values
// are copied into [FuelAPI]'s runtime representation, so query methods never
// dereference a possibly-nil *float64 EV-share field.
type fuelData struct {
	Version int `json:"version"`

	Eras []fuelEra `json:"eras"`

	FuelDemand struct {
		Comment                     string  `json:"comment,omitempty"`
		CarLitresPerTick            float64 `json:"carLitresPerTick"`
		VanLitresPerTick            float64 `json:"vanLitresPerTick"`
		TruckLitresPerTick          float64 `json:"truckLitresPerTick"`
		LogisticsFleetLitresPerTick float64 `json:"logisticsFleetLitresPerTick"`
	} `json:"fuelDemand"`

	ChargingProfile struct {
		Comment        string      `json:"comment,omitempty"`
		BaseKWhPerTick float64     `json:"baseKWhPerTick"`
		HourlyWeight   [24]float64 `json:"hourlyWeight"`
	} `json:"chargingProfile"`

	StrategicReserve struct {
		Comment     string  `json:"comment,omitempty"`
		DaysOfCover float64 `json:"daysOfCover"`
	} `json:"strategicReserve"`

	Duty struct {
		Comment           string  `json:"comment,omitempty"`
		RatePencePerLitre float64 `json:"ratePencePerLitre"`
		TaxInstrument     string  `json:"taxInstrument"`
	} `json:"duty"`

	Forecourt struct {
		Comment                               string  `json:"comment,omitempty"`
		TargetForecourtsPerThousandPopulation float64 `json:"targetForecourtsPerThousandPopulation"`
	} `json:"forecourt"`

	Tanker struct {
		Comment                     string  `json:"comment,omitempty"`
		PortThroughputLitresPerTick float64 `json:"portThroughputLitresPerTick"`
	} `json:"tanker"`

	// Meta is the file's documentation block. It and the per-section
	// Comment fields above are provenance prose (ASM-307 placeholder
	// disclosures) — never consumed at runtime, but DECLARED because the
	// BUG-281 r2 strict loader rejects undeclared fields and only strips
	// $-prefixed keys at a file's top level, never nested ones.
	Meta json.RawMessage `json:"meta,omitempty"`
}

// fuelEra is one milestone era's fleet-composition EV-share row. EV-share
// fields are *float64 (not float64) so a JSON author omitting one decodes to
// nil and is REJECTED by Validate — distinguishing "missing" from a legitimate
// 0.0 early-era figure (AC-8: a missing EV-share must never silently default
// to zero and read as "no EV adoption yet").
type fuelEra struct {
	Comment      string   `json:"comment,omitempty"`
	Era          string   `json:"era"`
	CarEVShare   *float64 `json:"carEVShare"`
	VanEVShare   *float64 `json:"vanEVShare"`
	TruckEVShare *float64 `json:"truckEVShare"`
}

// Validate implements foundation/data.Validator (pointer receiver) so the
// generic data.Load can run schema-level validation immediately after JSON
// decoding. It returns a *data.FieldError naming the offending field/rule;
// data.Load maps a "version" FieldError to its dedicated MissingVersion code.
func (f *fuelData) Validate() error {
	if f.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(f.Eras) == 0 {
		return &data.FieldError{Field: "eras", Rule: "required, must declare at least one era"}
	}
	seenEras := map[string]bool{}
	for i := range f.Eras {
		e := &f.Eras[i]
		prefix := "eras[" + itoa(i) + "]"
		if e.Era == "" {
			return &data.FieldError{Field: prefix + ".era", Rule: "required, must be a non-empty era key"}
		}
		if seenEras[e.Era] {
			return &data.FieldError{Field: prefix + ".era", Rule: "duplicate era key " + e.Era}
		}
		seenEras[e.Era] = true
		for _, seg := range []struct {
			field string
			v     *float64
		}{
			{prefix + ".carEVShare", e.CarEVShare},
			{prefix + ".vanEVShare", e.VanEVShare},
			{prefix + ".truckEVShare", e.TruckEVShare},
		} {
			if seg.v == nil {
				return &data.FieldError{Field: seg.field, Rule: "required — a defined era must declare its EV-share figure (missing would silently read as 0% EV, masking a data-authoring bug)"}
			}
			if !num.IsFinite(*seg.v) || *seg.v < 0 || *seg.v > 1 {
				return &data.FieldError{Field: seg.field, Rule: "must be a finite fraction in [0,1], got " + ftoa(*seg.v)}
			}
		}
	}

	if !num.IsFinite(f.FuelDemand.CarLitresPerTick) || !num.IsFinite(f.FuelDemand.VanLitresPerTick) ||
		!num.IsFinite(f.FuelDemand.TruckLitresPerTick) || !num.IsFinite(f.FuelDemand.LogisticsFleetLitresPerTick) {
		return &data.FieldError{Field: "fuelDemand", Rule: "per-segment litre figures must be finite (not NaN/±Inf)"}
	}
	if f.FuelDemand.CarLitresPerTick < 0 || f.FuelDemand.VanLitresPerTick < 0 ||
		f.FuelDemand.TruckLitresPerTick < 0 || f.FuelDemand.LogisticsFleetLitresPerTick < 0 {
		return &data.FieldError{Field: "fuelDemand", Rule: "per-segment litre figures must be >= 0"}
	}

	sum := 0.0
	for h, w := range f.ChargingProfile.HourlyWeight {
		if !num.IsFinite(w) {
			return &data.FieldError{Field: "chargingProfile.hourlyWeight[" + itoa(h) + "]", Rule: "must be finite (not NaN/±Inf)"}
		}
		if w < 0 {
			return &data.FieldError{Field: "chargingProfile.hourlyWeight[" + itoa(h) + "]", Rule: "must be >= 0 (a negative charging-load value is never valid)"}
		}
		sum += w
	}
	if sum <= 0 {
		return &data.FieldError{Field: "chargingProfile.hourlyWeight", Rule: "must contain at least one positive weight (an all-zero profile would silently read as 'no charging demand')"}
	}
	if !num.IsFinite(f.ChargingProfile.BaseKWhPerTick) || f.ChargingProfile.BaseKWhPerTick < 0 {
		return &data.FieldError{Field: "chargingProfile.baseKWhPerTick", Rule: "must be finite and >= 0"}
	}

	if !num.IsFinite(f.StrategicReserve.DaysOfCover) || f.StrategicReserve.DaysOfCover <= 0 {
		return &data.FieldError{Field: "strategicReserve.daysOfCover", Rule: "must be finite and > 0"}
	}
	if !num.IsFinite(f.Duty.RatePencePerLitre) || f.Duty.RatePencePerLitre <= 0 {
		return &data.FieldError{Field: "duty.ratePencePerLitre", Rule: "must be finite and > 0"}
	}
	if f.Duty.TaxInstrument == "" {
		return &data.FieldError{Field: "duty.taxInstrument", Rule: "required, must name an engine.tax instrument"}
	}
	if !num.IsFinite(f.Forecourt.TargetForecourtsPerThousandPopulation) || f.Forecourt.TargetForecourtsPerThousandPopulation <= 0 {
		return &data.FieldError{Field: "forecourt.targetForecourtsPerThousandPopulation", Rule: "must be finite and > 0"}
	}
	if !num.IsFinite(f.Tanker.PortThroughputLitresPerTick) || f.Tanker.PortThroughputLitresPerTick < 0 {
		return &data.FieldError{Field: "tanker.portThroughputLitresPerTick", Rule: "must be finite and >= 0"}
	}
	return nil
}

// loadFuelData reads and schema-validates data/fuel.json from dir through
// foundation/data's generic Load (GR#3 reuse: JSON decode, duplicate-key
// detection, version check, and FieldError-based schema validation all come
// from foundation/data, never a re-implemented loader). Every failure is
// wrapped in the registry-sourced ErrFuelDataInvalid — never a silent default.
func loadFuelData(dir, correlationID string) (fuelData, error) {
	f, err := data.Load[fuelData, *fuelData](filepath.Join(dir, fileFuel), correlationID)
	if err != nil {
		return fuelData{}, errs.Wrap(ErrFuelDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return f, nil
}

// itoa/ ftoa are tiny formatting helpers kept local so data.go's Validate can
// name the offending index/value in a FieldError without spelling strconv
// calls inline at every site.
func itoa(i int) string {
	return strconv.Itoa(i)
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
