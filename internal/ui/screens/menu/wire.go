package menu

import (
	"encoding/json"
	"fmt"
)

// ViewSession is the one view this screen subscribes to (SF-1/SF-2/
// MEN-*): the running game's current-session summary, sourced from
// engine.core. It follows int.protocol's ValidateViewName grammar
// (<screen>.<projection>). The field set is this screen's own choice (no
// spec enumerates it) — ASM-526.
const ViewSession = "f10.session"

// knownViews is every view this screen subscribes to — used by Subscribe
// to reject an unrecognised name (MET-U603) rather than silently issuing
// a Subscribe command int.protocol's own ValidateViewName might accept
// but this screen never asked for.
var knownViews = map[string]bool{ViewSession: true}

// wireSchemaVersion is the only schema version the f10.session view
// understands today (mirrors ui.screen.map's/ui.screen.demo's wirePatch
// convention). A patch declaring any other value is malformed and dropped
// (SF-7), never guessed at.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw f10.session patch payload BEFORE
// it is ever unmarshalled, mirroring ui.screen.map's SEC-039 discipline
// (patch.go) so an oversized payload cannot force an expensive decode
// before this package gets a chance to reject it. One small summary
// struct is far under this bound; the limit is a guardrail, not a budget.
const maxPatchWireBytes = 1 << 20 // 1 MiB

// wireSessionPatch is the f10.session wire shape, decoded from
// protocol.Delta.Patch raw JSON (SF-1: this package's own copy of the
// view schema, never an internal/engine type).
type wireSessionPatch struct {
	SchemaVersion int   `json:"schemaVersion"`
	WorldSeed     int64 `json:"worldSeed"`
	Tick          int64 `json:"tick"`
	GameMonth     int64 `json:"gameMonth"`
	Paused        bool  `json:"paused"`
	Speed         int   `json:"speed"`
}

func (p *wireSessionPatch) schemaVersion() int { return p.SchemaVersion }

// decodeWirePatch is the shared byte-size gate + JSON decode + schema
// check every f10.session patch runs through (mirrors ui.screen.demo's
// decodeWirePatch): the SEC-039-style size gate runs BEFORE json.Unmarshal,
// and an unrecognised schemaVersion is rejected rather than guessed at.
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

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch can report; both feed MET-U601's {cause}
// template field via logMalformed (screen.go), mirroring ui.screen.demo's
// errPatchTooLarge/errUnsupportedSchemaVersion convention (malformed.go).
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
