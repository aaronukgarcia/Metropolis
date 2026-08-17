// Package rail is a STUB-FOR-BASELINE stand-in for engine.rail (MOD-060,
// still open at build time). It exists for one reason: feat.containerport
// (FEAT-099) has a registered outbound edge to engine.rail's RailAPI
// (code.json), and that edge needs a consuming surface before MOD-060's full
// build lands. This stub exposes ONLY the intermodal container-transfer
// surface that edge consumes — [RailAPI.IntermodalTransfer] — implementing
// engine.rail.md AC-3's sea↔rail↔road tonnes-conservation contract (in ==
// out + dwell, independently summable).
//
// # What is deliberately NOT here
//
// engine.rail.md's real scope — fleet/works accounting (stabling → depot →
// heavy works scaled to fleet size), transfer-quality → mode-logit for
// engine.traffic, rolling-stock works build-cost discount, and the §47
// journey-planner/data contracts — is MOD-060's own build, not this stub. The
// stub keeps no fleet state, no works capacity, and no transfer-quality model.
//
// # The GR#20 wiring (no import cycle)
//
// This package imports engine.freight to implement freight.RailIntermodal
// (the consumer-driven seam feat.containerport defines), exactly as
// engine.firms implements freight.FirmRegistrar. The freight package never
// imports rail, so the freight → rail edge is satisfied by dependency
// inversion and there is no Go import cycle.
package rail
