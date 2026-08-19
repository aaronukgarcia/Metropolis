// Package extcommute is the external commuting & dormitory-arithmetic module
// (MOD-035): the §21/A6 off-map job-pool model — aggregate off-map commuter
// flows that load the transport network, not individual records.
//
// Module key: engine.extcommute (see code.json; GUID 39bb5039-2f59-4e32-9cb5-d7f899a05d3a)
// Spec ref:   §21 (External Commuting & Housing); A6 (R6 "out-commuter tax
//
//	farming exploit" finite-era-scaled-capacity amendment); §19.3
//	commute accounting; §39/F2 taxation incidence; F6 in-commuting
//	wage-leak view.
//
// # The three pools (AC-2)
//
// The named off-map job pools — London, Ashford, Dover — are loaded from
// data/external_world.json (via foundation/data's ExternalWorld schema), not
// hardcoded. Each pool carries a per-era capacity table (A6: "London absorbs
// a bounded and slowly growing share"), a monthly off-map wage, and a
// transport-requirement list. [ExtCommuteAPI.Capacity] reports the
// era-scaled capacity; [ExtCommuteAPI.FilledSlots] reports how many citizens
// currently hold a job in a pool.
//
// # The two-cap model (AC-3/AC-8)
//
// An off-map assignment is subject to two independent caps:
//
//  1. Pool capacity — FilledSlots(pool) <= Capacity(pool, era), enforced as a
//     hard invariant: the (Capacity+1)th assignment is rejected with
//     ErrPoolFull, never silently accepted (AC-3/AC-4).
//  2. Transport capacity — the reaching leg (motorway car/coach, or from tier
//     5 the external-rail season ticket) must have available headroom after
//     engine.traffic's congestion. A pool with free slots but no reaching leg
//     is rejected with ErrTransportCapacity (AC-8).
//
// # The dormitory arithmetic identity (AC-6/AC-7)
//
// §21's accounting identity is:
//
//	TotalWorkingAgeResidents == Σ LocallyEmployed + Σ_pool OffMapEmployed(pool)
//	                           + Unemployed + NotInLaborForce
//
// Every off-map assignment is one citizen, one pool (the assignments map is
// the single source of truth for FilledSlots), never double-counted across
// pools (AC-11). On a successful [ExtCommuteAPI.Assign] this module also flips
// the citizen's coarse employment state to EmploymentOffMap through the
// citizens seam's ApplyLifeEventEmployment — and on [ExtCommuteAPI.Release]
// flips it back to EmploymentUnemployed — so a citizen's own record now agrees
// with this module's bookkeeping (ICD engine.citizens-offmap.md §4, FEAT-198's
// EmploymentOffMap enum extension). With that write in place the full identity
// is computable and exact: LocallyEmployed/Unemployed/NotInLaborForce are
// read from citizens' own EmploymentState (NotInLaborForce := Retired +
// Student + adult-never-worked), OffMapEmployed(pool) is this module's
// FilledSlots(pool), and the two are cross-checked
// (count(EmploymentOffMap) == Σ_pool FilledSlots(pool)) — see
// TestDormitoryArithmeticIdentity and TestDormitoryArithmeticSequence.
//
// # Fiscal thinness (AC-12/A6c)
//
// An off-map job yields income tax but NO business rates and NO corporate
// share. This package records only the off-map wage (income-tax-eligible)
// via the finance seam's RecordOffMapWage; it never calls RecordBusinessRates
// or RecordCorpShare, so the rates/corp-share category is exactly zero for an
// off-map-employed citizen by construction.
//
// # In-commuting (AC-9/AC-10)
//
// [ExtCommuteAPI.InCommute] fills an aggregate local labour shortage with
// off-map in-commuters. The filling workers are never residents — the method
// never touches the citizens seam's population and creates no citizen record
// — and the wage they take home is recorded as a distinct wage-leakage entry
// via RecordWageLeakage (F6's "player sees the leak" ledger fact).
//
// # No invented soft-cap or mental-health penalty (AC-13/AC-14)
//
// This package computes NO separate mental-health or wellbeing penalty for
// dormitory commuters: out-commuters' door-to-door commute time reaches
// engine.wellbeing through the same commute-time figure engine.traffic and
// engine.citizens already carry (§19.3). It also imposes no tenure cap —
// a citizen may hold an off-map job indefinitely, subject only to the pool
// cap, the transport cap, and engine.wellbeing's emigration-probability soft
// cap.
//
// # Determinism (GR#21)
//
// Pool selection uses a counter-based hash stream
// det.NewStream(worldSeed, 0, month, "extcommute.select") — never a shared or
// global RNG, never math/rand, never a wall-clock read. Every observable map
// iteration is either order-independent counting or sorted by key.
//
// # Dependencies (GR#20, contract-first)
//
// The only concrete engine dependency is foundation/data (the §24 data layer,
// always allowed). engine.citizens, engine.traffic, and engine.finance are
// consumed through the local CitizensSeam / TrafficSeam / FinanceSeam
// interfaces, wired by the composition root via SetCitizensSeam /
// SetTrafficSeam / SetFinanceSeam — never a concrete cross-module import, so
// no unregistered edge is introduced (GR#25).
package extcommute
