package airport

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the GR#15 data-file contract for engine.airport:
// airportConfig is the validated, ordered view of data/airport.json, and
// LoadAirportConfig is the loader. Every tunable number the airport module
// consumes — the airport-tier ladder (milestone + cost) and each tier's
// runways/per-runway-rate/gates/per-gate-rate/reach-multiplier/freight-apron/
// contour-radius/land-footprint/jobs/reduced-throughput-percentage — comes
// from here, never from a Go literal in this package (GR#15). The loader is
// self-contained (os.ReadFile + encoding/json + buildAirportConfig) so this
// module consumes no unregistered module edge for its own data file (GR#20),
// the same pattern engine.freight's LoadConfig and feat.containerport's
// LoadContainerPortConfig use. Loading is all-or-nothing: any missing/
// malformed/schema failure returns ErrAirportDataInvalid and no config —
// there is no partial ladder and no silent default substitution (AC-11).

// AccessTier is the §44 tourism access-tier ladder rung: no airport (none),
// then domestic, continental and global — a step function of the airport's
// tier/scale, never a flat "+tourists" bonus (AC-5).
type AccessTier string

const (
	AccessNone        AccessTier = "none"
	AccessDomestic    AccessTier = "domestic"
	AccessContinental AccessTier = "continental"
	AccessGlobal      AccessTier = "global"
)

// ordinal returns the ladder position of the access tier, used to validate
// and test the §44 reach ladder is monotonic non-decreasing (none < domestic
// < continental < global). An unrecognised tier returns -1 so it sorts below
// every real rung and fails the monotonic check rather than passing silently.
func (a AccessTier) ordinal() int {
	switch a {
	case AccessNone:
		return 0
	case AccessDomestic:
		return 1
	case AccessContinental:
		return 2
	case AccessGlobal:
		return 3
	default:
		return -1
	}
}

// isInternationalRunway reports whether the tier has an international-scale
// runway (AC-6): every rung above domestic does; a regional (domestic-only)
// airport does not, so the aerospace-campus requirement sheet's "runway
// access" is unsatisfiable by a domestic airport (§46).
func (a AccessTier) isInternationalRunway() bool {
	return a == AccessContinental || a == AccessGlobal
}

// AirportTier is one rung of the airport-tier ladder (AC-2/AC-3):
// regional_airport (domestic) -> continental_hub (continental) ->
// heathrow_class_international_airport (global), each with its milestone/cost
// (for ladder ordering) and the component/capacity figures the tier's
// throughput is computed from. Every figure is a data-driven placeholder
// (Disclosure names it pending Aaron's balance pass) — no final magnitude is
// pinned in Go.
type AirportTier struct {
	Key                      string
	Name                     string
	Milestone                int
	CostMillions             int64
	Runways                  int64
	PaxPerRunwayPerDay       int64
	TerminalGates            int64
	PaxPerGatePerDay         int64
	AccessTier               AccessTier
	ReachMultiplier          int64
	FreightApronTonnesPerDay int64
	BlightClass              mining.BlightClass
	ContourRadiusM           int64
	NoiseLevelDBA            int64
	LandFootprintHectares    int64
	Jobs                     int64
	RequiresRailSpur         bool
	SurfaceAccessReducedPct  int64
	Disclosure               string
}

// airportConfig is the fully-validated, ordered view of data/airport.json:
// the airport-tier ladder (sorted ascending by milestone, then cost, then key
// so [AirportAPI.Tiers] is deterministic even for equal-rank tiers) and a
// keyed lookup.
type airportConfig struct {
	tiers []AirportTier
	byKey map[string]AirportTier
}

// rawAirportData is data/airport.json's JSON wire shape, decoded only to be
// validated and folded into the ordered config above.
type rawAirportData struct {
	Version int              `json:"version"`
	Tiers   []rawAirportTier `json:"tiers"`
}

type rawAirportTier struct {
	Key                      string `json:"key"`
	Name                     string `json:"name"`
	Milestone                int    `json:"milestone"`
	CostMillions             int64  `json:"costMillions"`
	Runways                  int64  `json:"runways"`
	PaxPerRunwayPerDay       int64  `json:"paxPerRunwayPerDay"`
	TerminalGates            int64  `json:"terminalGates"`
	PaxPerGatePerDay         int64  `json:"paxPerGatePerDay"`
	AccessTier               string `json:"accessTier"`
	ReachMultiplier          int64  `json:"reachMultiplier"`
	FreightApronTonnesPerDay int64  `json:"freightApronTonnesPerDay"`
	BlightClass              string `json:"blightClass"`
	ContourRadiusM           int64  `json:"contourRadiusM"`
	NoiseLevelDBA            int64  `json:"noiseLevelDBA"`
	LandFootprintHectares    int64  `json:"landFootprintHectares"`
	Jobs                     int64  `json:"jobs"`
	RequiresRailSpur         bool   `json:"requiresRailSpur"`
	SurfaceAccessReducedPct  int64  `json:"surfaceAccessReducedPct"`
	Disclosure               string `json:"disclosure"`
}

