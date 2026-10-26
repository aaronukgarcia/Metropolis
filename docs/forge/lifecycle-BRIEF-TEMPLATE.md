# Lifecycle dispatch brief (template, SAFE-121, 2026-10-18)

Fill every line; delete none. Spec: `dev-lifecycle-PROPOSAL.md` sections 6a, 6b and 7. ROLE, ROUND and BUDGET are read by the GGR58 dispatch gate, each on its own line (`ROUND: <n>` alone on a line, or the gate refuses the brief).

Lifecycle role to GGR58 `ROLE:` line (the gate knows only developer, tester, hostile, hostile-guards, qa, integration):

| Lifecycle role | ROLE line | Model | MODEL-REASON needed |
|---|---|---|---|
| Developer | developer | Sonnet; Opus from round 2 (the gate requires it) | no |
| T1 quality, clerical | qa | Haiku or Sonnet | no |
| T2 registry, T3 functional | tester | Sonnet | no |
| T4 security | hostile-guards | Opus | no |
| AC Reviewer, Verdict | hostile | Opus | yes: "lifecycle <role> is T3 (proposal 6a)" |

```
ROLE: <developer|tester|hostile|hostile-guards|qa|integration>
ROUND: <n>
TIER: <T1|T2|T3>
BUDGET: <n>m / <k>k tokens / status at half / minimum: <the smallest deliverable that still ends the run usefully>
MODEL-REASON: <only when the tier's model differs from GGR58's role table, e.g. "lifecycle verdict is T3, proposal 6a">

TASK <BOW code>: <goal and constraints in one paragraph>
INPUTS: repo <path>, base <full SHA>; read <files>; prior evidence <path or none>
OWNED PATHS (<project temp>/lifecycle/<BOW>/owned.txt):
  <path>
  <dir>/
CHECKS: <exact commands that must pass>
STOP WHEN: checks pass | half the budget is spent without the minimum | a decision only Aaron can make (report it, do not guess)

RULES: work only in the owned paths; read the owned files, their callers and their tests, do not survey the repo;
stage explicit paths only; no AI trailer in commits (GGR64); never touch .env, keys or tokens; never push; never skip hooks (the no-verify flag or any other bypass), not even in a throwaway probe repo (a reviewer tried it on 2026-10-24; the deletion gate stopped it).
REPORT: full evidence (commands, outputs, hashes, pass/fail totals) to <project temp>/lifecycle/<BOW>/<role>-report.md.
Final message 10-12 lines: outcome, CHECKS with PASS/FAIL, files changed, tokens used, anything deferred.
Testers and verdict end with the block in section 7 (TASK / REVISION / REVIEWER / SCOPE / CHECKS / FINDINGS / VERDICT),
saved verbatim to <project temp>/lifecycle/<BOW>/review.md.
```

Token ceilings are GGR58's per role (developer 250k, tester and hostile 150k, qa 100k, integration 400k). A tier T1 brief should sit well under them; going over needs a `BUDGET-REASON:` line.
