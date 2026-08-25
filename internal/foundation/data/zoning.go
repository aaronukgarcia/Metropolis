package data

import (
	"path/filepath"
	"regexp"
)

// zoningIDPattern mirrors buildings.go's buildingIDPattern slug shape.

// FEAT-199: data/zoning.json's typed schema — the six zone families
// (residential, commercial, office, industry, farming, mining; industry
// deliberately NOT split, Aaron 2026-08-18), each with a density ladder
// 1..5 and semantic palette keys (res1..res5 etc) resolved against the
// file's own colours map. Routed through the SAME generic [Load] every
// other config file in this package uses (pacing.go's split), never a
// bespoke decode path.
//
// GR#15 discipline: every density bound and every colour lives in the
// data file — nothing here hardcodes "the ladder is light-green to
// dark-green" or any tcell index. The colours themselves are
// PLACEHOLDERS pending Aaron's rendered-swatch UI pass (FEAT-199 desc);
// swapping them is a data edit, not a code change.
//
// Consumers:
//   - compose (zoning_wire.go) resolves a KindZone command's SS34 slug to
//     its family via Aliases, validates the commanded density against
//     DensityMin/DensityMax, and derives the wire palette key for the
//     f1.viewport publish.
//   - cmd/metropolis boots the map screen's zone colours from Colours.

// zoningIDPattern mirrors buildings.go's buildingIDPattern slug shape.
var zoningIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{2,63}$`)

// FileZoning is data/zoning.json's filename, relative to the resolved
// data directory (see ResolveDataDir). Added after the SS24 set, matching
// FilePacing/FileMarket's precedent for later files — not part of the
// eight-file set LoadAll aggregates.
const FileZoning = "zoning.json"

// ZoneDensity is one zone family's FEAT-199 entry.
type ZoneDensity struct {
	// ID is the stable family key ("residential", "mining", ...).
	ID string `json:"id"`

	// DensityMin/DensityMax bound the family's density ladder. FEAT-199
	// fixes every family at [1,5]; Validate enforces 1 <= min <= max <= 5
	// so a future re-tune stays inside the engine-side uint8 level space.
	DensityMin int `json:"densityMin"`
	DensityMax int `json:"densityMax"`

	// PaletteKeys are the semantic colour keys, one per density level,
	// ordered low -> high ("res1".."res5"). Length must equal
	// DensityMax-DensityMin+1 and every key must exist in the top-level
	// colours map (Validate).
	PaletteKeys []string `json:"paletteKeys"`

	// Aliases are the SS34 eight-way build-catalogue slugs that resolve
	// into this family ("dwelling" -> residential, "heavy_industry" ->
	// industry, ...). Optional per zone, but an alias must appear on at
	// most ONE zone (Validate) so slug resolution is unambiguous.
	Aliases []string `json:"aliases,omitempty"`
}

// ZoningCatalogue is data/zoning.json's top-level schema.
type ZoningCatalogue struct {
	Version int    `json:"version"`
	Comment string `json:"comment,omitempty"`

	Zones []ZoneDensity `json:"zones"`

	// Colours maps every referenced semantic palette key to its tcell
	// 256-colour index. Keys are validated by membership: a palette key a
	// zone references but this map lacks is a schema error (GR#15 — a
	// dangling key would silently render as "no colour").
	Colours map[string]int `json:"colours"`
}

// ZoneByID resolves a family by its stable id.
func (c *ZoningCatalogue) ZoneByID(id string) (ZoneDensity, bool) {
	for _, z := range c.Zones {
		if z.ID == id {
			return z, true
		}
	}
	return ZoneDensity{}, false
}

// ZoneByAlias resolves a family by one of its SS34 build-catalogue alias
// slugs (compose's KindZone path). Linear scan, never a map: the catalogue
// is six entries loaded once, and a derived index would need invalidation
// reasoning for zero benefit.
func (c *ZoningCatalogue) ZoneByAlias(slug string) (ZoneDensity, bool) {
	for _, z := range c.Zones {
		for _, a := range z.Aliases {
			if a == slug {
				return z, true
			}
		}
	}
	return ZoneDensity{}, false
}

// ColourKeyFor returns the palette key for zone z at density d, reporting
// miss (not a panic, not an out-of-bounds index) when d falls outside z's
// declared ladder or the key list is malformed relative to it.
func (c *ZoningCatalogue) ColourKeyFor(z ZoneDensity, d int) (string, bool) {
	if d < z.DensityMin || d > z.DensityMax {
		return "", false
	}
	idx := d - z.DensityMin
	if idx < 0 || idx >= len(z.PaletteKeys) {
		return "", false
	}
	key := z.PaletteKeys[idx]
	if key == "" {
		return "", false
	}
	return key, true
}

// Validate implements Validator.
func (c *ZoningCatalogue) Validate() error {
	if err := requireVersion(c.Version); err != nil {
		return err
	}
	if len(c.Zones) == 0 {
		return fieldErr("zones", "required non-empty: a zoning catalogue with no zone families is a data error, not an empty-ok default")
	}
	seenIDs := make(map[string]int, len(c.Zones))
	seenAliases := make(map[string]string)
	for i, z := range c.Zones {
		prefix := "zones[" + itoa(i) + "]"
		if !zoningIDPattern.MatchString(z.ID) {
			return fieldErr(prefix+".id", "must be a non-empty lowercase slug")
		}
		if first, dup := seenIDs[z.ID]; dup {
			return fieldErr(prefix+".id", "duplicate id (first seen at zones["+itoa(first)+"])")
		}
		seenIDs[z.ID] = i

		if z.DensityMin < 1 {
			return fieldErr(prefix+".densityMin", "must be >= 1: density level 0 means unzoned and is never a catalogued rung")
		}
		if z.DensityMax > 5 {
			return fieldErr(prefix+".densityMax", "must be <= 5: FEAT-199's ladder tops out at 5")
		}
		if z.DensityMin > z.DensityMax {
			return fieldErr(prefix+".densityMin", "must be <= densityMax")
		}
		if len(z.PaletteKeys) != z.DensityMax-z.DensityMin+1 {
			return fieldErr(prefix+".paletteKeys", "must carry exactly one key per density level (got "+itoa(len(z.PaletteKeys))+", want "+itoa(z.DensityMax-z.DensityMin+1)+")")
		}
		for _, key := range z.PaletteKeys {
			if _, ok := c.Colours[key]; !ok {
				return fieldErr("colours", "palette key "+key+" (referenced by "+z.ID+") has no colour entry")
			}
		}
		for _, a := range z.Aliases {
			if owner, dup := seenAliases[a]; dup {
				return fieldErr(prefix+".aliases", "alias "+a+" already claimed by zone "+owner+" — slug resolution must be unambiguous")
			}
			seenAliases[a] = z.ID
		}
	}
	return nil
}

// LoadZoningFile loads and validates zoning.json from dir.
func LoadZoningFile(dir, correlationID string) (ZoningCatalogue, error) {
	return Load[ZoningCatalogue, *ZoningCatalogue](filepath.Join(dir, FileZoning), correlationID)
}
