# Requirement pack: WIRE engine.coastal into the composition root

Standard: `docs/planning/acceptance/TEMPLATE-requirement-pack.md`. Author: BA lane (Lane H, completed 2026-10-30). Original module criteria are in the MOD-044 acceptance doc; not restated here, this pack is the wiring increment only.

## 1. Context and goal

- **BOW:** MOD-044 (done, `engine.coastal`, `internal/engine/coastal/`). The lead files a new wiring BOW item (this lane does not write BOW).
- **Why:** coastal models small-boat arrivals, rescue, processing, hotels and integration (asylum-seeker case lifecycle). It has zero non-test importers (`code.json`: `consumers: []`), so in the running game no boat ever arrives. Surface: `coastal.New(seed uint64, cfg Config, correlationID string) (*CoastalAPI, error)`; setters `SetCitizens`, `SetServices`, `SetNews`, `SetShore(ShoreSource)`, `SetEraTier`, `SetSeasonIndex`, `SetWorldConditions`; monthly `Advance(month int64) (AdvanceResult, error)`; policy sliders `SetProcessingFunding`, `SetHousingApproach`, `SetIntegrationInvestment`.
- **Northstar stage:** depth (not Baseline One). Wire after the spine; do not displace game-first lanes.
- **Facts verified in code (2026-10-30):**
  1. **No edges to money or migration.** `code.json` registers outbound edges to citizens, services, news, foundation only. There is NO coastal -> attract and NO coastal -> finance edge. Both are needed (GR#25: register in the master plan, never ad hoc in `code.json`).
  2. **Granted cases mint citizens** via `ApplyLifeEventCommand(LifeEventBirth)` with citizen ID = case ID. Compose has its own citizen ID seam, so there is an ID-namespace collision risk, and compose's `peopleDelta` / `residentIDs` / people-conservation ledger must count coastal arrivals or the conservation invariant breaks.
  3. **Costs are unposted.** `HotelCost`, `DepartureCost`, `TotalCost` are plain int64 counters; nothing posts them to finance. FEAT-231 "one money" requires a ledger posting.
  4. **Rescue capacity depends on services ids that do not exist.** `coastal/advance.go` `rescueCapacity` reads `svc.Capacity(ServiceID("coastguard"))` and `("lifeboat")` (ids set in `data/coastal.json`). `data/services.json` contains neither id (only `data/buildings.json` has `coastguard_station` / `lifeboat_station`). Capacity falls through to 0 SILENTLY on error. This is a built-but-not-wired trap inside the module itself.
  5. `engine.attract` migration is an aggregate step with no coastal-arrivals seam today; `engine.news` is also not wired in compose; `ShoreSource` needs an adapter over `engine.world`.
- **Open edge decision (lead/Aaron choice, GR#25):** (a) **recommended:** register `engine.attract` consuming `engine.coastal` (arrivals are a migration source, one owner of inflow); or (b) a compose bridge (`feat.compositionroot` -> `engine.coastal`) that injects arrivals as a separate inflow term. The pack's ACs hold under either; the developer must not start until the lead records the choice.
- Prior rulings: FEAT-231 one-money doctrine (`onemoney_guard_test.go` in compose); verification standards memory (mutation-provable ACs).

## 2. Acceptance criteria

Surfaces: **Ledger** = `FinanceAPI.LinesByCategory` / `AccountBalance` and the `f2.finance` view; **View** = a new protocol view for coastal (suggested `f4.services` extension or `f4.coastal`; lead decides); **News** = news feed.

Scenario C: same seed, populated start city on a coastal tile, shore adapter reporting a coastline, `SetWorldConditions` raised so arrivals occur, run 24 months through `compose.Wire` ticks.

- **AC-1 (reachability).** Given `compose.Wire` with default Deps, then `RegistrationOrder()` includes a coastal monthly hook, and Subscribe to the coastal view returns arrivals, in-processing, hotel and granted counts. *Mutation:* remove the `registrationOrder` row; the order test and view test both fail.
- **AC-2 (arrivals occur and are deterministic).** Given Scenario C, then at least one arrival event is recorded, and two same-seed runs produce identical view output. *Mutation:* seed from the wall clock or `math/rand`; equality fails. Also `SetWorldConditions(0)`: arrivals zero (proves the driver is read live, not a literal).
- **AC-3 (rescue capacity is real).** Given default data, then `svc.Capacity("coastguard")` and `("lifeboat")` resolve without error (ids exist in `data/services.json`, or the config ids are repointed to existing ones) and rescue outcome for a small arrival is fully-resourced when stations are built and unresourced when none exist. *Mutation:* rename the id in `data/coastal.json`; capacity becomes 0 and the test fails (today's silent state is exactly this mutation).
- **AC-4 (arrivals counted in people conservation).** Given Scenario C with granted cases, then each granted case adds exactly one citizen to the composed city with an ID that does not collide with any compose-assigned citizen ID (no duplicate ID across the full population), `peopleDelta`/`residentIDs` include them, and the people-conservation invariant holds every tick. *Mutation:* mint with a raw case ID in the collision range; duplicate-ID assertion fails. Separately, skip the `peopleDelta` update; conservation fails.
- **AC-5 (costs hit the one-money ledger).** Given Scenario C with hotel-housed cases, then each month a balanced treasury-to-external (or approved counterparty) posting appears under a coastal ledger category (suggest `coastal.hotels`, `coastal.departure`; open `finance.Category` string), its monthly total equals the month's `HotelCost + DepartureCost` from `AdvanceResult`, and `FindConservationViolations()` is empty. *Mutation:* delete the posting (no line); post only the debit side (conservation violation); post to the wrong counterparty (balance check fails).
- **AC-6 (policy sliders move outcomes).** Given Scenario C for A (`SetProcessingFunding` high) and B (low), same seed, then A's backlog (cases awaiting decision) at month 24 is lower than B's (directional only, no numbers), and A's processing spend is higher on the Ledger. *Mutation:* make the setter a no-op; A equals B.
- **AC-7 (migration seam).** Given arrivals, then the attract/migration view for the month reflects arrivals through the chosen edge (a) or (b): total inflow for the month includes coastal arrivals exactly once. *Mutation:* feed arrivals into both attract and the compose bridge; double counting is caught.
- **AC-8 (news reachability, if news is wired by then).** Given an arrival event, then a news item is published to the feed, or, if `engine.news` is still unwired, record N/A with reason and the BOW code that wires news. *Mutation:* drop `SetNews`; item absent.
- **AC-9 (save/load round-trips coastal state).** Given Scenario C at month 12, saved and loaded, `StateDigest()` equal, including open cases and slider values. *Mutation:* omit coastal from the save participant list; digests differ.

## 3. Non-goals

- Political reaction, public opinion and deportation policy beyond what coastal already models; no new modules.
- A dedicated F-screen for coastal (none exists; protocol view only).
- TS twin: no webconsole model of coastal exists; Go is SSOT. No webconsole change and therefore no converge-fixture regeneration unless the developer touches `computeFlows`.
- Balance numbers (arrival rates, costs, backlog sizes) stay data placeholders for Aaron's balance pass.
- Wiring `engine.news` generally (tracked separately; AC-8 is conditional).

## 4. Developer direction

- **Files:** `internal/engine/compose/compose.go` (simState fields; construct after citizens, services, world, finance; add a monthly `registrationOrder` row; update `RegistrationOrder`/`BaselineOneHookCount` test literals); new `compose/coastal_wire.go`, `coastal_publish.go`, a `ShoreSource` adapter over `engine.world`, and tests; `compose/save_wire.go`; `data/services.json` (add coastguard/lifeboat service ids, or fix `data/coastal.json` ids) with the data-first rule (GR#15).
- **Citizen ID seam:** route granted-case citizen creation through the compose citizen ID allocator, or reserve a disjoint namespace documented in code; the developer states which and tests AC-4.
- **Ledger bridge:** compose-owned function posts `finance.Transaction`s; edge `feat.compositionroot` -> `engine.coastal` and the posting consumer to `engine.finance` registered in the master plan first. Run `/edge-lint` and `/codejson-audit`. Do NOT add a coastal -> finance import before the edge is registered.
- **Constraints:** GR#21 determinism (sorted iteration, month from the clock in `RunShard`, effects in `ApplyEffect`), BUG-602, units registry, error codes only via `tools/plan/add-error.js` riding in the feature commit with a pre-assigned range (parallel-lane collision class).
- **Verification (scoped only):** `go test -count=1 ./internal/engine/coastal/... ./internal/engine/compose/...` plus golangci-lint per `/ci-green`. Never the full suite; 30-minute watchdog.
- Branch from current main; commit `[feat]: ... [<wiring BOW key>]`.

## 5. Tester watch-fors

1. **Built-but-not-wired first.** Coastal package tests green proves nothing. Show non-test compose code constructs `coastal.New`, `RegistrationOrder()` contains the hook, and Scenario C driven only by `compose.Wire` ticks produces arrivals and ledger lines at the named surfaces. A fixture that calls `Advance` itself is not evidence.
2. **Silent zero capacity.** Check `data/services.json` actually contains the ids named in `data/coastal.json` (AC-3); a rescue that "works" with capacity 0 is the trap.
3. Prove each test fails under its named mutation, then revert.
4. Duplicate citizen IDs and people-conservation across a long run; one-money guard (`onemoney_guard_test.go`) and `FindConservationViolations()`.
5. Directional A/B only; no absolute numbers; no wall-clock bounds; both liveness bounds if a run-length floor is asserted.
6. Dual engine: confirm no webconsole file changed (else regen converge fixture: `cd webconsole; node --import tsx/esm test/converge-fixture-emit.mjs --write` then `go test ./internal/converge/...`). State SSOT (Go only).
7. Arrivals counted once if both an attract path and a bridge path exist.

## 6. Round guidance (GR#23)

Attacker is never the author; verdict recorded from a `.claude/worktrees/*` cwd. Attack first: (1) the citizen ID-namespace collision: mint a case whose ID equals an existing compose citizen and check for overwrite or duplicate; (2) the unposted/mis-posted costs: any path where hotel/departure cost accrues but the ledger line is absent, one-sided, or posted twice (month replay, load-then-advance); (3) money creation through negative/NaN/overflowing cost or funding slider values; (4) arrivals double counted between attract and bridge; (5) load of a save from before coastal existed; (6) silent capacity-zero masking (does the module distinguish "no stations" from "id not found"?).

## 7. Done means

In Scenario C the composed city, driven only through `compose.Wire` ticks, shows boat arrivals feeding unique, conserved citizens and balanced coastal cost lines in the one-money ledger, rescue capacity resolved from real service ids, with conservation clean, scoped tests plus `/ci-green` green, CI green after push, and the wiring BOW item carrying the destructive ACCEPT and refs.
