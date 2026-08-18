// Package policies implements the Metropolis policy system (BOW MOD-064;
// module key `engine.policies`; GUID c364533c-6d39-45c5-b7a7-9abfb0459681;
// spec §52 "Policies v2 & Named Districts" and §II.7's high-level summary).
//
// It owns the policy library loaded from data/policies.json (categories:
// movement, layout & wellbeing, economy, social), where every policy states
// its mechanism as an explicit list of (coefficientKey, delta) pairs, its
// cost/enforcement needs, its scope (citywide/district/road), and its
// conflict pairs — the §52 "modelled instruments" claim made checkable
// against real code (US-1/AC-1/AC-2).
//
// # Named districts (ASM-285)
//
// Districts are the scope system and an identity mechanic. A district is a
// name plus a set of world cell references (engine.world's TileCoord +
// CellLocal), NOT freeform vector polygons (ASM-285). The name is
// queryable metadata (AC-12) that engine.firms' location logic will consume
// in Sprint 10; renaming preserves the DistrictID and its existing
// policy/tax scoping (AC-8). The district→cell mapping lives in this
// package, since engine.world's Cell carries no DistrictID field and this
// item's file ownership does not extend to engine.world.
//
// # The preview promise (AC-6)
//
// [PoliciesAPI.PreviewImpact] promises exactly one thing: a same-model
// conditional projection of the policy's declared coefficient deltas,
// computed by feeding the identical coefficient-delta payload the real
// enactment applies into engine.projections' registered curve providers,
// carrying engine.projections' own Computed/Extrapolated confidence tags
// (AC-4/AC-5). It is NOT a guarantee, NOT a point estimate with an implied
// error bar the game never states, and NOT independent of other decisions
// the player or the simulation makes between preview and enactment. Points
// beyond ProjectionsAPI's current horizon N are tagged Extrapolated by
// engine.projections itself, never Computed (AC-5).
//
// # PreviewDrift — what happens when the promise is wrong (AC-7, ASM-286)
//
// Every enacted policy's preview snapshot (the coefficient-delta payload
// and the Computed-tagged portion of its curve) is persisted at enactment
// time keyed by a policy-enactment ID. At the data-declared checkpoint
// cadence after enactment — quarterly by default (ASM-286:
// meta.previewDrift.checkpointMonths), driven by the simulation month via
// [PoliciesAPI.AdvanceMonth], never the wall clock — the actual observed
// curve values for the same coefficients are compared against the stored
// preview's Computed points. A divergence beyond the data-declared
// tolerance (ASM-286: ±10%, meta.previewDrift.tolerance) raises a
// queryable, registry-sourced PreviewDrift event naming the policy, the
// coefficient, the checkpoint, and the magnitude — never silently
// discarded (US-3). The tolerance and cadence are stored in
// data/policies.json (GR#15), and are placeholders pending Aaron's balance
// pass (ASM-286/ASM-284).
//
// # Enactment cost (AC-19)
//
// Every policy's declared cost debits through engine.finance's FinanceAPI
// at enactment, and its enforcement needs debit as a recurring opex line
// each month (AC-19). The engine.policies→engine.finance edge is registered
// in code.json (commit c36778b); AC-19 is a normal buildable check, not a
// deferred one — this package never posts a cost through engine.tax (the
// wrong category) and never silently skips a monetary posting (GR#17).
//
// # Determinism (GR#21, AC-14/AC-15)
//
// Nothing in this package reads the wall clock. Evaluation order
// (compounding resolution, PreviewDrift checkpoint evaluation, opex
// posting) is sorted by a documented stable key — enactment (PolicyID,
// district, road), coefficient key, and point month — never Go map
// iteration order.
package policies
