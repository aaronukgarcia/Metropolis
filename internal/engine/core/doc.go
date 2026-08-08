// Package core is the engine orchestrator: the two-layer clock,
// deterministic phase pipeline, and shard worker pool (T-ENGINE +
// POOL-SIM in M0-ENG §1.1's process & thread topology).
//
// Module key: engine.core (see code.json)
// Spec ref:   §3; M0-ENG §1.1-1.3; §9 (month index)
package core
