// Package farming is the shared home of engine.farming (MOD-045) and
// feat.farmtypes (FEAT-104): per-farm soil quality, the regional
// Biodiversity Index, crop/livestock choices with stocking density, and the
// §31 food chains. This file documents the farm-type catalogue half — the
// five §31 facility categories as distinct modelled facilities — which lives
// in this package as a file/type set alongside engine.farming's own eventual
// FarmingAPI/BDI surface, NOT as a fork or a second package.
//
// # Shared-package, no-separate-inbound-contract arrangement (GR#20)
//
// code.json's feat.farmtypes entry (path internal/engine/farming/) has NO
// inbound contract of its own (name/format/pattern all null) because it
// shares engine.farming's package and is expected to surface through that
// module's eventual FarmingAPI (code.json's engine.farming.inbound.name is
// "FarmingAPI"). The catalogue is therefore a plain in-package surface —
// FarmTypeParams, LoadFarmTypes, and FarmTypeCatalogue.Resolve/Types — that
// engine.farming's own regime/BDI surface will consume, not a parallel API.
// The loader is self-contained (os.ReadFile + encoding/json) and imports only
// internal/foundation/errs, so this package declares no unregistered module
// edge beyond what code.json already records.
//
// # The load-bearing distinct-facility contract (AC-2)
//
// Each farm type (arable, livestock, orchard, market garden, vineyard) is a
// DISTINCT modelled facility with its own footprint, soil-quality band,
// terrain/slope preference, BDI term, stocking density (livestock only) and
// typed chain output. Two same-category types — arable and orchard — resolve
// to two different parameter sets from data, with footprint, soil band, BDI
// term and chain output pairwise non-equal. A single shared row keyed by a
// type string (or a map where every entry shares one footprint/soil/BDI/chain
// and only the key differs) is the anti-pattern this feature's tests are
// written to fail.
//
// # Regime is orthogonal to type (ASM-679)
//
// Conventional vs organic is engine.farming AC-8's regime bundle applied to a
// resolved type, NOT a separate facility type. This catalogue supplies the
// per-type parameter sets that regime bundle multiplies; it does not model
// certification lag, yield multipliers, or BDI decline/recovery. The per-type
// BDI term this catalogue carries is an INPUT to engine.farming AC-2's
// five-factor regional BDI, not a re-derivation of that engine.
//
// # The GR#15 data file and the balance-number regime
//
// Every per-type figure — footprint (cells), soil band, terrain preference,
// BDI term, stocking density (head per cell), and chain handoff — is sourced
// from data/farmtypes.json at load time, and every figure is a PLACEHOLDER
// pending Aaron's balance pass (each "types" entry carries a non-empty
// "disclosure" field naming it as such). None of these figures is a Go
// numeric literal in this package (AC-4): a balance pass edits the data
// file, never this package. Tests assert shape/direction/distinctness, never
// a specific magnitude.
//
// # Spec refs
//
//   - §31 Farming & the Biodiversity Engine — per-farm soil quality, regional
//     BDI, crop/livestock choices with stocking density, chalk-slope vines,
//     and chains to mill/dairy/abattoir/packhouse/winery.
//   - §17 Resource Consumption Model — "Farms, quarries, plants: producer
//     coefficients in catalogue" (this catalogue's per-type parameter sets).
//   - §25 — food waste → compost → farm input, the inbound half of the
//     compost loop; cross-referenced only, since the compost-input
//     consumption is engine.farming's (MOD-045), not this catalogue's.
//   - engine.farming.md (MOD-045) — cross-referenced, not restated: this
//     file's per-type parameters are consumed by that file's AC-8 regime
//     bundle and AC-11 five chain-output accessors; no BDI/regime/
//     certification AC belongs here.
package farming
