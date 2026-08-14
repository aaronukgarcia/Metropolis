// Package households is the housing-demand module (engine.households,
// MOD-028): the §21 seventeen-typology housing catalogue, per-typology
// appeal profiles over household stage × wealth × personality, citywide
// demand expressed as a *distribution* over the 17 typologies, the
// unhoused-by-preference signal, and overcrowding / rent-burden
// derivation from household composition and dwelling capacity — all behind
// a single HouseholdsAPI.
//
// Module key: engine.households (see code.json; inbound GUID
// 5591af37-0157-4e07-a8cf-b4ab87db7466 "HouseholdsAPI", outbound GUID
// d6fe2f65-8d26-46e8-8097-7a55db5e5dbd). Spec refs: §5.4 (Households &
// housing: demand = households × dwelling-size preference; overcrowding and
// rent burden feed satisfaction and the migration balance); §21 (External
// Commuting & Housing: the 17-typology catalogue, each with an appeal
// profile over household stage × wealth × personality, citywide demand a
// distribution over types — "a city of identical blocks leaves whole
// personality segments unhoused-by-preference"); §18 (Wellbeing: financial
// stress = rent burden > 35% income, the one spec-stated numeric threshold,
// reused here as the rent-burden line).
//
// # The 17 housing typologies are data, never Go literals (GR#15, AC-3)
//
// The typology catalogue is loaded from data/buildings.json's entries whose
// catalogueSection is "HS" (Part IV's Housing Typology section), via
// foundation/data.LoadBuildings. Their names, unlock milestones, density
// text, and appealProfile tag arrays all come from that file — Batch tuning
// edits the JSON, not this package's source. The typology count is derived
// from the loaded entries at load time (there is no hardcoded literal 17):
// a buildings.json fixture with 18 HS entries yields an 18-typology API.
//
// # Household formation is engine.citizens' entity, consumed here (ASM-247)
//
// This package does NOT mint household ids, run a partnering RNG, or assign
// membership. Household membership and composition (which citizens belong
// to which household, and each household's DwellingRooms capacity) are read
// from engine.citizens via CitizensAPI's Household / HouseholdOf / CitizenAt
// queries — the single source of truth (GR#3). The ASM-247 boundary ruling:
// citizens owns the household entity and the partnering event; households
// owns the typology-appeal/demand layer that sits on top of it.
//
// # Built-stock seam (no engine.build call edge)
//
// engine.build's current BuildAPI exposes the §34 eight-way zone catalogue
// (Dwelling/Shop/…), not the 17 HS typologies, so the per-typology
// built-stock counts do NOT arrive through a call into engine.build. They
// arrive here as the command-based [HouseholdsAPI.ReportStock] mutation,
// populated by the composition root (FEAT-082) from wherever built housing
// structure counts live. code.json therefore registers no outbound call
// edge to engine.build (GR#20 — the edge is not a realized call; it was
// removed as plan drift). No HS-typology stock is re-derived from build
// internals.
//
// # Appeal is a function over stage × wealth × personality (AC-4)
//
// Each typology's appealProfile is a free-text tag array (e.g. "novelty",
// "retirees", "wealth", "community"). AppealOf translates those tags onto
// the household stage × wealth × personality axes via the documented
// tag→weight table in catalogue.go (ASM-248): a "novelty" tag weights the
// personality novelty-seeking axis positively, a "retirees" tag weights the
// retired life-stage positively, a "wealth" tag weights wealth positively.
// A typology whose tag array is empty or holds no recognised tag degrades
// to a documented neutral-appeal fallback (AppealScore.Fallback) rather
// than a divide-by-zero or an untyped-nil appeal function (AC-11).
//
// # Determinism (GR#21, AC-12/AC-13)
//
// No non-test file reads the wall clock, and there is no shared/global RNG
// source (math/rand is not imported): at Baseline One depth every appeal
// score and the dwelling-size preference are pure functions of their inputs,
// so there is no stochastic element needing a counter-based hash stream. If
// a later sprint adds dwelling-size-preference noise or appeal tie-breaking,
// it MUST draw from foundation/det.NewStream(worldSeed, householdId, month,
// purposeTag) per §1.2 — never a shared RNG. Iteration that affects an
// observable result is always over sorted slices (typology ids ascending),
// never a Go map (whose order is intentionally randomised).
//
// # Numeric safety (GR#16, FEAT-086)
//
// Every int64 quantity in this package — wealth aggregates, appeal scores,
// demand tallies, stock counts, rent/income magnitudes — routes through
// foundation/num's saturating helpers (num.SatAdd/num.SatSub/num.SafeMul)
// and every int64↔float64 conversion routes through
// num.ClampInt64FromFloat. Numeric inputs are validated at every entry
// point (constructor, mutator, query): negative rent/income or a negative
// stock count are rejected with registry-sourced errors rather than
// wrapped, and a ±MaxInt64 / mixed-sign input can never produce +Inf, NaN,
// or a wrapped-negative result.
package households
