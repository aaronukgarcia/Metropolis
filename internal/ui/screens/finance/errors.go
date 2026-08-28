package finance

import "fmt"

const (
	ErrMalformedPatch     = "MET-V300"
	ErrStaleSubscription  = "MET-V301"
	ErrInvalidLoanRequest = "MET-V303"
)

// errPatchTooLarge builds the decode rejection for an over-limit patch
// payload (wrapped into ErrMalformedPatch by the caller's log path).
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

// errUnsupportedSchemaVersion builds the decode rejection for a patch
// stamped with a schema version this screen cannot read.
func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
