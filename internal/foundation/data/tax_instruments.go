package data

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// This file defines data/tax_instruments.json's typed schema (FEAT-056 /
// engine.tax, MOD-052), routed through the SAME generic [Load] every other
// config file in this package uses — matching MarketFile's split
// (market.go) and Pacing's (pacing.go) rather than inventing a
// self-contained loader.
//
// SEC-089 / SEC-090: tax_instruments.json was authored and disclosure-clean
// but nothing read it, so malformed data was silently ignored (GR#17). This
// loader closes that gap: every failure — missing file, malformed JSON,
// missing version, schema violation — surfaces as a registry-sourced error
// via the shared [Load] path (CodeFileNotFound MET-F601, CodeMalformedJSON
// MET-F602, CodeMissingVersion MET-F603, CodeSchemaInvalid MET-F604, which
// renders "field {field}: {rule}") — never a silent default and never a
// silently-dropped instrument.
//
// # Instrument-ID convention (SEC-090 — see the logged ASM)
//
// data.taxinstruments.md AC-1 lists the six instrument IDs as vat,
// importDuties, corporationTax, paye, councilTax, businessRates AND, in the
// same sentence, demands they match engine.roads' lowercase-slug
// "buildingIDPattern" convention. Those two halves contradict each other:
// four of the six IDs are camelCase, not lowercase slugs. Per the dispatch
// brief, this loader accepts the six exact IDs as-authored and does NOT
// reject the file over the casing conflict; the criteria contradiction is
// flagged to Bill as an ASM rather than silently renaming the data (which
// would break the file's own meta assumptions and any future engine.tax
// lookup key).

// FileTaxInstruments is data/tax_instruments.json's filename, relative to
// the resolved data directory (see ResolveDataDir). Added per the same
// MOD-020-ruling-1 precedent that gave market.json its FileMarket constant
// rather than growing load.go's §24 constant block, which is written
// specifically about the eight files LoadAll aggregates.
const FileTaxInstruments = "tax_instruments.json"

// taxInstrumentIDs is the accepted six-instrument ID set FEAT-056 names
// (AC-1 / US-1), in a fixed order so the "missing required instrument"
// check and the "accepted set" error text are deterministic (GR#21). The
// IDs are accepted EXACTLY as authored in data/tax_instruments.json — see
// the package-level doc comment for the SEC-090 casing conflict.
var taxInstrumentIDs = []string{
	"vat",
	"importDuties",
	"corporationTax",
	"paye",
	"councilTax",
	"businessRates",
}

// taxInstrumentCategories is the closed category enum observed across the
// six instruments (US-1 / AC-3): consumption (VAT), import (import duties),
// corporateProfit (corporation tax), income (PAYE), property (council tax
// and business rates). An instrument with any other category is rejected,
// never silently defaulted.
var taxInstrumentCategories = map[string]bool{
	"consumption":     true,
	"import":          true,
	"corporateProfit": true,
	"income":          true,
	"property":        true,
}

// zoneClassEnum is §34's closed 8-way zone-class key set (AC-5), the only
// valid keys in any instrument's zoneOverrides map — matching the file's
// own meta.zoneClassEnum. A key outside this enum is rejected outright
// (weakness pattern #4: a zone-class key becomes a lookup key, so a
// spelling/casing variant is hostile input, never normalised).
var zoneClassEnum = map[string]bool{
	"dwelling": true, "shop": true, "office": true, "entertainment": true,
	"farming": true, "manufacturing": true, "heavyIndustry": true, "mining": true,
}

// bearerShareSumTolerance is the float-equality tolerance for the
// rate-point bearer shares summing to 1.0. Shares are legitimately
// fractional (M0-ENG §1.2 / the file's own meta), so an exact == comparison
// would false-reject a byte-correct file whose decimal shares round to
// 0.9999999999999999 in float64. Logged as an ASM (a chosen tolerance).
const bearerShareSumTolerance = 1e-9

// TaxInstruments is data/tax_instruments.json's top-level schema
// (FEAT-056): the six UK-today instruments, each carrying its name,
// category, valid rate range, elasticity coefficient, rate-dependent bearer
// weights, and an optional zone-override block. Meta is informational only
// (disclosure/provenance text) and is preserved as raw JSON, never
// validated field-by-field.
type TaxInstruments struct {
	Version     int                      `json:"version"`
	Meta        json.RawMessage          `json:"meta,omitempty"`
	Instruments map[string]TaxInstrument `json:"instruments"`
}

