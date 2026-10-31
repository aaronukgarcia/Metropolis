# Phase 3 population A/B report, 2026-10-30 (report-only)

BOW: FEAT-1972079936. Pack: `docs/planning/acceptance/FEAT-1972079936-phase3-next-domain.md`.
Reference = Go `engine.citizens` (driven by `internal/converge/population_domain.go`). Candidate = TS reducer (`webconsole/src/sim/engine.ts`), trajectory emitted by `webconsole/test/converge-fixture-emit-population.mjs` into `internal/converge/testdata/population-webconsole-v1.json`. Nothing flips; neither model was tuned.

## Build strings (manual stamp, BUG-1097 unbuilt)

- Go: `fffd464` (main at the time of the run, tree dirty with this increment), go1.25.6 windows/amd64.
- TS: webconsole app version as of the same commit (no version file; derived from `git describe`), Node v25.3.0 running `src/sim/engine.ts` directly.
- Manual stamping; BUG-1097 (Q100145 automatic stamping) is still unbuilt.

## Journal and scope

`webconsole/test/converge-fixtures/converge-population-actions.json`: 2,200 seeded citizens (1,200 aged 28, 400 aged 8, 300 aged 55, 300 aged 82), 36 monthly samples (1 TS tick = 1 Go day-tick, 30 per month). Natural change only. On the TS side migration is held at zero: after every reducer tick the emitter writes `seed + sum(births - deaths)` back into `state.population`, discarding the reducer's own move-in/out result. TS has no age structure, so only the headcount is shared; the Go age mix is a Go-side input.

Contract: `population`, `births`, `deaths` = TierDistribution (Window 12 samples, BandPct 0.25, placeholders for Aaron's balance pass); `seed_headcount` = TierExact.

## Verdict per field

| Field | Verdict | Cause |
|---|---|---|
| seed_headcount | PASS (exact, 2200 = 2200) | same journal on both sides |
| births | DIVERGES (every evaluated tick, 25 of 25) | model difference: TS `Math.round(pop * 0.0008)` per tick = 2/tick = 60/month at this size; Go draws fertility per couple, about 4 to 6/month. The ratio is about 10x to 15x |
| deaths | DIVERGES (every evaluated tick) | model difference: TS `Math.round(pop * 0.0005)` = 1/tick = 30/month rising to 53/month as population grows (Math.round step at 1.5); Go mortality is a per-citizen hazard with a monthly death-queue release, 0 to 2/month for this age mix |
| population | DIVERGES from tick 870 (evaluated ticks 360 to 840 sit inside the 25% band; 8 of 25 evaluated ticks diverge) | consequence of births and deaths: TS net +30/month rising, Go net about +4/month. Final: TS 2,994 vs Go 2,333 (trailing means) |

Expected, not a bug: TS rates are population-proportional floats applied as `Math.round` per tick (a systematic rounding bias at small N), Go acts per citizen. TS rates are labelled balance placeholders in `engine.ts`. No bug found on either side. Do not tune either toward the other; a flip needs Aaron's per-domain ruling (plan risk R4).

## Reproduce

`go test -count=1 -run Population -v ./internal/converge/` prints the Go per-tick table (non-zero births and deaths from the engine) and the full divergence list. `node webconsole/test/converge-fixture-emit-population.mjs --write` regenerates the TS fixture (the lead's act when `engine.ts` changes).
