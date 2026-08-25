package mapscreen

import (
	"encoding/json"
	"sync/atomic"
)

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

// wirePowerLine mirrors compose/viewport_publish.go's viewportPowerLine
// field for field (same JSON tags): one placed pylon span of the engine's
// power network, published under the patch's powerLines key. Absent
// entirely (not merely empty) while the engine has placed nothing — the
// omitempty contract this package's decode relies on by simply leaving
// PowerLines nil.
type wirePowerLine struct {
	ID         uint64  `json:"id"`
	Class      string  `json:"class"`
	FromX      int     `json:"fromX"`
	FromY      int     `json:"fromY"`
	ToX        int     `json:"toX"`
	ToY        int     `json:"toY"`
	CapacityMW float64 `json:"capacityMW"`
}

type wirePatch struct {
	SchemaVersion int             `json:"schemaVersion"`
	Full          bool            `json:"full"`
	Origin        wirePoint       `json:"origin"`
	Extent        wireExtent      `json:"extent"`
	Cells         []wireCell      `json:"cells"`
	PowerLines    []wirePowerLine `json:"powerLines,omitempty"`
}

// decodeWirePatch parses raw as a wirePatch. It returns an error for
// anything ApplyPatch should treat as malformed: an oversized wire
// payload (SEC-039/AC-10, checked first — see below), invalid JSON, a
// schemaVersion this package doesn't understand, a Cells array
// bigger than this screen could ever legitimately need (SEC-039/AC-11),
// or a powerLines array violating the same allocation discipline (count
// cap, per-line coordinate bounds, no degenerate spans — see the gates
// at the bottom of this function).
//
// Beyond that it performs no other validation (e.g. out-of-range cell
// coordinates are a decode-time no-op, handled by
// applyFullLocked/applySparseLocked instead) — keeping this function's
// job to exactly "is this well-formed, versioned, and reasonably sized
// f1.viewport JSON."
//
// SEC-039: SEC-009's existing Extent clamp (applyFullLocked, screen.go)
// bounds the DERIVED grid slab, but nothing bounded the incoming wire
// payload itself — a patch declaring a tiny Extent (satisfying SEC-009
// trivially) could still carry an arbitrarily large Cells array, and
// json.Unmarshal fully decodes it — allocating and parsing every
// wireCell entry's string fields — BEFORE Extent or Cells' length is
// ever available to check against anything. The Destructive-1 PoC
// measured 1.43s / 198MB for exactly this shape (300,000 cells behind a
// declared 1x1 Extent). Two gates close it, in order: the raw byte-size
// check below runs BEFORE json.Unmarshal (stops the expensive step from
// running at all — this is the fix the finding actually requires, per
// the SEC-037/038/039 acceptance criteria's correction of the original
// brief); the Cells-length check after decode is defense in depth,
// closing the class for BOTH full and sparse patches (this function is
// the single choke point both call through) rather than only the
// full-patch shape the PoC happened to demonstrate (weakness pattern
// #3 — fix the class, not the instance).
func decodeWirePatch(raw json.RawMessage) (wirePatch, error) {
	if len(raw) > maxPatchWireBytes {
		return wirePatch{}, errPatchTooLarge(len(raw), maxPatchWireBytes)
	}

	unmarshalAttempts.Add(1)
	var p wirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return wirePatch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return wirePatch{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	if len(p.Cells) > maxGridCells {
		return wirePatch{}, errTooManyCells(len(p.Cells), maxGridCells)
	}
	// SEC-039 class (FEAT-1972079851 follow-up): the same discipline the
	// Cells checks above apply to the grid slab, applied to PowerLines —
	// without it, raw wire endpoint ints flowed straight into the
	// renderers' span walks, which allocate adx+ady+1 cells from those
	// ints BEFORE any clamp runs (234-byte payload = ~119 GiB alloc PoC).
	// Three gates, mirroring the existing fields' reject-never-clamp
	// posture: a count cap derived from the grid budget (limits.go's
	// maxPowerLines), per-line coordinate bounds against the same
	// [0, maxGridSide) domain every legitimate snapshot extent is already
	// bounded by, and rejection of degenerate (from==to) spans — which
	// cover no drawable cells and exist only to make a walker allocate.
	if len(p.PowerLines) > maxPowerLines {
		return wirePatch{}, errTooManyPowerLines(len(p.PowerLines), maxPowerLines)
	}
	for i := range p.PowerLines {
		l := &p.PowerLines[i]
		for _, c := range [4]int{l.FromX, l.FromY, l.ToX, l.ToY} {
			if c < 0 || c >= maxGridSide {
				return wirePatch{}, errPowerLineOutOfBounds(i, l.ID, c, maxGridSide)
			}
		}
		if l.FromX == l.ToX && l.FromY == l.ToY {
			return wirePatch{}, errDegeneratePowerLine(l.ID)
		}
	}
	return p, nil
}

// unmarshalAttempts counts every json.Unmarshal call decodeWirePatch
// makes into a full wirePatch — i.e. every time execution gets PAST the
// SEC-039/AC-10 byte-size gate above. Production code never reads this;
// it exists solely so tests can prove, mechanically rather than by
// timing alone (which a faster or slower machine could make misleading
// either direction), that an oversized patch's expensive Unmarshal call
// never happens at all.
var unmarshalAttempts atomic.Int64
