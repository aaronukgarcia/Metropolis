package parking

// Registry-sourced error codes (GR#7) for engine.parking, claimed in
// data/errors.json's ranges.reserved table under G5200-G5299 via
// tools/plan/add-error.js claim-range. These replace the hand-painted
// non-registry MET-E_PARKING_nn strings the module previously raised —
// they were absent from data/errors.json, carried no correlation IDs and
// were invisible to the BUG-008 source-scan gate because the scanner's
// MET-[A-Z][0-9]{3,4} pattern cannot match them (BUG-376).
const (
	// ErrCopiedValue: a method was called on a struct-copied *ParkingAPI
	// (SEC-020 family, mirroring engine.coastal/engine.comms).
	ErrCopiedValue = "MET-G5200"

	// ErrUnknownFacility: a call referenced a facility ID this module
	// never minted.
	ErrUnknownFacility = "MET-G5201"

	// ErrNegativeSpaces: a negative space count reached capacity
	// registration or resize.
	ErrNegativeSpaces = "MET-G5202"

	// ErrInvalidFraction: an allocation fraction outside [0,1] was
	// rejected, never silently clamped.
	ErrInvalidFraction = "MET-G5203"

	// ErrNegativeRatePrice: a negative hourly rate or price reached a
	// pricing setter.
	ErrNegativeRatePrice = "MET-G5204"

	// ErrUnknownDistrict: a facility referenced a district ID no district
	// config registered.
	ErrUnknownDistrict = "MET-G5205"

	// ErrFacilityBusy: a use-change was rejected because the facility is
	// still busy or its sustained low-occupancy period is not met.
	ErrFacilityBusy = "MET-G5206"
)