// TaxInstrument is one instrument entry (vat, importDuties, corporationTax,
// paye, councilTax, businessRates). RateRange, Elasticity, BearerWeights
// and NIRates are pointers so "absent" and "present-but-zero" are
// distinguishable (GR#16/GR#17): a missing required block is rejected, not
// silently decoded to a zero value.
type TaxInstrument struct {
	Name      string     `json:"name"`
	Category  string     `json:"category"`
	RateRange *RateRange `json:"rateRange"`

	// Elasticity is the base elasticity of the taxed base to the rate
	// (coefficient >= 0, per the file's own "fractional shrinkage" framing).
	Elasticity *Elasticity `json:"elasticity"`

	// BearerWeights is the rate-dependent incidence split (AC-6): at least
	// one rate point, each with bearers whose shares sum to 1.0.
	BearerWeights *BearerWeights `json:"bearerWeights"`

	// IncomeTaxBands and NIRates are PAYE's two independently-settable
	// sub-components (AC-2/AC-13): the banded income-tax structure and the
	// employee/employer National Insurance rates. Required for paye, absent
	// for every other instrument.
	IncomeTaxBands []IncomeTaxBand `json:"incomeTaxBands,omitempty"`
	NIRates        *NIRates        `json:"niRates,omitempty"`

	// ZoneOverrides maps a §34 zone class to a per-zone rate multiplier or
	// categorical relief (AC-4). May be empty.
	ZoneOverrides map[string]ZoneOverride `json:"zoneOverrides"`
}

// RateRange is an instrument's valid-rate domain (AC-1/AC-3): min and max
// percent caps. minPercent must be >= 0 and maxPercent >= minPercent; no
// upper cap is imposed because the shipped bounds (30..400) are themselves
// disclosed placeholders, not a spec-fixed ceiling (GR#15).
type RateRange struct {
	MinPercent float64 `json:"minPercent"`
	MaxPercent float64 `json:"maxPercent"`
	Comment    string  `json:"comment,omitempty"`
}

// Elasticity is an instrument's base-elasticity coefficient.
type Elasticity struct {
	Coefficient float64 `json:"coefficient"`
	Comment     string  `json:"comment,omitempty"`
}

// BearerWeights is the rate-dependent incidence split: a set of example
// rate points, each carrying the bearer shares at that rate.
type BearerWeights struct {
	Comment    string      `json:"comment,omitempty"`
	RatePoints []RatePoint `json:"ratePoints"`
}

// RatePoint is one example rate and the bearer split at that rate.
type RatePoint struct {
	RatePercent float64  `json:"ratePercent"`
	Comment     string   `json:"comment,omitempty"`
	Bearers     []Bearer `json:"bearers"`
}

// Bearer is one incidence-share holder (e.g. consumer vs firm) with its
// fractional share at the parent rate point.
type Bearer struct {
	Category string  `json:"category"`
	Share    float64 `json:"share"`
	Comment  string  `json:"comment,omitempty"`
}

// IncomeTaxBand is one PAYE income-tax band (AC-2): a micro-pounds lower
// bound, an upper bound (nil = no upper bound, the top band), and a
// percentage rate. Monetary bounds are int64 micro-pounds (M0-ENG §1.2,
// AC-10) — never float.
type IncomeTaxBand struct {
	LowerBoundMicroPounds int64   `json:"lowerBoundMicroPounds"`
	UpperBoundMicroPounds *int64  `json:"upperBoundMicroPounds"`
	RatePercent           float64 `json:"ratePercent"`
	Comment               string  `json:"comment,omitempty"`
}

// NIRates is PAYE's National Insurance pair (AC-2): the employee leg and
// the employer leg, independently settable.
type NIRates struct {
	EmployeePercent float64 `json:"employeePercent"`
	EmployerPercent float64 `json:"employerPercent"`
	Comment         string  `json:"comment,omitempty"`
}

// ZoneOverride is one per-zone adjustment (AC-4): a rate multiplier
// (e.g. 0.7 = 30% discount) or a categorical relief percentage
// (e.g. 100 = full exemption), never both absent.
type ZoneOverride struct {
	RateMultiplier *float64 `json:"rateMultiplier,omitempty"`
	ReliefPercent  *float64 `json:"reliefPercent,omitempty"`
	Comment        string   `json:"comment,omitempty"`
}

