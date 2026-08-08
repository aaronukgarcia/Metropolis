// Package det is the determinism core: 256 fixed shards, phase barriers,
// counter-based (Philox-style) RNG streams, and fixed-point money. Same
// seed + same command log must produce a bit-identical world regardless of
// worker count, on any machine (M0-ENG §1.2, "the crown rule").
//
// Module key: foundation.det (see code.json)
// Spec ref:   §1.2; A8
package det
