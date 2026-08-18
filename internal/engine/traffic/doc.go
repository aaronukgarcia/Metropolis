// Package traffic implements the transport, routing & traffic module (MOD-023).
//
// Key: engine.traffic
// Cites: §19 Transport, Routing & Traffic, §51 Roads v2, A4 Assignment structure, and §II.5 Movement.
//
// # Coarse Approximation Baseline One Scope (Aaron, 2026-08-14):
//
// In accordance with lead-developer directions, the baseline one implementation
// builds a coarse-approximation layer providing stable query surfaces for link
// volumes, v/c ratios, and per-citizen commute times.
//
// NOTE: SUE assignment, junction queue spillback, and the warm-start route cache
// are deferred to a later heavy-model iteration. Current commute queries use a
// coarse v/c multiplier derived from the deterministic demand map.
package traffic
