# GR#25 Edge-Registration Proposal — code.json Graph Drift

**Status:** DRAFT — for Architect (Bev) review. No commits, BOW writes, or edits to
`master-plan-v2.1.json`/`code.json` have been made. This is a proposal only.
**Prepared:** 2026-08-30/31. **Unblocks:** FEAT-1972079852 (webconsole protocol
adapter, dispatch-blocked) and BUG-432 step 1 (spec-lint fix order).

---

## 0. TL;DR for the busy reader

- Ran `node tools/plan/spec-lint.js`: **620 findings across 552 unique cited
  module pairs** (up from BUG-432's original 62/292 count on 2026-08-28 —
  more acceptance docs have been written since; the number is not static).
- Built an independent, code-derived cross-check: scanned every `.go` file
  under `internal/**` for actual `github.com/aaronukgarcia/Metropolis/internal/...`
  imports, mapped each side to its master-plan key, and diffed against
  `code.json`'s registered `calls`/`consumers`. This found **466 real Go
  edges total, 383 already registered, 83 NOT registered** — this is the
  literal, mechanical "unregistered Go import" set BUG-432 refers to.
- Of those 83, **41 are calls to `foundation.errors`/`foundation.det`/
  `foundation.num`/`foundation.data`** — already declared as implicit
  "universal edges" in `master-plan-v2.1.json`'s own `conventions.universalEdges`
  block, but **neither `generate.js` nor `spec-lint.js` actually consults that
  block** — so the convention doesn't suppress the violation. Recommend
  fixing the tooling gap rather than hand-adding ~41 redundant array entries
  (see §3).
- The remaining **42 are genuine, specific, previously-unregistered
  module↔module edges** — real code exists, the graph just never recorded
  it. Proposed for direct registration (§2), all VERIFIED-IN-SOURCE.
- Cross-checked a 20-pair sample of the 552 *spec-cited* (not source-derived)
  pairs against real Go imports: **19 of 20 had no matching import at all**
  (LINT-ONLY) — the acceptance-doc citations are overwhelmingly aspirational/
  forward-looking prose about modules that are still stubs, not a
  source-code drift problem. Recommend NOT mechanically registering those;
  see §4.
- §5 covers the webconsole → `int.protocol` edge for FEAT-1972079852: no
  webconsole module entry exists in the master plan at all. Proposes a
  minimal new module item plus the edge, framed as a cross-language
  runtime/transport edge (not a Go import) — this is new territory for the
  schema.
- §6 has the exact regeneration command and expected effects. §7 lists
  open questions for the Architect.

---

## 1. How edges are declared (read-only findings, no files touched)

`docs/planning/master-plan-v2.1.json` items carry:

- `deps: [...]` — build/sprint ordering only; checked for acyclicity by
  `generate.js` (Kahn's algorithm). Not the graph spec-lint checks.
- `calls: [...]` — the item's **realized outbound edges** (the thing that
  matters here). `tools/plan/generate.js` turns this into `code.json`'s
  `modules[].outbound.calls[]` (an array of `{key, moduleGuid,
  inboundGuid}`), and **mechanically derives** the reverse
  `modules[].inbound.consumers[]` on the *target* module — you only ever
  edit `calls[]` on the calling item; `generate.js` computes the consumer
  side for you (see `generate.js` lines ~254-290, "Reverse pointers").
- `collaborations.consumesFrom` / `.suppliesTo` (seen in some items, not
  used below) are a stricter, independently-authored assertion that a
  `calls[]` edge exists — validated by `generate.js`'s MET-T025 check; not
  needed for what's proposed here.

`tools/plan/spec-lint.js` builds its own edge set (`registry.edges`, a
`Set` of `"from|to"` strings) from **both** `outbound.calls` and
`inbound.consumers` on every module in `code.json` (`spec-lint.js` lines
266-275), and an acceptance-doc citation of module X passes iff an edge
exists in **either direction** (`edgeRegisteredEitherDirection`, lines
307-310). So: **adding `to-key` to the `calls[]` array of the `from` item
in master-plan is the entire fix** — one-directional in the source file,
bidirectional in the derived graph check.

