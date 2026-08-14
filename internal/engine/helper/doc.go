// Package helper is FEAT-063's advisor-metadata registration contract
// and ask-driven panel query surface — "the Helper", a context-aware
// player advisor answering exactly two pull-only questions: "what
// should I do?" (Recommend) and "what if I do X?" (Preview).
//
// Module key: feat.helper (see code.json; GUID 5e185ef4-b9b0-4fa3-98ad-9f79079dc015)
// Spec ref:   docs/planning/acceptance/feat.helper.md; FEAT-063 BOW ruling (Aaron via Bill, 2026-08-12)
//
// # The three-member contract (AC-1)
//
// Every future player-action feature module implements and registers a
// [Registrant] with a [Registry] at boot. Registrant has EXACTLY three
// members — go doc ./internal/engine/helper Registrant must show
// exactly these three, no more:
//
//  1. TaxonomyID() ActionTaxonomyID — a stable, non-empty,
//     registry-unique identifier.
//  2. Preconditions() []Precondition — independently-evaluable gates,
//     never a single opaque boolean.
//  3. ProjectConsequence(state, params) (ConsequenceProjection, error)
//     — a structured consequence-pricing projection query.
//
// # Registering costs ONLY metadata (AC-4, the standing constraint)
//
// A Registrant implementing exactly those three members and nothing
// else compiles, registers, and appears correctly in Recommend/Preview
// with NO additional obligation: no UI rendering hook, no
// RenderHint/IconID/hover-callback field, ever. This package imports
// nothing from the UI package tree and has zero code path that fires on
// a selection/hover/cursor event (AC-4b) — that boundary is checked
// mechanically (this package's own source is grepped for the UI
// package's import path, mirroring AC-4b's check command), not just
// documented here.
//
// # Registration is sealed after boot (AC-3)
//
// [Registry.Register] is callable only during boot wiring. Once the
// registry has been read from — by [Registry.Recommend], [Registry.Preview],
// or an explicit [Registry.Seal] call — every further Register call
// returns a registry-sourced ErrRegistrationSealed rather than either
// panicking or silently succeeding into a registry a live query has
// already read a stale view of. This is enforced by [Registry]'s
// unexported sealed flag, checked under the same mutex every exported
// method takes — never merely asserted in a doc comment
// (docs/planning/dev-team-process.md: "a comment saying 'never X at
// runtime' is a code smell, not a control").
//
// # Consequence projections must be sourced from the real pricing path (AC-5)
//
// [Registrant.ProjectConsequence] MUST read from the same pricing/data
// source its owning feature's real execution path uses — never a
// second, hand-maintained estimate that can silently drift out of
// truth. This is weakness pattern #2 ("a value duplicated across a
// module boundary needs a drift test"), applied one layer up: if a
// registrant's ProjectConsequence promises a rate its real transaction
// never honours (e.g. it quotes £100 but the actual execution path
// charges £150 because a fee was added to one side and forgotten on the
// other), the player's real, sticking decision (design-north-star.md's
// "does the consequence stick?" test) was made on bad information — the
// Helper itself becomes the lie. internal/engine/helper/helperfixture
// demonstrates the required pattern: its fixture Registrant and a
// fixture "execute" stub both read the SAME shared data source, with a
// drift test proving the comparison can actually fail (weakness pattern
// #2 step 4) rather than only existing to always pass.
//
// # v1 scope boundary: contextual on-selection previews are NOT built here (AC-10)
//
// Per Aaron's 2026-08-12 ruling, cited verbatim: "Helper v1 = ASK-DRIVEN
// PANEL, pull-only: the player opens the Helper and asks 'what should I
// do?' / 'if I do X what happens?'. Current features pay ONLY the
// advisor-metadata registration cost (action taxonomy id, preconditions,
// consequence-pricing projection queries) — the standing constraint in
// this item's description is confirmed in force NOW. Presentation ships
// once, in the panel; contextual on-selection previews are a later
// layer over the same registered data, not a v1 requirement on every UI
// surface."
//
// The ONLY trigger for a Helper query in v1 is an explicit player action
// opening the panel and submitting an ask — no hover/selection/
// cursor-focus UI event may call Recommend or Preview. This is why
// Registrant has no RenderHint/IconID/hover-callback field: adding one
// now, even unused, is the first thread of that later layer pulled
// forward uninvited (AC-10's "what a lazy implementation looks like,
// direction b"). A future contextual-preview layer is a new UI surface
// reading the SAME registered data through Registry.Preview — this data
// model does not need reworking to support it (US-4).
package helper
