package roads

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// maxPoundsPerMoney is the largest whole-pound amount det.FromPounds can
// scale to Micropounds without overflowing int64 (math.MaxInt64 / 1e6 ≈
// 9.2e12). baseCostPounds/landCostPerCellPounds are bounded to it below so a
// schema-valid-but-hostile value cannot wrap negative inside FromPounds's
// ×1e6 multiply and turn a same-width upgrade into a negative quote
// (SEC-230).
const maxPoundsPerMoney = math.MaxInt64 / int64(det.MicropoundsPerPound)

// maxWidthCells is the largest road footprint width (in cells) a class may
// declare. computeFootprint dilates the Bresenham centerline with a square
// stamp of roughly widthCells² cells per step, so a schema-valid-but-hostile
// widthCells (e.g. 1,000,000) would make AddRoad/widening spend O(10^12)
// stamp operations per Bresenham step and exhaust memory (SEC-231). The cap
// must bound that O(width²) stamp to sane CPU, not just to out-of-memory:
// the widest real class in data/roads.json is a motorway at 5 cells, so a
// full-extent diagonal at that width is ~120k stamp operations, whereas a
// width of one whole world tile (world.TileSizeCells = 200) would stamp
// ~2.4M cells / ~240M operations in one AddRoad — tens of seconds of CPU
// (SEC-235). 24 cells is a generous realistic road-width ceiling (nearly 5x
// the widest class) that keeps the worst-case stamp bounded to sane CPU.
// WidthCells is validated against it in buildConfig, the single constructor
// every config flows through.
const maxWidthCells = 24

// This file is the GR#15 data-file contract: config is the validated,
// ordered view of data/roads.json, and LoadConfig is this package's
// self-contained loader (os.ReadFile + encoding/json + buildConfig — the
// engine.comms/engine.maintenance pattern; GR#20: a module's own data file
// is loaded without importing an unregistered edge). Every tunable number
// this module consumes — the eleven class rungs, the maintenance decay/
// penalty factors, the upgrade cost factors, and the roadworks phase
// defaults — comes from here, never a Go literal (GR#15). Loading is
// all-or-nothing: any failure returns ErrRoadsDataInvalid and no config.

// config is the fully-validated, ordered view of data/roads.json.
type config struct {
	classes     [numClasses]classConfig
	maintenance maintenanceConfig
	upgrade     upgradeConfig
	roadworks   roadworksConfig
	naming      namingConfig
}

// classConfig is one data/roads.json "classes" entry: the §51 per-rung
// attribute set (AC-3).
type classConfig struct {
	ID             string
	Name           string
	Lanes          int
	SpeedLimit     int
	SpeedMin       int
	SpeedMax       int
	Parking        bool
	TreeVerge      bool
	WidthCells     int
	BaseCostPounds int64
}

// maintenanceConfig parameterises per-road condition decay and the pothole
// cost/speed effects (US-6/AC-2).
type maintenanceConfig struct {
	ConditionDecayPerMonth          float64
	SpeedPenaltyPerConditionBelow   float64
	CostMultiplierPerConditionBelow float64
	RepairConditionPerPound         float64
}

// upgradeConfig parameterises the in-place upgrade cost model (AC-4): the
// rung-distance scaling, the rebuild-disruption fraction, and the per-cell
// land-purchase cost for footprint widening (AC-5). The two "permille"
// fields are integer per-mille so money math stays fixed-point (GR#16 —
// float64 never touches a Micropounds computation).
type upgradeConfig struct {
	RungDistanceCostPermille  int64
	RebuildDisruptionPermille int64
	LandCostPerCellPounds     int64
}

// roadworksConfig parameterises the default roadworks phase an upgrade
// schedules (AC-6): its duration and the fraction of lanes closed during a
// phase.
type roadworksConfig struct {
	PhaseDurationMonths   int64
	LaneReductionFraction float64
}

// namingConfig carries the non-road object-kind naming vocabularies (§20):
// the civic-type, infrastructure-type and transit-colour lists. The Kentish
// toponyms and road suffixes live in data/naming_corpus.json (foundation.
// data) and are reused for roads/districts; these lists are the placeholder
// vocabularies §20 names by example but does not enumerate authoritatively.
type namingConfig struct {
	CivicTypes          []string
	InfrastructureTypes []string
	TransitColours      []string
}

