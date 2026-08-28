# NORTHSTAR — how Metropolis gets built

> Written by Bev (lead) at Aaron's direction, 2026-08-28. Everything the team does must
> contribute to this, traceably. If a piece of work can't say which section below it
> serves, it is a distraction — file it as a **P5 BOW feature** and get back on the path.
> Recorded in session memory and Vestige; this file is the canonical copy.

## 1. The North Star

**A finished, playable Metropolis**: a deterministic city-simulation game where
persistent citizens live, money moves through one ledger, the player builds and the
city responds — deep enough to be fascinating, honest enough that every number on
screen traces to the sim. "It's a game, not NASA code" (Aaron, 2026-08-14).

The full design is `docs/METROPOLIS-MASTER-v2.1.md`. The BOW is the single source of
truth for the work; this file is the single statement of *why the queue is ordered the
way it is*.

## 2. The waypoints (in order)

1. **Watchable Baseline One** — FEAT-083 / FEAT-226 (P0, nearly closed). The loop
   runs and you can watch it: citizens consume, money moves, migration responds.
   The one-money / one-world / one-view doctrine (FEAT-231, done 2026-08-28) is its
   permanent guard rail.
2. **The webconsole dogfood lane** — the React console is the fast proving ground for
   gameplay mechanics (activation gates, power, waste, density, dispatch…). Cheap to
   build, instantly watchable, journal-replayable. Mechanics prove themselves here
   first, under BA criteria and destructive rounds like everything else.
3. **Engine convergence** — the Go engine is the product. The webconsole's mock sim is
   replaced by the live Go engine feed (FEAT-1972079852 protocol adapter), and cities
   survive upgrades via hard-reset genesis replay (FEAT-1972079897, GR#27
   capture-before-wipe). Mechanics proven in the dogfood lane migrate into engine
   modules behind registered contracts (GR#20/25).
4. **Depth waves** — the M3/M4 estate (screens, services, crime, death services,
   mega-facilities, land registry, HMRC…) rides the same pipeline once the spine is
   watchable. Game code beats framework, always.
5. **The balance pass** — every player-felt number is a placeholder until Aaron's
   row-by-row approval (balance-number regime). Ships last, deliberately.

## 3. The operating loop (how every unit of work moves)

```
sit-rep → converge trunk (GR#26) → interview Aaron to clear DD/balance blockers
  → BA acceptance criteria (docs lane, parallelisable, commit docs-only)
  → Haiku dev lane builds to criteria           (Aaron 2026-08-28: dev = Haiku)
  → lead verifies ON DISK (never trust a report; run the suite yourself)
  → independent destructive round (GR#23 — attacker ≠ author)
  → commit exact paths, push same session (GR#24) → sweep to main → done-on-merge
```

Standing mechanics:
- **One webconsole code lane at a time** — all webconsole features funnel through
  `store.tsx`/`types.ts`/`data.ts`; concurrent code lanes there destroy each other.
  BA/docs/audit lanes parallelise freely.
- **Interview to unblock**: when lanes are blocked on design decisions, the lead
  compiles the DD queue and interviews Aaron in batches. Rulings are recorded on the
  BOW items the moment they land.
- **Verify-on-disk**: a "COMPLETE" report from any agent is a claim, not a fact. The
  lead greps the code, runs the suite, and checks git status before acting on it.
  (2026-08-28 example: a dev lane reported grace-period code as satisfying a ruling
  that explicitly rejected grace periods.)
- **Distractions → P5**: anything interesting-but-off-path is filed as a P5 BOW
  feature immediately (Aaron 2026-08-28). It is not worked, not polished, not
  discussed further; the queue remembers it.
- **Utilisation with named blockers**: load lanes until saturated or name the exact
  blocker (usually: awaiting Aaron ruling, or the shared-file ceiling).

## 4. Priority doctrine

- **P0 is exactly the watchable spine** — nothing else. (Re-triaged 2026-08-28:
  assumptions and protocol extensions moved out.)
- **P1** = game-first build queue + rulings that gate it.
- **P2/P3** = everything real but not spine-critical.
- **P5** = filed distractions. Parked by design, revisited only when the spine is done.

## 5. Traceability rule

Every dispatch brief, BOW item, and commit should be answerable to: *which numbered
waypoint (§2) does this serve?* BA criteria cite their item; items relate to the
spine or carry their waypoint in the description; a P5 tag is a legitimate answer
("none — parked"). The lead's sit-reps call out any work that has drifted off-path.
