package ticker

import "encoding/json"

// View subscription names this screen owns (SF-1/SF-2). Each follows
// int.protocol's ValidateViewName grammar (<screen>.<projection>) and is
// documented in doc.go's SF-2 field-traceability table. They are named
// against engine.news (§29's four layers) — the outbound edge this
// screen is registered with in code.json (ui.screen.ticker → engine.news).
const (
	// ViewTicker carries the rolling feed of atomic events (TIK-1),
	// from engine.news's ticker layer (§29.1).
	ViewTicker = "f9.ticker"

	// ViewBulletin carries the monthly bulletin front page — 3–5
	// salience-ranked stories (TIK-2), from engine.news's bulletin layer
	// (§29.2).
	ViewBulletin = "f9.bulletin"

	// ViewAnnual carries the annual review — year in numbers plus the
	// biggest story (TIK-3), from engine.news's annual layer (§29.3).
	ViewAnnual = "f9.annual"

	// ViewArchive carries the full searchable history archive — also the
	// epilogue's data source (TIK-4/TIK-6), from engine.news's archive
	// (§29's "archive searchable" clause).
	ViewArchive = "f9.archive"
)

// knownViews is every view this screen subscribes to — used by Subscribe
// to reject an unrecognised name (MET-U702) rather than silently issuing
// a Subscribe command int.protocol's own ValidateViewName might accept
// but this screen never asked for (mirrors ui.screen.demo's knownViews).
var knownViews = map[string]bool{
	ViewTicker:   true,
	ViewBulletin: true,
	ViewAnnual:   true,
	ViewArchive:  true,
}

// wireSchemaVersion is the only schema version every f9.* view
// understands today (mirrors ui.screen.demo's wireSchemaVersion and
// ui.screen.map's wirePatch convention). A patch declaring any other
// value is malformed and dropped (SF-7), never guessed at.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw patch payload BEFORE it is ever
// unmarshalled, mirroring ui.screen.map's SEC-039 discipline so an
// oversized f9.* payload cannot force an expensive allocation-heavy
// decode before this package gets a chance to reject it. The archive view
// is the one that grows, and it grows per atomic event (spec 29.4's
// "never lost" whole history), not per monthly bulletin — a ~77-byte
// story means this ceiling is reached at roughly 53k events, after which
// applyArchive stops applying and surfaces archiveStalled (SEC-072,
// MET-U705) rather than freezing silently. The ceiling stays as the
// SEC-039 allocation bound; surfacing the stop is what keeps the freeze
// honest (GR#17).
const maxPatchWireBytes = 4 << 20 // 4 MiB

// wireStory is the wire shape of one story carried by f9.ticker,
// f9.annual's biggestStory, and f9.archive (same JSON tags). Duplicated
// here rather than imported so this package never depends on
// internal/engine (GR#20/SF-1) — the schema is the contract, not the Go
// type that happens to produce it engine-side.
type wireStory struct {
	EventID string `json:"eventId"`
	Tick    int64  `json:"tick"`
	Name    string `json:"name,omitempty"`
	Text    string `json:"text"`
}

// wireTickerPatch is the "f9.ticker" patch schema: a rolling window of
// atomic events, newest last, in the engine's order.
type wireTickerPatch struct {
	SchemaVersion int         `json:"schemaVersion"`
	Events        []wireStory `json:"events"`
}

// wireBulletinStory is the wire shape of one salience-ranked bulletin
// story (f9.bulletin): a story plus the engine editor's salience score
// and rank.
type wireBulletinStory struct {
	wireStory
	Salience float64 `json:"salience"`
	Rank     int     `json:"rank"`
}

// wireBulletinPatch is the "f9.bulletin" patch schema: one month's
// front-page story selection, already ranked by the engine.
type wireBulletinPatch struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Month         int64               `json:"month"`
	Stories       []wireBulletinStory `json:"stories"`
}

// wireAnnualNumber is one "year in numbers" figure in f9.annual.
type wireAnnualNumber struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
}

// wireAnnualPatch is the "f9.annual" patch schema: year in numbers plus
// the biggest story.
type wireAnnualPatch struct {
	SchemaVersion int                `json:"schemaVersion"`
	Year          int64              `json:"year"`
	Numbers       []wireAnnualNumber `json:"numbers"`
	BiggestStory  *wireStory         `json:"biggestStory,omitempty"`
}

// wireArchivePatch is the "f9.archive" patch schema: the full history
// archive. The engine sends the complete archive each time (append-only
// history as a full snapshot). Because it is a full snapshot of the
// city's whole atomic-event history, it is the one f9.* view that can
// outgrow maxPatchWireBytes; applyArchive surfaces that as a stopped
// archive (SEC-072/MET-U705) rather than freezing silently.
type wireArchivePatch struct {
	SchemaVersion int         `json:"schemaVersion"`
	Stories       []wireStory `json:"stories"`
}

// decodeWirePatch is the shared byte-size gate + JSON decode + schema
// check every f9.* view's applyX runs through, parametrised over the
// concrete wire type via a pointer target — keeps the SEC-039-style size
// gate (checked BEFORE json.Unmarshal) and schema-version check in
// exactly one place rather than duplicated four times (mirrors
// ui.screen.demo's decodeWirePatch).
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

func (p *wireTickerPatch) schemaVersion() int   { return p.SchemaVersion }
func (p *wireBulletinPatch) schemaVersion() int { return p.SchemaVersion }
func (p *wireAnnualPatch) schemaVersion() int   { return p.SchemaVersion }
func (p *wireArchivePatch) schemaVersion() int  { return p.SchemaVersion }