// rawRoadsData is data/roads.json's JSON wire shape, decoded only to be
// validated and folded into the ordered config above.
type rawRoadsData struct {
	Version     int            `json:"version"`
	Classes     []rawClass     `json:"classes"`
	Maintenance rawMaintenance `json:"maintenance"`
	Upgrade     rawUpgrade     `json:"upgrade"`
	Roadworks   rawRoadworks   `json:"roadworks"`
	Naming      rawNaming      `json:"naming"`
}

type rawClass struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Lanes          int    `json:"lanes"`
	SpeedLimit     int    `json:"speedLimit"`
	SpeedMin       int    `json:"speedMin"`
	SpeedMax       int    `json:"speedMax"`
	Parking        bool   `json:"parking"`
	TreeVerge      bool   `json:"treeVerge"`
	WidthCells     int    `json:"widthCells"`
	BaseCostPounds int64  `json:"baseCostPounds"`
}

type rawMaintenance struct {
	ConditionDecayPerMonth          float64 `json:"conditionDecayPerMonth"`
	SpeedPenaltyPerConditionBelow   float64 `json:"speedPenaltyPerConditionBelow"`
	CostMultiplierPerConditionBelow float64 `json:"costMultiplierPerConditionBelow"`
	RepairConditionPerPound         float64 `json:"repairConditionPerPound"`
}

type rawUpgrade struct {
	RungDistanceCostPermille  int64 `json:"rungDistanceCostPermille"`
	RebuildDisruptionPermille int64 `json:"rebuildDisruptionPermille"`
	LandCostPerCellPounds     int64 `json:"landCostPerCellPounds"`
}

type rawRoadworks struct {
	PhaseDurationMonths   int64   `json:"phaseDurationMonths"`
	LaneReductionFraction float64 `json:"laneReductionFraction"`
}

type rawNaming struct {
	CivicTypes          []string `json:"civicTypes"`
	InfrastructureTypes []string `json:"infrastructureTypes"`
	TransitColours      []string `json:"transitColours"`
}

// fileRoads is data/roads.json's filename, relative to the resolved data
// directory (see foundation/data.ResolveDataDir).
const fileRoads = "roads.json"

// LoadConfig reads, decodes and validates data/roads.json from path,
// returning the ordered config or ErrRoadsDataInvalid. Every failure is a
// registry-sourced *errs.E — never a panic, never a silent default.
func LoadConfig(path, correlationID string) (config, error) {
	var zero config
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrRoadsDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawRoadsData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrRoadsDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	return buildConfig(raw, path, correlationID)
}

