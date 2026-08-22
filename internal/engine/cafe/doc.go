// Package cafe implements the Café Culture & Street-Life Economy module (MOD-054).
//
// Key: engine.cafe
// Cites: §41 Café Culture & the Street-Life Economy and §18 Wellbeing.
//
// The core mechanic computes a per-centre vitality index according to the
// five-term formula:
//
//	Vitality index per centre = footfall × venue density × dwell quality × safety(§28) × weather-adjusted outdoor capacity
//
// # Outbound call blocks (BUG-058):
//
//   - AC-5 (Safety Term): Sourcing safety from engine.crime's deprivation/policing
//     signal is currently blocked because no engine.cafe -> engine.crime edge is registered
//     in code.json (BUG-058 finding #8). Sourced from BaseSafetyValue placeholder instead.
//
//   - AC-9 (Policy Instruments): Sourcing policy changes from engine.policies via
//     PoliciesAPI is currently blocked because no engine.cafe -> engine.policies edge
//     is registered in code.json (BUG-058 finding #9). Thus, code.json's own registered
//     inbound pattern text ("policy instruments via PoliciesAPI") is not yet true of
//     the shipped code. Configurations are set via coastal-API-equivalent test fixtures.
package cafe