// Validate implements Validator. Every check here is a single-file
// structural/semantic rule (see the package-level doc comment); the
// "exactly these six instrument IDs" completeness check is included
// because the dispatch brief scoped it here rather than deferring it to
// engine.tax (unlike MarketFile/LogisticsFile, which push key-set
// completeness to the owning engine module). Errors name the offending
// instrument/field via [FieldError] so the generic [Load] reports them as
// CodeSchemaInvalid (MET-F604).
func (t *TaxInstruments) Validate() error {
	if err := requireVersion(t.Version); err != nil {
		return err
	}

	// Deterministic (sorted) iteration over the instrument map — Go map
	// iteration order is randomized per-run, so a file with MULTIPLE
	// violations would otherwise blame a different instrument on different
	// runs against the byte-identical file (GR#21, BUG-098's lesson,
	// mirrored from MarketFile.Validate).
	names := make([]string, 0, len(t.Instruments))
	for name := range t.Instruments {
		names = append(names, name)
	}
	sort.Strings(names)

	// Instrument-ID set: every present ID must be one of the six, and every
	// one of the six must be present — so the loaded set is EXACTLY the six
	// (no unknown IDs, none missing). A count-only check would pass six
	// arbitrary instruments (AC-1's false-pass warning); a superset-only
	// check would silently keep a stray seventh. Both directions are
	// rejected loudly.
	present := make(map[string]bool, len(names))
	for _, name := range names {
		if !isKnownTaxInstrumentID(name) {
			return fieldErr("instruments["+name+"]",
				fmt.Sprintf("unknown instrument ID %q (accepted set: %s)",
					name, strings.Join(taxInstrumentIDs, ", ")))
		}
		present[name] = true
	}
	for _, id := range taxInstrumentIDs {
		if !present[id] {
			return fieldErr("instruments", fmt.Sprintf("missing required instrument %q", id))
		}
	}

	// Per-instrument validation, still in sorted-ID order.
	for _, name := range names {
		if err := t.Instruments[name].validate(name); err != nil {
			return err
		}
	}

	return nil
}

// isKnownTaxInstrumentID reports whether id is one of the six accepted
// instrument IDs. A linear scan over the fixed six-element list keeps the
// accepted set single-sourced in taxInstrumentIDs (GR#3 — no duplicated
// map literal to drift).
func isKnownTaxInstrumentID(id string) bool {
	for _, known := range taxInstrumentIDs {
		if id == known {
			return true
		}
	}
	return false
}

// validate checks one instrument's fields, naming every failure with an
// "instruments[<id>].<field>" path.
func (ins TaxInstrument) validate(id string) error {
	prefix := "instruments[" + id + "]"

	if err := requireNonEmptyString(prefix+".name", ins.Name); err != nil {
		return err
	}

	if !taxInstrumentCategories[ins.Category] {
		return fieldErr(prefix+".category",
			fmt.Sprintf("must be one of consumption, import, corporateProfit, income, property; got %q", ins.Category))
	}

	if ins.RateRange == nil {
		return fieldErr(prefix+".rateRange", "required")
	}
	if err := ins.RateRange.validate(prefix + ".rateRange"); err != nil {
		return err
	}

	if ins.Elasticity == nil {
		return fieldErr(prefix+".elasticity", "required")
	}
	if ins.Elasticity.Coefficient < 0 {
		return fieldErr(prefix+".elasticity.coefficient",
			fmt.Sprintf("must be >= 0, got %v", ins.Elasticity.Coefficient))
	}

	if ins.BearerWeights == nil {
		return fieldErr(prefix+".bearerWeights", "required")
	}
	if err := ins.BearerWeights.validate(prefix+".bearerWeights", ins.RateRange); err != nil {
		return err
	}

	// PAYE-only sub-components (AC-2/AC-13): the income-tax band structure
	// and the NI pair are required for paye and must both be present — a
	// paye entry missing either is rejected, never treated as a flat rate.
	if id == "paye" {
		if len(ins.IncomeTaxBands) == 0 {
			return fieldErr(prefix+".incomeTaxBands",
				"required for paye (income tax is banded, never a single flat rate)")
		}
		for i, band := range ins.IncomeTaxBands {
			if err := band.validate(fmt.Sprintf("%s.incomeTaxBands[%d]", prefix, i)); err != nil {
				return err
			}
		}
		if ins.NIRates == nil {
			return fieldErr(prefix+".niRates",
				"required for paye (National Insurance is a separate deduction with its own employer leg)")
		}
		if err := ins.NIRates.validate(prefix + ".niRates"); err != nil {
			return err
		}
	}

	// zoneOverrides: keys must stay inside the closed 8-class enum (AC-5).
	zoneNames := make([]string, 0, len(ins.ZoneOverrides))
	for z := range ins.ZoneOverrides {
		zoneNames = append(zoneNames, z)
	}
	sort.Strings(zoneNames)
	for _, z := range zoneNames {
		if !zoneClassEnum[z] {
			return fieldErr(prefix+".zoneOverrides["+z+"]",
				fmt.Sprintf("zone class %q is outside the closed 8-class enum (dwelling, shop, office, entertainment, farming, manufacturing, heavyIndustry, mining)", z))
		}
		if err := ins.ZoneOverrides[z].validate(prefix + ".zoneOverrides[" + z + "]"); err != nil {
			return err
		}
	}

	return nil
}

