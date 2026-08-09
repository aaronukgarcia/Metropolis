package mapscreen

import "encoding/json"

// ViewSubscriptionName is the one view F1 subscribes to (AC-1/AC-12):
// "f1.viewport", per int.protocol's ValidateViewName grammar and
// internal/engine/stub/viewport.go's v1 patch schema doc.
const ViewSubscriptionName = "f1.viewport"

// wireSchemaVersion is the only "f1.viewport" schema version this
// package understands. A patch declaring any other value is treated as
// malformed (AC-9/AC-8 posture: logged and dropped, never applied,
// never a panic) rather than guessed at — a future incompatible schema
// bump is expected to arrive as a new, explicitly-handled version, not a
// silent best-effort decode.
const wireSchemaVersion = 1

// wirePoint/wireExtent/wireCell/wirePatch mirror
// internal/engine/stub/viewport.go's ViewportPatch wire shape field for
// field (same JSON tags), duplicated here rather than imported so this
// package never depends on internal/engine (GR#20, AC-1) — the schema is
// the contract, not the Go type that happens to produce it engine-side.
type wirePoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type wireExtent struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type wireCell struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Terrain   string `json:"terrain,omitempty"`
	Elevation int    `json:"elevation,omitempty"`
	Road      string `json:"road,omitempty"`
	Building  string `json:"building,omitempty"`
}

type wirePatch struct {
	SchemaVersion int        `json:"schemaVersion"`
	Full          bool       `json:"full"`
	Origin        wirePoint  `json:"origin"`
	Extent        wireExtent `json:"extent"`
	Cells         []wireCell `json:"cells"`
}

// decodeWirePatch parses raw as a wirePatch. It returns an error for
// anything ApplyPatch should treat as malformed: invalid JSON, or a
// schemaVersion this package doesn't understand. It performs no other
// validation (e.g. out-of-range cell coordinates are a decode-time
// no-op, handled by applyFullLocked/applySparseLocked instead) — keeping
// this function's job to exactly "is this well-formed, versioned
// f1.viewport JSON."
func decodeWirePatch(raw json.RawMessage) (wirePatch, error) {
	var p wirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return wirePatch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return wirePatch{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	return p, nil
}