`generate.js` was read (not modified) enough to confirm this mapping;
`tools/plan/spec-lint.js` was read (not modified) to confirm the edge-set
construction and the either-direction check.

---

## 2. PRIMARY proposal — 42 specific, VERIFIED-IN-SOURCE edges

**Method:** `git ls-files "internal/**/*.go"` (respects the repo's
tracked-file list), regex-scanned every file for
`"github.com/aaronukgarcia/Metropolis/(internal/...)"` import strings,
resolved both the importing file's directory and the imported path to
master-plan keys via longest-prefix match on each item's `path` field,
then diffed the resulting edge set against `code.json`'s existing
`outbound.calls`/`inbound.consumers` (either direction). This is a
mechanical, reproducible scan — not spec-prose-derived — so every row
below is **VERIFIED-IN-SOURCE by construction**; the `count` column is the
number of `.go` files (prod + test) containing that import, i.e. the
strength of the evidence.

Two rows (`engine.core → feat.debugmode`, `feat.debugmode → engine.core`)
are **test-only** imports (`speed8x_test.go`, `determinism_test.go`
respectively — confirmed by direct grep) rather than production code; Go
disallows true production import cycles, so this is not a design smell,
just worth flagging as weaker evidence than the rest.

Ambiguity note: several Go packages host more than one master-plan key at
the same `path` (e.g. `internal/engine/mining/` hosts `engine.mining` +
four `feat.*` sub-keys). Where the scan's target package could resolve to
several co-located keys, I've applied the convention seen elsewhere in the
plan (edges named on the parent module, not a feature sharing its
package) — flagged in §7 for confirmation.

| # | From | To | Evidence | Files |
|---|------|-----|----------|-------|
| 1 | engine.attract | engine.build | VERIFIED-IN-SOURCE | 1 |
| 2 | engine.attract | engine.logistics | VERIFIED-IN-SOURCE | 1 |
| 3 | engine.attract | engine.market | VERIFIED-IN-SOURCE | 1 |
| 4 | engine.attract | engine.season | VERIFIED-IN-SOURCE | 1 |
| 5 | engine.attract | engine.world | VERIFIED-IN-SOURCE | 1 |
| 6 | engine.comms | engine.citizens | VERIFIED-IN-SOURCE | 1 |
| 7 | feat.compositionroot | ui.alerts | VERIFIED-IN-SOURCE | 1 |
| 8 | feat.compositionroot | ui.widgets | VERIFIED-IN-SOURCE | 1 |
| 9 | feat.compositionroot | ui.screen.finance | VERIFIED-IN-SOURCE | 1 |
| 10 | feat.compositionroot | ui.screen.services | VERIFIED-IN-SOURCE | 1 |
| 11 | feat.compositionroot | ui.screen.map | VERIFIED-IN-SOURCE | 1 |
| 12 | engine.core | feat.debugmode | VERIFIED-IN-SOURCE (test-only) | 1 |
| 13 | feat.debugmode | engine.core | VERIFIED-IN-SOURCE (test-only) | 1 |
| 14 | engine.defence | engine.season | VERIFIED-IN-SOURCE | 1 |
| 15 | engine.destination | engine.world | VERIFIED-IN-SOURCE | 3 |
| 16 | feat.detgate | feat.compositionroot | VERIFIED-IN-SOURCE | 1 |
| 17 | engine.fuel | engine.market | VERIFIED-IN-SOURCE | 1 |
| 18 | harness.stub | engine.core | VERIFIED-IN-SOURCE | 1 |
| 19 | foundation.integration | int.serializer | VERIFIED-IN-SOURCE | 1 |
| 20 | feat.metricsdash | engine.core | VERIFIED-IN-SOURCE | 1 |
| 21 | feat.metricsdash | int.protocol | VERIFIED-IN-SOURCE | 1 |
| 22 | ui.harness | harness.stub | VERIFIED-IN-SOURCE | 1 |
| 23 | ui.harness | ui.keys | VERIFIED-IN-SOURCE | 1 |
| 24 | ui.harness | ui.screen.map | VERIFIED-IN-SOURCE | 1 |
| 25 | ui.harness | ui.widgets | VERIFIED-IN-SOURCE | 1 |
| 26 | ui.diagrams | int.protocol | VERIFIED-IN-SOURCE | 1 |
| 27 | ui.keys | int.protocol | VERIFIED-IN-SOURCE | 1 |
| 28 | ui.screen.debug | engine.core | VERIFIED-IN-SOURCE | 1 |
| 29 | ui.screen.demo | ui.dash | VERIFIED-IN-SOURCE | 2 |
| 30 | feat.devmode | int.serializer | VERIFIED-IN-SOURCE | 2 |
| 31 | feat.devmode | engine.core | VERIFIED-IN-SOURCE | 1 |
| 32 | feat.devmode | int.protocol | VERIFIED-IN-SOURCE | 1 |
| 33 | ui.screen.districts | ui.core | VERIFIED-IN-SOURCE | 6 |
| 34 | ui.screen.districts | ui.screen.finance | VERIFIED-IN-SOURCE | 1 |
| 35 | ui.screen.districts | ui.widgets | VERIFIED-IN-SOURCE | 1 |
| 36 | ui.screen.finance | ui.core | VERIFIED-IN-SOURCE | 3 |
| 37 | ui.screen.finance | ui.widgets | VERIFIED-IN-SOURCE | 2 |
| 38 | ui.screen.map | harness.stub | VERIFIED-IN-SOURCE | 1 |
| 39 | ui.screen.menu | ui.screen.map | VERIFIED-IN-SOURCE | 1 |
| 40 | ui.screen.services | ui.core | VERIFIED-IN-SOURCE | 7 |
| 41 | ui.screen.services | ui.widgets | VERIFIED-IN-SOURCE | 6 |
| 42 | ui.screen.services | ui.screen.map | VERIFIED-IN-SOURCE | 1 |

