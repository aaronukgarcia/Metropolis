package converge

import "encoding/json"

// JournalEntry is one operation in a canonical Journal: a tick number
// (the sim-time the operation belongs to) plus a domain-specific op
// name and its arguments, encoded generically as json.RawMessage so
// this package stays domain-agnostic — a Domain adapter (finance's
// lives in internal/engine/finance/converge_finance.go, driving
// *finance.FinanceAPI directly) is the only place that knows what "op"
// names and Args shapes are valid for it.
type JournalEntry struct {
	Tick int64           `json:"tick"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Journal is an ordered sequence of JournalEntry operations — the single
// canonical input both a Domain's Go reference run and (once the bridge
// docs/planning/phase3-convergence-plan.md §3 P2 describes is built) a
// translated TS-side run are driven from.
type Journal struct {
	Entries []JournalEntry `json:"entries"`
}

// Domain is one pluggable A/B-gated domain: something that can run a
// Journal against the live Go engine and report a deterministic
// Trajectory, plus the Contract its own fields should be checked under.
// engine.finance's adapter is the first implementation; the shape is
// deliberately domain-agnostic so a later domain (economy, demographics,
// ...) plugs in the same way without this package changing.
type Domain interface {
	// Name identifies the domain (e.g. "finance") — Report.Domain.
	Name() string

	// Contract is this domain's parity contract: which fields are
	// checked, and to what tier (docs/planning/phase3-convergence-plan.md
	// §2). Returned fresh each call — callers must not mutate the
	// returned map.
	Contract() Contract

	// Run drives j against a freshly-constructed instance of the
	// domain's live Go engine surface and returns the resulting
	// Trajectory. Run must be deterministic: the same Journal run twice
	// against two fresh instances produces reflect.DeepEqual
	// Trajectories (GR#21) — each Domain adapter's own tests prove this
	// for itself, since only the adapter knows how to construct "a
	// fresh instance" of its engine surface.
	Run(j Journal) (Trajectory, error)
}
