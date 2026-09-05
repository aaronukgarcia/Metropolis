package finance

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// opexFileName is FEAT-094's own balance-data file (GR#15): every
// numeric magnitude the OPEX-composition/backlog/efficiency machinery
// uses lives here, never as a Go literal in opex.go.
//
// GR#25 note: this loader is deliberately self-contained (encoding/json
// + os, not internal/foundation/data's generic Load) because
// code.json's engine.finance module has no registered outbound edge to
// foundation.data (verified via edge-lint at build time — importing it
// would have been a new, unregistered edge). engine.maintenance
// (MOD-072) already has that edge and uses foundation/data's richer
// Load helper; engine.finance does not need it for one small file, so
// this avoids growing engine.finance's dependency surface for a single
// consumer rather than asking the Architect to register a
// engine.finance->foundation.data edge for it.
const opexFileName = "opexintegration.json"

// opexDataDirEnv mirrors foundation/data's METROPOLIS_DATA_DIR override
// (same variable name, so a caller that already sets it for the rest of
// the app is respected here too) without importing that package.
const opexDataDirEnv = "METROPOLIS_DATA_DIR"

// opexDataDirMarker is the file used to positively identify a
// candidate "data" directory while walking upward from the working
// directory — mirrors foundation/data's own marker convention
// (data/consumption.json) so a same-repo "data" directory one level
// above this package (e.g. internal/foundation/data itself) is never
// mistaken for the real one.
const opexDataDirMarker = "consumption.json"

// OpexDataMeta documents the unit of every figure in
// data/opexintegration.json (GR#15/BUG-355 units discipline).
type OpexDataMeta struct {
	Note                         string `json:"note"`
	CostPerEngineerDayUnit       string `json:"costPerEngineerDayUnit"`
	BacklogEfficiencyDivisorUnit string `json:"backlogEfficiencyDivisorUnit"`
	MajorDrainMinFractionUnit    string `json:"majorDrainMinFractionUnit"`
}

// OpexData is the JSON shape of data/opexintegration.json.
type OpexData struct {
	Version                              int          `json:"version"`
	Meta                                 OpexDataMeta `json:"meta"`
	CostPerEngineerDayMicroPounds        int64        `json:"costPerEngineerDayMicroPounds"`
	BacklogEfficiencyDivisorEngineerDays int64        `json:"backlogEfficiencyDivisorEngineerDays"`
	MinEfficiencyBasisPoints             int64        `json:"minEfficiencyBasisPoints"`
	MajorDrainMinFractionBasisPoints     int64        `json:"majorDrainMinFractionBasisPoints"`
}

// validate checks every field this package derives a magnitude from is
// positive/in-range (AC-6/AC-11) — a schema failure is a registry error,
// never a silent zero-value substitution.
func (d *OpexData) validate(correlationID string) error {
	if d.Version <= 0 {
		return errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": "version", "rule": "required, must be a positive integer"})
	}
	if d.CostPerEngineerDayMicroPounds <= 0 {
		return errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": "costPerEngineerDayMicroPounds", "rule": "required, must be positive"})
	}
	if d.BacklogEfficiencyDivisorEngineerDays <= 0 {
		return errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": "backlogEfficiencyDivisorEngineerDays", "rule": "required, must be positive"})
	}
	if d.MinEfficiencyBasisPoints < 0 || d.MinEfficiencyBasisPoints > 10000 {
		return errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": "minEfficiencyBasisPoints", "rule": "required, must be within [0, 10000]"})
	}
	if d.MajorDrainMinFractionBasisPoints < 0 || d.MajorDrainMinFractionBasisPoints > 10000 {
		return errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": "majorDrainMinFractionBasisPoints", "rule": "required, must be within [0, 10000]"})
	}
	return nil
}

// OpexConfig is the runtime-shaped balance config derived from
// OpexData — the type FinanceAPI's opex machinery actually reads.
type OpexConfig struct {
	CostPerEngineerDay       Money
	BacklogEfficiencyDivisor int64
	MinEfficiencyBasisPoints BasisPoints
	MajorDrainMinFractionBps BasisPoints
}

func (d *OpexData) config() OpexConfig {
	return OpexConfig{
		CostPerEngineerDay:       Money(d.CostPerEngineerDayMicroPounds),
		BacklogEfficiencyDivisor: d.BacklogEfficiencyDivisorEngineerDays,
		MinEfficiencyBasisPoints: BasisPoints(d.MinEfficiencyBasisPoints),
		MajorDrainMinFractionBps: BasisPoints(d.MajorDrainMinFractionBasisPoints),
	}
}

// LoadOpexConfig reads and schema-validates data/opexintegration.json
// from dir and returns the derived OpexConfig. Every failure is a
// registry-sourced *errs.E (GR#1/GR#7) — never a panic, never a silent
// default substitution.
func LoadOpexConfig(dir, correlationID string) (OpexConfig, error) {
	path := filepath.Join(dir, opexFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return OpexConfig{}, errs.Wrap(ErrOpexDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var d OpexData
	if err := json.Unmarshal(b, &d); err != nil {
		return OpexConfig{}, errs.Wrap(ErrOpexDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	if err := d.validate(correlationID); err != nil {
		return OpexConfig{}, err
	}
	return d.config(), nil
}

// findOpexDataDirUpward looks for a directory named "data" joined onto
// start, containing opexDataDirMarker, then each successive parent
// directory, until found or the filesystem root is reached. Mirrors
// foundation/data's findDirUpward without importing that package (see
// this file's GR#25 note).
func findOpexDataDirUpward(start string) (string, bool) {
	dir := start
	for {
		candidate := filepath.Join(dir, "data")
		if info, err := os.Stat(filepath.Join(candidate, opexDataDirMarker)); err == nil && !info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// resolveOpexDataDir finds the data/ directory: $METROPOLIS_DATA_DIR if
// set, else walking upward from the running executable's directory,
// else walking upward from the current working directory (so `go test`
// works regardless of the per-package working directory Go gives it).
// Mirrors foundation/data.ResolveDataDir's documented resolution order.
func resolveOpexDataDir(correlationID string) (string, error) {
	if p := os.Getenv(opexDataDirEnv); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		if p, ok := findOpexDataDirUpward(filepath.Dir(exe)); ok {
			return p, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if p, ok := findOpexDataDirUpward(wd); ok {
			return p, nil
		}
	}
	return "", errs.New(ErrOpexDataSchema, correlationID, map[string]any{"field": opexDataDirEnv, "rule": "data directory not found"})
}

// LoadDefaultOpexConfig resolves the data directory and loads
// data/opexintegration.json — the convenience entry point for
// composition-root wiring and tests that don't already have a resolved
// data directory in hand.
func LoadDefaultOpexConfig(correlationID string) (OpexConfig, error) {
	dir, err := resolveOpexDataDir(correlationID)
	if err != nil {
		return OpexConfig{}, err
	}
	return LoadOpexConfig(dir, correlationID)
}
