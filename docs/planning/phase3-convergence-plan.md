# FEAT-1972079936 Phase 3 — Engine Convergence Plan

> **Status:** planning (Bev, 2026-08-31), grounded in three read-only investigations of the
> live codebase. This document reframes Phase 3's core premise based on what the code actually
> is, and proposes a workable path. **It needs Aaron's decisions before any code** (see §7).

## 0. TL;DR — the honest reframe

Aaron's Phase 3 premise was: *"hand off one engine/domain at a time; run the same journal
through the local TS mock AND the live Go engine; **byte-identical output proves parity** before
flipping the domain live."*

**Investigation verdict: "byte-identical" is the wrong bar and cannot be met — and, more
importantly, the thing blocking convergence is not the webconsole, it's the Go engine.** Three
findings drive this:

1. **The webconsole adapter half is READY.** The Go-engine path already exists end-to-end
   (`protocolClient.ts` connects to metroserve, subscribes to `f2.finance`, decodes the
   balance-sheet patch) — it just dead-ends in a read-only dev badge instead of the store. The
   flip seam is small and identified.
2. **The TS sim and Go engine are DIFFERENT MODELS, by deliberate design, not two encodings of
   one model.** They compute different quantities, by different formulas, at different
   granularity, in different units — and `wire.ts` explicitly mandates "independent
   reimplementation, never an import of Go source." Byte-identical would require them to be the
   same program; they are architecturally the opposite.
3. **The Go engine's domain drivers are still STUBS.** The finance compose hook posts toy values
   (`monthlyWages = 1 pound`); it does not yet produce authoritative numbers. **You cannot
   converge onto an engine that is not yet authoritative.**

**Therefore Phase 3 is not "prove the Go engine matches the TS sim." It is: (a) make the Go
engine's domains actually authoritative [upstream of the webconsole entirely — this is FEAT-083
Baseline-One work], (b) build a per-domain *semantic* parity harness [not byte-identical], (c)
flip each domain's display source from TS to Go once its Go model is real and passes its parity
contract, keeping the TS path as a reversible fallback.** Convergence is a **model replacement**:
when a domain flips, the game's numbers for it *change* (Go supersedes the TS placeholder), and
that is expected — the gate proves the change is *correct/reasonable*, not *identical*.

---

## 1. What the three investigations found (condensed, with evidence)

### 1a. Adapter seam — READY, quarantined
- `protocolClient.ts` is a complete WS/JSON-RPC client (handshake, subscribe, commands, deltas,
  schema-version guard). Its ONLY consumer is `LiveEngineBadge.tsx` (localStorage-flagged, default
  OFF), which renders Go's finance `netWorth` into a throwaway badge and **never touches the store**.
- The finance panel (`RightDock.tsx`) reads solely from `state.lastFlows`, written unconditionally
  by the TS `computeFlows` (`engine.ts:438/1286`).
- **Flip seam = the store-side writer of `SimState.lastFlows`**, gated by a per-domain flag, with
  `useSim()`→`state.lastFlows` as the stable interface RightDock keeps reading. The Go half exists;
  only the store-writer swap + A/B arbitration is missing.
- Reversibility is designed-for at the transport layer (auto-fallback on refusal) but the store
  arbitration ("run mock as shadow, display Go, revert on divergence") is not implemented.

### 1b. Finance model — DIVERGENT paradigms, Go driver is a stub
- **TS:** scalar `funds`; taxes on *zone/building counts* (Council/Business/Freight/Office);
  per-*tick*; ~15 bespoke lines (tourism, grid-export, recycling, commuter, policy multipliers);
  dimensionless *placeholder* balance-numbers (opening `funds=10_000_000`).
- **Go:** 6-account *double-entry ledger*; taxes on *economic flows* (income/sales/corp);
  per-*month*; int64 *micropounds*; a read surface of Treasury/Reserves/Debt/NetWorth.
- Go has **no representation** for freight/office/commuter/tourism/grid-export/recycling revenue,
  per-zone upkeep buckets, or the policy multipliers the TS view displays. Wages are *opposite-
  signed* (TS cost vs Go internal transfer). Cadence differs 30× (tick vs month). Units mismatch
  (BUG-355 class). **The Go finance compose hook is a toy stub.**
- Both finance paths are RNG-free and deterministic (the one clean agreement).

### 1c. Determinism/parity — byte-identical impossible; the obstacle is model divergence
- **Numerics:** TS = IEEE-754 doubles rounded to whole pounds; Go = int64 micropounds truncating.
  They round differently at the boundary. No shared numeric model exists.
