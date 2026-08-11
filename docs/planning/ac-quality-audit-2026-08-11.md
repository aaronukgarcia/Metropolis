# Acceptance-criteria quality audit — 2026-08-11 (PARTIAL)

**Status: incomplete.** Roughly 40 of 109 criteria files were audited before the
session limit killed the remaining batches. **Committed anyway, because the
cross-file patterns below are already conclusive and would otherwise be
re-derived from scratch.** The per-file numbers are sound for the files listed;
the totals are not repo-wide.

## The definition used

An AC counts **UNFAILABLE** only if its Check clause reduces entirely to *"a test
function matching this name exists / passes"* — with **no** comparison,
differential fixture, literal value, or identity spelled out **in the Check
clause itself**.

Deliberately **not** counted: a name-grep paired with a `MET-` registry-code
grep, a `go doc` structural requirement, or an explicit "asserts X" clause.
Those are recorded separately as **name-grep-but-content-specified** — weaker
than a structural check, stronger than pure existence. That split is why an
earlier QA pass could not reproduce its own "~13" figure for
`engine.invariant.md`: under this definition it is **6 strictly unfailable plus
7 borderline**, which is the same baker's dozen, correctly separated.

## Findings, in order of what they cost

### 1. "Blocked pending BUG-058" means NO CHECK AT ALL, not a weak one

The sharpest finding, and it was invisible until someone counted. Several ACs
are honestly labelled *blocked pending BUG-058* (the missing `code.json` call
edges) — and their Check clause literally reads **"Check (once unblocked): …"**.
So today **nothing can fail**. `engine.destination.md` has three such ACs
(AC-6/7/8: transit gating, town-centre pressure, freight demand) with zero
current enforcement of any kind.

This is worse than a weak check, because the label reads as diligence. The
honesty is real — nobody hid it — but an AC that cannot fail is not a deferred
check, it is an absent one, and it will be read as coverage at dispatch time.

Concentrated on **money paths and conservation identities**: `engine.rail` AC-6
(export-contract revenue), `engine.tourism` AC-8 (bed-tax revenue),
`engine.policies` AC-19 (policy cost through `FinanceAPI`), `engine.spiral`
AC-6 (insolvency death condition — tested only via an injected fake signal),
plus the conservation-registration ACs in `engine.refuse`, `engine.social`,
`engine.dispatch`, `engine.education`, `engine.prison`.

### 2. The two weakest AC shapes are systematic, not incidental

Across nearly every file audited, the same two shapes recur:

- **GR#7 error handling**: `grep "MET-"` + *"passing test coverage (grep -rn
  "func Test.*Invalid…")"* — nothing states what the test must assert.
- **Race safety**: *"`grep -n "go func()"` finds at least one concurrency
  test"* — goroutine presence, not a race proof.

`engine.news.md` AC-12 is the **sole** race AC in the audited set that instead
demands a real `go test -race` run with no presence-grep escape. It is the
pattern the others should adopt.

Note where the error-handling weakness sits: it is almost always the *second,
minor clause* of an Error-handling section, in files whose primary functional
and conservation ACs are rigorously specified. The rigour does not degrade
evenly — it degrades at the end of the file.

### 3. Determinism checks are weakest exactly where determinism is most fragile

