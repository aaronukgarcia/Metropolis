package tunnels

// Registry-sourced error codes (GR#7) for engine.tunnels, claimed in
// data/errors.json's ranges.reserved table under G5300-G5399 via
// tools/plan/add-error.js claim-range. These replace the hand-painted
// non-registry MET-E_TUNNEL_nn strings the module previously raised —
// they were absent from data/errors.json, carried no correlation IDs and
// were invisible to the BUG-008 source-scan gate because the scanner's
// MET-[A-Z][0-9]{3,4} pattern cannot match them (BUG-376).
const (
	// ErrCopiedValue: a method was called on a struct-copied *TunnelsAPI
	// (SEC-020 family, mirroring engine.coastal/engine.parking).
	ErrCopiedValue = "MET-G5300"

	// ErrNoTBMProgramme: a bore order arrived with no active TBM
	// programme (fail closed, never fabricate capacity).
	ErrNoTBMProgramme = "MET-G5301"

	// ErrInvalidSegmentLength: a boring segment length that is not finite
	// and positive was rejected.
	ErrInvalidSegmentLength = "MET-G5302"

	// ErrHyperloopLocked: a Hyperloop construction order arrived before
	// the M12 unlock gate opened.
	ErrHyperloopLocked = "MET-G5303"
)