### Copy-pasteable JSON fragments

Each block below is the **complete new `calls` array** for that item —
replace the existing `"calls": [...]` array on the named `key` in
`master-plan-v2.1.json` with it (only the new entries are additions; the
existing entries are reproduced unchanged so this is a drop-in replace).

```json
// engine.attract (seq 370) — add engine.build, engine.logistics, engine.market, engine.season, engine.world
"calls": [
  "engine.citizens", "engine.finance", "engine.households",
  "foundation.det", "foundation.errors", "foundation.num",
  "engine.build", "engine.logistics", "engine.market", "engine.season", "engine.world"
]
```
```json
// engine.comms (seq 795) — add engine.citizens
"calls": [
  "engine.services", "engine.logistics", "engine.firms", "engine.traffic",
  "engine.market", "foundation.data", "foundation.errors", "foundation.num",
  "engine.citizens"
]
```
```json
// feat.compositionroot (seq 375) — add ui.alerts, ui.widgets, ui.screen.finance, ui.screen.services, ui.screen.map
"calls": [
  "engine.core", "engine.world", "engine.citizens", "engine.market",
  "engine.consumption", "engine.finance", "engine.build", "engine.attract",
  "engine.invariant", "engine.logistics", "engine.season", "engine.households",
  "engine.crime", "engine.leisure", "engine.refuse", "engine.traffic",
  "engine.extcommute", "engine.services", "engine.firms", "engine.dispatch",
  "foundation.errors", "foundation.num", "int.protocol",
  "ui.alerts", "ui.widgets", "ui.screen.finance", "ui.screen.services", "ui.screen.map"
]
```
```json
// engine.core (seq 140) — add feat.debugmode (test-only edge, see note above)
"calls": [
  "foundation.det", "foundation.registry", "int.protocol", "int.serializer",
  "foundation.buildinfo", "foundation.data", "foundation.errors",
  "feat.debugmode"
]
```
```json
// feat.debugmode (seq 190) — add engine.core (test-only edge, see note above)
"calls": [
  "int.serializer", "foundation.errors",
  "engine.core"
]
```
```json
// engine.defence (seq 880) — add engine.season
"calls": [
  "engine.build", "engine.finance", "engine.citizens", "engine.world",
  "engine.spiral", "foundation.data", "foundation.det", "foundation.errors",
  "foundation.num",
  "engine.season"
]
```
```json
// engine.destination (seq 820) — add engine.world (foundation.* additions covered in §3)
"calls": [
  "engine.tourism", "engine.mining", "engine.parking",
  "engine.world"
]
```
```json
// feat.detgate (seq 150) — add feat.compositionroot
"calls": [
  "engine.core", "int.protocol", "foundation.errors",
  "feat.compositionroot"
]
```
```json
// engine.fuel (seq 830) — add engine.market (foundation.* additions covered in §3)
"calls": [
  "engine.traffic", "engine.consumption", "engine.tax", "engine.logistics",
  "engine.finance",
  "engine.market"
]
```
```json
// harness.stub (seq 100) — add engine.core
"calls": [
  "int.protocol", "foundation.errors",
  "engine.core"
]
```
```json
// foundation.integration (seq 1000) — add int.serializer
"calls": [
  "foundation.det", "foundation.errors", "int.protocol",
  "feat.checkpoint", "feat.saveux",
  "int.serializer"
]
```
```json
// feat.metricsdash (seq 950) — add engine.core, int.protocol
"calls": [
  "feat.devmode", "foundation.errors", "feat.debugmode", "harness.synth",
  "engine.core", "int.protocol"
]
```
```json
// ui.harness (seq 210) — add harness.stub, ui.keys, ui.screen.map, ui.widgets
"calls": [
  "ui.core", "harness.replay", "int.protocol", "foundation.errors",
  "int.serializer",
  "harness.stub", "ui.keys", "ui.screen.map", "ui.widgets"
]
```
```json
// ui.diagrams (seq 480) — add int.protocol
"calls": [
  "ui.widgets", "foundation.errors", "ui.core",
  "int.protocol"
]
```
```json
// ui.keys (seq 130) — add int.protocol
"calls": [
  "foundation.errors",
  "int.protocol"
]
```
```json
// ui.screen.debug (seq 180) — add engine.core
"calls": [
  "foundation.registry", "foundation.errors", "ui.widgets",
  "foundation.buildinfo", "ui.core",
  "engine.core"
]
```
```json
// ui.screen.demo (seq 550) — add ui.dash
"calls": [
  "ui.core", "ui.widgets", "engine.citizens", "engine.households",
  "engine.leisure", "engine.extcommute", "int.protocol", "foundation.errors",
  "ui.dash"
]
```
```json
// feat.devmode (seq 920) — add int.serializer, engine.core, int.protocol
"calls": [
  "feat.debugmode", "foundation.errors",
  "int.serializer", "engine.core", "int.protocol"
]
```
```json
// ui.screen.districts (seq 590) — add ui.core, ui.screen.finance, ui.widgets (foundation.errors covered in §3)
"calls": [
  "engine.policies", "engine.tax", "int.protocol", "ui.dash",
  "ui.core", "ui.screen.finance", "ui.widgets"
]
```
```json
// ui.screen.finance (seq 510) — add ui.core, ui.widgets (foundation.errors covered in §3)
"calls": [
  "engine.finance", "engine.tax", "engine.fiscal", "int.protocol",
  "ui.dash", "ui.diagrams",
  "ui.core", "ui.widgets"
]
```
```json
// ui.screen.map (seq 160) — add harness.stub
"calls": [
  "ui.core", "ui.widgets", "engine.world", "engine.citizens",
  "int.protocol", "foundation.errors",
  "harness.stub"
]
```
```json
// ui.screen.menu (seq 580) — add ui.screen.map
"calls": [
  "engine.core", "foundation.errors", "int.protocol", "int.serializer",
  "ui.core", "ui.dash", "ui.keys",
  "ui.screen.map"
]
```
```json
// ui.screen.services (seq 530) — add ui.core, ui.widgets, ui.screen.map (foundation.errors covered in §3)
"calls": [
  "engine.services", "engine.dispatch", "int.protocol", "ui.dash", "ui.keys",
  "ui.core", "ui.widgets", "ui.screen.map"
]
```

