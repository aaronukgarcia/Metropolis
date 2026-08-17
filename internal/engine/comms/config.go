package comms

import (
	"encoding/json"
	"os"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the GR#15 data-file contract: config is the validated,
// ordered view of data/comms.json, and LoadConfig is this package's
// self-contained loader (os.ReadFile + encoding/json + buildConfig, the
// engine.firms/engine.freight/engine.mining pattern — GR#20: a module's own
// data file is loaded without importing an unregistered edge). Every tunable
// number the comms module consumes — the per-era gate values, sector
// affinities, e-commerce/post/drain weights, and facility figures — comes
// from here, never a Go literal (GR#15). Loading is all-or-nothing: any
// failure returns ErrDataInvalid and no config.

// config is the fully-validated, ordered view of data/comms.json.
type config struct {
	eras           []eraConfig
	sectorAffinity [numSectors]float64
	eCommerce      eCommerceConfig
	post           postConfig
	drain          drainConfig
	fulfilment     facilityConfig
	lastMileDepot  facilityConfig
	postal         postalConfig
}

// eraConfig is one data/comms.json "eras" entry: the six §35 ladder rungs
// with their four capability-gate values plus the post/connectivity factors.
type eraConfig struct {
	ID                   string
	Name                 string
	Tier                 int
	OfficeTierCeiling    int
	DataCentreEligible   bool
	ResearchRateModifier float64
	RemoteWorkBase       float64
	LetterFactor         float64
	ParcelEraFactor      float64
	Connectivity         float64
	CellularSubTier      int
}

// eCommerceConfig parameterises the retail-share curve (AC-6).
type eCommerceConfig struct {
	ShareBase             float64
	ShareWealthWeight     float64
	NoInfrastructureFloor float64
}

// postConfig parameterises the letters-vs-parcels trend (AC-5).
type postConfig struct {
	BaseLetters             float64
	BaseParcels             float64
	ParcelWealthSensitivity float64
	ParcelShareSensitivity  float64
}

// drainConfig parameterises the high-street-drain/counterplay pair (AC-9).
type drainConfig struct {
	DrainPerShare           float64
	MaxCounterplayDampening float64
}

// facilityConfig is the firm-registration shape for the fulfilment centre
// and last-mile depot (AC-7): the §45 firm name, the staff count (the
// "thousands of jobs" figure, from data), the build zone class, and — for
// the last-mile depot — the logistics shelf capacity it provisions (AC-8).
type facilityConfig struct {
	Name          string
	Staff         int64
	Premises      string
	ShelfCapacity int64
}

// postalConfig carries the §35 postal service registration surface (US-5).
type postalConfig struct {
	Kind          string
	KindName      string
	SortingOffice postalInstance
	ParcelHub     postalInstance
}

// postalInstance is one postal service instance's registration fields.
type postalInstance struct {
	ID             string
	CapacityRaw    string
	CoverageRadius float64
}

// rawCommsData is data/comms.json's JSON wire shape, decoded only to be
// validated and folded into the ordered config above.
type rawCommsData struct {
	Version        int                `json:"version"`
	Eras           []rawEra           `json:"eras"`
	Sectors        map[string]float64 `json:"sectors"`
	ECommerce      rawECommerce       `json:"eCommerce"`
	Post           rawPost            `json:"post"`
	Drain          rawDrain           `json:"drain"`
	Fulfilment     rawFacility        `json:"fulfilment"`
	LastMileDepot  rawFacility        `json:"lastMileDepot"`
	PostalServices rawPostal          `json:"postalServices"`
}

type rawEra struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Tier                 int     `json:"tier"`
	OfficeTierCeiling    int     `json:"officeTierCeiling"`
	DataCentreEligible   bool    `json:"dataCentreEligible"`
	ResearchRateModifier float64 `json:"researchRateModifier"`
	RemoteWorkBase       float64 `json:"remoteWorkBase"`
	LetterFactor         float64 `json:"letterFactor"`
	ParcelEraFactor      float64 `json:"parcelEraFactor"`
	Connectivity         float64 `json:"connectivity"`
	CellularSubTier      int     `json:"cellularSubTier"`
}

type rawECommerce struct {
	ShareBase             float64 `json:"shareBase"`
	ShareWealthWeight     float64 `json:"shareWealthWeight"`
	NoInfrastructureFloor float64 `json:"noInfrastructureFloor"`
}

type rawPost struct {
	BaseLetters             float64 `json:"baseLetters"`
	BaseParcels             float64 `json:"baseParcels"`
	ParcelWealthSensitivity float64 `json:"parcelWealthSensitivity"`
	ParcelShareSensitivity  float64 `json:"parcelShareSensitivity"`
}

type rawDrain struct {
	DrainPerShare           float64 `json:"drainPerShare"`
	MaxCounterplayDampening float64 `json:"maxCounterplayDampening"`
}

type rawFacility struct {
	Name          string `json:"name"`
	Staff         int64  `json:"staff"`
	Premises      string `json:"premises"`
	ShelfCapacity int64  `json:"shelfCapacity"`
}