- **RNG paradigm:** TS has **no RNG** — births/deaths are aggregate float fractions
  (`population * 0.0008`). Go uses **per-citizen Monte-Carlo hazard draws** (Philox `det.Stream`,
  `stream.Float64() < hazard` per citizen). Aggregate-float vs per-agent-stochastic **can never
  match tick-by-tick under any seeding.**
- **Missing infra (load-bearing):** no shared canonical journal drives both sims. TS journals
  internal reducer `Action`s; the Go replay harness (`harness.replay.EnginePlayer`) replays
  `protocol.Command`s. **Two different command vocabularies — a bridge must be built.** The persist
  journal already speaks `protocol.Command`.

---

## 2. The revised parity model — a per-domain contract, three tiers

Retire "byte-identical proves parity." Replace with a **per-domain parity contract** that names,
for each domain, WHICH scalars are compared and to WHAT bar:

- **Tier A — exact integer equality** (after one agreed unit + rounding convention). For
  integer-modeled, deterministic, non-stochastic quantities (treasury balance, counts). This is
  the strongest gate and the target for money once units are reconciled.
- **Tier B — bounded tolerance.** For genuinely continuous/aggregate quantities where rounding
  order legitimately differs. A *stated* epsilon, justified per field.
- **Tier C — distributional agreement over a window.** For per-agent stochastic domains
  (population, births/deaths, migration, households). Tick-level parity is *mathematically
  precluded*; gate instead on aggregate trajectory within a band over N ticks (e.g. mean
  population within X% over a month). These domains are flipped on distribution, never on match.

Each domain's flip is gated by ITS tier's contract, defined explicitly before the flip.

---

## 3. The two hard prerequisites (both upstream of any flip)

### P1 — The Go engine domains must become authoritative (NOT the webconsole's problem)
This is the real critical path. Today the compose hooks are stubs. Before finance (or any domain)
can be flipped, the Go engine must actually produce that domain's real numbers. **This is
FEAT-083 "Baseline One" / composition-root de-stubbing work** — the "built but not wired" theme.
Convergence rides on it. Sequencing Phase 3 domains = sequencing which Go domains get made real.

### P2 — The canonical-journal A/B harness (the missing bridge)
Build a harness that drives BOTH sims from ONE journal and compares per the domain contract:
- **Bridge:** TS reducer `Action` ↔ `protocol.Command` (the persist journal already speaks
  `protocol.Command`; the Go `harness.replay.EnginePlayer` already replays them). Build the TS
  side of the bridge so a single recorded `protocol.Command` sequence replays through the TS sim
  too — OR translate the existing TS `Action` journal into `protocol.Command`s.
- **Comparator:** a per-domain function taking (TS domain scalars, Go domain scalars) → pass/fail
  under that domain's tier contract, with a readable diff report (the before/after GR#27 wants).
- **Harness output:** for a given journal, "domain D: TS vs Go — {exact match | within tol |
  distribution OK | DIVERGES: <fields>}." This IS the flip gate and the regression gate.

---

## 4. The flip mechanism (per domain, once P1+P2 hold for it)

1. In the store, add a per-domain source flag (extend `liveEngineFlag.ts` from one global bool to
   a per-domain map — `metropolis.liveEngine.finance`, etc.).
2. When a domain's flag is on: the store instantiates/uses `ProtocolClient`, subscribes to that
   domain's view (`f2.finance` exists), decodes the patch, and writes the domain's slice of state
   (e.g. `lastFlows`) FROM the Go patch instead of from `computeFlows`.
