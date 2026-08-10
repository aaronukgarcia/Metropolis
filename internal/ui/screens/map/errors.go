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
