# GR#25 Compliance Evidence Table

> Generated 2026-08-21 from `tools/plan/spec-lint.js` against `code.json` on the current base (origin/main). Data-derived — regenerate, never hand-edit. Command:
>
> ```powershell
> NODE_PATH=E:\git\Metropolis\node_modules node -e "const {runLint}=require('E:/git/Metropolis/tools/plan/spec-lint.js'); const r=runLint({repoDir:'E:/git/metropolis-bill'}); console.log(JSON.stringify({errors:r.totalErrors,warnings:r.totalWarnings,filesChecked:r.filesChecked,findingsByFile:r.findingsByFile},null,2))"
> ```

## Summary (data-derived, current graph)

| Metric | Value |
|---|---|
| Acceptance .md files scanned | 207 |
| Files with SPEC-LINT-001 graph violations | 134 |
| Total SPEC-LINT-001 findings | 574 |
| SPEC-LINT-002 (identifier) / 003 (dead path) | 2 / 0 |
| SPEC-LINT-004 (unregistered key) warnings | 48 |
| Files clean (no findings) | 73 |
| Unmapped real-prefix spec files (graph checks skipped) | 26 |

**Root cause** (checkpoint 2026-08-17, sitrep R3): `claude-spec-guard.js` was never written — GR#25 is prose-only — and the `code.json` graph is stale (292 real Go imports missing). These findings are plan-vs-Go-source drift surfaced by spec-lint, not new spec regressions. Fix route (Bev-owned registry): register missing edges in `master-plan-v2.1.json` -> regenerate `code.json` -> re-run this table -> then write/wire the guard.

## Per-spec findings (sorted by count, top 40)

