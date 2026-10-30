# Requirement pack: WIRE engine.defence into the composition root

Standard: `docs/planning/acceptance/TEMPLATE-requirement-pack.md`. Author: BA lane (Lane H, completed 2026-10-30). Original module criteria are in the MOD-067 acceptance doc; not restated here.

## 1. Context and goal

- **PRIORITY: LOW.** Defence only activates at mandate populations of 100k / 500k / 1M (`data/defence.json` `populationThreshold`) and the default Baseline One city is far below that. Do not displace game-first lanes. The one cheap, valuable part is the grants credit that makes the fiscal Sankey stop lying (AC-2).
- **BOW:** MOD-067 (done, `engine.defence`, `internal/engine/defence/`). The lead files a new wiring BOW item (this lane does not write BOW).
- **Why:** defence models central-government grant bids, formula support for low-tax-capacity cities, defence-mandate events (accept/refuse), and facilities that carry payroll and procurement. It has zero non-test importers, so no grant ever arrives and no mandate ever fires. Surface: `defence.New(cfg Config, worldSeed uint64, correlationID string) (*DefenceAPI, error)`; setters `SetBuild`, `SetFinance`, `SetCitizens`, `SetPlanningQuality`; commands `BidForGrant(GrantBid)`, `FormulaSupport(taxCapacity)`, `PendingMandates(population)`, `RespondToMandate(MandateResponse)`.
- **Northstar stage:** depth (late-game scale pressure). Baseline One does not need it.
- **Facts verified in code (2026-10-30):**
  1. `BidForGrant` credits the finance category `"grants"` (`defence/grants.go` `grantCategory`; a new open category it introduces) via `postGrant` on a win. A lost bid posts nothing. A refusal in effect gates every bid with `ErrGrantRefused`.
  2. The fiscal Sankey has a `NodeGrants` ("Central grants", source) that is a **Provisional zero node** today (`fiscal/topology.go`, `fiscal/doc.go`: engine.defence "does not exist yet"). Once grants lines exist the node must carry the real amount and drop the Provisional flag, or the Sankey lies. This pairs with `wire-engine.fiscal.md`; coordinate through the lead.
  3. `PendingMandates(population)` returns mandates whose threshold is reached and not yet answered, in data-file order; `RespondToMandate` is atomic and one-shot (`ErrMandateAlreadyResponded`); refusal is priced (no build, reputation penalty, grant access revoked).
  4. `RespondToMandate` accept path sites a facility via `engine.build`, credits compensation via `engine.finance`, and settles personnel as real citizen records via `engine.citizens`, so citizens created here must be counted by compose's people-conservation ledger and use the compose citizen ID seam (same class as the coastal ID-collision risk).
  5. Compose currently has no population-threshold trigger, no player command for bids or mandate responses, and no view.
- **Dormancy rule:** no default city is expected to reach 100k. State this in the PR. Mandate ACs therefore use a scripted population (test harness overrides the population input to the mandate query), clearly labelled as a test fixture, not a game feature.
- Prior rulings: FEAT-231 one-money doctrine; verification standards memory.

## 2. Acceptance criteria

Surfaces: **Ledger** = `FinanceAPI.LinesByCategory("grants")` and the `f2.finance` view; **Sankey** = the fiscal flows view; **View** = a new protocol view for defence (suggested `f4.defence`; lead decides).

- **AC-1 (reachability).** Given `compose.Wire` with default Deps, then a defence monthly hook is in `RegistrationOrder()` and Subscribe to the defence view returns pot list, formula support and pending mandates (empty at default population). *Mutation:* remove the `registrationOrder` row; order test and view test fail.
- **AC-2 (a won bid is a balanced credit; the Sankey stops lying).** Given a scripted winning bid (pot chosen with win probability forced to 1 via config or seeded stream, documented), when the bid is placed through the composed command path, then a balanced treasury-credit line appears under `grants` for exactly `AwardMicropounds`, `TotalMoneyInCirculation` rises by exactly that award and `FindConservationViolations()` is empty, and the fiscal Sankey `NodeGrants` has amount equal to the grants line total and its Provisional flag cleared. *Mutation:* credit one side only (conservation violation); skip the fiscal hookup (node remains zero/Provisional); credit a different amount than the award.
- **AC-3 (a lost bid posts nothing).** Given a bid forced to lose, then no `grants` line is added and money in circulation is unchanged. *Mutation:* post the award regardless of the draw; the test fails.
- **AC-4 (mandate threshold crossing is observable).** Given a scripted population below 100000, then pending mandates are empty; given a scripted population at 100000, then the 100k mandate appears in the defence view, and at 500000 the next is added, in data-file order, without the 100k one disappearing until answered. *Mutation:* hardcode a population ladder in Go instead of reading `data/defence.json`; changing the data threshold in the test fixture would not move the result, so the test (which edits the threshold) fails.
- **AC-5 (mandate response is atomic and one-shot).** Given a pending mandate, when accepted via the composed command, then the facility appears in the build state, compensation appears as a balanced ledger credit, and its personnel exist as citizen records with IDs unique across the population, with people-conservation holding; a second response is refused with the registry error and changes nothing. *Mutation:* allow a second response; or create personnel with colliding IDs; or skip the people-conservation update.
- **AC-6 (refusal gates bids).** Given a refused mandate, when a grant bid is placed, then it is refused with the refusal-specific registry code, no `grants` line is posted, and the view shows grant access revoked. *Mutation:* ignore `refusedMandates` in `BidForGrant`; the bid proceeds.
- **AC-7 (facility payroll and procurement reach the ledger).** Given an accepted mandate facility, then each month its payroll and procurement appear as balanced ledger lines under the existing wages/opex categories (or a documented defence category), conservation clean. If this is found to be unposted today, the developer files it as a defect and closes it in this increment. *Mutation:* delete the posting; the lines vanish.
- **AC-8 (save/load).** Given a state with a mandate answered and a bid sequence advanced, saved and loaded, `StateDigest()` equal and the next bid draw identical to the uninterrupted run (the bid stream is keyed off `bidSeq`). *Mutation:* omit `bidSeq` from the save; the post-load draw diverges.

