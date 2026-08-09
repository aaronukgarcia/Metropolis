package debug

// monthlyPhaseOrder mirrors internal/engine/core.MonthlyPhaseOrder's six
// PhaseKind string values, in the same fixed order (AC-8). It is a
// literal local copy, not an import: internal/ui packages may not import
// internal/engine in non-test code (GR#20, enforced by golangci-lint's
// depguard — see .golangci.yml's "ui-must-not-import-engine" rule, and
// doc.go's "GR#20 note" section). determinism_test.go imports
// internal/engine/core (the deliberate, verified test-only exemption)
// solely to assert this slice never drifts from the real one.
var monthlyPhaseOrder = []string{
	"production",
	"logistics-settlement",
	"consumption-shortfall",
	"population",
	"land-value-decay",
	"finance",
}