// LoadAirportConfig reads, decodes and validates data/airport.json from path,
// returning the ordered config or ErrAirportDataInvalid. Every failure is a
// registry-sourced *errs.E — never a panic, never a silent default (AC-11).
func LoadAirportConfig(path, correlationID string) (airportConfig, error) {
	var zero airportConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrAirportDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawAirportData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrAirportDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildAirportConfig(raw, path, correlationID)
}

func buildAirportConfig(raw rawAirportData, path, correlationID string) (airportConfig, error) {
	fail := func(field, rule string) (airportConfig, error) {
		return airportConfig{}, errs.New(ErrAirportDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
			"cause": field + ": " + rule,
		})
	}

	var c airportConfig
	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if len(raw.Tiers) == 0 {
		return fail("tiers", "required, must list at least one airport tier")
	}

	c.byKey = make(map[string]AirportTier, len(raw.Tiers))
	for i, rt := range raw.Tiers {
		field := "tiers[" + itoa(i) + "]"
		if rt.Key == "" {
			return fail(field+".key", "required, must be a non-empty tier key")
		}
		if _, dup := c.byKey[rt.Key]; dup {
			return fail(field+".key", "duplicate tier key "+rt.Key)
		}
		if rt.Name == "" {
			return fail(field+".name", "required, must be a non-empty tier name")
		}
		if rt.Disclosure == "" {
			return fail(field+".disclosure", "required — every numeric entry must carry a non-empty disclosure naming it a placeholder pending Aaron's balance pass (AC-15)")
		}
		if rt.Milestone < 0 {
			return fail(field+".milestone", "must be >= 0")
		}
		if rt.CostMillions < 0 {
			return fail(field+".costMillions", "must be >= 0")
		}
		if rt.Runways <= 0 {
			return fail(field+".runways", "must be > 0")
		}
		if rt.PaxPerRunwayPerDay <= 0 {
			return fail(field+".paxPerRunwayPerDay", "must be > 0")
		}
		if rt.TerminalGates <= 0 {
			return fail(field+".terminalGates", "must be > 0")
		}
		if rt.PaxPerGatePerDay <= 0 {
			return fail(field+".paxPerGatePerDay", "must be > 0")
		}
		if rt.ReachMultiplier <= 0 {
			return fail(field+".reachMultiplier", "must be > 0")
		}
		if rt.FreightApronTonnesPerDay <= 0 {
			return fail(field+".freightApronTonnesPerDay", "must be > 0")
		}
		if rt.ContourRadiusM <= 0 {
			return fail(field+".contourRadiusM", "must be > 0")
		}
		if rt.NoiseLevelDBA <= 0 {
			return fail(field+".noiseLevelDBA", "must be > 0")
		}
		if rt.LandFootprintHectares <= 0 {
			return fail(field+".landFootprintHectares", "must be > 0")
		}
		if rt.Jobs < 0 {
			return fail(field+".jobs", "must be >= 0")
		}
		if rt.SurfaceAccessReducedPct <= 0 || rt.SurfaceAccessReducedPct >= 100 {
			return fail(field+".surfaceAccessReducedPct", "must be in the open interval (0, 100) — a degraded airport is materially reduced but never zero or full")
		}
		// Capacity bounds (SEC-117): the two binding capacity products must be
		// representable in int64, or a data file could drive runways×rate (or
		// gates×rate) into an int64 overflow that the throughput model would
		// otherwise silently misreport. Reject loudly at load — the bound is
		// derived from the representable range, never a hardcoded balance figure
		// (GR#15).
		if _, overflowed := num.SafeMul(rt.Runways, rt.PaxPerRunwayPerDay); overflowed {
			return fail(field+".runways", "runways × paxPerRunwayPerDay overflows int64 — bound the figures so no capacity product can overflow (SEC-117)")
		}
		if _, overflowed := num.SafeMul(rt.TerminalGates, rt.PaxPerGatePerDay); overflowed {
			return fail(field+".terminalGates", "terminalGates × paxPerGatePerDay overflows int64 — bound the figures so no capacity product can overflow (SEC-117)")
		}

		access, ok := accessTierByName(rt.AccessTier)
		if !ok || access == AccessNone {
			return fail(field+".accessTier", "must be one of domestic/continental/global (a tier never carries the 'none' rung — that rung is the no-airport case)")
		}
		blight, ok := blightClassByName(rt.BlightClass)
		if !ok {
			return fail(field+".blightClass", "unknown blight class (want low/moderate/high/severe)")
		}

		c.byKey[rt.Key] = AirportTier{
			Key:                      rt.Key,
			Name:                     rt.Name,
			Milestone:                rt.Milestone,
			CostMillions:             rt.CostMillions,
			Runways:                  rt.Runways,
			PaxPerRunwayPerDay:       rt.PaxPerRunwayPerDay,
			TerminalGates:            rt.TerminalGates,
			PaxPerGatePerDay:         rt.PaxPerGatePerDay,
			AccessTier:               access,
			ReachMultiplier:          rt.ReachMultiplier,
			FreightApronTonnesPerDay: rt.FreightApronTonnesPerDay,
			BlightClass:              blight,
			ContourRadiusM:           rt.ContourRadiusM,
			NoiseLevelDBA:            rt.NoiseLevelDBA,
			LandFootprintHectares:    rt.LandFootprintHectares,
			Jobs:                     rt.Jobs,
			RequiresRailSpur:         rt.RequiresRailSpur,
			SurfaceAccessReducedPct:  rt.SurfaceAccessReducedPct,
			Disclosure:               rt.Disclosure,
		}
	}

	// Tiers in ascending (milestone, cost, key) order — deterministic iteration
	// and the AC-2/AC-3 ladder ordering (GR#21). The sort is stable AND the key
	// tie-break makes the order total: two tiers sharing an equal (milestone,
	// cost) rank (a valid data file — the duplicate check is on key only)
	// otherwise fall back to map-iteration seed, producing distinct Tiers()
	// orderings across loads.
	c.tiers = make([]AirportTier, 0, len(c.byKey))
	for _, t := range c.byKey {
		c.tiers = append(c.tiers, t)
	}
	sort.SliceStable(c.tiers, func(i, j int) bool {
		if c.tiers[i].Milestone != c.tiers[j].Milestone {
			return c.tiers[i].Milestone < c.tiers[j].Milestone
		}
		if c.tiers[i].CostMillions != c.tiers[j].CostMillions {
			return c.tiers[i].CostMillions < c.tiers[j].CostMillions
		}
		return c.tiers[i].Key < c.tiers[j].Key
	})

	// Reach/access ladder validation (AC-5, GR#21): the §44 ladder
	// domestic → continental → global must be monotonic non-decreasing in both
	// the access-tier ordinal and the reach multiplier, with at least one
	// strict reach increase — a "step-change" ladder, never a flat bonus. An
	// inverted or flat ladder is ErrAirportDataInvalid, never a silently
	// re-sorted ladder (AC-11's data-authoring guard).
	if err := validateAccessLadder(c, path, correlationID); err != nil {
		return airportConfig{}, err
	}

	return c, nil
}

