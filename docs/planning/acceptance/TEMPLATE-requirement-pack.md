# Requirement pack standard (Aaron, 2026-10-30)

Every piece of future build work gets a requirement pack in `docs/planning/acceptance/<mkey-or-CODE>.md` BEFORE any developer dispatch (dev-team process + GGR65 lifecycle direction). A pack that a reviewer can show assumes something unstated gets bounced back to the lead. Sections, all mandatory:

## 1. Context and goal
BOW code(s), mkey, why this exists in one paragraph, and the northstar stage it serves (Baseline One / dogfood / convergence / depth / balance). Link prior rulings by Q-number or BOW comment date — never restate them from memory (GR#14: look them up).

## 2. Acceptance criteria
Numbered AC-1..n, each in Given/When/Then form, each **mutation-provable**: name the mutation that would make it fail (a test that cannot fail is not a criterion — verification standards memory). Each AC states its observable surface (protocol view, screen, report line, DB row), never an internal variable. Player-felt numbers are placeholders + directional tests only; Aaron owns tuning at the balance pass (balance number regime).

## 3. Non-goals
What this increment deliberately does NOT do, with the BOW code where the deferred part lives. An unnamed non-goal becomes scope creep in the dev lane.

## 4. Developer direction
- Files/packages to touch and the integration points: compose table row, API construction in simState, protocol surface, convergence-fixture regeneration for any flows change (lead port hygiene 2026-09-11).
- Constraints that bind here: determinism (GR#21 — no map-range-with-break, order-independent folds), no storage/clock reads in hot paths (BUG-602 class), units via code.json units registry (/units-lint), error codes ONLY via tools/plan/add-error.js riding in the feature commit, import-cycle TDZ rule for new TS sim modules (constants in grid.ts leaf + entry-order test).
- Scoped verification only: the files you touch (scoped.mjs + tsc / go test ./<pkg>), NEVER full suites (agent test-scope contract); 30-minute watchdog applies.
- Branch/worktree per the team shape in force; commit format `[type]: ... [mkey]` with the most specific key.

## 5. Tester watch-fors
- **Built-but-not-wired is the house defect class**: do not pass on compile+tests-green — run the binary/dev server, prove the new path is REACHED at runtime (registration grep + an observable change at the surface named in the AC).
- Prove each new regression test CAN fail (mutate, watch red, revert).
- No wall-clock bounds in CI assertions; both bounds on liveness (progress floor AND rate ceiling).
- Dual-engine traps: a webconsole change needs a converge-fixture regen check; a Go engine change needs the TS twin question answered (which side is SSOT for this domain now?).
- StrictMode double-invoke (webconsole) and once-per-load vs reused-action classes where state ceremonies are touched.
- Tester gives PASS/FAIL against the ACs ONLY and never fixes; FAIL bounces to the same developer.

## 6. Round guidance (GR#23)
What the independent destructive reviewer should attack first: the specific invariant, the specific bypass, the specific laundering risk. Attacker is never the author; verdict recorded via `node claude-bow.js destructive <code> --verdict ...` from a worktree cwd.

## 7. Done means
The single sentence the lead checks before `done`: observable X happens at surface Y with gate Z green, and the BOW item carries the verdict + refs.
