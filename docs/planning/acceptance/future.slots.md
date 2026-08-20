# Acceptance criteria — future.slots (FEAT-023)

**BOW code:** FEAT-023
**Spec refs:** §16; V.1 out-of-scope; §29 (LLM) — as cited on the BOW item (`node claude-bow.js show FEAT-023`).
**Date:** 2026-08-16
**Status:** RESERVED — documentation-of-non-work. No build ACs; the item must NOT be built.
**Deliverable:** this acceptance file only. No code is built for this item.

## Purpose

`FEAT-023` is a **reserved future-dev slot**: it records systems that are explicitly out
of scope for Baseline One and must **not** be built now. The reserved slots are (from the
BOW item):

- Channel Tunnel mega-project (M11 slot)
- Dynamic world market + finite migration pool (Market/world hooks are ready as *seams*, not implementations)
- Multiplayer / shared worlds (architecture-ready, shelved)
- LLM soft layer for ticker/advisor prose (optional, online, **never** the number cruncher)
- Audio: none
- Localisation: English-only v1
- Modding: the JSON data files themselves

**What supersedes the build:** the Baseline One spine (FEAT-083) uses coarse
approximations for these systems. The seams (registered interfaces / hooks) stay honest —
present as placeholders where the master plan already declares them, with no fake
implementation that pretends a reserved system is live.

## Acceptance criteria

The item stays **reserved**; the only check is that it **stays inert** — none of the
reserved systems may be built or faked as built.

- **AC-1 (stays inert).** No reserved slot has a real production implementation:
  `node claude-bow.js show FEAT-023` still reports `Status: open` (not `done`), and a
  source scan of `internal/` / `cmd/` shows no package or binary whose purpose is one of
  the reserved systems — no multiplayer/shared-world runtime, no Channel Tunnel
  mega-project content beyond the coarse Baseline One approximation, no LLM prose
  generation on the tick path, no audio subsystem, no non-English localisation, and no
  binary/scripted mod loader (modding is the JSON data files only). Expected result: the
  item remains open and no reserved subsystem exists as production code. If any reserved
  system is found implemented, the reservation has been breached and this item must be
  reopened with Aaron.

No build, vet, or test gates apply — there is deliberately nothing to build.

## Out of scope

- Building any of the reserved systems — that is future work gated by Aaron and a future
  sprint, not this item.
- Removing the honest seams that already exist (the ready Market/world hooks) — the point
  is that they stay honest placeholders, not that they get deleted.

## Escalations

- None.

- **ASM-792 (fold).** The reserved future-dev slots (Channel Tunnel M11, dynamic world market + finite migration pool, multiplayer/shared worlds, LLM soft layer for prose only, audio none, localisation English-only v1, modding = JSON data files) must not be built or faked as built for Baseline One; the existing Market/world hooks stay honest seams, not implementations. AC-1 asserts no reserved subsystem exists as production code and the BOW item stays open.
