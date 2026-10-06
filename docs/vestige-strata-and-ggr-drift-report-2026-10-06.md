# Session report — Vestige 4.x (Strata) experience + GGR drift

**Author:** Bev (lead, main checkout)
**Date:** 2026-10-06
**Scope:** session startup → three handed tasks → discovery of the GGR67 Vestige-caller migration; a write-up of the Vestige upgrade experience and the forge / missing-GGR investigation Aaron asked for.

---

## 1. Executive summary

- **Vestige is not broken — it changed.** On 4.1.1 Strata, free-text/semantic recall and the `mcp__vestige__search` tool were **removed by design**. Recall is now by **exact handle** (memory id / id-prefix / exact tag) via **find-then-pull**. Metro's standing memory and hook text still described this as a "degraded / 2.6 noise" outage, which is wrong and actively harmful (a session that believes it files the capability change as noise and runs with no recall).
- **I re-filed the memory** (new `vestige-strata-recall`, old `vestige-v2.6.0-upgrade` banner-marked SUPERSEDED, index fixed).
- **Startup caller text is stale**: the SessionStart summary and the GR#14 UserPromptSubmit reminder still tell sessions to "confirm live with `mcp__vestige__search`" — a tool that no longer exists.
- **The permanent fix is GGR67** ("Vestige recall is by handle; a pointer index gives free-text findability"). Metro has **not** adopted it yet. Its harness Vestige callers need migrating to find-then-pull. Tracked as **FEAT-2326609814** (P2, lane work).
- **GGR drift:** Metro's `GGR.md` renders rules 1–66 and is **missing GGR67**. The forge's own on-disk `GGR.md` is even staler (1–63). SAFE is current (1–67). The forge **DB** is the SSOT and holds ≥67.
- **Side quest completed cleanly:** the 3 uncommitted `githooks/` changes (forge-asset parameterisation) were rounded (independent ACCEPT) and landed as `9efb9f9` under **FEAT-2326609812**; stale commit-msg hook reinstalled; two follow-ups filed (FEAT-2326609813, BUG-1106).

---

## 2. The Vestige upgrade experience (what I actually hit)

### 2.1 Startup
The mandatory startup sequence told me to run `mcp__vestige__search`. **That tool does not exist** — it returned `No such tool available: mcp__vestige__search`. I fell back to `mcp__vestige__recall` with a free-text query, which returned:

```
Initialization error: similarity_disabled: hybrid_search_filtered: embeddings, cosine,
BM25, FTS, Jaccard, and keyword or name matching are not Strata operations; pass an exact handle
```

`memory_status(view='tools')` confirmed the shape: `compiledFeatures: { embeddings:false, vectorSearch:false, connectors:false, cloudSync:false }`, catalog 4.1.1, store **healthy with 997 memories**. So the binary is live and fine — the capability set is deliberately smaller.

### 2.2 The misdiagnosis in our own memory
Metro's memory index carried:
> *Vestige v2.6.0 upgrade — … CLI DEGRADED is noise*

At startup I initially reported Vestige as "live but **DEGRADED**", reading `similarity_disabled` as the known v2.6 degradation. **Aaron corrected this:** it is the Strata capability change, not degradation. This is exactly the "silent mislabel" failure mode the migration one-pager warns about — filing the change as noise means the session never adopts the bridge and runs recall-blind.

### 2.3 The replacement — find-then-pull
Per Aaron and the one-pager, GR#14 recall is now three steps:
1. `mcp__vestige__session_start` — bulk startup context in one call.
2. `node E:/projects/63-SAFE/vestige-pointer.js find "<intent>" --scope metropolis` — free-text intent → handle.
3. `mcp__vestige__recall { handle: "<id-or-exact-tag>" }` — pull the named memory.

`session_start` worked (returned context, flagged needs: dream + backup). **Step 2 was blocked** in my session by the auto-mode classifier as `[Code from External]` — the finder executes JS from `E:/projects/63-SAFE`, a synced/sibling path (cf. GGR63). I did not route around the denial. So this session had `session_start` + exact-handle recall available, but **not** free-text find — a gap a Metro-local or permission-allowed finder would close.

### 2.4 What re-filing the memory involved
- Wrote `…/memory/vestige-strata-recall.md` (type: reference) describing the Strata model, the 3-step flow, the SAFE-bridge-is-temporary caveat, and the classifier-block caveat.
- Edited `vestige-v2.6.0-upgrade.md` to add a **SUPERSEDED** banner pointing at the new note (kept as historical v2.6-era record).
- Fixed the `MEMORY.md` index: replaced the "CLI DEGRADED is noise" line, added the new note.

---

## 3. The GGR67 rule and the one-pager

**GGR67v01 — "Vestige recall is by handle; a pointer index gives free-text findability."** (origin tag `GR#67 safe`, approved 2026-10-06 once SAFE cut over to 4.1.1, ref SAFE-058.)

> On Vestige 4.x (Strata) recall is by exact memory id or exact tag only — free-text and semantic recall are gone. Find a half-remembered memory through the external pointer index with find-then-pull: query the keyword index (`safe.vestige_pointer`) for {id, tag, gist}, then recall the exact memory from Strata by that id or tag. Every memory carries its project's one canonical tag so it stays findable.

