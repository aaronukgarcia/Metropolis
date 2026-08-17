// Package defence implements Metropolis's Defence & Central Government
// module (BOW MOD-067; module key `engine.defence`; GUID
// 38f3829b-91df-43da-89b3-8cedd390054c; spec §55 "Defence & Central
// Government").
//
// §55 gives central government two interactions with the municipal economy,
// and this package models both sides of the ledger honestly:
//
//   - Grants — optional competitive pots (transport, regeneration, culture)
//     won by bidding with match funding, whose win rate rises with both the
//     match-funding amount and §54 planning quality; plus formula support at
//     low tax capacity (unconditional, non-competitive).
//   - Mandates — population-threshold national requirements (100k → naval
//     facility, 500k → army garrison, 1M → air defence), each offering a
//     choice within compliance (which facility, where) plus a compensation
//     grant, and a genuinely priced refusal path (legal, blocks the build,
//     costs grant access and reputation).
//
// Facilities integrate rather than decorate: a built facility sits on a
// §34 zone via engine.build, its compensation posts through engine.finance's
// double-entry ledger, its personnel become real engine.citizens records
// (married-quarters households and forces-families children with school-place
// demand), and its payroll is the §55 anti-cyclical anchor (floor-protected
// under a wage-bill recession).
//
// # Refusal is priced, not blocked (AC-6)
//
// Refusing a mandate is a legal command that succeeds. It does NOT force a
// build; it records the refusal, applies a data-sourced reputation penalty
// (queryable via [DefenceAPI.ReputationPenalty]), and thereafter rejects
// grant bids with the refusal-specific [ErrGrantRefused] code — distinct
// from the ordinary [ErrUndeclaredPot]/[ErrInvalidInput] rejection paths.
// This is the spec's "legitimate libertarian-city strategy with a price
// tag": a real decision with a documented cost, never a free pass and never
// a forced build in disguise.
//
// # Anti-cyclical payroll (AC-7)
//
// A facility's payroll is computed as max(nominal × wage-bill factor, floor),
// where both the nominal wage bill and the floor are data-sourced
// (data/defence.json). The shipped data sets floor == nominal, so under a
// recession the facility payroll stays at its pre-recession baseline while
// an ordinary employer (no floor) contracts by the same factor. The floor
// is a real, enforced mechanism — [DefenceAPI.RecordRecession] applies the
// factor and [DefenceAPI.FacilityPayroll] floors the result — never a
// descriptive comment.
//
// # Blocked edges — BUG-058 (AC-2, AC-9) and the §32 shock seam (AC-10)
//
// Three outbound edges named by §55 do not exist in code.json's registry:
//
//   - engine.defence → engine.fiscal (planning quality) — the §54 planning-
//     quality input to the grant win rate (AC-2) is accepted as a pushed
//     input via [DefenceAPI.SetPlanningQuality] (mirroring engine.attract's
//     TermInputs pattern), NOT a live fiscal call. BUG-058.
//   - engine.defence → engine.fdi (procurement) — procurement contracts
//     (AC-9) are produced as a queryable value
//     ([DefenceAPI.ProcurementContractValue]) a future engine.fdi consumer
//     can subscribe to; engine.fdi has zero registered consumers today.
//     BUG-058.
//   - engine.defence → engine.spiral (closure shock) — §55's "closure
//     events are §32-scale local shocks" (AC-10) is produced as a
//     [ClosureEvent] output by [DefenceAPI.CloseFacility]; routing that
//     event into engine.spiral's shock machinery requires a
//     engine.defence → engine.spiral edge that is not registered in
//     code.json (engine.spiral's only registered consumer is engine.crime).
//     Until that edge lands, the closure output is documented and queryable
//     here, not a defence-owned duplicate blight formula.
//
// # Mandate timing is data-driven (AC-1, AC-4)
//
// Mandate events are sourced from data/defence.json's "mandates" array
// (populationThreshold per entry), never a hardcoded population if/switch
// ladder. code.json's inbound pattern text names "external_world.json era
// script" as the mandate source; that file is §21's external-commuting
// dataset (FEAT-047) and carries no mandate events, so the mandate data
// lives in this package's own data/defence.json (GR#15, GR#3 single source
// of truth, and file-ownership — external_world.json is not this module's
// to edit). The threshold figures (100000/500000/1000000) exist only in the
// data file and in test fixtures, never in production control flow.
//
// # Determinism (AC-13/AC-14)
//
// Grant-bid outcome draws use the counter-based hash stream
// det.NewStream(worldSeed, bidID, month, "grant-bid") — no shared/global
// RNG, no math/rand. Mandate generation is deterministic (pure threshold
// comparison). Nothing reads the wall clock.
package defence
