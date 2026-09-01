// Package converge is H-CONVERGE (harness.converge): the domain-agnostic
// A/B parity harness for FEAT-1972079936 Phase 3 (engine convergence).
//
// Module key: harness.converge (code.json registration owed — see this
// increment's dispatch report; this package is new and its one outbound
// edge, engine.finance -> harness.converge via the finance domain
// adapter in internal/engine/finance/converge_finance.go, needs the
// architect's sign-off before code.json is updated, per GR#20/GR#25).
//
// # Why this package exists
//
// docs/planning/phase3-convergence-plan.md reframes Phase 3: the TS
// webconsole sim and the Go engine are independent models (by design —
// wire.ts mandates "independent reimplementation, never an import of Go
// source"), so byte-identical output can never be the parity bar. What
// CAN be proven is per-domain SEMANTIC parity: for a fixed quantity (a
// domain's trajectory over a journal), does a candidate series agree
// with the Go engine's own reference series closely enough, under a
// tier appropriate to that quantity's nature (exact integer equality,
// bounded tolerance, or distributional agreement over a window)? That
// per-domain contract, evaluated by [Compare], is the A/B determinism
// gate a domain must pass before its store-flip (inc3.1) is allowed.
//
// # Shape
//
// A [Domain] adapter (finance's lives in engine.finance itself, not
// here — see below) takes a [Journal] (an ordered, domain-specific
// sequence of [JournalEntry] operations) and produces a [Trajectory]: a
// deterministic series of [Sample]s, each a tick number and a flat
// map of named int64 scalars. [Compare] takes two Trajectories — the
// Go engine's own reference run, and a candidate (either another Go
// run, for the determinism check, or a fixture captured from the TS
// sim) — plus a [Contract] naming, per field, which [Tolerance] tier
// gates it, and returns a [Report] carrying every field/tick divergence
// found.
//
// # The TS side is a fixture, never a live process (by design)
//
// This package cannot run the TS webconsole sim (it is TypeScript,
// browser/node-hosted, and explicitly not something Go code imports or
// shells out to for this purpose). Its input is instead a captured
// trajectory fixture — a small JSON file the webconsole can emit (see
// [LoadFixture] for the exact shape) recording the TS sim's own
// per-tick scalars for the same journal. A matching fixture passes
// [Compare]; a deliberately divergent one fails it — this package's own
// tests prove both directions (fixture_test.go), so the gate is proven
// to have teeth, not merely to compile.
//
// # Determinism (GR#21)
//
// [Trajectory] and [Compare] are pure functions of their inputs — no
// wall-clock, no map-iteration-order dependency (every map lookup here
// is keyed by an explicit field name and tick number, never ranged over
// for output ordering; Report.Diffs is built in tick-then-field
// iteration order, driven by the ref trajectory's own slice order, so
// two Compare calls over the same inputs produce byte-identical
// Reports). A [Domain] adapter is independently responsible for its own
// determinism (the finance adapter's determinism is proven by
// TestFinanceDomain_DeterministicTrajectory in
// internal/engine/finance/converge_finance_test.go: the same Journal
// run twice through the same fresh FinanceAPI produces
// reflect.DeepEqual Trajectories).
package converge