3. **Keep the TS computation running as a shadow** — for the A/B comparator AND as the reversible
   fallback. On transport refusal, schema mismatch, or a live divergence beyond the domain's
   contract, auto-revert to the TS source and surface it (the arbitration `protocolClient.ts`'s
   header already anticipates but the store doesn't yet implement).
4. The view component (RightDock etc.) is UNCHANGED — it reads `useSim()`→state as always.

Each flip is one small, independently reversible store change behind a flag.

---

## 5. Domain sequencing — finance is NOT the right first flip

Both the finance and determinism investigations agree: **finance is one of the HARDER domains**
(ledger-vs-scalar, flow-tax-vs-count-tax, micropound-vs-placeholder, month-vs-tick, and a stub
driver). Aaron chose it first for product reasons, but it should be budgeted as a *model
replacement*, not a parity-match.

**Recommended first target instead: prove the MACHINERY on the cheapest possible domain**, then do
finance as the first *real* domain once its Go model is built:
- **Increment 3.0 (machinery, no game-domain risk):** build P2 (the A/B harness + bridge) and
  exercise it on a trivially-shared, integer, non-stochastic quantity that BOTH sides already
  compute identically or near-identically (e.g. tick count / clock, or a simple building count) —
  Tier A. This proves the bridge, the comparator, the store flip seam, and reversibility with zero
  model-reconciliation risk.
- **Then finance** as the first substantive domain — but only after P1 makes the Go finance hook
  real and a units/rounding convention is agreed (the BUG-355 decision). Its contract is Tier A on
  treasury balance (exact, post-rounding) + Tier B on flow line-items.
- **Defer all per-agent-stochastic domains** (population/migration/households) to Tier C,
  distribution-gated, late — they can never tick-match and should convince via trajectory.
- Roads/services/mechanical domains slot by how integer-modeled and non-stochastic they are.

**Ordering principle:** sequence by (Go-model-readiness × integer-determinism × low-stochasticity),
NOT by product priority. The cheapest-to-prove domain goes first to validate the machinery.

---

## 6. Proposed increment breakdown

- **inc3.0 — A/B harness + bridge (P2).** TS `Action`↔`protocol.Command` bridge; per-domain
  comparator with tiered contracts; a report. Proven on a trivial Tier-A quantity. No game-domain
  flip yet. (Webconsole + Go test infra; touches the live webconsole test surface — coordinate
  with Aaron's dogfooding.)
- **inc3.1 — store flip seam + reversibility.** Per-domain source flag (extend `liveEngineFlag`),
  store arbitration (Go-source writer + TS shadow + auto-revert), proven on the trivial domain.
- **inc3.2 — first real domain (finance), PENDING P1.** Blocked until the Go finance hook is
  de-stubbed (FEAT-083) and the units/rounding convention is ruled. Then: finance parity contract
  (Tier A treasury + Tier B lines), flip behind the flag, A/B-gated.
- **inc3.3+ — subsequent domains**, each: make Go domain real (P1) → define contract → flip.
  Stochastic domains land last, Tier C.
- **Phase 3 done when** the browser runs zero sim for the converged domains and the Go engine (still
  local) is authoritative for them. THEN Phase 4 hosts that authoritative engine on Azure.

---

## 7. Decisions Aaron must make before code

1. **Accept the reframe?** Convergence = model *replacement* gated by *semantic* per-domain parity
   (not byte-identical). When a domain flips, its numbers change to the Go engine's — OK?
2. **The critical-path truth:** Phase 3 real convergence is blocked on the Go engine becoming
   authoritative (FEAT-083 de-stubbing). Do we (a) drive FEAT-083 first and treat Phase 3 as its
   downstream consumer, or (b) build the Phase-3 machinery (inc3.0/3.1) now in parallel while the
   Go domains are still stubs, ready to flip domains as they become real?
3. **Units/rounding convention** (BUG-355): what is the agreed TS-pounds ↔ Go-micropounds mapping
   and rounding rule for the finance parity contract? (This is a balance-regime decision — placeholder
   proposal: display in whole pounds, Go micropounds ÷ 1,000,000 truncated, exact-integer compare.)
4. **First domain:** accept "prove machinery on a trivial domain first, finance as first real
   domain" — or insist finance is literally first (accepting the larger model-replacement cost)?
5. **Stochastic domains:** accept that population/migration/households can only be distribution-
   gated (Tier C), never tick-matched?

---

## 8. Risk register

- **R1 (highest):** Phase 3 is gated on FEAT-083 (Go engine authoritative). If the Go domains stay
  stubs, there is nothing real to converge onto — only machinery. Mitigate by making P1 explicit
  per domain.
- **R2:** the Action↔Command bridge may reveal the two sims can't even be driven from one journal
  without a translation layer per command kind — scope inc3.0 to discover this early on a trivial domain.
- **R3:** per-agent stochastic domains will *look* broken to a byte-parity expectation; the Tier-C
  distributional contract must be socialised so a "divergence" there isn't misread as a bug.
- **R4:** flipping a domain live changes the player-visible numbers (model replacement) — needs the
  GR#27-style before/after report so the change is legible, not alarming.
- **R5:** all of this touches the live-dogfood webconsole — inc3.0/3.1 test/store changes must be
  coordinated so they don't disrupt Aaron's running game (the 714k-city lesson).
