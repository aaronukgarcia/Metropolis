# FEAT-084 follow-up ruling — flagged/edge/ST items

**FOR BEV** (Aaron-directed, 2026-08-17). Consolidated ruling target for the FEAT-084 batch-1 execution follow-ups that Bob's executors surfaced. None were auto-filed by Bob — queued here for a single ruling so the dispositions apply once, not piecemeal.

## (1) FIX — soundness gap (needs a BUG item)

- **ASM-1295** — `engine.maintenance` config/data validation leaves `EngineerDaysPerYear` / `LifetimeYears` / cost figures positive-unbounded; a near-MaxInt64 authoring value silently saturates at load time (SEC-117 shape). Re-classified FIX, not a balance close.

## (2) GR#25 edge registrations — master-plan/code.json edge must land before the fold prose goes live

- **ASM-1004** — feat.minetypes → engine.mining (BlightAPI ordinal reconciliation).
- **ASM-999** — wellbeing ↔ faith (cohesion fed via community-access surface).
- **ASM-1021** — refuse → wellbeing (`ReportPollutionExposure` seam, GUID da2c5c2a).
- **ASM-1012** — feat.harbour → engine.coastal (cross-cut unregistered/ambiguous).

## (3) ST — convert to new BOW items (new CitizensAPI surface / registrations)

- **ASM-1039** — CitizensAPI attainment/stage-history write command (education holds attainment in its own pupil record today).
- **ASM-1056** — CitizensAPI life-event-stream subscription (education `RemovePupil` is a manual bridge).
- **ASM-1344** — CitizensAPI `LifeEventSocial` command kind (social writes AC-6 marker via `LifeEventHealth→HealthBand` today).
- **ASM-908** — feat.factorytypes missing code.json registration (register-guid).
- **ASM-1174** — feat.pharmacampus → engine.freight outbound edge (`TradeEdge.AddExports` → `FreightAPI.Export`).

## (4) Re-classified light items (CC/SF doc-fixes — none balance, none soundness; still open)

ASM-863 (SF: ui.diagrams unfilled placeholder + empty DiagramAPI consumers) · ASM-971 (CC: firms demand-signal proxy) · ASM-1041 (SF: crime precursor semantics) · ASM-1254 (CC: fdi test-adapter scaffolding) · ASM-1428 (SF: coastal world-conditions shape) · ASM-1432 (SF: coastal per-case-per-month requisition) · ASM-1457 (CC: coastal friction finite-guard bounds) · ASM-1458 (CC: coastal SEC-233/234 ceilings) · ASM-774 (CC: secret-guard allowlist doc record) · ASM-849 (SF: harness.headless stale line refs) · ASM-1411 (CC: ledger resource ceilings).

## (5) Deferred CC-BAL (6 balance items excluded from the auto-close sweep to avoid file collision; still open, ready to close citing the balance-number regime)

ASM-1099, ASM-1103 (engine.wellbeing) · ASM-331 (engine.firms) · ASM-327 (engine.crime) · ASM-323 (engine.coastal) · ASM-329 (engine.defence — fold is close-note-only; `engine.defence.md` is in Bev's dirty set).

---

**Context:** Bob's batch-1 execution closed ~111 ASMs (91 balance + 20 spec-folds) across wellbeing/citizens/freight/coastal/firms/crime/fiscal-circuit and the 92-item CC-BAL sweep. Prep notes: `docs/planning/feat-084-ba-prep.md` and `docs/planning/asm-disposition.md`. No git mutation by any executor; all BOW writes via `claude-bow.js`.
