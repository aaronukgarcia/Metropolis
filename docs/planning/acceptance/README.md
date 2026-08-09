# Acceptance criteria — standard gates

**Date:** 2026-08-08
**Status:** active
**Owner:** BA

This file holds the checks common to every wave-2 acceptance file
(`MOD-002.md`, `INT-001.md`, `INT-002.md`, `INT-003.md`). Each item file
references this section by name ("Standard gates, see README.md") instead
of repeating it. The Tester runs every gate below for every item, in
addition to the item's own numbered AC list.

## Standard gates (run from repo root, `E:\git\Metropolis`)

- **SG-1 Build:** `go build ./...` exits 0, no output.
- **SG-2 Vet:** `go vet ./...` exits 0, no output.
- **SG-3 Format:** `gofmt -l .` outputs nothing (no files listed as needing formatting).
- **SG-4 Package tests:** `go test <item-package(s)> -race -count=1 -v` exits 0; every test listed PASS, none SKIP unless the skip reason is itself asserted by another passing test.
- **SG-5 Forbidden-touch:** `git status --porcelain` (after the junior's change, before commit) shows modified/added paths only under the item's declared `path:` (from `node claude-bow.js show <mkey>`) plus, where the item's own AC list explicitly calls for it, `data/errors.json`, `code.json`, or a `docs/design/*.md` file. Any path outside that set is a FAIL.
- **SG-6 No Co-Authored-By:** `git log -1 --format=%B` for the item's commit(s) contains no `Co-Authored-By:` trailer (belt-and-braces on top of the `claude-pre-commit-check.js` hook).
- **SG-7 Determinism smoke:** for any package whose AC list requires "no wall-clock time on the tick path" or "no `time.Now()`", run `grep -rn "time.Now" <package-dir>/*.go` (excluding `_test.go` files) and confirm every match is inside the specific injectable-clock plumbing the item's AC list names — an unexplained match is a FAIL.

## How to read an item file

Each item file (`MOD-002.md`, `INT-001.md`, `INT-002.md`, `INT-003.md`) has:
1. Header — mkey, BOW code, spec refs, date, status.
2. Scope — one line.
3. AC list, grouped **Functional / Error handling / Determinism & safety / Documentation**. Every AC is a command + expected observable result, or a checkable file/property.
4. Out of scope — explicitly listed so absent future work is not a FAIL.
5. Escalations — spec/brief conflicts or untestable requirements, for Bill. May be empty.

A PASS on an item requires **all** Standard gates above AND every AC in the item's own list. A single FAILing AC (numbered or standard-gate) is enough to bounce the item back to the same junior with the exact AC/gate ID that failed.

## Conventions ratified during Sprint 1

- **Per-module error subranges are claimed at build time by the owning
  module**, not pre-allocated in a master table. When an item's junior
  developer needs registry-sourced errors (GR#7), they claim the next free
  subrange within their layer's block in `data/errors.json` and record the
  claim there (e.g. `ui.screen.map`/FEAT-005 claimed `MET-U100`–`U100-U199`
  for the F1 map screen — see `data/errors.json`'s range table and
  `internal/ui/screens/map/errors.go`). The Tester's SG-5 (forbidden-touch)
  gate already allows `data/errors.json` as a sanctioned touch path for
  exactly this reason.
- **`_test.go` files are exempt from the GR#20 depguard ban** on
  `internal/ui` importing `internal/engine`. GR#20 (Contract-First,
  Stub-Forever) protects *production* decoupling — UI code must consume the
  engine only via `internal/protocol` — but test files may import
  `internal/engine/stub` to build fixtures (the sanctioned H-STUB test
  path). This is enforced mechanically in `.golangci.yml`'s
  `depguard.rules.ui-must-not-import-engine.files` list (`"!**/*_test.go"`
  exclusion), lead ruling 2026-08-09. Example: `internal/ui/screens/map/map_test.go`
  imports `internal/engine/stub` for fixture JSON; `internal/ui/screens/map/screen.go`
  and its non-test siblings do not and must not.