type rawPostal struct {
	Kind          string        `json:"kind"`
	KindName      string        `json:"kindName"`
	SortingOffice rawPostalInst `json:"sortingOffice"`
	ParcelHub     rawPostalInst `json:"parcelHub"`
}

type rawPostalInst struct {
	ID             string  `json:"id"`
	CapacityRaw    string  `json:"capacityRaw"`
	CoverageRadius float64 `json:"coverageRadius"`
}

// fileComms is data/comms.json's filename, relative to the resolved data
// directory (see foundation/data.ResolveDataDir).
const fileComms = "comms.json"

// eraOrder is the canonical §35 ladder order (AC-2), a slice so LoadConfig
// validates and stores eras in this fixed order (GR#21). The six slugs are
// §35's fixed vocabulary — a schema fact, not a balance number.
var eraOrder = []string{
	"telephone-exchange",
	"dial-up",
	"broadband-hub",
	"fibre-backbone",
	"cellular-masts",
	"submarine-cable",
}

// LoadConfig reads, decodes and validates data/comms.json from path,
// returning the ordered config or ErrDataInvalid. Every failure is a
// registry-sourced *errs.E — never a panic, never a silent default.
func LoadConfig(path, correlationID string) (config, error) {
	var zero config
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawCommsData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	return buildConfig(raw, path, correlationID)
}

