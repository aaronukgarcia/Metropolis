package core

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file closes FEAT-030 / MOD-012's interim ruling (see clock.go's
// DefaultSecondsPerMonthAt1x doc comment): secondsPerMonthAt1x is now
// read from data/pacing.json rather than existing only as a hardcoded
// Go var. It follows engine.season's own Load/LoadDefault split
// (internal/engine/season/season.go) exactly — a module-owned loader
// built on foundation/data's shared generic Load, not a new pattern.

// LoadSecondsPerMonthAt1x reads and validates data/pacing.json from dir
// (via foundation/data.LoadPacing) and returns its secondsPerMonthAt1x
// value, ready to pass to NewClock or WithSecondsPerMonthAt1x.
// correlationID is attached to any error this call constructs (GR#1).
// Every failure is a registry-sourced *errs.E (MET-E016) — never a
// silent fallback to DefaultSecondsPerMonthAt1x; a caller that wants
// fallback-on-error behaviour is expected to decide that explicitly at
// the call site, not have it hidden inside this loader.
func LoadSecondsPerMonthAt1x(dir, correlationID string) (int64, error) {
	pacing, err := data.LoadPacing(dir, correlationID)
	if err != nil {
		return 0, errs.Wrap(ErrPacingDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return pacing.SecondsPerMonthAt1x, nil
}

// LoadDefaultSecondsPerMonthAt1x resolves data/'s directory via
// foundation/data.ResolveDataDir and then [LoadSecondsPerMonthAt1x]s
// it — the convenience entry point for callers (boot wiring, tests)
// that don't already have a resolved data directory in hand, mirroring
// engine.season.LoadDefault exactly.
func LoadDefaultSecondsPerMonthAt1x(correlationID string) (int64, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return 0, err
	}
	return LoadSecondsPerMonthAt1x(dir, correlationID)
}
