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
