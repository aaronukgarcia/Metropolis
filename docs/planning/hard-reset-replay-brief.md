# Hard-Reset & Deterministic Replay — Design Brief

**Goal (Aaron, 2026-08-27):** let the dogfood adopt a *new build's engine logic* mid-game without losing progress — hard-reset onto the fresh code, then deterministically **replay the recorded player actions** to rebuild the city on the new build. "Everything is deterministic, so it should be a simple replay of keystrokes and placements to get back to where we are, but on the new build."

---

## 1. Why this is needed (and how it differs from the hot-upgrade already shipped)

This session shipped a **hot version upgrade**: on commit, the version badge updates live and the running sim keeps going, so a commit no longer resets the game mid-play. But that path deliberately keeps the **old engine module graph** running — it swaps the *displayed version*, not the *simulation logic*. debug7 proved the cost: the badge read a current version while the running sim was still the 13:08 engine (`b52f236`), ~5 commits of bug-fixes stale (missing BUG-414, the police fix, the growth-rate bump).

So there are two complementary modes:

| Mode | What it does | When |
|---|---|---|
| **Hot upgrade** (shipped) | Keep playing on the *old* engine; update version display only. No reset. | Cosmetic / you don't need the new logic yet. |
| **Hard reset + replay** (this brief) | Reload the *new* engine, replay your action log from genesis under the new rules, resume live. | You want the bug-fixes/behaviour of the new build applied to your city. |

---

## 2. What already exists — FEAT-1972079854

The webconsole already has the deterministic substrate:

