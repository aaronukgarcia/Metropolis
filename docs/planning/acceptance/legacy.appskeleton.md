# Acceptance criteria — legacy.appskeleton (MOD-001)

**BOW code:** MOD-001
**Spec refs:** M0-ENG §5 (Go monorepo — `cmd/metropolis`, `internal/...`, no `app/` directory); M0-ENG §3 (version/build info injected via `-ldflags` from `git describe`, never hand-maintained files); the BOW item's own comments — Bill's 2026-08-08 16:03 BLOCKED note and Aaron's 2026-08-08 16:15 CANCELLED note (`node claude-bow.js show MOD-001`).
**Date:** 2026-08-16
**Status:** RETIRED — documentation-of-non-work. No build ACs.
**Deliverable:** this acceptance file only. No code is, or will ever be, built for this item.

## Purpose

`MOD-001` was the Prix Six `app/package.json` + `app/src/lib/version.ts` application
skeleton. It was **cancelled** by Aaron on 2026-08-08 (recommendation (a) accepted):
Metropolis is a Go monorepo (M0-ENG §5), and version/build info is injected via
`-ldflags` from `git describe` at build time (M0-ENG §3) — the hand-maintained two-file
version pattern is exactly what the stack bans.

**Superseded by:** MOD-003 `foundation.repo` (Go monorepo skeleton + CI pipeline +
build-info injection). The version-guard retarget that retired the two-file check is
tracked separately in FEAT-002 `legacy.versionguard`
(`docs/planning/acceptance/legacy.versionguard.md`).

## Acceptance criteria

The item is RETIRED; the only check is that it **stays inert** — the retired two-file
layout must never reappear.

- **AC-1 (stays inert).** The retired Prix Six layout is absent from the repo:
  `test ! -e app/package.json && test ! -e app/src/lib/version.ts` exits 0 (equivalently,
  `git ls-files` shows no tracked `app/` path). Expected result: no `app/` directory and
  no `app/package.json` / `app/src/lib/version.ts` at the repo root. If either path ever
  reappears, the cancellation decision has been regressed and this item must be reopened
  with Aaron.

No build, vet, or test gates apply — there is no code to build for a cancelled item.

## Out of scope

- Retargeting `claude-version-guard.js` off the retired two-file check — that is FEAT-002
  `legacy.versionguard`, already landed.
- Any change to how `-ldflags` injects version info into the built binary — that is
  MOD-003's build/CI concern.

## Escalations

- None. The cancellation decision is recorded on the BOW item itself.
