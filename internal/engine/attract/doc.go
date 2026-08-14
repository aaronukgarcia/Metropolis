// Package attract is the attractiveness & migration module (engine.attract,
// MOD-029): the §11 "master dial" — the seven-term attractiveness score A,
// the signed monthly net-migration response g(A − A_world), the §4
// static/infinite world pool, per-resident personality-weighted emigration,
// and the asymmetric reputation-momentum term that makes the Detroit trap a
// mechanic. All behind a single [AttractAPI].
//
// Module key: engine.attract (see code.json; inbound GUID
// 0c36bd43-d123-42bd-ab73-be06d33887d3 "AttractAPI", outbound GUID
// 7c3d420c-3d6e-48de-9c21-f46ff1b38ead). Spec refs: §11 (Attractiveness &
// Migration — the master dial); §4 (Population Scale — growth is
// migration-dominated, pulling migrants from an abstract world pool).
//
// # The seven-term formula (AC-1/AC-2)
//
//	A = w₁·jobAvailability + w₂·housingAffordability + w₃·serviceCoverage
//	    + w₄·environment + w₅·leisureFit + w₆·safety + w₇·reputation(momentum)
//
// The seven weights are loaded from config data ([Config]/[ParseConfig]),
// never literals in this package's source (GR#15): rebalancing is a data
// edit. Each term is independently queryable ([AttractAPI.JobAvailability],
// [AttractAPI.HousingAffordability], [AttractAPI.ServiceCoverage],
// [AttractAPI.Environment], [AttractAPI.LeisureFit], [AttractAPI.Safety],
// [AttractAPI.Reputation]) and [AttractAPI.A] returns the composite — the
// code.json "term-decomposed A score, every term drill-through" pattern.
//
// # Registered-edge terms vs pushed-input terms (AC-3, ASM-243/BUG-058)
//
// Five of the seven terms have no registered outbound call edge from
// engine.attract in code.json (no edge to engine.firms/market,
// engine.services, engine.world, engine.leisure, or engine.crime), so this
// package does NOT silently call those modules — that would be a GR#20
// violation disguised as a working feature. Those five (JobAvailability,
// ServiceCoverage, Environment, LeisureFit, Safety) are accepted as pushed
// input through the documented [TermInputs] struct via
// [AttractAPI.SetTermInputs]. HousingAffordability is the one term computed
// for real today: it calls engine.households' HousingAffordability
// (engine.households AC-9) combined with engine.finance's wage/income
// context — both registered outbound edges. When BUG-058 wires the five
// missing edges, this package's pushed-input surface and the end-to-end
// scenario both need a revision pass (AC-3's escalation).
//
// # Reputation momentum — asymmetric, the Detroit trap (AC-5, US-2)
//
// Reputation is a slow-moving, signed momentum term: it lags the deviation
// of the six-term fundamentals from a baseline anchored at the first
// observed value, converging at a strictly-larger rate toward negative
// deviations than positive ones. "Cities rising attract beyond
// fundamentals; cities falling repel beyond fundamentals" — and the repel
// is stronger. That asymmetry is structural: Config validation rejects a
// symmetric or reversed rate pair (fallRate ≤ riseRate), so a build can
// never silently ship a one-way dial that makes the Detroit spiral
// (engine.spiral, MOD-030) a marketing description instead of a reachable
// failure state.
//
// # Migration (AC-4/AC-6/AC-7)
//
// Monthly net migration is g(A − A_world) = migrationRate · (A − A_world),
// signed: positive admits migrants, negative removes residents — the same
// attractiveness engine run in reverse (US-1). Immigration is bounded by
// housing vacancy (dwelling units, via engine.households at the scenario
// level) and junction arrival throughput (via engine.logistics at the
// scenario level, ASM-246 — no direct call edge is registered); a vacancy of
// zero caps admission at zero regardless of a large positive gap (AC-7).
// Emigration is per-resident and personality-weighted: each resident's
// hazard is strictly increasing in their ambition axis (§11's "ambitious
// citizens leave sooner when opportunity dries up", AC-6), and each
// departure is decided by the counter-based hash stream
// hash(worldSeed, id, month, purposeTag) — no shared RNG (AC-12).
//
// # World pool (AC-8)
//
// A_world — the comparison baseline — is read through the [WorldPool] seam,
// never as a bare float literal at each call site. v1 is the
// [StaticWorldPool]: a static, data-loaded value ("infinite pool, static
// world", §4). A future finite/dynamic world pool (§4's explicit future
// hook) is an interface/data change, not a rewrite. A missing A_world
// (nil WorldPool) is rejected with ErrWorldPoolMissing, distinguishable
// from a genuine zero (AC-11).
//
// # Determinism & numeric safety (AC-12/AC-13, GR#16/FEAT-086)
//
// Nothing in this package reads the wall clock, and there is no shared or
// global RNG source: every stochastic draw (which specific residents
// emigrate) is a counter-based [det] stream keyed (worldSeed, id, month,
// purpose). Every int64 quantity routes through saturating arithmetic and
// every float64↔int64 conversion through a clamping choke point, every
// numeric input is validated at every entry point (constructor, mutator,
// query), and a ±MaxInt64 / mixed-sign / NaN / ±Inf input can never wrap
// negative or produce +Inf/NaN.
package attract