- **`journal.ts`** — an ordered log of `{tick, action}` for every **state-affecting** action. UI-only actions (speed, tool, clipboard, dismissNotice) are excluded, so the log is **semantic actions, not raw mouse pixels**. `place`, `bulldoze`, tax, policy, debug rewards, and the `tick` advances themselves are recorded. Ring-buffer `JOURNAL_CAP = 50000` (placeholder).
- **Pure deterministic reducer** — "same action sequence on the same initial seed → identical final state."
- **`initialState()`** — no seed argument, no `Math.random`, no `Date.now`. The Folkestone start is **byte-identical across builds** (GR#21). Nothing to capture: every build begins from the same world.
- **`replay.ts` / Savepoints** — snapshot at tick N + journal tail + timestamp, persisted to `localStorage`, autosave 30s, boot-time recovery wired in `store.tsx`.

**The gap:** `restoreFromSavepoint` replays the journal tail *onto a SimState snapshot*. A snapshot is frozen old-rules numbers — loading it on a new build resurrects the old city, it does **not** re-derive it under new rules. Aaron's ask needs a **genesis replay**: start from `initialState()` on the new engine and re-apply the player actions.

---

## 3. The one hard truth: deterministic ≠ identical across a rules change

Replay is exact **only when the engine logic is unchanged** between builds (e.g. a UI/display-only commit). Then genesis replay is byte-identical — perfect recovery.

When the new build **changes engine rules** (the whole point of a bug-fix build), replaying the same actions produces a **different** city — *the city your actions would have produced under the corrected rules*. Example: the growth-rate fix (0.05→0.15) means the same placements grow population faster, so tick-2644 population won't match the old screenshot. This is **desirable** (you want the corrected simulation), but must be communicated, and two consequences must be handled:

1. **Divergence is expected** — show a before/after summary ("rebuilt on v0.3.0-71: population 431k vs 425k under old rules"), never claim pixel-identity.
2. **Some past actions may become invalid** under new rules (a placement the new rules reject, a building spec that was renamed/removed). Replay must apply each action **defensively**: skip-and-log an action the new engine rejects rather than aborting, and catch any hard crash (Aaron: "if it crashes then fair enough — catch it"). A replay report lists skipped/failed actions.

---

## 4. Design

### 4.1 Journal shape — sparse player actions, synthesized ticks
Today the journal stores every `tick` action, so tick 2644 ≈ 2644+ entries and the 50k ring-buffer will eventually drop genesis (fatal for genesis replay). Change the persisted replay log to store **only the sparse player actions, each stamped with its tick**; the replayer **synthesizes the tick advances between them**. A long game has thousands of ticks but only dozens–hundreds of player actions, so:
- the log shrinks by 1–2 orders of magnitude,
- genesis is never evicted (raise/remove the cap for player actions; keep the full ordered list),
- the exact interleaving is preserved (each action still lands on its recorded tick).

Keep the existing snapshot+tail savepoints for **fast same-build resume**; add the genesis action-log for **cross-build rebuild**.

### 4.2 Headless fast-forward replayer
A pure loop, no React render: from `initialState()`, for each journaled player action in order, advance ticks (dispatch `tick`) until its recorded tick, then apply the action; finally advance to the last recorded tick. The pure reducer does thousands of ticks/sec, so catching up to tick ~2644 is well under a second. Render only the final state.

### 4.3 Build stamping
Stamp the persisted log with the `appVersion` it was captured under. On load, compare to the running build:
- **same engine hash** → replay is exact; restore silently or via the fast snapshot path.
- **different** → offer "rebuild on the new build," run genesis replay, show the divergence + skipped-action report.

(Engine-logic identity is coarser than `git describe`: a docs-only commit changes the version string but not the engine. A cheap approach is a content hash of the built `sim/` bundle; a precise approach is out of scope for inc1 — treat any version change as "may differ" and let the divergence summary tell the truth.)

### 4.4 UX flow
1. New build deployed → page reloads onto new engine.
2. Boot detects a persisted action-log stamped with a different build.
3. Prompt: **"Rebuild your city on v… ?"** (Rebuild / Keep old snapshot / Start fresh).
4. On Rebuild: headless genesis replay with a progress indicator; catch crashes.
5. Show a **rebuild report**: new vs old key metrics, and any actions skipped as invalid under new rules.
6. Resume live on the new engine.

### 4.5 Guardrails
- Replay is deterministic: assert byte-identical reruns of the *same* build (a determinism self-test — reuse the existing consistency checker at the end of replay).
- Never let replay silently produce a broken city: run the consistency checks post-replay and surface failures.
- The action-log write path must be crash-safe (BUG-337-style move-then-add) so a crash mid-write can't corrupt genesis.

---

## 5. Phased increments

- **inc1 — Genesis replay core.** Sparse-action log (player actions + tick stamps), headless fast-forward replayer from `initialState()`, determinism self-test (same-build rerun byte-identical). No UI yet; covered by tests.
- **inc2 — Cross-build rebuild UX.** Build stamping, boot detection, the Rebuild/Keep/Fresh prompt, progress indicator, defensive per-action apply (skip-and-log invalid actions), crash catch, the rebuild report (before/after metrics + skipped actions).
- **inc3 — Robustness & scale.** Log compaction/persistence limits, very-long-game handling, optional mid-game snapshots as replay accelerators (replay from the last *build-independent* checkpoint that is itself genesis-replayable), and a "verify determinism" developer command.

---

## 6. Open questions for Aaron

1. **Default on a new build:** auto-rebuild, or always prompt? (Recommend prompt — divergence can surprise.)
2. **Divergence acceptance:** confirmed that a rules-change build yielding a *different* (corrected) city is the intended outcome, not a bug? (Assumed yes.)
3. **Invalid past actions:** skip-and-log (recommended) vs pause-and-ask per invalid action?
4. **Retention:** keep the full genesis action-log indefinitely in localStorage, or cap game length / offer export-to-file for very long games?
5. **Scope home:** webconsole-only (prototype surface) for now; the Go engine has its own determinism/replay story (integration engine WAL) — keep them separate until the webconsole proves the UX?

---

## 7. Risks

| Risk | Mitigation |
|---|---|
| Replay diverges silently and player trusts a wrong city | Post-replay consistency checks + explicit before/after rebuild report; never claim identity across a rules change. |
| Genesis evicted by the 50k ring buffer | Sparse-action log (inc1) removes per-tick entries; keep full ordered player-action list. |
| An old action is invalid under new rules and aborts the whole replay | Defensive per-action apply: skip-and-log, crash-catch, report. |
| localStorage size limits on a very long game | inc3 compaction / export-to-file. |
| "Determinism" quietly broken by a stray `Date.now`/`Math.random` in new engine code | GR#21 already bans them; add a replay determinism self-test as a standing guard. |

---

*Design brief 2026-08-27. Extends FEAT-1972079854. Awaiting Aaron's answers on the open questions before inc1 build.*