---

## 3. The 41 `foundation.*` edges — recommend a tooling fix, not 41 manual edits

`master-plan-v2.1.json`'s own `conventions.universalEdges` block already
declares:

```json
"universalEdges": {
  "note": "These call edges apply to every module and are NOT repeated per item to keep the graph readable; they are as binding as explicit edges.",
  "edges": [
    "* -> foundation.errors : ...",
    "engine.* -> foundation.det : ...",
    "engine.* -> foundation.registry : ...",
    "ui.screen.* -> int.protocol : ..."
  ]
}
```

But neither `generate.js` (code.json builder) nor `spec-lint.js` (edge
registry builder, `spec-lint.js` lines 266-291) actually reads
`conventions.universalEdges` — they only look at each module's literal
`calls`/`consumers` arrays. So the convention is currently
**documentation-only** and doesn't suppress the violation; that's the real
tooling gap, and it's the same shape of gap BUG-432 already names for
"blocked-edge tripwires" (fix-order step 2: "teach spec-lint to recognise
[a declared exemption class] as compliant").

The 41 edges below all target `foundation.errors` / `foundation.det` /
`foundation.num` / `foundation.data` — exactly the classes the
`universalEdges` note already covers (plus `foundation.num`/`.data`, which
aren't in the note's four bullets but follow the identical pattern — every
module touching money/quantities imports them). Two options for the
Architect:

- **(a) Recommended:** extend `conventions.universalEdges` to formally
  cover `foundation.num` and `foundation.data` (2 more bullets), then teach
  `generate.js`/`spec-lint.js` to treat `* -> foundation.{errors,det,num,data,registry}`
  as always-satisfied — closes this entire class permanently instead of
  needing a fresh manual sweep every time a module gains a new
  foundation-package call.
- **(b) Fallback, if (a) is out of scope right now:** register all 41
  literally, same mechanism as §2. Full list (from → to → file count):

| From | To | Files |
|------|----|----|
| engine.airunits | foundation.det | 4 |
| engine.airunits | foundation.errors | 4 |
| engine.airunits | foundation.num | 2 |
| feat.commoditymarket | foundation.errors | 5 |
| feat.commoditymarket | foundation.num | 2 |
| feat.commoditymarket | foundation.det | 1 |
| feat.commoditymarket | foundation.data | 1 |
| engine.cafe | foundation.errors | 2 |
| engine.citizens | foundation.data | 1 |
| engine.citizens | foundation.num | 1 |
| feat.compositionroot | foundation.data | 2 |
| engine.destination | foundation.data | 2 |
| engine.destination | foundation.errors | 3 |
| engine.destination | foundation.num | 2 |
| engine.extcommute | foundation.errors | 3 |
| engine.extcommute | foundation.det | 1 |
| engine.extcommute | foundation.num | 1 |
| engine.fuel | foundation.data | 3 |
| engine.fuel | foundation.num | 2 |
| engine.fuel | foundation.errors | 3 |
| engine.maintenance | foundation.det | 2 |
| engine.policies | foundation.errors | 9 |
| engine.policies | foundation.num | 1 |
| engine.prison | foundation.data | 3 |
| engine.prison | foundation.errors | 4 |
| engine.prison | foundation.det | 1 |
| engine.prison | foundation.num | 1 |
| engine.shopping | foundation.errors | 3 |
| engine.staffing | foundation.errors | 2 |
| engine.tourism | foundation.errors | 4 |
| engine.tourism | foundation.num | 2 |
| engine.tourism | foundation.data | 2 |
| engine.traffic | foundation.errors | 3 |
| engine.traffic | foundation.num | 1 |
| harness.headless | foundation.num | 1 |
| harness.synth | foundation.num | 1 |
| harness.synth | foundation.data | 1 |
| ui.screen.districts | foundation.errors | 3 |
| ui.screen.finance | foundation.errors | 4 |
| ui.screen.services | foundation.errors | 3 |

(If (b) is chosen, these can be merged into the same `calls[]` replacement
blocks shown in §2 for the modules that also had specific edges.)

---

## 4. The 552 spec-cited pairs — mostly aspirational, NOT proposed for registration

`spec-lint.js`'s SPEC-LINT-001 violations come from acceptance-criteria
prose citing a module key with no registered edge — 620 findings, 552
unique `(from, to)` pairs after dedup, spanning all 230 acceptance docs
(the BUG-432 count of 62 was from 2026-08-28, before ~160 more acceptance
files existed; this is organic growth of the AC corpus, not a new problem).

**Sample cross-check (n=20, first 20 alphabetically):** each pair's two
Go package directories were greped both directions for a real import.

| From | To | fwd imports | rev imports | Verdict |
|------|----|----|----|---------|
| engine.accelerator | engine.defence | 0 | 0 | LINT-ONLY |
| engine.accelerator | engine.leisure | 0 | 0 | LINT-ONLY |
| engine.accelerator | feat.pharmacampus | 0 | 0 | LINT-ONLY |
| engine.accelerator | feat.refinery | 0 | 0 | LINT-ONLY |
| engine.airport | engine.chemicals | 0 | 0 | LINT-ONLY |
| engine.airport | engine.fdi | 0 | 0 | LINT-ONLY |
| engine.airport | engine.tourism | 0 | 0 | LINT-ONLY |
| engine.airport | feat.decommission | 0 | 0 | LINT-ONLY |
| engine.airport | feat.megafacilities | 3 | 0 | **VERIFIED-IN-SOURCE*** |
| engine.airunits | engine.attract | 0 | 0 | LINT-ONLY |
| engine.airunits | engine.invariant | 0 | 0 | LINT-ONLY |
| engine.airunits | engine.projections | 0 | 0 | LINT-ONLY |
| engine.airunits | engine.services | 0 | 0 | LINT-ONLY |
| engine.airunits | engine.traffic | 0 | 0 | LINT-ONLY |
| engine.airunits | ui.screen.census | 0 | 0 | LINT-ONLY |
| engine.attract | engine.coastal | 0 | 0 | LINT-ONLY |
| engine.attract | engine.core | 0 | 0 | LINT-ONLY |
| engine.attract | engine.crime | 0 | 0 | LINT-ONLY |
| engine.attract | engine.farming | 0 | 0 | LINT-ONLY |
| engine.attract | engine.fdi | 0 | 0 | LINT-ONLY |

\* `feat.megafacilities` shares its package (`internal/engine/mining/`)
with four other keys (`engine.mining`, `feat.resourcedeposits`,
`feat.extraction`, `feat.minetypes`); the real import is to that shared
package, so this is genuinely a §2-class finding once resolved to the
right key (`engine.airport → engine.mining` is **already registered** per
`engine.destination`'s and other modules' calls — worth an Architect
double-check but not re-proposed here to avoid double-registration).

**Reading:** 19/20 (95%) of sampled spec citations have zero
corresponding Go import in either direction — these are BA-authored
forward-looking prose about modules that are still `harness`-stub
placeholders (per GR#20 "contract-first, stub-forever" and the
"Built but not wired" dominant-defect-class memory note). GR#25's own text
anticipates this case explicitly: *"If a specification requires a new
dependency, the BA must first coordinate with the Architect to register
the new outbound edge/contract in code.json before writing the prose."*
That coordination evidently didn't happen for most of these 552 pairs.

**Recommendation:** do NOT mechanically register 469-odd (552 total minus
the 42 §2 + ~41 §3, allowing for the handful of genuine overlaps like the
mining case above) speculative edges just to silence spec-lint — that
would corrupt the graph with edges that don't reflect real code and defeat
GR#25's purpose. Instead: treat this as its own triage item — either (i)
BA passes trim the aspirational citations out of the affected acceptance
docs until the edge is real, or (ii) the Architect does a deliberate,
module-by-module pass pre-registering the subset that represents genuine
near-term planned wiring (contract-first is legitimate under GR#20, but
should be a conscious per-edge decision, not a bulk mechanical import of
everything a BA happened to type).

---

## 5. webconsole → int.protocol edge (FEAT-1972079852 unblock)

**Finding:** there is **no master-plan item for the webconsole at all** —
`webconsole/` (a TypeScript/React app, not Go) has zero entries in
`docs/planning/master-plan-v2.1.json`. `naming.goPackages` in
`conventions` only describes Go package layouts
(`internal/engine/<name>`, `internal/ui/<name>`, `internal/protocol`,
`cmd/*`); there's no existing convention for a TS front-end's key/path.

`int.protocol` (path `internal/protocol/`) is registered, with a single
outbound call to `foundation.errors` today, and its `inbound.pattern`
already says: *"versioned command/event/delta envelope with
correlationId"* — this is the intended seam.

**The webconsole cannot express this edge as a Go import** — it's a
different language runtime. Per the BOW comment on FEAT-1972079852 (Bro,
2026-08-28): Go side is ready (`subscribe.go` `SubscriptionServer`,
finance/chrome/services publishers, `InProcTransport` only — no network
layer yet); the TS side (`webconsole/src/sim/backend.ts`) has no seam at
all yet. So this edge is necessarily a **runtime/transport dependency**,
not a compile-time one — conceptually identical in kind to the
`universalEdges` rule `"ui.screen.* -> int.protocol : ... transport is
always the protocol"`, just crossing a language boundary instead of
staying inside Go.

### Proposed minimal module entry (new item, added to `master-plan-v2.1.json`)

```json
{
  "key": "ui.webconsole",
  "type": "module",
  "seq": 1002,
  "priority": "P1",
  "milestone": "M3",
  "layer": "ui",
  "title": "Web console: React/TypeScript dogfood UI, engine protocol adapter",
  "desc": "Standalone TypeScript/React front-end (webconsole/) driving the deterministic Go engine over int.protocol. Currently ships with a self-contained mock simulation store (webconsole/src/sim/) as an offline fallback; FEAT-1972079852 replaces the mock store boundary with a live adapter speaking the real command/event/delta protocol so fiscal, demographic and map state derive from the real engine. This is a cross-language runtime edge (HTTP/WS or equivalent transport, not a Go import) — int.protocol's dormant gRPC transport or an equivalent wire adapter is the carrier; in-process channels do not apply across the process/language boundary.",
  "specRef": "FEAT-1972079852; int.protocol inbound.pattern (versioned command/event/delta envelope)",
  "path": "webconsole/",
  "deps": [],
  "calls": [
    "int.protocol"
  ],
  "sprint": 9
}
```

Notes on the fragment:

- `layer: "ui"` reuses the existing, already-known-to-spec-lint namespace
  (`ui`) rather than inventing a new one — spec-lint derives its namespace
  list dynamically from whatever prefixes exist in `code.json`, so a new
  namespace *would* work mechanically, but reusing `ui` avoids a pointless
  first-of-its-kind namespace for what is, functionally, another UI
  consuming the protocol exactly like `ui.screen.*` and `ui.dash` do.
- `path: "webconsole/"` is deliberately **not** under `internal/` — it's
  outside the Go module entirely. This is new for the schema (every
  existing item's `path` is a Go directory, `cmd/*`, `tools/plan/`, or
  `docs/`); nothing in `generate.js`'s validation appears to require
  `path` to be a Go directory, but this hasn't been exercised before —
  flagged in §7.
- `seq: 1002` follows the current max (`1001`); `sprint: 9` is a guess
  (FEAT-1972079852 sits well past the Baseline One M1 spine) — Architect's
  call.
- Only `int.protocol` is listed in `calls` for now — the webconsole's
  *internal* TS module graph (components, sim store, etc.) is out of
  scope for GR#25 (that rule is about **cross-module** dependencies
  registered in code.json; webconsole's internals are TypeScript-internal
  and not master-plan items).

