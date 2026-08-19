package finance

import "fmt"

const (
	ErrMalformedPatch     = "MET-V300"
	ErrStaleSubscription = "MET-V301"
	ErrInvalidLoanRequest = "MET-V303"
)

func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
