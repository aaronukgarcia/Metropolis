// Package census is the living-city census & life-history module
// (MOD-078), feature key feat.citycensus and module key engine.census (see
// code.json once ES-1's registration is resolved). It owns four
// non-blocking observation threads running OFF the determinism-critical
// tick path, the cradle-to-grave object-bio assembly, the consistency
// checker's GUID + least-check-in liveness tracking, and the demographics +
// KPI + drill-in data model.
//
// # Spec refs
//
// §13 F6 Demographics (ASCII population pyramid, education pipeline,
// workforce by sector/skill, personality & leisure-taste distribution),
// §18 Wellbeing (Wellbeing = f(physical, mental, satisfaction)), §40 Social
// Services (homelessness services, unemployment duration), §45 Firms (the
// blue/white-collar split emergent from employment/sector data), §46 FDI
// (anchor-archetype industry ties), UI-SPEC §4 (the drill-through rule —
// every number selectable, Enter goes to its source), and §5.2 determinism
// (counter-based hash streams, no map-iteration nondeterminism).
//
// # Balance-number regime (AC-16)
//
// Every bell-curve parameter (lifespan mean/spread, retirement age, annual
// mileage, the crime→education elasticity, the blue/white-collar baseline,
// the happiness-weight composition) and every thread threshold (the CC's
// check-in lag, the regulator's crime/unfed/uneducated alarm levels) lives
// in data/census.json as a disclosed placeholder pending Aaron's row-by-row
// balance pass. No AC in this package is satisfied by a hardcoded final
// figure; tests check direction and structure, never a pinned magnitude.
//
// # Observer boundary (AC-2, GR#21)
//
// The four threads — stats generator, auditor, consistency checker,
// regulator — are observers: they read a committed [Snapshot] of resolved
// tick state and write only to the census's own output surfaces (latest
// aggregates, history, check-ins, findings). Running them over a given tick
// leaves the seven consumed modules' state byte-identical to what it was
// before the threads ran. The census never advances the wall clock (no
// wall-clock read anywhere in this package's non-test sources); every
// time-dependent value advances by simulation tick/month only.
//
// # Consumption boundary (AC-9/AC-12, GR#20)
//
// The census reads only through its seven narrow source interfaces —
// [CitizensSource], [EducationSource], [CrimeSource], [WellbeingSource],
// [ServicesSource], [PoliciesSource], [FinanceSource] — and never re-
// implements any of them. It never reaches for another module's concrete
// struct types (a citizen record, a cold shard, an education record, a
// finance ledger). It surfaces the income figure the finance ledger tracks
// and computes no levy on it — the census reports income, it does not
// assess it (AC-12).
//
// # Liveness vs invariant (ASM-647)
//
// The consistency checker's GUID + least-check-in tracking is object
// LIVENESS — "is every tracked object still being observed" — and is
// distinct from engine.invariant's people/money/goods CONSERVATION. The
// census does not re-implement or register with the invariant checker.
package census

// ASM-1272 (confirm-and-close). SEC-152 canonicalises GUID spelling (leading-zero padding normalised to kind-name:unpadded-id) rather than rejecting it.
// ASM-1288 (confirm-and-close). SEC-161 fixed with num.SatAdd/num.SatSub saturation (SEC-131 precedent), not reject-on-overflow or a Config.validate upper bound.
// ASM-1200 (confirm-and-close). TrackObject rejects a citizen GUID registered LifeSpanShortLived because citizens are always whole-game (checkLocked invariant).
// ASM-647 (confirm-and-close). Census CC tracks liveness (GUID + least-check-in), distinct from engine.invariant conservation — observer boundary documented, not merged or duplicated.
//
// ASM-1206 (confirm-and-close). Source(happiness) rejects a non-finite float
// via num.SafeInt64 (the GR#16 reject-form choke point), so the returned
// error code is foundation.num's MET-F800/MET-F801 with its own correlation
// ID — the census does not add a duplicate census code for the same coercion
// (matches config.go, which already calls num.SafeInt64). If a census-
// specific code is later wanted for the drill-in surface, that is the wrap
// point.

// ASM-1204 (confirm-and-close). Population-bounded counters and healthSum stay plain arithmetic, not num.SatAdd — none can wrap int64 (max 100M citizens; healthSum adds uint8 HealthBand in 0..5); revisit if the cap rises or HealthBand widens.
// ASM-1290 (confirm-and-close). Per-citizen int64 counters stay plain increments — bounded by len(snap.Citizens), outside the source-controlled magnitude class SatAdd/SatSub guards.

// ASM-1168 (confirm-and-close). Source() drill-in covers the eight KPI aggregates only; the three spline series (age/sex/education-tier) and the blue/white-collar datum carry no explicit drill-target (documented scope).