func (rr RateRange) validate(prefix string) error {
	if rr.MinPercent < 0 {
		return fieldErr(prefix+".minPercent", fmt.Sprintf("must be >= 0, got %v", rr.MinPercent))
	}
	if rr.MaxPercent < rr.MinPercent {
		return fieldErr(prefix+".maxPercent",
			fmt.Sprintf("must be >= minPercent (%v), got %v", rr.MinPercent, rr.MaxPercent))
	}
	return nil
}

func (bw BearerWeights) validate(prefix string, rr *RateRange) error {
	if len(bw.RatePoints) == 0 {
		return fieldErr(prefix+".ratePoints", "required, must have at least one rate point")
	}
	for i, rp := range bw.RatePoints {
		rpPrefix := fmt.Sprintf("%s.ratePoints[%d]", prefix, i)
		if rp.RatePercent < rr.MinPercent || rp.RatePercent > rr.MaxPercent {
			return fieldErr(rpPrefix+".ratePercent",
				fmt.Sprintf("must be within rateRange [%v, %v], got %v", rr.MinPercent, rr.MaxPercent, rp.RatePercent))
		}
		if len(rp.Bearers) == 0 {
			return fieldErr(rpPrefix+".bearers", "required, must have at least one bearer")
		}
		sum := 0.0
		for j, b := range rp.Bearers {
			if err := requireNonEmptyString(fmt.Sprintf("%s.bearers[%d].category", rpPrefix, j), b.Category); err != nil {
				return err
			}
			if b.Share < 0 || b.Share > 1 {
				return fieldErr(fmt.Sprintf("%s.bearers[%d].share", rpPrefix, j),
					fmt.Sprintf("must be in [0, 1], got %v", b.Share))
			}
			sum += b.Share
		}
		if math.Abs(sum-1.0) > bearerShareSumTolerance {
			return fieldErr(rpPrefix+".bearers",
				fmt.Sprintf("shares must sum to 1.0, got %v", sum))
		}
	}
	return nil
}

func (b IncomeTaxBand) validate(prefix string) error {
	if b.LowerBoundMicroPounds < 0 {
		return fieldErr(prefix+".lowerBoundMicroPounds",
			fmt.Sprintf("must be >= 0, got %d", b.LowerBoundMicroPounds))
	}
	if b.UpperBoundMicroPounds != nil && *b.UpperBoundMicroPounds <= b.LowerBoundMicroPounds {
		return fieldErr(prefix+".upperBoundMicroPounds",
			fmt.Sprintf("must be > lowerBoundMicroPounds (%d), got %d (or null for no upper bound)",
				b.LowerBoundMicroPounds, *b.UpperBoundMicroPounds))
	}
	if b.RatePercent < 0 {
		return fieldErr(prefix+".ratePercent", fmt.Sprintf("must be >= 0, got %v", b.RatePercent))
	}
	return nil
}

func (n NIRates) validate(prefix string) error {
	if n.EmployeePercent < 0 {
		return fieldErr(prefix+".employeePercent", fmt.Sprintf("must be >= 0, got %v", n.EmployeePercent))
	}
	if n.EmployerPercent < 0 {
		return fieldErr(prefix+".employerPercent", fmt.Sprintf("must be >= 0, got %v", n.EmployerPercent))
	}
	return nil
}

func (z ZoneOverride) validate(prefix string) error {
	if z.RateMultiplier == nil && z.ReliefPercent == nil {
		return fieldErr(prefix, "must specify rateMultiplier or reliefPercent")
	}
	if z.RateMultiplier != nil && *z.RateMultiplier < 0 {
		return fieldErr(prefix+".rateMultiplier", fmt.Sprintf("must be >= 0, got %v", *z.RateMultiplier))
	}
	if z.ReliefPercent != nil && (*z.ReliefPercent < 0 || *z.ReliefPercent > 100) {
		return fieldErr(prefix+".reliefPercent", fmt.Sprintf("must be in [0, 100], got %v", *z.ReliefPercent))
	}
	return nil
}

// LoadTaxInstruments loads and schema-validates data/tax_instruments.json
// from dir. It is the canonical entry point for any consumer (notably
// engine.tax) that needs the six-instrument tax set, and it fails loudly on
// malformed/out-of-range/missing-required input rather than returning a
// silently-empty or partially-populated result (GR#17).
func LoadTaxInstruments(dir, correlationID string) (TaxInstruments, error) {
	return Load[TaxInstruments, *TaxInstruments](filepath.Join(dir, FileTaxInstruments), correlationID)
}