### If a whole new module entry is too heavy for this unblock alone

A lighter alternative: add `"ui.webconsole"` — or reuse an even more
minimal placeholder — is unavoidable in some form, because spec-lint's
edge check needs **both** endpoints to be known module keys (unregistered
keys fail SPEC-LINT-004, not just SPEC-LINT-001). There isn't a way to
register "the edge" without registering "the thing on the other end" of
it first. This is flagged as an open question (§7) in case the Architect
wants a different key name or a narrower scope than a full module entry.

---

## 6. Regeneration command and expected effects

```
node tools/plan/generate.js
```

(**Not run** as part of this proposal — it rewrites `code.json` and mints
GUIDs; that's the Architect's call after reviewing/approving edits to
`master-plan-v2.1.json`.)

Expected effects once the master-plan edits above are applied and
`generate.js` is run:

1. `code.json`'s `modules[]` entries for the 37 `from`-keys in §2 (and, if
   §3(b) is chosen, the additional `foundation.*` targets) gain new
   `outbound.calls[]` entries with freshly-minted `inboundGuid`s; the
   corresponding target modules gain reciprocal `inbound.consumers[]`
   entries (generate.js's reverse-pointer pass — no manual edit needed on
   the target side).
2. A new `modules[]` entry appears for `ui.webconsole` (§5), with its own
   `guid`, and `int.protocol`'s `inbound.consumers[]` gains a
   `ui.webconsole` entry.
