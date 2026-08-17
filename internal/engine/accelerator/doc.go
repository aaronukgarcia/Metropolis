// Package accelerator is the CERN-class particle-accelerator mega-facility
// module (MOD-077), the facility-specific behaviour behind feature key
// feat.particleaccelerator (FEAT-098). It models the accelerator's own
// mechanics: the massive electricity/water draw posted into
// engine.consumption's UtilityAPI, the research-rate multiplier that feeds
// engine.education's research output, the health spillover into
// engine.wellbeing, the FDI anchor draw into engine.fdi, the inherited
// permit + decommission obligations, and a queryable prestige output.
//
// Module key: engine.accelerator (see code.json)
// GUID:        fe85f850-f2fc-4552-801f-5580b4da6198
// Spec ref:    resources-design-brief.md §8 ("End-game and mega-facilities";
// SpaceX/CERN/Aldermaston-class science/defence facilities are permitted with
// the shared expert-gate shape) and METROPOLIS-MASTER-v2.1.md §MP (the
// "Hadron Research Ring" mega-project: "research rate xx, science tourism,
// prestige").
//
// # Catalogue reconciliation (AC-1)
//
// This module reconciles against the existing hadron_research_ring
// mega-project entry in data/buildings.json by SHAPE (a): the accelerator
// *is* that entry, enriched in place. The entry keeps its id, name, milestone
// (M10) and cost (2B) byte-equivalent; the only change is that its empty
// consumptionRef now resolves to the "accelerator" class in
// data/consumption.json. There is no second "particle accelerator" entry.
//
// # The shared expert gate (consumed, not reimplemented)
//
// The build/operate path consumes the shared §8 expert gate verdict — the
// numeric threshold on engine.education's research output that
// feat.megafacilities (FEAT-055) owns — through the local [ExpertGate]
// contract shape. This package never reimplements the education accounting
// (enrolment, promotion, output production) that produces the figure; it
// reads the output through the registered engine.accelerator → engine.education
// edge ([ResearchSource]) and asks the gate for a verdict. Because the
// accelerator's own research-rate multiplier raises that same output, a
// running accelerator can push the figure further above the threshold — this
// end-game compounding is deliberate (§8's snowball summit), not an accident.
//
// # Balance-number regime
//
// Every numeric magnitude (draw throughput, peak/base split, research
// multiplier, health coefficient, FDI draw, prestige, threshold) is a
// placeholder in data/accelerator.json, each carrying a disclosure naming it
// pending Aaron's balance pass. No acceptance criterion may pin a specific
// figure; tests assert direction and structure only.
//
// # Inherited-gate boundary
//
// The permit (feat.facilitypermits, FEAT-053) and the day-one decommission
// liability (feat.decommission, FEAT-054) are inherited from
// resources-design-brief.md §7, not reimplemented here: the build path
// delegates to local [PermitSource] / [DecommissionSource] contract shapes.
// The utility-network solve, coefficient resolution, and peak/base machinery
// belong to engine.consumption.md; this package only posts demand.
//
// # Cross-references (not restated)
//
//   - feat.megafacilities.md — the shared expert gate (FEAT-055 owns the
//     threshold/check logic; this package consumes the verdict).
//   - engine.consumption.md — the UtilityAPI the draw posts into.
//   - engine.education.md — the research-output spillover target.
//   - engine.wellbeing.md — the health spillover target.
//   - engine.fdi.md — the FDI anchor-prospect target.
//   - feat.facilitypermits.md / feat.decommission.md — the §7 obligations.
package accelerator