// accessTierByName resolves a data/airport.json "accessTier" string to its
// AccessTier value.
func accessTierByName(name string) (AccessTier, bool) {
	switch AccessTier(name) {
	case AccessDomestic, AccessContinental, AccessGlobal:
		return AccessTier(name), true
	case AccessNone:
		return AccessNone, true
	default:
		return "", false
	}
}

// blightClassByName resolves a data/airport.json "blightClass" string to
// engine.mining's BlightClass ordinal, deriving the names from mining's own
// canonical BlightClass.String table (GR#3 — no airport-local blight-name
// copy).
func blightClassByName(name string) (mining.BlightClass, bool) {
	for b := mining.BlightLow; b <= mining.BlightSevere; b++ {
		if b.String() == name {
			return b, true
		}
	}
	return 0, false
}

// validateAccessLadder rejects a data file whose §44 reach ladder is inverted
// or flat: across the milestone/cost-ordered tiers, the access-tier ordinal
// must be non-decreasing, the reach multiplier non-decreasing, and at least
// one adjacent pair must strictly increase in reach (AC-5).
func validateAccessLadder(c airportConfig, path, correlationID string) error {
	fail := func(rule string) error {
		return errs.New(ErrAirportDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": "tiers",
			"rule":  rule,
			"cause": "tiers: " + rule,
		})
	}

	strictIncrease := false
	for i := 1; i < len(c.tiers); i++ {
		prev, cur := c.tiers[i-1], c.tiers[i]
		if cur.AccessTier.ordinal() < prev.AccessTier.ordinal() {
			return fail(prev.Key + " must sort at or below " + cur.Key + " on the access-tier ladder — ladder inverted (AC-5)")
		}
		if cur.ReachMultiplier < prev.ReachMultiplier {
			return fail(prev.Key + " must carry a reach multiplier <= " + cur.Key + "'s — reach ladder inverted (AC-5)")
		}
		if cur.ReachMultiplier > prev.ReachMultiplier {
			strictIncrease = true
		}
	}
	if !strictIncrease {
		return fail("the §44 reach ladder must step-change: at least one adjacent tier pair must strictly increase in reach multiplier (AC-5)")
	}
	return nil
}

// itoa renders i as a base-10 string (mirrors engine.freight's params.go).
func itoa(i int) string {
	return strconv.Itoa(i)
}
