// Package power is engine.power's pylon catalogue and placement store
// (FEAT-1972079851, trio slice 1 of 3).
//
// Scope of this first slice: data/pylons.json's tier catalogue
// (catalogue.go — local pole / standard lattice / super grid; the HVDC
// France interconnector arrives as a later class, this package's class
// enum + catalogue schema are that seam), and placement of pylons as
// world line-segment objects carrying their tier's transmission capacity
// (api.go). The compose composition root publishes placed lines through
// "f1.viewport"'s powerLines field (omitempty — an engine with no placed
// pylons publishes nothing); the map consumers (internal/ui/screens/map,
// web MapCanvas) draw them under their 'Power' layer toggles.
//
// Deliberately NOT in this slice (documented seams, per the dispatch
// brief): network connectivity/flow solving, consumption coupling,
// Sellindge import integration, and the two later trio slices.
//
// Conventions: the loader is self-contained (imports only
// internal/foundation packages — no unregistered module edge, GR#20),
// loading is all-or-nothing with registry-sourced errors (GR#7/GR#15),
// every numeric domain check rejects rather than clamps, and iteration
// order is deterministic (GR#21). The *PowerAPI follows the house
// copy-guard discipline (SEC-020 family, astgate-scannable).
package power

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Registry error codes (GR#7). Range: G5200-G5299, claimed for
// FEAT-1972079851 via tools/plan/add-error.js claim-range (the E layer
// was fully exhausted long ago; BUG-234's four-digit widening applies).
// Every code below IS registered in data/errors.json's ranges.reserved
// table with real severity/module/message/remedy fields.
const (
	// ErrCatalogueDataInvalid: data/pylons.json could not be read or
	// failed schema validation (missing file, malformed JSON, a missing
	// or duplicate or unknown tier key, non-positive version/capacity/
	// cost/footprint, a non-finite capacity). The catalogue does NOT
	// proceed with silent defaults or a partially-populated tier set
	// (GR#15).
	ErrCatalogueDataInvalid = "MET-G5200"

	// ErrUnknownClass: a placement named a pylon class absent from the
	// loaded catalogue. Loud rejection, never a silent default-substituted
	// tier (GR#1).
	ErrUnknownClass = "MET-G5201"

	// ErrPlacementInvalid: a placement was rejected because its segment is
	// degenerate (both endpoints identical) or an endpoint falls outside
	// the tile-local cell domain. Loud rejection (GR#1).
	ErrPlacementInvalid = "MET-G5202"

	// ErrPowerAPICopied: a *PowerAPI method was called on a struct copy of
	// the value New returned (SEC-020 family, mirroring engine.mining's
	// ErrDepositMapCopied / engine.comms' ErrCopiedValue).
	ErrPowerAPICopied = "MET-G5203"
)

// PylonClass is one catalogue tier, in canonical enum order. The enum IS
// the taxonomy: the loader derives its accepted JSON keys from String()
// names, never from a second hand-maintained list (GR#15). Later trio
// slices append new classes here (e.g. an HVDC interconnector class) —
// appending keeps every existing ordinal stable (GR#21).
type PylonClass uint8

const (
	ClassLocalPole PylonClass = iota
	ClassStandardLattice
	ClassSuperGrid

	pylonClassCount
)

// String returns the data/pylons.json key for c. Out-of-range classes
// report "unknown" rather than panicking, mirroring mining's DepositType
// discipline.
func (c PylonClass) String() string {
	switch c {
	case ClassLocalPole:
		return "localPole"
	case ClassStandardLattice:
		return "standardLattice"
	case ClassSuperGrid:
		return "superGrid"
	default:
		return "unknown"
	}
}

// pylonClassByName resolves a data/pylons.json tier key to its
// PylonClass by iterating the enum's canonical String names.
func pylonClassByName(name string) (PylonClass, bool) {
	for c := PylonClass(0); c < pylonClassCount; c++ {
		if c.String() == name {
			return c, true
		}
	}
	return 0, false
}

// maxDataMagnitude is the upper bound on any single float magnitude read
// from data/pylons.json (mirroring engine/mining/params.go's overflow
// guard): a magnitude far above it overflows to +Inf/NaN once arithmetic
// touches it. A hostile or corrupt data edit is rejected at load time
// rather than let through to produce +Inf capacities.
const maxDataMagnitude = 1e12

// PylonTier is one class's validated catalogue row, in canonical enum
// order (see PylonCatalogue.Tiers).
type PylonTier struct {
	Class           PylonClass
	CapacityMW      float64 // transmission capacity per placed span
	CostMicropounds int64   // build cost per placed span
	FootprintCells  int     // siting footprint per endpoint pylon
}

// PylonCatalogue is the fully-validated, ordered view of data/pylons.json.
type PylonCatalogue struct {
	Version int
	Tiers   []PylonTier
}

// rawPylonData is the JSON wire shape of data/pylons.json, decoded only
// to be validated and folded into the ordered PylonCatalogue above.
type rawPylonData struct {
	Version int                `json:"version"`
	Tiers   map[string]rawTier `json:"tiers"`
}

type rawTier struct {
	CapacityMW      float64 `json:"capacityMW"`
	CostMicropounds int64   `json:"costMicropounds"`
	FootprintCells  int     `json:"footprintCells"`
}

// LoadDefault loads data/pylons.json from the resolved data directory
// (foundation/data.ResolveDataDir) — the constructor seam compose's Wire
// uses, mirroring refuse.LoadDefault/services.LoadDefault's shape.
func LoadDefault(correlationID string) (PylonCatalogue, error) {
	var zero PylonCatalogue
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return zero, errs.Wrap(ErrCatalogueDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	return LoadPylonCatalogue(filepath.Join(dir, "pylons.json"), correlationID)
}

// LoadPylonCatalogue reads, decodes and validates the pylon catalogue at
// path, returning the ordered PylonCatalogue or ErrCatalogueDataInvalid.
// Loading is all-or-nothing: any missing/malformed/schema failure returns
// an error and no catalogue — there is no partial tier set and no silent
// default substitution (AC-11 posture, GR#15).
func LoadPylonCatalogue(path, correlationID string) (PylonCatalogue, error) {
	var zero PylonCatalogue
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrCatalogueDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawPylonData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrCatalogueDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildCatalogue(raw, path, correlationID)
}

// buildCatalogue folds the decoded raw data into an ordered, validated
// PylonCatalogue. The canonical tier order is the PylonClass enum order,
// NOT the JSON object's key order (Go map iteration is randomised), so
// anything ranging Tiers is deterministic (GR#21).
func buildCatalogue(raw rawPylonData, path, correlationID string) (PylonCatalogue, error) {
	fail := func(field, rule string) (PylonCatalogue, error) {
		return PylonCatalogue{}, errs.New(ErrCatalogueDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if len(raw.Tiers) == 0 {
		return fail("tiers", "required, at least one tier must be present")
	}

	// Every key must resolve to a known PylonClass and every class must be
	// present exactly once — out-of-taxonomy keys are rejected before the
	// ordered fold (AC-11's own schema example names this failure).
	byClass := make(map[PylonClass]rawTier, len(raw.Tiers))
	for key, r := range raw.Tiers {
		c, ok := pylonClassByName(key)
		if !ok {
			return fail("tiers."+key, "unknown tier key: not in the pylon class taxonomy")
		}
		if _, dup := byClass[c]; dup {
			return fail("tiers."+key, "duplicate tier entry (name aliases a known class)")
		}
		byClass[c] = r
	}

	cat := PylonCatalogue{Version: raw.Version}
	cat.Tiers = make([]PylonTier, 0, len(byClass))
	for c := PylonClass(0); c < pylonClassCount; c++ {
		r, ok := byClass[c]
		if !ok {
			continue // classes appended by later trio slices may be absent
		}
		if !num.IsFinite(r.CapacityMW) || r.CapacityMW <= 0 || r.CapacityMW > maxDataMagnitude {
			return fail(fmt.Sprintf("tiers.%s.capacityMW", c), "must be finite and in (0, 1e12]")
		}
		if r.CostMicropounds <= 0 {
			return fail(fmt.Sprintf("tiers.%s.costMicropounds", c), "must be > 0")
		}
		if r.FootprintCells <= 0 {
			return fail(fmt.Sprintf("tiers.%s.footprintCells", c), "must be > 0")
		}
		cat.Tiers = append(cat.Tiers, PylonTier{
			Class:           c,
			CapacityMW:      r.CapacityMW,
			CostMicropounds: r.CostMicropounds,
			FootprintCells:  r.FootprintCells,
		})
	}
	return cat, nil
}

// Tier returns the catalogue row for c, ok false if absent (a class the
// file legitimately omits because a later slice owns it).
func (p PylonCatalogue) Tier(c PylonClass) (PylonTier, bool) {
	for _, t := range p.Tiers {
		if t.Class == c {
			return t, true
		}
	}
	return PylonTier{}, false
}