3. `node tools/plan/spec-lint.js` should then show **at most** 552 - 42 (or
   552 - 42 - 41 if §3(b) is taken) = **510 (or 469)** unique-pair
   violations remaining — all in the §4 aspirational-prose category, not
   this proposal's scope. It will **not** hit zero; that requires the §4
   triage decision separately (BUG-432 fix-order step 1 only ever claimed
   to fix the Go-import-drift subset, not the whole spec corpus).
4. `node tools/plan/generate.js`'s own validators (acyclicity of `deps`,
   MET-T022 self-call rejection, MET-T025 collaboration checks) should
   still pass — none of the proposed edges are self-calls, and none
   introduce a `deps` cycle (all proposed edges are on `calls[]` only,
   which `generate.js` does not acyclicity-check).
5. FEAT-1972079852 can then move from dispatch-blocked to
   dispatchable — the GR#25 gate cited in its BOW comment is the
   `ui.webconsole → int.protocol` edge from §5.
6. BUG-432 can update its comment thread to reflect: step 1 (register the
   real Go-import edges) proposal is ready for Architect approval; step 2
   (teach spec-lint about the universalEdges exemption AND/OR the
   blocked-edge-tripwire class) is unstarted and is a separate, larger
   piece of tooling work, not covered by this edge-registration pass.