func buildConfig(raw rawCommsData, path, correlationID string) (config, error) {
	fail := func(field, rule string) (config, error) {
		return config{}, errs.New(ErrDataInvalid, correlationID, map[string]any{
			"path":   path,
			"field":  field,
			"reason": rule,
		})
	}
	var c config

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Eras: exactly the six §35 rungs, in canonical order, each subsequent
	// rung never regressing a gate (AC-2's monotonic ladder). numEras is the
	// enum's own size; eraOrder is the canonical slug order — they must agree,
	// and data/comms.json must carry exactly that many rungs.
	if len(raw.Eras) != numEras || len(eraOrder) != numEras {
		return fail("eras", "must declare exactly the six §35 eras in order")
	}
	prev := rawEra{}
	for i, re := range raw.Eras {
		if re.ID != eraOrder[i] {
			return fail("eras["+itoa(i)+"].id", "must be "+eraOrder[i]+" (canonical §35 order)")
		}
		if re.Name == "" {
			return fail("eras["+itoa(i)+"].name", "required, must be a non-empty display name")
		}
		if re.Tier < 0 || re.Tier > 13 {
			return fail("eras["+itoa(i)+"].tier", "must be a milestone tier in 0..13")
		}
		if re.OfficeTierCeiling < 0 {
			return fail("eras["+itoa(i)+"].officeTierCeiling", "must be >= 0")
		}
		if re.ResearchRateModifier <= 0 {
			return fail("eras["+itoa(i)+"].researchRateModifier", "must be > 0")
		}
		if !inUnit(re.RemoteWorkBase) {
			return fail("eras["+itoa(i)+"].remoteWorkBase", "must be in [0,1]")
		}
		if !inUnit(re.LetterFactor) || re.LetterFactor <= 0 {
			return fail("eras["+itoa(i)+"].letterFactor", "must be in (0,1]")
		}
		if re.ParcelEraFactor < 1 {
			return fail("eras["+itoa(i)+"].parcelEraFactor", "must be >= 1 (parcels grow by era)")
		}
		if !inUnit(re.Connectivity) {
			return fail("eras["+itoa(i)+"].connectivity", "must be in [0,1]")
		}
		if re.CellularSubTier < 0 || re.CellularSubTier > 5 {
			return fail("eras["+itoa(i)+"].cellularSubTier", "must be in 0..5 (2G..5G)")
		}
		if i > 0 {
			if re.OfficeTierCeiling < prev.OfficeTierCeiling {
				return fail("eras["+itoa(i)+"].officeTierCeiling", "must not regress below the prior era (monotonic ladder)")
			}
			if re.Connectivity < prev.Connectivity {
				return fail("eras["+itoa(i)+"].connectivity", "must not regress below the prior era (monotonic ladder)")
			}
			if re.RemoteWorkBase < prev.RemoteWorkBase {
				return fail("eras["+itoa(i)+"].remoteWorkBase", "must not regress below the prior era (monotonic ladder)")
			}
			if re.ResearchRateModifier < prev.ResearchRateModifier {
				return fail("eras["+itoa(i)+"].researchRateModifier", "must not regress below the prior era (monotonic ladder)")
			}
			if re.LetterFactor > prev.LetterFactor {
				return fail("eras["+itoa(i)+"].letterFactor", "must not rise above the prior era (letters decline)")
			}
			if re.ParcelEraFactor < prev.ParcelEraFactor {
				return fail("eras["+itoa(i)+"].parcelEraFactor", "must not regress below the prior era (parcels grow)")
			}
			if prev.DataCentreEligible && !re.DataCentreEligible {
				return fail("eras["+itoa(i)+"].dataCentreEligible", "must stay true once a prior era is eligible (monotonic ladder)")
			}
		}
		c.eras = append(c.eras, eraConfig(re))
		prev = re
	}

	// Sectors: every one of the five buckets required, each affinity in [0,1].
	for i := 0; i < numSectors; i++ {
		slug := sectorSlug(Sector(i))
		aff, ok := raw.Sectors[slug]
		if !ok {
			return fail("sectors."+slug, "required sector affinity missing")
		}
		if !inUnit(aff) {
			return fail("sectors."+slug, "must be in [0,1]")
		}
		c.sectorAffinity[i] = aff
	}

	// eCommerce curve (AC-6).
	ec := raw.ECommerce
	if !inUnit(ec.ShareBase) {
		return fail("eCommerce.shareBase", "must be in [0,1]")
	}
	if ec.ShareWealthWeight < 0 {
		return fail("eCommerce.shareWealthWeight", "must be >= 0")
	}
	if !inUnit(ec.NoInfrastructureFloor) {
		return fail("eCommerce.noInfrastructureFloor", "must be in [0,1]")
	}
	c.eCommerce = eCommerceConfig(ec)

	// Post trend (AC-5).
	po := raw.Post
	if po.BaseLetters < 0 {
		return fail("post.baseLetters", "must be >= 0")
	}
	if po.BaseParcels < 0 {
		return fail("post.baseParcels", "must be >= 0")
	}
	if po.ParcelWealthSensitivity < 0 {
		return fail("post.parcelWealthSensitivity", "must be >= 0")
	}
	if po.ParcelShareSensitivity < 0 {
		return fail("post.parcelShareSensitivity", "must be >= 0")
	}
	c.post = postConfig(po)

	// Drain/counterplay (AC-9): the counterplay offset dampens but must never
	// fully cancel the raw drain, so the dampening ceiling is strictly < 1.
	dr := raw.Drain
	if dr.DrainPerShare < 0 {
		return fail("drain.drainPerShare", "must be >= 0")
	}
	if !inUnit(dr.MaxCounterplayDampening) || dr.MaxCounterplayDampening >= 1 {
		return fail("drain.maxCounterplayDampening", "must be in [0,1) so the offset never fully cancels the drain")
	}
	c.drain = drainConfig(dr)

	// Facility figures (AC-7's "thousands of jobs" is data, never a literal).
	if err := validateFacility("fulfilment", raw.Fulfilment, fail); err != nil {
		return config{}, err
	}
	c.fulfilment = facilityConfig(raw.Fulfilment)
	if err := validateFacility("lastMileDepot", raw.LastMileDepot, fail); err != nil {
		return config{}, err
	}
	c.lastMileDepot = facilityConfig(raw.LastMileDepot)

	// Postal service surface (US-5).
	ps := raw.PostalServices
	if ps.Kind == "" || ps.KindName == "" {
		return fail("postalServices.kind", "kind and kindName are required")
	}
	if ps.SortingOffice.ID == "" {
		return fail("postalServices.sortingOffice.id", "required")
	}
	if ps.ParcelHub.ID == "" {
		return fail("postalServices.parcelHub.id", "required")
	}
	if !num.IsFinite(ps.SortingOffice.CoverageRadius) || ps.SortingOffice.CoverageRadius < 0 {
		return fail("postalServices.sortingOffice.coverageRadius", "must be finite and >= 0")
	}
	if !num.IsFinite(ps.ParcelHub.CoverageRadius) || ps.ParcelHub.CoverageRadius < 0 {
		return fail("postalServices.parcelHub.coverageRadius", "must be finite and >= 0")
	}
	c.postal = postalConfig{
		Kind:          ps.Kind,
		KindName:      ps.KindName,
		SortingOffice: postalInstance{ID: ps.SortingOffice.ID, CapacityRaw: ps.SortingOffice.CapacityRaw, CoverageRadius: ps.SortingOffice.CoverageRadius},
		ParcelHub:     postalInstance{ID: ps.ParcelHub.ID, CapacityRaw: ps.ParcelHub.CapacityRaw, CoverageRadius: ps.ParcelHub.CoverageRadius},
	}

	return c, nil
}

// validateFacility checks a fulfilment/last-mile facility record: a name, a
// non-negative staff count, and a premises zone class.
func validateFacility(field string, f rawFacility, fail func(field, rule string) (config, error)) error {
	if f.Name == "" {
		_, err := fail(field+".name", "required, must be a non-empty firm name")
		return err
	}
	if f.Staff < 0 {
		_, err := fail(field+".staff", "must be >= 0")
		return err
	}
	if f.Premises == "" {
		_, err := fail(field+".premises", "required, must be a build zone slug")
		return err
	}
	if f.ShelfCapacity < 0 {
		_, err := fail(field+".shelfCapacity", "must be >= 0")
		return err
	}
	return nil
}

// inUnit reports whether v is a finite value in [0,1].
func inUnit(v float64) bool {
	return num.IsFinite(v) && v >= 0 && v <= 1
}

// itoa is the package-local int→string helper (no strconv.Itoa spam).
func itoa(i int) string { return strconv.Itoa(i) }