## 3. Non-goals

- Reaching 100k in a real game; scaling or performance work (BUG-666 rowOf index and the 100m finish line are separate).
- An F-screen for defence (none exists; protocol view only).
- TS twin: none exists for defence; Go is SSOT. No webconsole change and no converge-fixture regeneration unless `computeFlows` is touched (it should not be; the Sankey grants node is on the Go fiscal side).
- Balance numbers (award sizes, win probabilities, compensation, payroll) stay data placeholders for Aaron's balance pass.
- Speculative bid cost (explicitly out of scope in the module at Baseline One).

## 4. Developer direction

- **Files:** `internal/engine/compose/compose.go` (simState; construct after build, finance, citizens; monthly `registrationOrder` row; update `RegistrationOrder`/`BaselineOneHookCount` test literals); new `compose/defence_wire.go`, `defence_publish.go`, tests; protocol commands for bid and mandate response in `handleGameplay` (lead decides Kind names; check for an existing Kind before adding); `compose/save_wire.go`; fiscal grants node hookup per `wire-engine.fiscal.md`.
- **Ledger edge:** defence already depends on finance (`SetFinance`) so no new import; if the edge is not registered in `code.json`, register it in the master plan first (GR#25). Run `/edge-lint` and `/codejson-audit`.
- **Constraints:** GR#21 determinism (`det.NewStream` per draw is already in place; sorted iteration only), BUG-602, units registry, error codes only via `tools/plan/add-error.js` riding in the feature commit with a pre-assigned range.
- **Verification (scoped only):** `go test -count=1 ./internal/engine/defence/... ./internal/engine/compose/...` plus golangci-lint per `/ci-green`. Never the full suite; 30-minute watchdog.
- Branch from current main; commit `[feat]: ... [<wiring BOW key>]`.

## 5. Tester watch-fors

1. **Built-but-not-wired first.** Package tests green proves nothing. Show non-test compose code constructs `defence.New`, `RegistrationOrder()` contains the hook, and a bid placed through the real command path produces a `grants` line visible at the Ledger view and in the Sankey node. A test that calls `BidForGrant` directly is not evidence.
2. Prove each test fails under its named mutation, then revert.
3. Scripted population is a labelled fixture: reject any test that sets population through a production path that real play could not take, and reject any "pass" that merely shows the module is dormant at default population.
4. Sankey honesty: grants node amount must equal the ledger sum, and must not remain Provisional once lines exist (and must stay Provisional/zero when none exist).
5. One-money guard (`onemoney_guard_test.go`) and `FindConservationViolations()` clean; people-conservation clean after mandate acceptance.
6. Dual engine: confirm no webconsole file changed (else regen converge fixture: `cd webconsole; node --import tsx/esm test/converge-fixture-emit.mjs --write` then `go test ./internal/converge/...`). State SSOT (Go only).
7. No wall-clock bounds; directional assertions only for probabilities.

## 6. Round guidance (GR#23)

Attacker is never the author; verdict recorded from a `.claude/worktrees/*` cwd. Attack first: (1) an unbalanced grant credit: any path where the `grants` credit side and the offsetting debit differ, negative or overflowing `AwardMicropounds` / `MatchFunding`, or a bid replayed after load that credits twice; (2) the Sankey lying: grants node shown non-zero with no ledger lines, or zero with lines, or still Provisional; (3) mandate double-response races (first response silently overwritten); (4) personnel citizen ID collision and people-conservation after acceptance; (5) refusal bypass through a path that calls the grant credit without `BidForGrant` (laundering).

## 7. Done means

In a scripted-population harness driven only through `compose.Wire` and the real command path, a won grant bid appears as a balanced `grants` credit that turns the fiscal Sankey grants node non-Provisional with the matching amount, a mandate threshold crossing and its one-shot response are observable in the defence view, refusal gates bids, conservation is clean, scoped tests plus `/ci-green` green and CI green after push, and the wiring BOW item carries the destructive ACCEPT and refs.
