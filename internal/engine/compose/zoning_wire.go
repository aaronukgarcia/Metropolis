package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-199: the zoning-density vertical slice's compose half.
//
// data/zoning.json (loaded via foundation/data's shared LoadZoningFile —
// the same generic loader every other catalogue uses, NOT a bespoke
// decode) declares six zone families, each with a density ladder 1..5 and
// semantic palette keys. Each family also lists the §34 eight-way
// build-catalogue slugs that resolve into it, so the slug->family bridge
// below is DATA-DRIVEN (GR#15): adding or re-homing a slug is an edit to
// zoning.json, never a Go literal here.
//
// Two seams live in this file:
//
//  1. The WRITE-THROUGH (handleGameplay KindZone): after engine.build
//     accepts the zoning, compose records the family + density into
//     engine.world's per-cell ledger via ApplyOwnershipCommand (the
//     registered compose->world edge), so Cell.Zoning/ZoningDensity are
//     real state every future consumer can read.
//
//  2. The VIEW PUBLISH (buildViewportPatch, viewport_publish.go): each
//     zoned cell publishes its family id, density level and semantic
//     palette colour key on the f1.viewport wire. The UI resolves colours
//     from its own data-driven palette injection (mapscreen.SetZonePalette,
//     fed from this same file's Colours by cmd/metropolis) — the UI never
//     hardcodes a colour and never reads engine types (GR#20/AC-1).

// loadZoningCatalogue resolves the data/ directory (foundation/data's
// documented resolution order) and loads+validates data/zoning.json.
// Every failure is a registry-sourced *errs.E under ErrModuleFailed
// (GR#7) — never a silent default substitution.
func loadZoningCatalogue(correlationID string) (data.ZoningCatalogue, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return data.ZoningCatalogue{}, err
	}
	catalogue, err := data.LoadZoningFile(dir, correlationID)
	if err != nil {
		return data.ZoningCatalogue{}, errs.Wrap(ErrModuleFailed, correlationID, err, map[string]any{
			"module": "zoning_wire", "file": data.FileZoning,
		})
	}
	return catalogue, nil
}

// familyToWorldZoning bridges a zoning.json family id onto engine.world's
// Zoning enum. This is the ONE place the six-family vocabulary meets the
// per-cell enum; both sides are small, closed sets, so an explicit switch
// beats a reflection trick (and fails closed on a family the enum cannot
// represent).
func familyToWorldZoning(familyID string) (world.Zoning, bool) {
	switch familyID {
	case "residential":
		return world.ZoningResidential, true
	case "commercial":
		return world.ZoningCommercial, true
	case "office":
		return world.ZoningOffice, true
	case "industry":
		return world.ZoningIndustrial, true
	case "farming":
		return world.ZoningAgricultural, true
	case "mining":
		return world.ZoningMining, true
	default:
		return world.ZoningNone, false
	}
}

// worldZoningToFamilyID is the read side of familyToWorldZoning: it maps
// an engine.world cell enum back onto the zoning.json family id the
// f1.viewport wire publishes. Kept adjacent to its reverse so the two can
// never drift apart silently.
func worldZoningToFamilyID(z world.Zoning) (string, bool) {
	switch z {
	case world.ZoningResidential:
		return "residential", true
	case world.ZoningCommercial:
		return "commercial", true
	case world.ZoningOffice:
		return "office", true
	case world.ZoningIndustrial:
		return "industry", true
	case world.ZoningAgricultural:
		return "farming", true
	case world.ZoningMining:
		return "mining", true
	default:
		return "", false
	}
}

// resolveZoneCommandDensity validates a KindZone command's density level
// against the data-driven catalogue and returns the world-side values to
// write through: the family's engine.world Zoning and the level itself.
//
// A density of 0 means "no density commanded" — the write-through then
// carries the family mapping with level 0 (the cell is zoned but at no
// catalogued rung yet, exactly what the old no-density commands meant).
// A non-zero density outside the family's declared [densityMin,
// densityMax] rejects the command with engine.world's own
// ErrZoningDensityOutOfRange code (MET-E407 — the code's semantic owner;
// engine.build still sees nothing because this rejects BEFORE the zone is
// submitted).
func (st *simState) resolveZoneCommandDensity(slug string, density int) (world.Zoning, uint8, error) {
	family, ok := st.zoning.ZoneByAlias(slug)
	if !ok {
		// A build-catalogue slug with no zoning.json alias is catalogue
		// drift: fail loudly rather than zoning in build while stranding
		// the world ledger (GR#12/#17 posture).
		return world.ZoningNone, 0, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "zoning_wire",
			"cause":  "zone slug has no data/zoning.json family alias",
			"slug":   slug,
		})
	}
	z, ok := familyToWorldZoning(family.ID)
	if !ok {
		return world.ZoningNone, 0, errs.New(ErrModuleFailed, st.cid, map[string]any{
			"module": "zoning_wire",
			"cause":  "zone family has no engine.world Zoning bridge",
			"family": family.ID,
		})
	}
	if density == 0 {
		return z, 0, nil
	}
	if density < family.DensityMin || density > family.DensityMax {
		return world.ZoningNone, 0, errs.New(world.ErrZoningDensityOutOfRange, st.cid, map[string]any{
			"density": density,
			"min":     family.DensityMin,
			"max":     family.DensityMax,
			"family":  family.ID,
		})
	}
	return z, uint8(density), nil
}

// zoningFamilyIDOrEmpty is worldZoningToFamilyID's omitempty-friendly
// form for the wire struct literal: unzoned cells publish no zone field.
func zoningFamilyIDOrEmpty(z world.Zoning) string {
	id, _ := worldZoningToFamilyID(z)
	return id
}

// zoneColourKeyForCell derives the f1.viewport palette key for one cell's
// stored zoning state, or "" when the cell is unzoned (the wire field is
// omitempty). Pure function of (cell, catalogue): same inputs, same key,
// every publish — determinism by construction, no map iteration anywhere.
func (st *simState) zoneColourKeyForCell(z world.Zoning, density uint8) string {
	familyID, ok := worldZoningToFamilyID(z)
	if !ok || density == 0 {
		return ""
	}
	family, ok := st.zoning.ZoneByID(familyID)
	if !ok {
		return ""
	}
	key, ok := st.zoning.ColourKeyFor(family, int(density))
	if !ok {
		return ""
	}
	return key
}
