package extcommute

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file loads engine.extcommute's own GR#15 data-sourced tuning
// (data/extcommute.json): the reaching-transport-leg base capacity per
// channel — the SECOND cap of the two-cap model (A6b/R6(b)). The FIRST cap
// (per-pool job capacity) is loaded from data/external_world.json via
// foundation/data; that file is not duplicated here (GR#3).

// fileExtCommute is data/extcommute.json's filename, relative to the
// resolved data directory (see data.ResolveDataDir).
const fileExtCommute = "extcommute.json"

// config is data/extcommute.json decoded and validated (GR#15). Unexported —
// the only way another package reaches an extcommute balance figure is
// through ExtCommuteAPI's exported surface (GR#20).
type config struct {
	Version int    `json:"version"`
	SpecRef string `json:"specRef"`

	// TransportCapacity maps a transport channel ("motorway",
	// "externalRail") to its base off-map-commuter headroom per tick.
	TransportCapacity map[string]int64 `json:"transportCapacity"`

	Disclosure string `json:"disclosure"`
}

// Validate implements foundation/data.Validator for config. It enforces the
// structural rules a well-formed data/extcommute.json must satisfy: a
// positive version, a non-empty transport-capacity table, and a strictly
// positive base capacity per channel — a zero-capacity channel would be a
// silently permanent dead-end, the exact AC-15 anti-pattern.
func (c *config) Validate() error {
	if c.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	if len(c.TransportCapacity) == 0 {
		return &data.FieldError{Field: "transportCapacity", Rule: "required, must be non-empty"}
	}
	for channel, cap := range c.TransportCapacity {
		if cap <= 0 {
			return &data.FieldError{
				Field: "transportCapacity." + channel,
				Rule:  "must be > 0 (a zero-capacity channel is a permanent dead-end, never a silent default)",
			}
		}
	}
	return nil
}

// loadConfig reads and validates data/extcommute.json from dir via
// foundation/data's generic Load[T]. Every failure is a registry-sourced
// *errs.E wrapped under this package's own ErrExtCommuteDataInvalid — never a
// silent default substitution, never a panic (GR#7, GR#15).
func loadConfig(dir, correlationID string) (config, error) {
	cfg, err := data.Load[config, *config](filepath.Join(dir, fileExtCommute), correlationID)
	if err != nil {
		return config{}, errs.Wrap(ErrExtCommuteDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	return cfg, nil
}
