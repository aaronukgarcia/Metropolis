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