`engine.mining.md` AC-12 is the worst instance found: the Check is a bare test-
name grep (with a typo'd alternation), while **the AC's own prose warns that
viewshed floating-point elevation comparisons "can silently diverge across
shard/worker boundaries."** The file identifies its own fragility and then
checks it with the weakest mechanism in the audit.

`engine.logistics` AC-15 and `engine.market` AC-12 both hedge the cross-worker
comparison with *"(if feasible)"* — which lets a build skip the half GR#21
actually cares about.

Counter-example worth copying: `engine.traffic` AC-17/AC-20 requires **three or
more worker counts, repeated, on a synthetic-city-scale fixture**, plus an
isolated reduction-function test. That can fail a plausible-but-wrong parallel
reduction. It is the standard.

### 4. `engine.invariant.md` is the outlier, and it matters more than most

6 strictly unfailable + 7 borderline out of 22. It is the module that hosts
**every other module's conservation identity**, so a weak check here weakens
everything registered through it. AC-2's check greps for four invariant *type
names* — a build could define all four, wire none into `RunSuite`, and pass.

### 5. Files to copy from

`engine.fdi.md` (0 unfailable, every AC framed by the lazy implementation it
rejects), `engine.tax.md` (differential elasticity/incidence checks, explicit
cross-check against `engine.finance`), `engine.traffic.md` (determinism done
properly), `engine.news.md` AC-5 (redact a log record, require the epilogue to
change).

## Transferable patterns this wave produced

Worth adopting wherever the shape fits:

- **Mutate the data, don't grep for it.** A grep is satisfied by `1.150` or
  `115/100`; only changing the file and requiring the new number proves the
  value flows. Now standard for GR#15.
- **Permutation test** — two populations with an identical *multiset* of
  attribute values but different holders must produce results that track the
  permutation. Kills an aggregate model wearing per-entity labels.
- **Additive identity + isolation** — the total equals baseline plus the sum of
  parts, AND perturbing one input moves only that part. Either alone is
  fakeable; together they kill a weighted blend with post-hoc labels.
- **Equal distance, different elevation** — a fixture a radius model cannot pass
  by construction, where "decays with distance" would have passed trivially.
- **Cross-module identity** — check a figure against another module's
  independently-built ledger. A remainder term can hide inside a single-module
  identity; it cannot hide a disagreement between two.

## Per-file counts (audited subset)

| File | ACs | Unfailable | Rule-shaped risk |
|---|---|---|---|
| engine.headless | 13 | 0 | 1 |
| engine.households | 15 | 1 | 1 |
| **engine.invariant** | 22 | **6** | **6** |
| engine.leisure | 16 | 3 | 1 |
| engine.logistics | 19 | 3 | 2 |
| engine.market | 16 | 3 | 2 |
| engine.mining | 15 | 3 | 2 |
| engine.news | 14 | 1 | 1 |
| engine.parking | 15 | 1 | 0 |
| engine.policies | 19 | 0 | 2 |
| engine.prison | 15 | 1 | 2 |
| engine.projections | 16 | 0 | 1 |
| engine.rail | 11 | 0 | 1 |
| engine.refuse | 18 | 1 | 1 |
| engine.roads | 20 | 1 | 0 |
| engine.season | 18 | 1 | 0 |
| engine.services | 17 | 1 | 0 |
| engine.shopping | 14 | 1 | 0 |
| engine.social | 18 | 1 | 2 |
| engine.spiral | 14 | 1 | 1 |
| engine.tax | 17 | 0 | 0 |
| engine.tourism | 20 | 0 | 1 |
| engine.traffic | 27 | 0 | 0 |
| engine.tunnels | 14 | 0 | 0 |
| engine.unlocks | 19 | 1 | 0 |
| engine.wellbeing | 19 | 0 | 1 |
| engine.world | 22 | 1 | 1 |
| engine.defence | 16 | 2 | 2 |
| engine.destination | 15 | **3** | 3 |
| engine.dispatch | 18 | 2 | 2 |
| engine.education | 17 | 3 | 3 |
| engine.extcommute | 19 | 2 | 1 |
| engine.farming | 17 | 3 | 2 |
| engine.fdi | 13 | **0** | **0** |
| engine.finance | 18 | 1 | 2 |
| engine.firms | 20 | 2 | 1 |

**Not audited** (session limit): `engine.fiscal`, `engine.freight`,
`engine.fuel`, `engine.attract`, `engine.build`, `engine.citizens`,
`engine.chemicals`, `engine.cafe`, `engine.coastal`, `engine.consumption`,
`engine.core`, `engine.crime`, all `feat.*`, `foundation.*`, `harness.*`,
`int.*`, `tool.*`, `ui.*`, `data.catalogue`, `balance.harness`.

## Recommended order of work

1. Give every *blocked pending BUG-058* AC an interim check that can fail today,
   or state in the file that the rule is unenforced — the current wording reads
   as coverage.
2. Replace the race-AC pattern with `engine.news` AC-12's (a real `-race` run).
3. Fix `engine.mining` AC-12 and remove the *"(if feasible)"* hedges.
4. Rework `engine.invariant.md` — it is upstream of every conservation claim.
5. Give the Error-handling second-clause ACs an assertion.
