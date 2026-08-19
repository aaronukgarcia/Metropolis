BOW code: FEAT-162

# Acceptance criteria — user-agreed infrastructure expansion (propose-at-cost, ×2/×5/×10/×50) (FEAT-162)

**BOW code:** FEAT-162
**Spec refs:** `docs/planning/proposals/population-flow-and-automation.md` §2.3 (infrastructure expansion is NEVER auto; when a line/junction saturates the game PROPOSES an expansion — bigger/more roads, more/faster trains — at a stated cost; the player must acknowledge and choose an expansion rate ×2/×5/×10/×50; big infrastructure is very expensive and a core strategic choice) and §3 (contrast: utilities DO auto-expand — transport infrastructure is user-agreed BECAUSE it is strategic/expensive); `docs/planning/proposals/integrated-transport.md` §1 (upgrade path: add loops → double-track the busiest section — "this is the user-agreed expansion (FEAT-162, ×2/×5/×10/×50 at cost)"); code.json: `engine.roads` (road identity/class/upgrade), `engine.rail` (rail fleet/works), `engine.finance` (money flows).
**Date:** 2026-08-18 (BA, Programme B)
**Status:** draft-ahead — post-Baseline-One future-stage item. **No code.json entry / no assigned package** (verified: absent from `code.json`; `claude-bow.js show FEAT-162` lists no Key/Path) — the owning module must be registered before build (Escalations). Not in the ready queue.
**Package under test:** **proposed** `internal/engine/transit/` (same new `engine.transit` module as FEAT-161, of which this is the paired expansion feature) — NOT yet registered in code.json. ACs below are package-agnostic and use injected seams for every cross-module side effect.
**Standard gates:** see `README.md` — all apply; package for SG-4/SG-7 is `./internal/engine/transit/...` (to be confirmed at registration).
**Cross-ref:** `FEAT-161.md` (the paired feature — its AC-4 binding-cause report is what triggers this file's proposal); `engine.roads.md`, `engine.rail.md`, `engine.finance` (the expansion side-effect owners).

## User stories

- **US-1.** As the game, I need infrastructure expansion to be NEVER automatic — when a line/junction saturates (per `FEAT-161.md` AC-4's binding cause), I produce a PROPOSAL, not a change — so transport capacity is a decision the player makes, never a background convenience.
- **US-2.** As the player, I need each proposal to state its cost and offer an explicit expansion-rate choice (×2 / ×5 / ×10 / ×50), and nothing happens until I acknowledge — so a strategic, very-expensive build is always consent-gated.
- **US-3.** As `engine.finance`/`engine.roads`/`engine.rail`, I need the acknowledged expansion to land through the right owners' mechanisms (road upgrade for roads, more/faster trains for rail, cost settled through finance) — and code.json registers none of those edges for this feature's owning module yet.
- **US-4.** As the contrast that keeps this honest, I need transport expansion to be deliberately unlike utilities' auto-expand (per §3), so the "never auto" rule is a real, checkable property, not a comment.

## Scope

The saturation-triggered expansion PROPOSAL (never a direct mutation); the stated-cost and ×2/×5/×10/×50 rate-choice surface; the player-acknowledgement gate (no expansion without explicit consent); the expansion side-effect routing (roads/rail/finance) as injected seams pending registered edges (AC-6, Escalations). The actual road/rail upgrade mechanics and finance cost settlement are the owning modules' jobs, consumed here, not reimplemented.

## Acceptance criteria

### Functional — proposal, not auto

- **AC-1 (§2.3 expansion is NEVER auto — the negative check that defines the feature).** No tick-path code path mutates any road/rail capacity directly; a saturation condition produces a queryable PROPOSAL record (target line/junction, saturated bound, proposed expansion, stated cost) and does nothing else until acknowledged. Check: `go doc ./internal/engine/transit` shows a `Proposal` type/accessor and an `Acknowledge`/command surface; a passing test saturates a line and asserts (a) a proposal appears, and (b) the underlying capacity value is UNCHANGED by the saturation event itself — the mutation only occurs through the acknowledgement command (`grep -rn "func Test.*[Nn]everAuto\\|func Test.*[Pp]roposalOnly" internal/engine/transit/*_test.go`). **Lazy violation this rejects:** a build whose saturation branch calls a capacity-setter directly would satisfy "expansion happens" while violating the feature's defining rule — the check requires the capacity to be provably unchanged before acknowledgement.

- **AC-2 (stated cost + ×2/×5/×10/×50 rate choice — the consent surface).** Every proposal carries a stated cost (data-driven, GR#15) and the four documented expansion rates ×2/×5/×10/×50; the acknowledgement command requires an explicit chosen rate, and an acknowledgement with an unrecognised rate is rejected. Check: `go doc ./internal/engine/transit` shows the four-rate enum and the cost field on the proposal; a passing test asserts each of the four rates is an accepted choice and that an unlisted rate (e.g. ×3) is rejected with a typed error (`grep -rn "func Test.*[Ee]xpansionRate\\|func Test.*[Ii]nvalidRate" internal/engine/transit/*_test.go`).

- **AC-3 (GR#15; expansion costs from data, not Go literals).** Proposal costs (and the per-rate cost multipliers) are loaded from `data/transit.json`, keyed by expansion class (road vs rail) and rate — no player-felt cost as a Go literal. Check: `grep -n "transit.json\\|TransitData" internal/engine/transit/*.go` shows the data-load path; a passing test loads the data file and asserts the ×50 cost exceeds the ×2 cost for the same expansion class by the documented multiplier (`grep -rn "func Test.*[Ee]xpansionCost" internal/engine/transit/*_test.go`). **Lazy violation this rejects:** a hardcoded `cost = rate * 100000` literal would satisfy "costs scale" while violating GR#15 — the check requires the figures to come from the data file.

- **AC-4 (GR#21; proposal generation is deterministic).** Which proposal fires when multiple lines saturate in the same tick, and the proposal ordering, use `det.NewStream`/a documented deterministic tie-break (never map-iteration order). Check: `grep -rn "det.NewStream" internal/engine/transit/*.go` (excluding `_test.go`) shows the draw site; `grep -rn "for .* := range" internal/engine/transit/*.go` shows no map-range feeding proposal selection without a prior `sort.`; a passing test saturates multiple lines and asserts identical proposal ordering across two runs (`grep -rn "func Test.*[Pp]roposalOrder\\|func Test.*[Dd]eterminis" internal/engine/transit/*_test.go`).

### Functional — acknowledgement lands through the right owners

- **AC-5 (acknowledgement routes through injected seams — the "who owns the change" boundary).** An acknowledged expansion dispatches through injected seams (`RoadExpansionSink`, `RailExpansionSink`, `FinanceSink` — or equivalent), not through direct imports of `engine.roads`/`engine.rail`/`engine.finance`; the seams are how the real edges (AC-6) will plug in later. Check: `go doc ./internal/engine/transit` shows the seam interfaces; `grep -rn "engine/roads\\|engine/rail\\|engine/finance\\|roads\\.\\|rail\\.\\|finance\\." internal/engine/transit/*.go` (excluding `_test.go`) finds NO direct cross-package import; a passing test acknowledges an expansion and asserts the correct sink received the expansion command with the chosen rate (`grep -rn "func Test.*[Aa]cknowledgeDispatch\\|func Test.*[Ss]ink" internal/engine/transit/*_test.go`).

- **AC-6 (GR#25 — real cross-module edges, BLOCKED; edge-needed list).** The real wiring for AC-5's seams requires registered edges that DO NOT exist in code.json: (a) `engine.transit`→`engine.roads` for road upgrades (bigger/more roads); (b) `engine.transit`→`engine.rail` for rail upgrades (more/faster trains); (c) `engine.transit`→`engine.finance` for settling the stated cost. This AC records the three module pairs + the call needed; it does NOT author prose describing a call this package makes today. **Tripwire (mechanical):** `node -e "const m=require('./code.json').modules.find(x=>x.key==='engine.transit'); process.exit(m?1:0)"` must exit **0** (module still unregistered). A nonzero exit means the module has landed and AC-5's seams must be replaced with real calls before the next commit touching the package.

### Error handling

- **AC-7 (GR#7).** Acknowledging an unknown/expired proposal, or a proposal with a malformed cost, returns a registry-sourced error (new `MET-E`-range code) rather than silently proceeding with a zero-cost expansion. Check: `grep -n "MET-" internal/engine/transit/*.go` finds a registry code reference; `grep -rn "func Test.*[U]nknownProposal\\|func Test.*[Mm]alformedCost" internal/engine/transit/*_test.go`.

- **AC-8 (GR#7).** A double-acknowledgement of the same proposal is rejected (a proposal is consumed exactly once), never silently re-applied. Check: `grep -rn "func Test.*[Dd]oubleAcknowledge" internal/engine/transit/*_test.go`.

### Determinism & safety

- **AC-9 (SG-7; GR#21).** `grep -rn "time.Now\\|time.Since" internal/engine/transit/*.go` (excluding `_test.go`) returns no matches.

- **AC-10 (GR#21; determinism).** Repeated runs from identical `(worldSeed, command log)` produce byte-identical proposals, orderings, and acknowledgement effects across worker counts. Check: `grep -rn "func Test.*[Dd]eterminis" internal/engine/transit/*_test.go` finds the test.

- **AC-11.** `go test ./internal/engine/transit/... -race -count=1` passes; `grep -n "go func()" internal/engine/transit/*_test.go` finds at least one concurrency test.

### Documentation

- **AC-12.** `internal/engine/transit/doc.go` states the module key `engine.transit`, the never-auto rule (AC-1) as the feature's defining property, the four-rate consent surface (AC-2), the injected-sink boundary (AC-5), and the AC-6 edge-needed list, and states the §3 contrast (transport is user-agreed; utilities auto-expand) so a future reader does not "helpfully" auto-wire it. Check: `grep -n "engine.transit" internal/engine/transit/doc.go` and `grep -n "never auto\\|user-agreed\\|acknowledg" internal/engine/transit/doc.go` and `grep -n "BUG-058\\|blocked\\|edge" internal/engine/transit/doc.go` all match.

## Out of scope

- `engine.roads`' own road-upgrade/class-ladder mechanics — this item only dispatches the upgrade command through a seam (AC-5/AC-6).
- `engine.rail`' own fleet/rolling-stock expansion mechanics — consumed through a seam, not reimplemented.
- `engine.finance`' own cost settlement/ledger — consumed through a seam; this item only states the cost on the proposal and dispatches the settlement command.
- Utilities auto-expand (§3) — a different, deliberately-auto feature; this file's never-auto rule is transport-specific and must not be read as overriding it.
- The exact expansion costs, per-rate multipliers, and which saturated bound maps to which expansion class — balance data in `data/transit.json`, pending M2 tuning (GR#15; balance-number regime).

## Escalations

- **For the Architect/Ben — the owning module is NOT registered (GR#20/GR#25 prerequisite, shared with FEAT-161).** FEAT-162 has no code.json entry. It shares `engine.transit` (proposed) with FEAT-161; register the module + the AC-6 outbound edges and assign the package before any build prose is legal.
- **For Ben/Bill — GR#25 edge gaps (three pairs, one call each).** (a) `engine.transit`→`engine.roads` — road upgrade (bigger/more roads). (b) `engine.transit`→`engine.rail` — rail upgrade (more/faster trains). (c) `engine.transit`→`engine.finance` — settle the stated expansion cost. All three are absent from code.json today (roads' outbound is world/foundation; rail's is traffic/freight; finance has no transit consumer).
- **For Aaron (balance numbers).** Expansion costs and per-rate multipliers are placeholders under the balance-number regime; the ×2/×5/×10/×50 rate set itself is Aaron-ruled (§2.3), the cost magnitudes are not.
