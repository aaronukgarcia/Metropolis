---
description: Audit that every unit of measurement in use is registered in code.json's units section
allowed-tools: Bash(node:*), Read
---

# /units-lint — enforce the units registry (code.json `units`)

Every unit of measurement used anywhere in the codebase must be described in
`code.json`'s top-level `units` section (sourced from
`docs/planning/master-plan-v2.1.json` → `tools/plan/generate.js`, like
`conventions` — GR#3). A bare magnitude with no registered unit is exactly how
BUG-355's `initialTreasury` (micropounds) collided with `finance.LandPrice`
(whole pounds) and erased a demolish compensation.

## Execution

```bash
node tools/plan/units-lint.js
```

## Findings handled

- **[UNITS-LINT-001] Unregistered unit:** a unit token in Go source or
  `data/*.json` (a `Micropounds`/`PerMille`/`PerDay`/`Tonnes`/`Litres`/`kWh`…
  spelling, or a `µ£`/`‰`/`m³` symbol) resolves to a dimension with no
  registered unit. Fix: add the unit to `master-plan-v2.1.json`'s `units`
  array, then `node tools/plan/generate.js`.
- **[UNITS-LINT-002] Stale definition:** a registered unit's `definedAt`
  (`path:line`) no longer resolves — the constant/type moved or was renamed.
  Fix: correct the `definedAt` pointer in the master plan, regenerate.

`units-lint` never edits code.json, the master plan, or Go source — it reports
and names the fix route. Register missing units before relying on the number.

## Scope (documented)

The registry covers **units of measurement**: (1) physical dimensions and
their scales (money/mass/volume/energy/power/length/area/time/speed/noise),
(2) fixed-point ratio units (per-mille/basis-point/percent), and (3) concrete
countable entities used as the denominator of a money rate (cost/wage/subsidy/
price/rate/grant/award/penalty per entity — case, staff, place, offender,
engineer-day, tile, milestone, detective, vermin, …) plus count×time labour
compounds.

It deliberately excludes dimensionless game-mechanic scores (`points`,
`attainment/research/prestige points`, `weight`, `fraction`, `rate`,
`probability/month`, `-draw units`) and per-METRIC (continuous-quantity)
denominators (`per-condition`, `per-contamination`, `per-stress`,
`per-deprivation`, `per-novelty`, `per-pressure`, `per-wear-point`,
`per-exposure`, `per-money`, `per-funding`). Those carry no scale to mismatch
(BUG-355); only a concrete entity or a physical unit can.
