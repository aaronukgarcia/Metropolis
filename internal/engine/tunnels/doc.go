// Package tunnels implements the Tunnels, TBMs & Hyperloop module (MOD-065).
//
// Key: engine.tunnels
// Cites: §53 Tunnels, TBMs & Hyperloop and §32 (spoil reclamation).
//
// # Cumulative-km Learning Curve (AC-3):
//
// The per-km tunnelling rate is a monotonically non-increasing function of the same
// TBM's cumulative km bored across its program history. Cost decays as cumulative
// km bored rises, representing the team's operational learning curve.
//
// # Spoil-to-Reclamation Reuse (AC-7):
//
// Tunnelling spoil tonnage is a real output that is fed directly to the already-registered
// reclamation-fill surface in engine.mining via a live call into BlightAPI's Reclaim.
//
// # Hyperloop Prestige-bet Relationships (AC-8):
//
// Hyperloop is research-gated and explicitly designed as a prestige bet:
//  1. Its passenger volume capacity (250) is strictly smaller than the standard
//     metro transit backbone comparator (5000).
//  2. Its prestige attractiveness-gain-per-capex ratio (10.0) is strictly larger
//     than standard rail/metro comparators (0.2).
package tunnels
