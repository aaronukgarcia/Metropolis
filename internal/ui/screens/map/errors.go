package mapscreen

import (
	"errors"
	"fmt"
)

// errUnsupportedSchemaVersion is decodeWirePatch's error for an
// "f1.viewport" patch whose schemaVersion this package doesn't
// understand (patch.go's wireSchemaVersion doc comment).
func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("mapscreen: unsupported f1.viewport schemaVersion %d (want %d)", got, wireSchemaVersion)
}

// errSparseBeforeSnapshot is ApplyPatch's error for a sparse
// ("full": false) "f1.viewport" patch arriving before any full snapshot
// has been applied — there is no base state to patch on top of.
var errSparseBeforeSnapshot = errors.New("mapscreen: sparse f1.viewport patch received before an initial full snapshot")

// errExtentTooLarge is applyFullLocked's SEC-009 rejection cause: a
// "f1.viewport" full patch's Extent (after the existing >=0 clamp) still
// exceeds maxGridSide per dimension, or maxGridCells as a product
// (limits.go). Logged through the exact same MET-U100 path as every
// other malformed-patch cause (ApplyPatch's own doc comment) — an
// oversized Extent is treated exactly like bad JSON or an unsupported
// schema version: rejected, not clamped, the grid keeps its
// last-known-good state.
func errExtentTooLarge(w, h, maxSide, maxCells int) error {
	return fmt.Errorf("mapscreen: f1.viewport full-patch extent %dx%d exceeds the allocation ceiling (max %d per side, %d cells total; SEC-009)", w, h, maxSide, maxCells)
}

// errPatchTooLarge is decodeWirePatch's SEC-039/AC-10 rejection cause: a
// "f1.viewport" patch's raw wire byte size exceeds maxPatchWireBytes
// (limits.go) — rejected BEFORE json.Unmarshal ever runs, the cheapest
// possible check and the one that actually stops the PoC's dominant
// cost (a full decode of an oversized payload) rather than merely
// reacting to it afterward. Logged through the same MET-U100
// malformed-patch path as every other decodeWirePatch rejection
// (ApplyPatch's own doc comment) — an oversized wire payload is treated
// exactly like bad JSON or an unsupported schema version: rejected
// outright, never truncated or partially decoded.
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("mapscreen: f1.viewport patch is %d bytes, exceeding the wire-size ceiling of %d bytes (SEC-039) — rejected before decoding", gotBytes, maxBytes)
}

// errTooManyCells is decodeWirePatch's SEC-039/AC-11 rejection cause: a
// decoded patch's Cells array carries more entries than this screen
// could ever legitimately need (maxGridCells, limits.go) — rejected
// BEFORE either applyFullLocked's or applySparseLocked's per-cell loop
// runs. Defense in depth alongside errPatchTooLarge's earlier byte-size
// gate: closes the class for both full and sparse patches, since
// decodeWirePatch is the single choke point both call through.
func errTooManyCells(got, max int) error {
	return fmt.Errorf("mapscreen: f1.viewport patch carries %d cells, exceeding the %d-cell ceiling (SEC-039) — rejected before applying", got, max)
}

// ErrUnknownTerrainSurface is the registry code (MET-U102) for an
// unrecognised terrain surface reaching the render path (BUG-334). It is
// deliberately NOT MET-U100: a surface string this build doesn't teach
// is not a malformed f1.viewport patch — the patch applied fine and the
// grid rendered, the engine simply produced a class this renderer
// doesn't know (a sixth real surface, or a corrupt wire byte). The cell
// still draws glyphUnknown ('?'); only the log line differs. Registered
// in data/errors.json's MET-U102 slot (ui.screen.map range U100-U199).
const ErrUnknownTerrainSurface = "MET-U102"
