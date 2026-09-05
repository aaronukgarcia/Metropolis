# GR#25 Edge Audit — Benefits / Jobs Model / Wealth-Inheritance

**Status:** DRAFT — for Architect (Bev) review. No commits, BOW writes, or edits to
`master-plan-v2.1.json`/`code.json` have been made. Read-only analysis only.
**Prepared:** 2026-08-31. **Unblocks:** FEAT-1972079931 (benefits), FEAT-1972079929
(jobs model), FEAT-1972079930 (wealth/ownership/inheritance) — all `draft-ahead`,
none may pass to code per GR#25 until the edges below are registered/ruled on.

**Method:** for every cross-module reach cited in the three acceptance docs, the
SOURCE module's actual `"calls": [...]` array was read directly from
`docs/planning/master-plan-v2.1.json` (line numbers below are current as of this
proposal) and checked for the target. Per the prior batch's confirmed mechanics
(`docs/planning/proposals/gr25-edge-registration-2026-08-30.md` §1): `spec-lint.js`
treats an edge as registered if it exists in **either direction** between the two
modules — so an edge already declared for one purpose (e.g. wages) satisfies GR#25
for a second named flow over the same module pair (e.g. benefits); only a pair with
**no edge at all today** is a real gap.

**Sentiment/approval module — the real name:** there is no `engine.social`
mistake to make here explicitly, but it's worth stating plainly: the module the
Civic Sentiment epic (FEAT-218) extends, and the one benefits INC3's
welfare-generosity lever must reach, is **`engine.wellbeing`** (BOW `MOD-034`,
`docs/planning/master-plan-v2.1.json:1890`), **not** `engine.social` (which is a
different, already-registered module — "Social services: the floor of every
downturn", homelessness/addiction/family-support caseload, BOW-equivalent of
`engine.services`' social-care arm, `master-plan-v2.1.json:3127`). FEAT-218's own
architectural ruling (Bev, 2026-08-21, quoted in BOW) is explicit: the
Happiness/WellbeingState calculus "is NOT a new domain — it is squarely
`engine.wellbeing`'s stated scope... GR#3 forbids standing up a second happiness
calculus in a new `engine.sentiment` module." `engine.sentiment` (FEAT-220/221/222)
is **not yet a registered module key** in either `master-plan-v2.1.json` or
`code.json` — confirmed by grep, zero hits — so it cannot be an edge target today.

---

## 1. FEAT-1972079931 — Benefits (safety net)

Per its own § 6 GR#25 Edge Audit table (rows renumbered here for cross-reference).

### Row 1 — `engine.finance → engine.citizens` (welfare credit, `PostBenefit`)

**Source array** (`engine.finance`, `master-plan-v2.1.json:1427-1437`):
```json
"calls": [
  "engine.core", "engine.market", "engine.world", "engine.consumption",
  "engine.invariant", "engine.projections", "foundation.errors",
  "foundation.num", "engine.citizens"
]
```
`engine.citizens` is present (line 1436). **Classification: ALREADY-REGISTERED.**
The doc's own caveat — confirm the edge covers a second named flow, not just
`PostWages` — is satisfied by spec-lint's either-direction/module-pair mechanics
(§1 of the prior proposal): GR#25 checks the module *pair* is wired, not the
specific method signature. No action needed; the benefits ledger labels
(`Jobseeker's Allowance`, etc.) are a Go-level API-shape decision, not a
graph-registration one.

### Row 2 — `<benefits module> → engine.citizens` (read `EmploymentState`/`Stage`/`AgeBand`)

Benefits' likely home is confirmed by FEAT-1972079929's own Out-of-Scope line
("Unemployment benefits, welfare payments... belong to `engine.services`
(MOD-033), not this feature") — so the calling module is **`engine.services`**.

**Source array** (`engine.services`, `master-plan-v2.1.json:1879-1886`):
```json
"calls": [
  "engine.consumption", "engine.finance", "engine.citizens",
  "foundation.data", "foundation.errors", "foundation.num"
]
```
`engine.citizens` is present. **Classification: ALREADY-REGISTERED.**

### Row 3 — `engine.citizens → engine.finance` (population-by-eligibility-category feeds `WelfareOutflowThisMonth()`)

Whichever direction actually implements the query, the module PAIR
`engine.services ↔ engine.finance` already exists (`engine.services.calls` above
includes `engine.finance`, line 1881). **Classification: ALREADY-REGISTERED**
(pair-level; GR#25 doesn't care which side literally calls which).

### Row 4 — consumption ← benefit income (source-agnostic `Wealth` spend)

**Verified in source** (`internal/engine/finance/*.go`, `internal/engine/compose/moneycirc.go`):
`PostHouseholdSpend` is a `finance`-owned ledger stage that debits household
wealth for consumption at `engine.market`/`engine.consumption`-priced quantities
— it does **not** distinguish wage-sourced from benefit-sourced `Wealth`
(`docs/planning/acceptance/engine.consumption.md:82` confirms `engine.consumption`
only ever exposes a `BilledAmount`, never posts to the ledger itself; the actual
debit is `engine.finance`'s `PostHouseholdSpend`, already wired: `engine.finance`
does not call `engine.consumption` for this purpose — `engine.consumption` calls
**into** `engine.finance` instead, `master-plan-v2.1.json:1396`, already
registered). **Classification: NO NEW EDGE NEEDED** — confirms the BA's own guess
in row 4 of its table. `engine.citizens` and `engine.consumption` have **no edge
between them today, in either direction**, and none is needed for this feature:
the money-in/money-out both route through `engine.finance`, which already touches
both sides.

### Row 5 — sentiment/approval ← welfare generosity (INC3 only)

**Source array** (`engine.services`, same as Row 2) does **not** contain
`engine.wellbeing`. **Classification: NEW EDGE.** `engine.services → engine.wellbeing`
must be added so the INC3 welfare-generosity lever / welfare-outflow trend can
feed the wellbeing calculus (per FEAT-219's threshold-based income/tax penalty
driver, which is exactly this kind of signal — the BA doc even names the pattern:
"disposable income approaching GBP 0 → -12 penalty (source: engine.finance)").

### Row 6 — jobs model ← unemployment eligibility

Both benefits and jobs-model only ever *read* `engine.citizens.EmploymentState` —
neither calls into the other's package. **Classification: NO EDGE NEEDED**
(confirmed, matches the BA's own reasoning).

### Row 7 — wealth/inheritance ← pensioner means-testing (conditional on Open Q 6)

If Aaron rules means-testing IN: the check is against `citizen.Wealth` (the same
field FEAT-1972079930's inheritance credits into) — this is a plain
`engine.services → engine.citizens` read, **already registered** (Row 2). No new
edge is needed even in the conditional case — it was never actually a distinct
module-pair, just a data-field read via an edge that already exists.

**Benefits summary:** 1 NEW edge (`engine.services → engine.wellbeing`), 4
already-registered rows (1,2,3,7 collapse to 2 unique already-existing pairs:
`engine.finance↔engine.citizens`, `engine.services↔engine.finance`,
`engine.services↔engine.citizens`), 2 rows needing no edge at all (4, 6).

---

## 2. FEAT-1972079929 — Jobs Model

The doc's own GR#25 line claims "no new `code.json` edge beyond what FEAT-225
registered" — checked against the actual graph and against source:

**Verified in source:** `grep -rl "occupations.json\|R_occ\|occupation" internal/`
returns **zero hits** — FEAT-225's occupation registry has not actually landed in
Go yet (despite a BOW comment dated 2026-08-22 claiming it had); this doc is fully
`draft-ahead`. No jobs-model package exists to check imports against, so this
section is graph-only (no source cross-check possible, unlike §1's).

### Wage crediting (`finance.PostWages`, occupation-scaled)

Same edge as benefits Row 1: `engine.finance → engine.citizens`,
**ALREADY-REGISTERED** (`master-plan-v2.1.json:1436`).

### Job posting on building completion → job assignment

`engine.build` already calls `engine.staffing`
(`master-plan-v2.1.json:1566-1577`, entry at line 1575) and `engine.staffing`
already calls `engine.citizens`, `engine.finance`, `engine.services`
(`master-plan-v2.1.json:2903-2909`). If the job-family/desirability/essential-fill
assignment algorithm (AC-3, AC-5) lives inside `engine.staffing` (the natural
home — "citizens as operators... matched against each service's demand" is
exactly AC-3/AC-5's shape), **no new edge is needed**: build→staffing→citizens/
finance/services is a fully-connected chain already.

### AC-9 commute-distance / workplace-location query — genuinely unresolved

`engine.staffing`'s registered `calls` (citizens, maintenance, finance,
foundation.data, services) contains **no path to building/workplace location**
(`engine.build` or `engine.world`). AC-9 requires filtering job candidates by
commute distance from a citizen's home to a workplace's location — this data
lives in `engine.world`/`engine.build`, not in anything `engine.staffing`
currently reaches. **Classification: OPEN QUESTION, not asserted as a new edge**
(see §4, OQ-1) — no source package exists yet to confirm which module will own
this call, and GR#25 explicitly bans speculative registration.

### Config page (S_base, inflation, per-job override)

Reaches the player via the F8/F9 screen over the standard `ui.screen.* →
int.protocol` universal edge (`conventions.universalEdges`) and the composition
root's existing `ui.screen.*` wiring — no jobs-model-specific new edge.

**Jobs model summary:** 0 confirmed NEW edges (all the doc's own claimed reuse
holds up), 1 OPEN QUESTION (commute-distance data source), all other reaches
ALREADY-REGISTERED via the existing build↔staffing↔citizens/finance/services
cluster.

---

## 3. FEAT-1972079930 — Wealth, Ownership & Inheritance

This doc has no explicit edge-audit table — edges derived here directly from its
ACs.

### AC-5/6/11 — Inheritance, CGT, Escheat (finance ledger posts on citizen death)

Same module pair as benefits Row 1/jobs' wage edge: `engine.finance ↔
engine.citizens`, **ALREADY-REGISTERED** (`master-plan-v2.1.json:1436`). This is
a third distinct named flow over the same registered pair (wages, welfare,
now inheritance/CGT/escheat) — GR#25 is satisfied at the module-pair level per
the confirmed spec-lint mechanics; flagged here only so the Architect is aware
three separate BA docs are now stacking flows on one edge.

### AC-3/AC-10 — Ownership type assignment (Individual/LLP/State), tied to a specific citizen

**Source array** (`engine.build`, `master-plan-v2.1.json:1566-1577`):
```json
"calls": [
  "engine.world", "engine.finance", "engine.logistics", "engine.market",
  "foundation.data", "engine.season", "foundation.errors", "foundation.num",
  "engine.staffing", "engine.firms"
]
```
`engine.citizens` is **absent**. `engine.citizens`'s own `calls`
(`master-plan-v2.1.json:1283-1289`: `engine.core`, `int.serializer`,
`engine.invariant`, `foundation.det`, `foundation.errors`) does not list
`engine.build` either. **There is no edge between `engine.build` and
`engine.citizens` in either direction today.** **Classification: NEW EDGE** —
`engine.build → engine.citizens`, needed to resolve "which citizen owns this
Individual-type property" at build time (AC-3) and to enforce ownership
immutability (AC-10).

**BUT** — see §4 OQ-2: `engine.firms` already has **all four** of
`engine.citizens`, `engine.finance`, `engine.build`, `engine.market` wired
(`master-plan-v2.1.json:3301-3311`). If the Architect instead scopes ownership-type
assignment (Individual/LLP/State) as `engine.firms`' concern — which fits its own
stated remit ("banking layer... turns deposits into firm credit"; LLP is
explicitly a firm-shaped construct per this doc's own § "Ownership Types") rather
than `engine.build`'s — **zero new edges are needed at all**, because
`engine.firms` can already reach every module this AC touches. This is the
single biggest lever in this proposal for shrinking the new-edge count from 3 to
2 (or even keeping `engine.build`'s edge for the *Individual/State* cases while
routing LLP-specific logic through the already-wired `engine.firms`).

### AC-4 — Tenure decision by wealth (own/rent/share), needs local rental/purchase prices

**Source array** (`engine.households`, `master-plan-v2.1.json:1630-1635`):
```json
"calls": [
  "engine.citizens", "foundation.data", "foundation.errors", "foundation.num"
]
```
No `engine.finance`, no `engine.market`. AC-4 requires "local rental/purchase
prices" to compute affordability — `engine.finance` is the module that already
owns "continuous land pricing base x access x amenity x scarcity" per its own
description (`master-plan-v2.1.json:1415`). **Classification: NEW EDGE** —
`engine.households → engine.finance`.

*Note:* `engine.attract`'s inbound pattern (`master-plan-v2.1.json:1652`) already
says "housingAffordability computed via `engine.households` + `engine.finance`
(registered edges)" — `engine.attract` already calls both
(`master-plan-v2.1.json:1659-1671`). This confirms the households+finance
combination is an established pattern elsewhere in the graph, just not yet wired
as a direct `households→finance` edge for THIS (tenure, not migration)
consumer — supporting evidence the new edge is the right shape, not a novel
one.

### AC-9 — Parent-child tracking prerequisite

Internal to `engine.citizens` (a data-model extension: does the citizen record
gain a `Children`/`Parent` field). Not a cross-module edge at all — no
registration action.

**Wealth/inheritance summary:** 2 NEW edges proposed (`engine.build →
engine.citizens`, `engine.households → engine.finance`), 1 of the 2 conditionally
eliminable (see OQ-2), 1 already-registered pair reused for a third named flow
(`engine.finance ↔ engine.citizens`).

---

## 4. CONSOLIDATED — NEW edges to register

| # | Edge (source → target) | Append to `calls[]` on | Master-plan line (array start) | Justifying flow | Needed by |
|---|---|---|---|---|---|
| N1 | `engine.services → engine.wellbeing` | `engine.services` | `master-plan-v2.1.json:1879` (array `1879-1886`) | INC3 welfare-generosity lever / welfare-outflow trend feeds the wellbeing (sentiment/approval) causal-driver calculus, per FEAT-219's "disposable income" driver pattern | FEAT-1972079931 (Benefits) |
| N2 | `engine.build → engine.citizens` | `engine.build` | `master-plan-v2.1.json:1566` (array `1566-1577`) | Ownership-type assignment (Individual/LLP/State) needs to resolve/assign the specific owning citizen at build time and enforce immutability | FEAT-1972079930 (Wealth/Inheritance) — **see OQ-2: may be eliminable if ownership logic is scoped to `engine.firms` instead, which needs no new edges** |
| N3 | `engine.households → engine.finance` | `engine.households` | `master-plan-v2.1.json:1630` (array `1630-1635`) | Tenure decision (own/rent/share) needs local rental/purchase prices, which `engine.finance` already owns (continuous land pricing) | FEAT-1972079930 (Wealth/Inheritance) |

**Copy-pasteable JSON fragments** (drop-in replacement for each item's existing
`calls` array — new entries only, existing entries reproduced unchanged):

```json
// engine.services (master-plan-v2.1.json line 1879) — add engine.wellbeing
"calls": [
  "engine.consumption", "engine.finance", "engine.citizens",
  "foundation.data", "foundation.errors", "foundation.num",
  "engine.wellbeing"
]
```
```json
// engine.build (master-plan-v2.1.json line 1566) — add engine.citizens
"calls": [
  "engine.world", "engine.finance", "engine.logistics", "engine.market",
  "foundation.data", "engine.season", "foundation.errors", "foundation.num",
  "engine.staffing", "engine.firms",
  "engine.citizens"
]
```
```json
// engine.households (master-plan-v2.1.json line 1630) — add engine.finance
"calls": [
  "engine.citizens", "foundation.data", "foundation.errors", "foundation.num",
  "engine.finance"
]
```

No new module entries are required (unlike the prior 2026-08-30 batch's
`ui.webconsole` case) — every source and target module above already exists in
`master-plan-v2.1.json`.

---

## 5. Verification checklist (run after editing `master-plan-v2.1.json`)

Exact sequence, per `tools/plan/generate.js`'s own validators and the prior
batch's confirmed regenerate/verify flow
(`docs/planning/proposals/gr25-edge-registration-2026-08-30.md` §6):

```
# 1. Regenerate code.json from the edited master plan (mints GUIDs for new
#    outbound.calls entries, computes reciprocal inbound.consumers on the
#    target modules — no manual edit needed on the target side)
node tools/plan/generate.js

# 2. Re-run spec-lint to confirm the specific SPEC-LINT-001 findings this
#    batch targets are gone (benefits/jobs/inheritance's cited pairs should
#    drop out of the violation set; the doc corpus's other ~552 aspirational
#    pairs are NOT expected to clear — see the 2026-08-30 proposal §4)
node tools/plan/spec-lint.js

# 3. Units registry check (unaffected by this batch, but part of the standard
#    plan-edit verification pass)
node tools/plan/units-lint.js

# 4. Confirm generate.js's own structural validators still pass: acyclicity
#    of deps (Kahn's algorithm), MET-T022 self-call rejection, MET-T025
#    collaboration checks — these run INSIDE step 1; a non-zero exit or a
#    printed validation error there is the fail signal, not a separate command
```

Expected effects once applied:
- `engine.services`, `engine.build`, `engine.households` gain the three new
  `outbound.calls[]` entries with freshly-minted `inboundGuid`s.
- `engine.wellbeing`, `engine.citizens` (x2 — from both build and households),
  `engine.finance` gain reciprocal `inbound.consumers[]` entries automatically.
- `spec-lint.js`'s SPEC-LINT-001 count should drop for exactly the pairs this
  batch registers; it will **not** reach zero (that requires the separate,
  much larger §4-class triage from the 2026-08-30 proposal, out of scope here).
- FEAT-1972079931/929/930 move from GR#25-blocked to dispatchable for their
  respective INC1 builds (modulo the OQ-2 ownership-module decision below,
  which affects only N2 and only FEAT-1972079930's AC-3/AC-10).

---

## 6. Open questions for the Architect

**OQ-1 — Jobs model AC-9 commute-distance data source (unresolved, no new edge proposed).**
`engine.staffing`'s registered edges give it no path to workplace/building
location (`engine.world`/`engine.build`). No jobs-model Go package exists yet
to confirm which module will host the assignment algorithm, so registering a
speculative `engine.staffing → engine.build` (or `→ engine.world`) edge now would
violate GR#25's own "hand-written, unregistered speculative dependency prose is
an instant fail-closed rejection" principle in reverse (pre-registering an edge
no code shape has confirmed yet). Recommend: rule on which module owns job
assignment (a `engine.staffing` extension, or a new `feat.jobsmodel` living
inside `internal/engine/staffing/` per the `feat.refinery`-in-`engine.chemicals`
precedent) before this edge is added — likely as part of dispatching
FEAT-1972079929's INC2.

**OQ-2 — Ownership-type assignment (N2): `engine.build → engine.citizens`, or route through the already-fully-wired `engine.firms`?**
`engine.firms` already calls `engine.citizens`, `engine.finance`, `engine.build`,
`engine.market` (`master-plan-v2.1.json:3301-3311`) — a superset of what
ownership-type assignment needs, with **zero new edges**. `engine.build` today
has no edge to `engine.citizens` at all, and adding one is a genuine graph
change with its own layering question: `engine.build` is infrastructure/zoning
(GR#20 "lower" layer); reaching into `engine.citizens` to resolve/assign an
owning citizen is a different kind of dependency than `engine.build`'s existing
edges (world, finance, logistics, market, season, staffing, firms — all either
peers or already-established consumers). Options: **(a)** keep N2 as proposed
— `engine.build` gains a direct citizens edge (simplest, matches this doc's own
"Relates to: engine.build (property creation)" framing of AC-3); **(b)** scope
Individual/State ownership assignment to stay in `engine.build` using data it
already has (a building's completion event could carry the assigned owner's ID
as a parameter rather than `engine.build` querying `engine.citizens` back —
avoiding the edge entirely, push not pull); **(c)** route ALL ownership-type
logic (including Individual/State, not just LLP) through `engine.firms` since it
is already the fully-connected hub. Recommend (c) if firms is an acceptable
architectural home for personal (non-LLP) ownership bookkeeping too — otherwise
(a).

**OQ-3 (informational, not blocking) — three BA docs are now stacking distinct
named money-flows onto the single `engine.finance ↔ engine.citizens` edge**
(wages, welfare/benefits, inheritance+CGT+escheat). GR#25/spec-lint is satisfied
at the module-pair level (confirmed mechanics), so this is not a registration
gap — flagged only so the Architect is aware the edge's `inbound.pattern` text
("double-entry ledger; every figure drill-through to ledger lines") may be worth
updating to explicitly enumerate all three flows for future doc-readers, though
that's a documentation nicety, not a GR#25 requirement.

---

*Prepared 2026-08-31, read-only. No files under `docs/planning/master-plan-v2.1.json`
or `code.json` were modified. All line numbers refer to the state of
`master-plan-v2.1.json` at the time of this analysis — re-verify line numbers if
other edits land first.*
