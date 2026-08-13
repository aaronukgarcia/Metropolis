package demo

import "encoding/json"

// View subscription names this screen owns (SF-1/SF-2/DEMO-10). Each
// follows int.protocol's ValidateViewName grammar
// (<screen>.<projection>) and is documented in doc.go's field-traceable
// table.
const (
	// ViewPopulation carries the month-age population pyramid (DEMO-1)
	// and the personality-trait distribution (DEMO-7), from
	// engine.citizens.
	ViewPopulation = "f6.population"

	// ViewLeisure carries the "how your city spends Saturday" hours-by-
	// activity breakdown (DEMO-4) and the leisure-taste distribution
	// (DEMO-7), from engine.leisure.
	ViewLeisure = "f6.leisure"

	// ViewHousing carries per-typology demand-vs-stock (DEMO-5), from
	// engine.households.
	ViewHousing = "f6.housing"

	// ViewCommute carries the in/out commuting-leak figures (DEMO-6),
	// from engine.extcommute.
	ViewCommute = "f6.commute"
)

// knownViews is every view this screen subscribes to — used by
// Subscribe to reject an unrecognised name (MET-U502) rather than
// silently issuing a Subscribe command int.protocol's own
// ValidateViewName might accept but this screen never asked for.
var knownViews = map[string]bool{
	ViewPopulation: true,
	ViewLeisure:    true,
	ViewHousing:    true,
	ViewCommute:    true,
}

// wireSchemaVersion is the only schema version every f6.* view
// understands today (mirrors ui.screen.map's wirePatch convention — see
// internal/ui/screens/map/patch.go). A patch declaring any other value
// is malformed and dropped (SF-7/DEMO-9), never guessed at.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw patch payload BEFORE it is ever
// unmarshalled, mirroring ui.screen.map's SEC-039 discipline (patch.go)
// so an oversized f6.* payload cannot force an expensive allocation-heavy
// decode before this package gets a chance to reject it.
const maxPatchWireBytes = 1 << 20 // 1 MiB — generous for four small dashboards' worth of aggregate figures

// --- f6.population ------------------------------------------------------

type wireAgeBucket struct {
	MonthAge int `json:"monthAge"`
	Male     int `json:"male"`
	Female   int `json:"female"`
}

type wireTraitBucket struct {
	Trait string `json:"trait"`
	Count int    `json:"count"`
}

type wirePopulationPatch struct {
	SchemaVersion int               `json:"schemaVersion"`
	AgeMonths     []wireAgeBucket   `json:"ageMonths"`
	Personality   []wireTraitBucket `json:"personality"`
}

// --- f6.leisure -----------------------------------------------------------

type wireActivityHours struct {
	Activity string  `json:"activity"`
	Hours    float64 `json:"hours"`
}

type wireTasteBucket struct {
	Taste  string  `json:"taste"`
	Weight float64 `json:"weight"`
}

type wireLeisurePatch struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	HoursByActivity []wireActivityHours `json:"hoursByActivity"`
	LeisureTaste    []wireTasteBucket   `json:"leisureTaste"`
}

// --- f6.housing -------------------------------------------------------

type wireTypology struct {
	Typology string `json:"typology"`
	Demand   int    `json:"demand"`
	Stock    int    `json:"stock"`
}

// wireHousingPatch mirrors the map screen's full/sparse convention: a
// Full patch declares the COMPLETE current typology set (any typology
// previously known but absent from a Full patch is retired — see
// applyHousingLocked); a sparse (Full==false) patch updates only the
// typologies it lists, leaving every other typology's row untouched
// (DEMO-5's own "every OTHER typology's rendered row is byte-identical"
// differential check depends on this).
type wireHousingPatch struct {
	SchemaVersion int            `json:"schemaVersion"`
	Full          bool           `json:"full"`
	Typologies    []wireTypology `json:"typologies"`
}

// --- f6.commute -------------------------------------------------------

type wireCommutePatch struct {
	SchemaVersion int `json:"schemaVersion"`
	OutCommuters  int `json:"outCommuters"`
	InCommuters   int `json:"inCommuters"`
}

// decodeWirePatch is the shared byte-size gate + JSON decode + schema
// check every f6.* view's ApplyXPatch runs through, parametrised over
// the concrete wire type via a pointer target — keeps the SEC-039-style
// size gate (checked BEFORE json.Unmarshal) and schema-version check in
// exactly one place rather than duplicated four times.
func decodeWirePatch(raw json.RawMessage, target interface{ schemaVersion() int }) error {
	if len(raw) > maxPatchWireBytes {
		return errPatchTooLarge(len(raw), maxPatchWireBytes)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	if target.schemaVersion() != wireSchemaVersion {
		return errUnsupportedSchemaVersion(target.schemaVersion())
	}
	return nil
}

func (p *wirePopulationPatch) schemaVersion() int { return p.SchemaVersion }
func (p *wireLeisurePatch) schemaVersion() int    { return p.SchemaVersion }
func (p *wireHousingPatch) schemaVersion() int    { return p.SchemaVersion }
func (p *wireCommutePatch) schemaVersion() int    { return p.SchemaVersion }
