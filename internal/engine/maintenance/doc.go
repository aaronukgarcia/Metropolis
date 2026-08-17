// Package maintenance implements the per-instance maintenance system for
// the Metropolis engine: code.json's engine.maintenance module (GUID
// 7fe830e0-c929-416e-adbf-033306925716, inbound contract MaintenanceAPI
// GUID 53cbab1a-a4ec-48bb-9ec5-7487d269bef3).
//
// # What this package owns
//
// Every placed structure carries its OWN MaintenanceView — engineer-days
// per year scaled by class and size, an age, an efficiency, and a
// lifetime. This generalises §20's per-object road-maintenance precedent
// ("maintenance state ... maintenance budget is real") from roads to every
// placed object: two otherwise-identical objects of the same class placed
// at different months hold distinct records with distinct ages, and aging
// one never moves the other.
//
// An instance's efficiency declines monotonically with age (new objects
// need less repair than old), and an instance whose age has reached its
// lifetime transitions to a distinct, queryable NeedsRefit end-of-life
// state — the §12 decay-state precedent — rather than silently continuing
// at full efficiency.
//
// A maintenance crew's day is a bounded engineer-day budget applied to a
// list of jobs (a pothole costs a fraction of a school's repair): in one
// crew-day the crew resolves MULTIPLE jobs when its budget suffices, never
// one-trip-one-fix. Un-fixed demand accumulates as a visible backlog that
// grows under under-funding and decreases only by exactly the engineer-days
// a crew actually applies — a conservation invariant (GR#16), proven, not
// asserted.
//
// # Three boundaries this package observes (GR#20)
//
// (a) Balance-number regime: every per-class engineer-day rate and lifetime
// in data/maintenance.json, and both cost-per-engineer-day figures, is a
// PLACEHOLDER pending Aaron's balance pass. The tested invariants are
// direction and structure (new < old; distinct classes; a past-lifetime
// object is flagged), never a specific magnitude. Rebalancing is a data
// edit, never a code change.
//
// (b) Crew-supply boundary (AC-9): the crew capacity — how many engineer-
// days are available today — is an INJECTED input via SetDailyBudget, wired
// by engine.staffing (MOD-073). This package owns the demand and the
// application of work; the city-wide repair demand it surfaces (the §54
// Public Service Pie staffing target, the MOD-073 tie-in) is computed by
// aggregation over the per-instance views, never a separately-maintained
// counter. It never computes supply from population state, and imports no
// staffing/population package.
//
// (c) OPEX boundary (AC-10): maintenance spend settles through
// engine.finance's existing SettleOpex surface, never a maintenance-local
// currency ledger. The only "Money" token in this package's source is
// engine.finance's own type at the settlement call; spend is computed as a
// plain int64 micro-pound figure here and handed to finance.
//
// # Determinism
//
// The maintenance tick (aging, efficiency derivation, backlog growth, crew
// application) is a deterministic function of prior tick state, loaded
// data, and injected inputs. Age advances by simulation month index only —
// time.Now/time.Since are never read on the tick path. Instances are
// iterated in sorted StructureID order and jobs in FIFO enqueue order, so
// map-iteration order never influences output (GR#21).
package maintenance
