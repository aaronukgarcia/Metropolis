# Proposal: Game Speed (production) + Dev Fast-Time (testing)

**Author:** Bev, 2026-08-18 · **Status:** Aaron-ruled (this session); BOW items filed for the dev team to build. Design grounded in the clock research of the same date (cites below are real file:line).

## The determinism pivot (why this split exists)

The clock stores only `tick` (`internal/engine/core/clock.go`); month = `tick / DailyTicksPerMonth` (30, fixed). Two categories of "faster":

- **Pacing** — how fast ticks are *called* from outside the engine. The loop runs flat-out today; nothing paces it (`AdvanceTicks(n)` is called explicitly, no sleep/ticker anywhere in `cmd/`). Changing pacing is **provably determinism-safe (GR#21)**: same seed + same tick count = byte-identical city, watched faster.
- **Sim-math** — how much sim-time a tick represents, or which phase group a hook runs on. Changing this changes the actual trajectory, so it must be a **global constant identical across all seeds**, never a silent per-session toggle — UNLESS it is debug-gated and its config is treated as part of the seed regime (see Dev Fast-Build).

## A. Production game speed — pause / normal / fast / fastest

**Already half-built:** `Speed` enum (1x/2x/4x/8xDebug, clock.go:46-56), `KindSetSpeed` protocol command (`internal/protocol/commands.go`), pause — all wired. `Speed` is currently queryable-only ("affects nothing in-engine yet", clock.go:39-45). **Missing:** the wall-clock *tick-pump* — a real-time driver in the UI/transport that reads `Speed`/`TicksPerRealSecond()` and calls `AdvanceTicks(1)` on a cadence.

**Aaron's ruling (2026-08-18):** ship **pause / 1x / 2x / 4x / 8x**, with **8x promoted OUT of debug-gating** as production "fastest" (today `Speed8xDebug` is default-deny via `Engine.speed8xGate`, MET-E015). Mapping: normal=1x, fast=2x–4x, fastest=8x. Pure pacing — determinism-safe, no balance impact. `secondsPerMonthAt1x` (data/pacing.json, default 480 = ~8 real min/month) sets the 1x real-time rate.
→ **FEAT (game-speed pacing driver + 8x ungate).**

## B. BUG-268 — build cadence fix (the real "builds are slow")

Not a speed issue. The build hook is registered on `core.PhaseLandValueDecay` (monthly group, `compose.go:151`) but `BuildAPI.Tick` elapses only 1 day of lead per call (`build.go:618`, `daysPerTick=1`), while lead times are day-denominated (`baseLeadTimeDays`). So a 45-day dwelling takes 45 **months**.

**Aaron's ruling:** **move the build hook to the daily phase group** so 1 sim-day elapses per day — day-denominated lead times then behave exactly as the data says. Global sim-math change (deterministic, applies to production too). → **BUG-268** (approach recorded).

## C. Dev fast-time for testing (debug-gated)

Aaron wants layered, opt-in speedups for pre-production testing. Four levers:

1. **Headless flat-out** — already the case; CI/long-horizon runs are CPU-bound. No work.
2. **Dev watch-speed ladder (>8x)** — extend the devmode-gated speed ladder (16x / 32x / uncapped) purely for *watching* months fly by in the TUI. Pure pacing, determinism-safe, changes **zero** numbers. → **FEAT.**
3. **Dev per-class fast-build override (Aaron's key ask)** — with **debug mode ON as a hard prerequisite**, allow enabling "fast build" **per building type / class**, individually. Examples Aaron gave: all *dwellings* build in 1/10th time; a *CERN* or *Heathrow*-class mega-facility builds in ~10 weeks instead of ~10 years. Mechanism: a debug-only per-class lead-time scale (e.g. `fastBuild[className] = factor`), applied to `effectiveLeadTime`. **This changes sim numbers**, so:
   - Hard-gated behind `debug.State` (feat.devmode) — impossible to reach in a production build.
   - Per-class toggle, default off; enabling a class scales only that class.
   - The active fast-build config is part of the **seed regime** — a run is still deterministic (same seed + same fast-build config = same city), but a fast-build city is **NOT comparable to production balance** and must be **flagged dev-tainted** in saves/telemetry so no balance decision is ever drawn from one.
   - **Never** on during determinism-gate or perf CI or the balance pass. → **FEAT.**
4. **Global dev duration-scale** — a single blunt "all lead times × factor" is subsumed by (3) as the special case "every class same factor"; build (3) and this falls out for free.

## Determinism guard-rails (all of C.3)

- Debug gate is fail-closed (mirror `Speed8xGate`/MET-E015 pattern).
- Config surfaced in any save header + dev-mode console as an explicit "DEV FAST-BUILD ACTIVE: <classes>" banner.
- Determinism-gate and perf CI assert fast-build is OFF (a gate that could silently run tainted is itself a defect — cf. BUG-071 lineage).

## Build order (suggested)

BUG-268 first (makes production watchable at all), then A (production speeds — the tick-pump is the one genuinely-missing production piece), then C.2 (cheap, safe watch ladder), then C.3 (the richest, needs the debug-gate + taint-flag discipline). None blocks the current commit sweep.