---

## 7. Open questions for the Architect

1. **§3 choice:** fix the universalEdges tooling gap (recommended,
   durable) vs. hand-registering 41 `foundation.*` edges now (fast,
   mechanical, but leaves the same gap to recur on every future module).
2. **Co-located-package ambiguity:** for packages hosting multiple
   master-plan keys (e.g. `internal/engine/mining/` → `engine.mining` +
   4 `feat.*` keys), is "register on the parent module key" the right
   convention, or should some of these edges instead target the specific
   `feat.*` key that actually contains the importing/imported code? I
   defaulted to the parent-module convention seen elsewhere in the plan;
   this affects at least the `engine.airport ↔ mining-package` case noted
   in §4 and possibly others not surfaced by the 42-edge scan (the scan
   picks one key per path by longest-prefix + array order, which is not
   fully deterministic across co-located keys of equal path length).
3. **`ui.webconsole` shape (§5):** is a full `type: "module"` entry the
   right weight, or does the webconsole deserve its own top-level `layer`
   (e.g. `"webconsole"` or `"console"`) distinct from `"ui"` now, given
   it's a different language/runtime and will likely grow its own
   sub-items (components, sim store, etc.) the way `ui.screen.*` has
   sub-keys? Also: is `path: "webconsole/"` (non-`internal/`, non-Go)
   going to trip any `generate.js` validation once actually run — this
   proposal did not execute `generate.js`, only read it, per the task's
   constraint.
4. **§4 scope:** who owns the 510ish aspirational spec-cited pairs going
   forward — a BA sweep to trim/qualify the prose, a dedicated Architect
   pre-registration pass for the subset that's genuinely near-term planned
   wiring, or a new spec-lint severity tier (warning vs. hard failure) for
   citations of modules that are still `harness`-stub? This is likely its
   own BOW item, separate from BUG-432.
5. **test-only edges (`engine.core ↔ feat.debugmode`):** register as
   full `calls[]` entries as proposed, or is there a lighter annotation
   convention preferred for edges that only exist in `_test.go` files
   (i.e. don't reflect a production dependency)?
