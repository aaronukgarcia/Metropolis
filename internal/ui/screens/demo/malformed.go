package demo

import "fmt"

// errPatchTooLarge/errUnsupportedSchemaVersion are the two decode-time
// causes decodeWirePatch can report; both feed MET-U500's {cause}
// template field via logMalformed (screen.go). Plain errors (not
// registry-sourced themselves) — the registry-sourced error is the
// MET-U500 wrapper logMalformed constructs around whichever of these
// caused the drop, mirroring ui.screen.map's errExtentTooLarge/
// errUnsupportedSchemaVersion convention (patch.go).
func errPatchTooLarge(gotBytes, maxBytes int) error {
	return fmt.Errorf("patch payload %d bytes exceeds the %d byte limit", gotBytes, maxBytes)
}

func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("unsupported schemaVersion %d (want %d)", got, wireSchemaVersion)
}
