package gameinit

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FileGameInit is the game-initialization config filename, relative to the
// resolved data directory.
const FileGameInit = "gameinit.json"

// Number is one schema-validated numeric parameter in data/gameinit.json:
// value + unit + placeholder flag + disclosure (AC-6, mirrors
// deathservices.Number exactly).
type Number struct {
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	Placeholder bool    `json:"placeholder"`
	SpecRef     string  `json:"specRef,omitempty"`
	Disclosure  string  `json:"disclosure"`
}

// Meta is data/gameinit.json's documentation block (AC-6).
type Meta struct {
	Module        string   `json:"module"`
	BowCode       string   `json:"bowCode"`
	SpecRefs      []string `json:"specRefs"`
	BalanceRegime string   `json:"balanceRegime"`
}

// Params holds every loaded numeric parameter.
type Params struct {
	StartingCapitalMicropounds Number `json:"startingCapitalMicropounds"`
}

// Config is the loaded data/gameinit.json configuration.
type Config struct {
	Version int    `json:"version"`
	Comment string `json:"$comment"`
	Meta    Meta   `json:"meta"`
	Params  Params `json:"params"`
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// maxStartingCapitalMicropounds bounds params.startingCapitalMicropounds
// (FEAT-143 round finding P3/AC-6): a value that passes the plain
// isFinite+positive float64 check can still overflow the int64 cast
// StartingCapitalMicropounds performs -- 1e30 is finite and positive as a
// float64, but int64(1e30) wraps to a large NEGATIVE number (undefined
// behaviour territory in Go for an out-of-range float-to-int conversion),
// silently turning a "finite positive capital" bundle into a
// non-positive one at the accessor rather than at validation time.
// Capping validate's own check at the int64 max (as a float64, since the
// schema stores a float64) closes that gap while staying well inside any
// remotely plausible balance-number magnitude.
const maxStartingCapitalMicropounds = float64(math.MaxInt64)

// validate rejects a schema-invalid Config (GR#15/GR#7): a missing
// unit/disclosure, a non-finite value, or a non-positive starting capital
// (AC-6 -- real mode's starting capital must be a genuine finite positive
// number, never zero or negative, which would make "real mode" indistinguishable
// from an already-insolvent city at genesis).
func (cfg *Config) validate(correlationID string) error {
	bad := func(rule string) error {
		return errs.New(ErrGameInitDataInvalid, correlationID, map[string]any{"rule": rule})
	}
	if cfg.Version <= 0 {
		return bad("version must be positive")
	}
	if cfg.Meta.Module == "" || cfg.Meta.BowCode == "" {
		return bad("meta.module and meta.bowCode are required")
	}
	n := cfg.Params.StartingCapitalMicropounds
	if !isFinite(n.Value) {
		return bad("params.startingCapitalMicropounds.value must be finite")
	}
	if n.Unit == "" {
		return bad("params.startingCapitalMicropounds.unit is required")
	}
	if n.Disclosure == "" {
		return bad("params.startingCapitalMicropounds.disclosure is required")
	}
	if n.Value <= 0 {
		return bad("params.startingCapitalMicropounds.value must be positive (AC-6: real mode starts with a finite POSITIVE capital)")
	}
	if n.Value >= maxStartingCapitalMicropounds {
		return bad("params.startingCapitalMicropounds.value must fit in an int64 (AC-6 round finding P3: a value this large overflows the int64 cast StartingCapitalMicropounds performs, silently producing a non-positive result)")
	}
	return nil
}

// LoadConfig reads and validates data/gameinit.json from dir, returning
// the parsed Config. Every failure is a registry-sourced *errs.E (GR#7).
func LoadConfig(dir, correlationID string) (Config, error) {
	var cfg Config
	path := filepath.Join(dir, FileGameInit)
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, errs.Wrap(ErrGameInitDataInvalid, correlationID, err, map[string]any{
			"path": path, "rule": "file must exist and be readable", "cause": err.Error(),
		})
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, errs.Wrap(ErrGameInitDataInvalid, correlationID, err, map[string]any{
			"path": path, "rule": "JSON must decode with no unknown fields", "cause": err.Error(),
		})
	}
	if err := cfg.validate(correlationID); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadDefaultConfig resolves data/'s directory via foundation/data and
// loads data/gameinit.json.
func LoadDefaultConfig(correlationID string) (Config, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return Config{}, err
	}
	return LoadConfig(dir, correlationID)
}

// StartingCapitalMicropounds returns the configured real-mode starting
// treasury balance in micro-pounds (AC-6, disclosed placeholder).
func (cfg Config) StartingCapitalMicropounds() int64 {
	return int64(cfg.Params.StartingCapitalMicropounds.Value)
}
