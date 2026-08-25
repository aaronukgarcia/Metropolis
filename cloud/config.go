package cloud

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// Config is the explicit A9 cut-over configuration (§3 of the ICD): the
// thresholds that decide where local hands off to cloud, named and
// defaulted here rather than implicit in the code. The zero value is the
// all-local degenerate case — cloud disabled, the v1 reality where the
// game runs with no cloud dependency at all.
//
// Operational-number regime (see doc.go): these are not player-felt
// balance numbers. The one class of threshold that IS a spec constant is
// the A9 citizen ceiling, which is single-sourced in int.solver's sizing
// package and consumed here (see ShouldOffloadCitizenShards), never
// re-hardcoded (GR#3).
type Config struct {
	// Enabled gates the whole cloud tier. When false (the zero value), the
	// solver backend is a pure local pass-through and the composition root
	// is expected not to register a BlobStore at all — cloud is a config
	// change, never a code path the engine must fork on.
	Enabled bool

	// Name is the diagnostic backend name reported in
	// solver.Response.Backend for offloaded solves. Defaults to
	// "azure.solver" when empty.
	Name string

	// CitizenShardThreshold is the population at or above which
	// citizen-shard offload is eligible (A9). A value <= 0 falls back to
	// int.solver's LocalCitizenCeilingLow via solver.ExceedsLocalCPUCeiling
	// — the A9 threshold is never re-declared here (GR#3/GR#15).
	CitizenShardThreshold int64

	// MaxRetries feeds integration.ConnectionConfig.MaxRetries; <= 0 falls
	// back to integration.DefaultMaxRetries.
	MaxRetries int64

	// Hooks feeds integration.ConnectionConfig.Hooks (re-auth + name
	// lookup). nil falls back to integration.LocalReconnectHooks{} — the
	// degenerate always-connected case.
	Hooks integration.ReconnectHooks

	// CorrelationID threads through every registry error the Connection
	// surfaces for this tier (GR#1).
	CorrelationID string
}

// connectionConfig folds Config into the integration.ConnectionConfig the
// resilience layer expects, applying the same documented defaults
// integration.NewConnection would.
func (c Config) connectionConfig() integration.ConnectionConfig {
	return integration.ConnectionConfig{
		MaxRetries:    c.MaxRetries,
		Hooks:         c.Hooks,
		CorrelationID: c.CorrelationID,
	}
}

// backendName resolves the diagnostic backend name for offloaded solves.
func (c Config) backendName() string {
	if c.Name != "" {
		return c.Name
	}
	return "azure.solver"
}

// ShouldOffloadCitizenShards reports whether citizen-shard work (the
// cold-pass and the shard store) is eligible to migrate to the cloud tier
// for the given citizen count. It consumes int.solver's A9 helpers — the
// single source of the 20M/30M bounds — rather than re-hardcoding them
// (GR#3; ICD §3 "thresholds live in explicit A9 config, never implicit").
//
// When CitizenShardThreshold is set explicitly it wins; otherwise the
// conservative low end of the A9 range (solver.LocalCitizenCeilingLow)
// fires the signal before a player would notice slowdown.
func (c Config) ShouldOffloadCitizenShards(citizenCount int64) bool {
	if !c.Enabled {
		return false
	}
	if c.CitizenShardThreshold > 0 {
		return citizenCount >= c.CitizenShardThreshold
	}
	return solver.ExceedsLocalCPUCeiling(citizenCount)
}