**Why (from the registry):** 4.0 deleted embeddings and keyword search from the shipped binary; the gain is the signed append-only log, provenance receipts, clean multi-agent serving, and the Windows fixes. The pointer index restores findability *outside* the log without a second source of truth — the pull is always back against Strata. An earlier draft of the rule was held deliberately because approving it while semantic recall still worked would have misled every project.

**The one-pager** (authority for the migration): `E:/projects/63-SAFE/docs/vestige-4x-caller-migration.md`. Key points:
- Each project migrates **its own** callers; SAFE does not reach in.
- Changes: drop `mcp__vestige__search`; use `session_start` for bulk startup recall; move the GR#14 prefetch hook (`claude-memory-prefetch.js`) to find-then-pull with the project `--scope`. SAFE's migrated copy (commit `fadaba5`) is the reference.
- **The SAFE finder + `safe.vestige_pointer` index is the approved, documented bridge** while the permanent cross-project (global-tier) distribution stays **DEFERRED** (Aaron, 2026-10-06). The index is rebuilt nightly from a full-store export of **all** scopes — Metro's 997 rows are already in it.

**Metro's gap (→ FEAT-2326609814, P2, lane work):**
1. `claude-startup.js` + startup summary text — remove the `mcp__vestige__search` instruction; use `session_start`.
2. `harness/claude-memory-prefetch.js` — find-then-pull with `--scope metropolis`.
3. The GR#14 UserPromptSubmit reminder text that names `mcp__vestige__search`.
4. Any other free-text `recall` callers in `harness/`.
Acceptance + constraints (independent GR#23 round, CI green, edit the `harness/` copies not the root shims) are in the BOW item.

---

## 4. Forge investigation + missing GGRs

I went looking because GGR67 was referenced but not in Metro's `GGR.md`, and the forge is the SSOT.

### 4.1 What's where

| GGR.md render | Rules present | Notes |
|---|---|---|
| Metro `E:/git/Metropolis/GGR.md` | **1–66** | generated 2026-10-06; **missing GGR67** |
| forge `E:/GoogleDrive/Tools/new/GGR.md` | **1–63** | the forge's *own* on-disk render is the **stalest** — its DB is ahead of it |
| SAFE `E:/projects/63-SAFE/GGR.md` | **1–67** | current; includes GGR67 |

The forge **DB** (`forge.rule` table, reached via `forge.js`, on `E:/GoogleDrive/Tools/new`) is the single source of truth. SAFE rendered 67 rules from it, so the DB holds **≥67**. The on-disk `GGR.md` files are per-project snapshots and **each drifts independently** — there is no mechanism forcing them to re-render when the DB gains a rule.

### 4.2 The missing rule(s)
- **Metro is missing GGR67.** Fix is mechanical: `node E:/GoogleDrive/Tools/new/forge.js ggr --out GGR.md` from Metro (re-render), plus `ggr-mark metropolis`. *(Caveat: `forge.js` lives on a synced folder, so running it from my session is classifier-blocked — this is a run-it-yourself / permission-rule step, same class as the finder.)*
- **The forge's own `Tools/new/GGR.md` is missing GGR64, 65, 66, 67** vs its DB — worth a separate forge-side re-render; a stale SSOT render is how a project ends up citing rules nobody can read.

### 4.3 Related doc drift already on record
- `MEMORY.md` note *[Forge sync 2026-10-06]* flags that Metro's `CLAUDE.md` still says **"63 rules"** (stale; it also references GGR64/65/66 in prose, so the number lags the content).
- `CLAUDE.md` open items from that note: GGR65 lifecycle vs Metro Dev-Team Process convergence (FEAT-2326609810), GGR58v02 dispatch-guard gap.

### 4.4 Recommendation
1. Re-render Metro `GGR.md` from forge to pick up GGR67 (mechanical; needs forge.js run outside the classifier block).
2. Land **FEAT-2326609814** (the caller migration) as lane work.
3. Fix the `CLAUDE.md` "63 rules" count and the stale startup/GR#14 `mcp__vestige__search` text as part of (2).
4. Forge-side: re-render `Tools/new/GGR.md` from its own DB so the SSOT render isn't 4 rules behind.

---

## 5. What I had to do this session (ledger)

| # | Task | Outcome | Refs |
|---|------|---------|------|
| 1 | Re-file the wrong Vestige "degraded" memory | Done — new `vestige-strata-recall`, old note SUPERSEDED, index fixed | memory files |
| 2 | Stale commit-msg hook | Reinstalled both hooks; now healthy; old copy backed up | `harness/claude-committhook-install.js` |
| 3 | 3 uncommitted `githooks/` changes | Forge-asset parameterisation; independent round ACCEPT; committed + pushed | `9efb9f9`, **FEAT-2326609812** |
| 3a | Test-coverage gaps from the round | Filed | **FEAT-2326609813** (P3) |
| 3b | committhook-install writes to real `.git/hooks` under TEMP-in-repo | Filed | **BUG-1106** (P3) |
| 4 | GGR67 Vestige-caller migration | Spec'd + registered as lane work | **FEAT-2326609814** (P2) |
| 5 | GGR drift investigation | This report | — |

**Open loops at time of writing:** CI run for `9efb9f9` (37503310982) queued — FEAT-2326609812 not marked `done` until CI is confirmed green (GR#28). FEAT-2326609814 awaiting dispatch.