func buildConfig(raw rawRoadsData, path, correlationID string) (config, error) {
	fail := func(field, rule string) (config, error) {
		return config{}, errs.New(ErrRoadsDataInvalid, correlationID, map[string]any{
			"path":   path,
			"field":  field,
			"reason": rule,
		})
	}
	var c config

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Classes: exactly the eleven §51 rungs, in canonical ladder order. The
	// slug order is fixed vocabulary (classSlugs) and data/roads.json must
	// carry exactly that many rungs in that order (GR#21 — the ladder is a
	// fixed slice, never a map).
	if len(raw.Classes) != int(numClasses) {
		return fail("classes", "must declare exactly the eleven §51 rungs in ladder order")
	}
	for i, rc := range raw.Classes {
		field := "classes[" + itoa(i) + "]"
		if rc.ID != classSlugs[i] {
			return fail(field+".id", "must be "+classSlugs[i]+" (canonical §51 order)")
		}
		if rc.Name == "" {
			return fail(field+".name", "required, must be a non-empty display name")
		}
		if rc.Lanes <= 0 {
			return fail(field+".lanes", "must be > 0")
		}
		if rc.SpeedLimit <= 0 || rc.SpeedMin <= 0 || rc.SpeedMax <= 0 {
			return fail(field+".speedLimit", "speed values must be > 0")
		}
		if rc.SpeedLimit < rc.SpeedMin || rc.SpeedLimit > rc.SpeedMax {
			return fail(field+".speedLimit", "must fall within speedMin..speedMax")
		}
		if rc.WidthCells <= 0 {
			return fail(field+".widthCells", "must be > 0")
		}
		if rc.WidthCells > maxWidthCells {
			return fail(field+".widthCells", "must be <= "+strconv.Itoa(maxWidthCells)+" (the realistic road-width bound; a wider stamp would exhaust memory and CPU in the footprint walk)")
		}
		if rc.BaseCostPounds < 0 || rc.BaseCostPounds > maxPoundsPerMoney {
			return fail(field+".baseCostPounds", "must be in [0, "+strconv.FormatInt(maxPoundsPerMoney, 10)+"] (fits the Micropounds ×1e6 scale)")
		}
		c.classes[i] = classConfig(rc)
	}

	// Maintenance (US-6/AC-2).
	m := raw.Maintenance
	if !unitRange(m.ConditionDecayPerMonth) || m.ConditionDecayPerMonth <= 0 {
		return fail("maintenance.conditionDecayPerMonth", "must be in (0,1]")
	}
	if !unitRange(m.SpeedPenaltyPerConditionBelow) {
		return fail("maintenance.speedPenaltyPerConditionBelow", "must be in [0,1]")
	}
	if m.CostMultiplierPerConditionBelow < 0 {
		return fail("maintenance.costMultiplierPerConditionBelow", "must be >= 0")
	}
	if !unitRange(m.RepairConditionPerPound) || m.RepairConditionPerPound <= 0 {
		return fail("maintenance.repairConditionPerPound", "must be in (0,1]")
	}
	c.maintenance = maintenanceConfig(m)

	// Upgrade (AC-4/AC-5).
	u := raw.Upgrade
	if u.RungDistanceCostPermille < 0 {
		return fail("upgrade.rungDistanceCostPermille", "must be >= 0")
	}
	if u.RebuildDisruptionPermille < 0 {
		return fail("upgrade.rebuildDisruptionPermille", "must be >= 0")
	}
	if u.LandCostPerCellPounds < 0 || u.LandCostPerCellPounds > maxPoundsPerMoney {
		return fail("upgrade.landCostPerCellPounds", "must be in [0, "+strconv.FormatInt(maxPoundsPerMoney, 10)+"] (fits the Micropounds ×1e6 scale)")
	}
	c.upgrade = upgradeConfig(u)

	// Roadworks (AC-6).
	rw := raw.Roadworks
	if rw.PhaseDurationMonths <= 0 {
		return fail("roadworks.phaseDurationMonths", "must be > 0")
	}
	if !unitRange(rw.LaneReductionFraction) || rw.LaneReductionFraction <= 0 {
		return fail("roadworks.laneReductionFraction", "must be in (0,1]")
	}
	c.roadworks = roadworksConfig(rw)

	// Naming vocabularies (§20, non-road kinds).
	nm := raw.Naming
	if err := validateWordList("naming.civicTypes", nm.CivicTypes, fail); err != nil {
		return config{}, err
	}
	if err := validateWordList("naming.infrastructureTypes", nm.InfrastructureTypes, fail); err != nil {
		return config{}, err
	}
	if err := validateWordList("naming.transitColours", nm.TransitColours, fail); err != nil {
		return config{}, err
	}
	c.naming = namingConfig(nm)

	return c, nil
}

// validateWordList checks a naming vocabulary list is non-empty with
// non-blank, distinct entries.
func validateWordList(field string, words []string, fail func(field, rule string) (config, error)) error {
	if len(words) == 0 {
		_, err := fail(field, "required, must be non-empty")
		return err
	}
	seen := make(map[string]bool, len(words))
	for i, w := range words {
		if w == "" {
			_, err := fail(field+"["+itoa(i)+"]", "required, must be non-empty")
			return err
		}
		if seen[w] {
			_, err := fail(field+"["+itoa(i)+"]", "duplicate entry "+w)
			return err
		}
		seen[w] = true
	}
	return nil
}

// unitRange reports whether v is a finite value in [0,1].
func unitRange(v float64) bool {
	return num.IsFinite(v) && v >= 0 && v <= 1
}

// itoa is the package-local int→string helper.
func itoa(i int) string { return strconv.Itoa(i) }

// loadRoadsConfig resolves the data directory relative to the given dir
// (or the repo root via ResolveDataDir when dir is empty) and loads
// data/roads.json. Used by Load; kept separate so tests can pass an
// explicit temp dir with a fixture.
func loadRoadsConfig(dir, correlationID string) (config, error) {
	return LoadConfig(filepath.Join(dir, fileRoads), correlationID)
}
