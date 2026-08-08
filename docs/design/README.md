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
| `errors.md` | `foundation.errors` | MOD-002 | in progress — not yet doc-passed |
| `protocol.md` | `int.protocol` | INT-001 | in progress — not yet doc-passed |
| `save-format.md` | `int.serializer` | INT-002 | in progress — not yet doc-passed |
| `solver-contract.md` | `int.solver` | INT-003 | in progress — not yet doc-passed |

(Table updated as each doc is doc-passed and as new design docs are added under later sprints.)

## Freeze review packet for Aaron

Sprint 0's exit gate (`docs/planning/sprint-plan-v1.md` §3) is Aaron reviewing and freezing v1 of the three contracts — `int.protocol`, `int.serializer`, `int.solver` — plus the error registry foundation (`foundation.errors`) they all depend on. This section will collect, for each of the four Sprint-0 documents above, a doc-passed link and a one-line "ready for freeze review" note once the Tester has passed the item and the Documentation pass is complete.

Nothing is listed here yet — all four documents are still in progress.