| Spec file | Findings | Unregistered citations (distinct) |
|---|---|---|
| feat.deathwave.md | 14 | engine.citizens, engine.invariant, engine.core, int.serializer, engine.build, engine.cafe, engine.consumption, engine.education, engine.farming, engine.projections, engine.tourism, engine.wellbeing, feat.disasters |
| ui.screen.census.md | 14 | ui.screen.build, ui.screen.menu, ui.screen.proj, ui.screen.ticker, ui.screen.trade, harness.stub, ui.screen.services, engine.traffic, engine.build, engine.airunits, ui.screen.map, ui.screen.debug, ui.screen.districts, ui.screen.finance |
| ui.screen.districts.md | 13 | engine.projections, engine.world, ui.screen.map, ui.widgets, engine.fiscal, ui.diagrams, ui.core, ui.screen.debug, ui.screen.finance, ui.screen.build, ui.screen.proj, ui.screen.services, ui.screen.trade |
| ui.screen.finance.md | 12 | harness.stub, ui.screen.map, ui.screen.demo, ui.widgets, ui.screen.debug, ui.screen.proj, engine.freight, engine.logistics, engine.tourism, engine.extcommute, engine.fdi, engine.defence |
| feat.pharmacampus.md | 11 | feat.facilitypermits, feat.decommission, engine.finance, engine.build, engine.staffing, feat.commoditymarket, feat.megafacilities, engine.mining, engine.accelerator, engine.news, engine.freight |
| engine.attract.md | 9 | engine.firms, engine.services, engine.world, engine.leisure, engine.crime, engine.logistics, engine.core, engine.market, engine.coastal |
| engine.capexport.md | 9 | engine.consumption, ui.screen.proj, engine.rail, ui.screen.finance, ui.screen.trade, engine.fdi, engine.tax, engine.fuel, engine.market |
| engine.crime.md | 9 | engine.market, engine.firms, engine.invariant, engine.finance, engine.traffic, engine.attract, engine.world, engine.refuse, engine.news |
| engine.leisure.md | 9 | engine.attract, engine.build, engine.services, engine.refuse, engine.season, engine.social, engine.accelerator, engine.firms, engine.education |
| engine.refuse.md | 9 | engine.invariant, engine.citizens, engine.finance, engine.traffic, engine.news, engine.mining, engine.dispatch, engine.consumption |
| feat.commoditymarket.md | 9 | engine.mining, engine.fdi, engine.freight, feat.facilitypermits, feat.resourcesurvey, feat.decommission, feat.megafacilities, engine.logistics, engine.finance |
| feat.deathservices.md | 9 | engine.traffic, engine.projections, engine.season, engine.dispatch, engine.invariant, engine.core, int.serializer, engine.refuse, engine.finance |
| feat.megafacilities.md | 9 | engine.finance, feat.resourcedeposits, feat.extraction, feat.facilitypermits, feat.decommission, engine.mining, data.catalogue, engine.unlocks, feat.resourcesurvey |
| feat.refinery.md | 9 | engine.freight, engine.dispatch, feat.commoditymarket, engine.mining, feat.facilitypermits, feat.decommission, engine.finance, engine.consumption, engine.market |
| ui.screen.trade.md | 9 | ui.diagrams, engine.unlocks, engine.traffic, engine.roads, ui.screen.build, engine.chemicals, engine.fuel, ui.screen.proj, ui.screen.debug |
| engine.destination.md | 8 | engine.shopping, engine.cafe, engine.traffic, engine.logistics, engine.build, engine.world, engine.farming, engine.season |
| engine.social.md | 8 | engine.spiral, engine.invariant, engine.crime, engine.firms, engine.education, engine.finance, engine.traffic, engine.census |
| plan.pipeline.md | 8 | feat.resourcedeposits, feat.resourcesurvey, feat.extraction, feat.commoditymarket, feat.facilitypermits, feat.decommission, feat.megafacilities, engine.mining |
| data.catalogue.md | 7 | engine.build, engine.services, engine.consumption, engine.unlocks, feat.minetypes, feat.farmtypes, feat.factorytypes |
| engine.cafe.md | 7 | engine.policies, engine.crime, engine.tourism, engine.shopping, engine.finance, engine.prison, engine.social |
| engine.coastal.md | 7 | engine.world, engine.education, engine.households, engine.build, engine.tourism, engine.season, engine.projections |
| engine.fdi.md | 7 | engine.capexport, engine.rail, engine.fuel, engine.attract, engine.tax, engine.education, engine.projections |
| engine.roads.md | 7 | data.catalogue, engine.season, ui.screen.map, engine.build, engine.services, engine.rail, engine.citizens |
| feat.saveux.md | 7 | engine.citizens, engine.world, engine.market, engine.finance, cloud.azure, engine.unlocks, engine.core |
| feat.spaceport.md | 7 | engine.finance, data.catalogue, engine.unlocks, engine.firms, engine.world, engine.build, engine.staffing |
| engine.build.md | 6 | engine.households, engine.consumption, engine.education, engine.farming, engine.projections, engine.tourism |
| engine.comms.md | 6 | engine.unlocks, engine.cafe, engine.education, engine.shopping, engine.spiral, engine.build |
| engine.defence.md | 6 | engine.fdi, engine.fiscal, engine.accelerator, engine.spaceport, feat.pharmacampus, engine.core |
| engine.freight.md | 6 | engine.invariant, engine.citizens, engine.finance, engine.traffic, engine.social, engine.projections |
| engine.news.md | 6 | engine.season, engine.market, engine.finance, engine.crime, engine.attract, engine.wellbeing |
| engine.rail.md | 6 | ui.screen.trade, engine.logistics, engine.capexport, engine.finance, engine.prison, engine.policies |
| engine.services.md | 6 | engine.unlocks, data.catalogue, engine.world, feat.disasters, engine.farming, engine.mining |
| engine.tourism.md | 6 | harness.synth, engine.households, engine.tax, engine.cafe, engine.build, engine.projections |
| engine.tunnels.md | 6 | engine.unlocks, engine.world, engine.attract, engine.chemicals, engine.consumption, engine.policies |
| feat.decommission.md | 6 | feat.facilitypermits, engine.mining, ui.screen.finance, feat.resourcedeposits, engine.build, engine.world |
| feat.facilitypermits.md | 6 | engine.unlocks, engine.mining, feat.decommission, data.catalogue, ui.screen.build, int.serializer |
| feat.factorytypes.md | 6 | engine.freight, engine.firms, engine.fdi, data.catalogue, engine.consumption, engine.mining |
| feat.helper.md | 6 | engine.market, feat.saveux, feat.debugmode, feat.disasters, int.protocol, engine.core |
| feat.metricsdash.md | 6 | int.protocol, ui.core, ui.dash, feat.skeleton, tool.bow, tool.planguard |
| feat.skeleton.md | 6 | harness.stub, feat.detgate, ui.harness, harness.replay, ui.screen.debug, harness.synth |
