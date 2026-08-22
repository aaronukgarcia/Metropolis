package projections

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file loads engine.projections' two GR#15 data-sourced inputs:
// the base forecasting horizon (horizon.json, ASM-237) and the
// FEAT-068 death-warning thresholds (deathwarnings.json, AC-20).
//
// # A scope shortfall this junior cannot fix (flag for Bill/Aaron)
//
// Every other §24-style config file in this codebase (seasonal.json,
// market.json, pacing.json, ...) lives under data/ at the repo root
// and loads through internal/foundation/data's shared generic Load
// helper (see that package's load.go/types.go and engine.season's
// Load for the house pattern this package would otherwise match).
// This build's dispatch brief scoped this junior to
// internal/engine/projections/ ONLY ("DO NOT: touch any file outside
// internal/engine/projections/") specifically so this session does not
// step on data/errors.json's own concurrent claim by another window --
// but data/horizon.json and data/deathwarnings.json are new files that
// would need to be created outside that boundary, and
// internal/foundation/data/{load.go,types.go} would need a new
// Horizon/DeathWarnings struct + loader added to be touched at all.
//
// Rather than violate the boundary, both files are embedded INSIDE
// this package (go:embed) and unmarshalled/validated directly here.
// This keeps AC-20's real requirement -- the threshold and minimum
// lead-time figures live in a JSON data file, never a Go literal, and
// carry a non-empty disclosure field -- genuinely true (see
// TestDeathWarningDataCarriesDisclosure below), but it is NOT the
// house §24 convention (a hand-editable file under data/, loaded via
// foundation/data.Load, resolved via foundation/data.ResolveDataDir so
// ops/save-compatible overrides work the same way as every other
// config file). Recommended follow-up once this package's scope
// boundary lifts: move horizon.json/deathwarnings.json to data/, add
// Horizon/DeathWarnings types + LoadHorizon/LoadDeathWarnings to
// internal/foundation/data, and delete this file's go:embed directives
// in favour of that shared loader — mirroring exactly what
// engine.season/engine.market already do.

//go:embed horizon.json
var embeddedHorizonJSON []byte

//go:embed deathwarnings.json
var embeddedDeathWarningsJSON []byte

// horizonConfig is horizon.json's schema.
type horizonConfig struct {
	Version           int    `json:"version"`
	BaseHorizonMonths int64  `json:"baseHorizonMonths"`
	Rationale         string `json:"rationale"`
}

// deathWarningEntry is one metric's row inside deathwarnings.json
// (AC-20): the "months-remaining crosses into warning" threshold
// (AC-19), the minimum lead time a downstream death-condition gate
// (engine.finance AC-29 / engine.spiral AC-15) is entitled to expect,
// and a non-empty pending-tuning disclosure — the same convention
// data/market.json's "pending M2 Batch tuning" fields use.
type deathWarningEntry struct {
	WarningThresholdMonths float64 `json:"warningThresholdMonths"`
	MinWarningLeadMonths   float64 `json:"minWarningLeadMonths"`
	Disclosure             string  `json:"disclosure"`
}

// deathWarningConfig is deathwarnings.json's schema: insolvency and
// ghost-city may carry different values (AC-20 — their dynamics
// differ) rather than sharing one number.
type deathWarningConfig struct {
	Version    int               `json:"version"`
	Insolvency deathWarningEntry `json:"insolvency"`
	GhostCity  deathWarningEntry `json:"ghostCity"`
}

var (
	horizonOnce sync.Once
	horizonCfg  horizonConfig
	horizonErr  error

	deathWarningOnce sync.Once
	deathWarningCfg  deathWarningConfig
	deathWarningErr  error
)

// loadHorizonConfig unmarshals the embedded horizon.json exactly once
// per process. correlationID is only used to construct the (should be
// unreachable — the embedded bytes are fixed at compile time and
// covered by TestHorizonConfigLoads) failure path.
func loadHorizonConfig(correlationID string) (horizonConfig, error) {
	horizonOnce.Do(func() {
		if err := json.Unmarshal(embeddedHorizonJSON, &horizonCfg); err != nil {
			horizonErr = errs.Wrap(ErrEmbeddedConfigInvalid, correlationID, err, map[string]any{
				"curve": "horizon.json",
				"cause": err.Error(),
			})
			return
		}
		if horizonCfg.BaseHorizonMonths <= 0 {
			horizonErr = errs.New(ErrEmbeddedConfigInvalid, correlationID, map[string]any{
				"curve": "horizon.json",
				"cause": "baseHorizonMonths must be positive",
			})
		}
	})
	return horizonCfg, horizonErr
}

// loadDeathWarningConfig unmarshals the embedded deathwarnings.json
// exactly once per process, and additionally checks (AC-20) that both
// entries carry a non-empty disclosure field.
func loadDeathWarningConfig(correlationID string) (deathWarningConfig, error) {
	deathWarningOnce.Do(func() {
		if err := json.Unmarshal(embeddedDeathWarningsJSON, &deathWarningCfg); err != nil {
			deathWarningErr = errs.Wrap(ErrEmbeddedConfigInvalid, correlationID, err, map[string]any{
				"curve": "deathwarnings.json",
				"cause": err.Error(),
			})
			return
		}
		if deathWarningCfg.Insolvency.Disclosure == "" || deathWarningCfg.GhostCity.Disclosure == "" {
			deathWarningErr = errs.New(ErrEmbeddedConfigInvalid, correlationID, map[string]any{
				"curve": "deathwarnings.json",
				"cause": "insolvency and ghostCity entries must both carry a non-empty disclosure field (AC-20)",
			})
		}
	})
	return deathWarningCfg, deathWarningErr
}

// resetConfigCacheForTest clears the sync.Once-cached embedded config
// state — test-only, mirrors errs.resetRegistryForTest's precedent, so
// TestEmbeddedConfigMalformed (which swaps in a broken byte slice) does
// not leak into other tests sharing the same process.
func resetConfigCacheForTest() {
	horizonOnce = sync.Once{}
	horizonCfg = horizonConfig{}
	horizonErr = nil
	deathWarningOnce = sync.Once{}
	deathWarningCfg = deathWarningConfig{}
	deathWarningErr = nil
}
