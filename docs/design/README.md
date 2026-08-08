# docs/design/ — Design Documentation Index

This directory holds the low-level design documents for Sprint 0's frozen contracts and the foundation modules that back them. It is maintained by the Documentation role (see `docs/planning/dev-team-process.md`) as the persistent, standing owner of documentation consistency across `docs/design/`.

## Purpose

Each document in this directory is the detailed design for one BOW item — an interface contract (`int.*`) or a foundation module (`foundation.*`) — expanding on the summary already carried in `docs/METROPOLIS-MASTER-v2.1.md` and the BOW item's own `desc`/`specRef` fields. These are the documents Aaron reviews to freeze v1 of each Sprint 0 contract (`docs/planning/sprint-plan-v1.md` §3).

## House style (self-serve checklist for authors)

Every design doc in this directory must:

1. **Open with a header block** stating:
   - What the document is (one sentence).
   - The BOW mkey (e.g. `int.protocol`) and short code (e.g. `INT-001`).
   - The spec refs it expands on (master doc `§` numbers and/or amendment refs), matching the BOW item's `specRef` field exactly.
   - Status: `draft` / `awaiting freeze review` / `frozen`.
2. **Use master-doc terminology exactly** — e.g. "daily tick" (the 30 logistics ticks inside a calendar month) vs "monthly tick" (the fixed-phase economic/demographic resolution at cycle end); "view subscription" (named protocol projection, deltas pushed only while live); "shard" (one of the 256 fixed determinism shards); GUID-style codes as `MET-<layer><NNN>` for errors. Terminology follows `docs/METROPOLIS-MASTER-v2.1.md` — check any term you're unsure of against it before using it.
3. **Verify every cross-reference before writing it.** `§` references must point at real sections of `docs/METROPOLIS-MASTER-v2.1.md` (Part III game systems §1–§55, or the UI-SPEC / M0-ENG sections folded into it); file-path references must be real repo paths. No invented section numbers, no dead links.
4. **Write in British English**, plain engineering register — no marketing tone.
5. **Preserve open questions.** Every "open questions" section a junior developer writes stays in the document. The Documentation role curates (reorders, deduplicates, clarifies wording) but never deletes a junior's open question — unresolved questions are surfaced to Aaron at freeze review, not quietly dropped.

Documents in this directory are edited by their owning junior developer while in progress. The Documentation role does a pass only **after** an item is test-clean (Tester PASS), per the flow in `docs/planning/dev-team-process.md`.

## Index

| Doc | BOW mkey | Code | Status |
|---|---|---|---|
| `errors.md` | `foundation.errors` | MOD-002 | tested PASS — awaiting freeze review |
| `protocol.md` | `int.protocol` | INT-001 | tested PASS — awaiting freeze review |
| `save-format.md` | `int.serializer` | INT-002 | tested PASS — awaiting freeze review |
| `solver-contract.md` | `int.solver` | INT-003 | tested PASS — awaiting freeze review |

(Table updated as each doc is doc-passed and as new design docs are added under later sprints.)

## Freeze review packet for Aaron

Sprint 0's exit gate (`docs/planning/sprint-plan-v1.md` §3) is Aaron reviewing and freezing v1 of the three contracts — `int.protocol`, `int.serializer`, `int.solver` — plus the error registry foundation (`foundation.errors`) they all depend on. All four wave-2 items have passed testing and had a documentation pass; ready for Aaron's review.

| Doc | mkey / code | Open questions | Notes |
|---|---|---|---|
| `protocol.md` | `int.protocol` / INT-001 | 5 (§7) | The engine↔UI contract; MOD-008/009/012/013 all block on this freezing. |
| `save-format.md` | `int.serializer` / INT-002 | 5 | Save format IS the fixture format (M0-ENG §2.2, H-REPLAY); binary-format size threshold (A3) still unset. |
| `solver-contract.md` | `int.solver` / INT-003 | 5 | CPU/GPU/cloud offload seam; four `ProblemKind` slots, three are minimal stubs pending owning engine modules. |
| `errors.md` | `foundation.errors` / MOD-002 | 5 | Every other module depends on this one; GR#1/GR#7 enforcement. |

**Two cross-cutting freeze questions the lead is adding, on top of each document's own open questions:**

(a) **OD matrix cell precision: `f32` vs `f64`.** Logged on INT-003 (`solver-contract.md` open question 1): R3's worked example ("~5,000 zones ⇒ ~100 MB of OD") only reconciles if OD cells are `float32`; the current `TrafficAssignmentRequestV1`/`VDFParamsV1`/`sizing.go` use `float64` throughout, which gives ~200 MB for the same worked example. Needs a decision before `engine.traffic` builds against the sizing tables.

(b) **Duplicate correlation-ID generators.** `internal/protocol` (`envelope.go`, referenced in `protocol.md` §2 — "Minted by the initiating side (`NewCorrelationID()` or caller-supplied)") and `internal/foundation/errs` (`correlation.go`, documented in `errors.md` under "Correlation IDs") each mint their own UUIDv4 correlation IDs. This is deliberate v1 duplication — `int.protocol` is neutral ground (`internal/protocol/doc.go`: "imports nothing from internal/engine or internal/ui") and cannot depend on `internal/foundation/errs` without breaking that ban — but it means two independent UUIDv4 implementations exist for the same concept. Freeze review should confirm this duplication is acceptable for v1 or direct that a shared leaf package (below both, imported by both, itself neutral ground) mint correlation IDs once.

Neither (a) nor (b) is currently listed as an open question inside the four documents themselves; they are cross-cutting concerns the lead is layering on top at the freeze-review stage, so this table — not the individual docs — is the right place for them.
