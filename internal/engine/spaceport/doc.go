// Package spaceport implements the end-game science/prestige mega-facility
// (module key engine.spaceport, MOD-076; feature key feat.spaceport): a
// SpaceX-class launch/landing site gated on the shared expert gate — money,
// milestones, and development points alone cannot buy it. It is code.json's
// engine.spaceport inbound contract (GUID da932e36-b1a5-4e83-84a8-86611a79e99e,
// "SpaceportAPI ... launch operations, launch-exclusion blight, FDI/tourism
// draw, prestige").
//
// # Spec refs
//
// resources-design-brief.md §8 (lines 54-79) — "SpaceX / CERN /
// Aldermaston-class science/defence mega-facilities … same shape:
// expert-gated, permit-gated, enormous"; and METROPOLIS-MASTER-v2.1.md §MP
// line 498 — "Space Launch Complex | M11 + research | 3B | launches (events,
// exports), aerospace sector unlock, noise blight (coastal siting
// realistic)". This package details that thin §MP placeholder into real,
// queryable mechanics.
//
// # Catalogue reconciliation (AC-1, shape (a))
//
// The spaceport IS the existing space_launch_complex entry in
// data/buildings.json (catalogueSection "MP", supplement "S1",
// supplementCategory "mega-projects", M11 + research, costRaw "3B",
// blightClass "medium"). This feature enriches that entry in place — the
// effect data (exclusion radius, prestige/fdi/tourism figures, the expert
// threshold) lives in data/spaceport.json, keyed to the anchor — rather than
// forking a second launch-site entry (GR#3). The existing entry's fields are
// untouched; the anchor is resolved at Load and must match exactly one
// entry (ErrCatalogueAnchorUnresolved otherwise).
//
// # Balance-number regime
//
// Every numeric magnitude — launch cadence, export value, prestige, the
// FDI/tourism draw, the exclusion radius, and the expert threshold — is a
// PLACEHOLDER in data/spaceport.json, each carrying a unit and a disclosure
// naming it pending Aaron's balance pass (GR#15). Tests assert direction
// and structure only, never a specific figure.
//
// # Inherited gates (GR#20 — consumed, never reimplemented)
//
// The shared expert gate is FEAT-055's / engine.education's, not this
// package's: this package reads the single numeric education output (the
// EducationGate seam, satisfied by engine.education's EducationAPI) and
// compares it against data/spaceport.json's expert threshold. It keeps no
// enrolment tally, no output computation, and no staffing counter of its
// own. The §7 permit (feat.facilitypermits.md, FEAT-053) and the
// day-one "put back to nature" decommission liability (feat.decommission.md,
// FEAT-054) are likewise delegated through the PermitGate and
// DecommissionLiability seams. The FDI/tourism draw is injected through the
// FdiDraw/TourismDraw seams into engine.fdi (engine.fdi.md) and
// engine.tourism (engine.tourism.md), which own their internal models. The
// shared gate shape is documented in feat.megafacilities.md and
// engine.education.md; this file cross-references rather than restates
// their content.
//
// # Determinism (AC-11)
//
// Launch scheduling, build progression, exclusion-contour evaluation, and
// prestige accumulation are pure functions of (seed, tick, prior state, data
// file, commands). This package has no random draws — the cadence, build
// duration, radius, and every magnitude are data-sourced — so determinism is
// structural: byte-identical across repeated runs and worker counts, with no
// shared/global RNG and no wall-clock read on the tick path. The world seed
// is retained in New for API symmetry and any future per-launch draw.
package spaceport
