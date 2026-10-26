# <Product name> — plan

Version 0.1.0 · <date> · author <identity> for Aaron · status: DRAFT · mode: lifecycle (I0 = not a lash-up) · path: refactor | green field

> Built from the forge `lifecycle-PLAN-TEMPLATE.md`. Reference instance: `E:\projects\Photos\PLAN.md`. Every section is required; write "none" rather than deleting one.

## 0. Objective
One paragraph: what exists today (for a refactor: where, how big, is it backed up offsite), what the end state is, and the exit test that proves it.

## 1. Planning response (honest)
Numbered points: what is expensive, what is blocked today and by what, what transfers from the old code and what does not, effort in days or weeks.

## 2. Decisions taken
| # | Decision | Consequence baked into this plan |
|---|---|---|

## 3. Error-handling standard
The four pillars in this language: log (where), registered code (registry file, generated enum or constants), correlation id (format), copyable display (component). Module boundaries return a result type; a swallowed error is a defect. Background jobs heartbeat (GGR3).

## 4. Intake answers and checkpoints
### 4.1 Intake (I0 to I11, plus project-specific questions)
| # | Question | Answer | Decides |
|---|---|---|---|
| I0 | Lash-up? | no | lifecycle applies |

### 4.2 Checkpoints
CP-0, CP-spec, CP-review, CP-dev, CP-T1 to CP-T4, CP-verdict, CP-commit, CP-milestone, CP-session, as in the lifecycle proposal section 5. Add the project's own evidence for each.

## 5. Architecture (contract-first)
Module keys `<layer>.<name>`; each is also the BOW key and the code.json key. One line per module: purpose, and for a refactor the old scripts it replaces. Universal edges listed once.

## 6. Type, memory and security baseline
Numbered, language-specific: compiler flags, static analysis, sanitizers, banned APIs, ownership model, strong types, trust-boundary validation, no elevation, network surface, rate limiting and budgets (numbers from I7), least privilege (I5), data integrity. T1 gates on these mechanically; T4 attacks each line.

## 7. Milestones
| M | Deliverable | Exit test |
|---|---|---|
| M0 | riskiest dependency first | time box and written fallback |

A blocked external stops only its own milestone unless code.json records a dependency.

## 8. code.json and BOW
code.json shape and generator (never hand-edited); BOW codes; AC format (Given/When/Then AC-1..n, error codes, baseline items, edges, parity test for a refactor, "Assumptions: none"); state machine as in the lifecycle proposal section 5.

## 9. Roles
| Role | Writes | Never writes | Model and budget |
|---|---|---|---|
Architect, AC Reviewer, Developer, T1 Quality, T2 Registry and logic, T3 Functional, T4 Security, Verdict. Developers own unit tests; T3 owns acceptance tests.

## 10. Risks

## 11. Next actions
