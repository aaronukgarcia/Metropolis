# BUG-1097 / Q100145: relax the handshake build-string gate (with audit)

**Mkey:** BUG-1097 (vehicle) under FEAT-1972079936 Phase 4 / FEAT-2326609775.

**Aaron ruling (2026-09-11, Q100145):** *relax with two conditions, and be sure to record in a server-side audit the mismatched client, and also in the client-side debug files.* ASM-1510 accepted.

**GR#25 scope:** changes stay inside already-registered modules and their existing contracts: `int.protocol` (`internal/protocol/wsserver` handshake), `cmd.metroserve` (hosted server, health), the webconsole (`LiveEngineBadge.tsx`, `protocolClient.ts`, `debugjson.ts`, `captureBeforeWipe.ts`), and `internal/converge` (`Report`). No new code.json edges: the audit record uses the existing registry-error path (`errs.New` + the server's correlation id), the converge stamp is a new field on the existing `Report` struct, and the client records into files it already writes.

---

## Background (verified on disk 2026-09-11)

`internal/protocol/wsserver/server.go` line ~539: `if normalizeVersion(params.ClientVersion) != normalizeVersion(s.engineVersion)` refuses the handshake with `MET-P010` (`ErrHandshakeVersionMismatch`). `normalizeVersion` strips only `-dirty`. Both sides derive the string from `git describe`; the GitHub runner emits a 7-character short hash (`v0.3.0-609-g3b657a7`) while a local checkout emits 8 (`v0.3.0-609-g3b657a70`), so the SAME commit was refused the first time a browser reached the Azure engine. The protocol semver window (Phase 0 inc2) and capability flags (inc3) are the real compatibility contract; the wire version never reaches the engine or the journal, so the build-string gate protects no determinism property.

## Design

- The handshake **never refuses on build string**. It still carries `ClientVersion` (request) and `ServerVersion` (response) unchanged, so both sides know both builds.
- **Server audit:** on every accepted handshake whose `normalizeVersion(ClientVersion) != normalizeVersion(engineVersion)` the server emits ONE registry warning through the same path handshake errors use today (`errs.New` with the connection's correlation id) carrying `{clientVersion, serverVersion, negotiatedWire, remoteAddr-or-connection-id, tenantId, cityId, at}`. The code is a NEW warn-level `MET-P0xx` claimed via `node tools/plan/add-error.js claim-range int.protocol --size 10` then `add` (module-key owner, never a BUG-/FEAT- owner, or the Go scanner reds CI). It is logged to the server's stdout/stderr sink the way `MET-P010` is today AND counted on `/health` (`buildMismatchHandshakes` monotonic counter since process start) so it is observable without log access. Never silent, never fatal.
- **Client debug files:** `protocolClient.ts` exposes `serverVersion` and `buildMismatch: boolean` after the handshake; `debugjson.ts` writes `meta.engineBuild`, `meta.clientBuild`, `meta.engineBuildMismatch` (present only when a live-engine handshake has completed in this session, else absent); the GR#27 pre-wipe capture (`captureBeforeWipe.ts`) carries the same three fields because it is built from the same debug JSON.
- **Badge:** `LiveEngineBadge.tsx` shows a distinct warning state (colour + tooltip `engine <serverVersion> / client <clientVersion>`) when `buildMismatch` is true; the connected state is otherwise unchanged. It never blocks.
- **Converge stamp:** `internal/converge` `Report` gains `ReferenceBuild` and `CandidateBuild` strings; `Compare` callers pass them; the report text prints both. A comparison with either stamp empty is a validation error, not a silent report.
- `MET-P010`'s registered meaning becomes "reserved - build-string refusal retired by Q100145 (BUG-1097)"; it is no longer emitted. inc2's temporary reuse of it (server.go ~556) is retired in the same change or explicitly left with a comment naming this doc.

## Acceptance criteria

### AC-1 (same protocol, different build string CONNECTS)
**Scenario:** server engineVersion `v0.3.0-609-g3b657a7`, client `v0.3.0-609-g3b657a70` (the observed pair), then `v0.2.0-1-gabc1234`, then `dev`. **Check:** each handshake is accepted, `negotiated` is the current wire version, and commands flow (a SetSpeed round-trip succeeds). **Mutation:** restore the equality refusal - all three red. **False-pass:** a test that only asserts "no error" without a subsequent command round-trip.

### AC-2 (protocol window still refuses)
**Scenario:** a client whose `clientMaxVersion` major is below the window floor. **Check:** refused with the existing below-floor code and message, exactly as before this change (pin the code and text). **Mutation:** widen the window by one - red.

### AC-3 (server audit record, every mismatch, once per handshake)
**Scenario:** AC-1's first pair. **Check:** exactly ONE registry warning with the new code is emitted per accepted mismatched handshake, carrying clientVersion, serverVersion, negotiated wire version, correlation id, tenant and city; a matching client emits NONE; the correlation id equals the one the handshake response carries. **Mutation:** drop the emit - red; emit twice - red; emit on match - red. **False-pass:** asserting the log line by substring only; the test must decode the registry error's fields.

### AC-4 (the audit code is registered and template-clean)
**Check:** `node tools/plan/add-error.js check` OK; the Go registry test (`internal/foundation/errs`) green; message has no malformed brace tokens (BUG-885 class); the code's owner is `int.protocol`. **Mutation:** use an unregistered code - the MET-F003 fallback text appears and the test reds.

### AC-5 (health counter)
**Scenario:** three mismatched handshakes and two matching ones on one process. **Check:** `/health` reports `buildMismatchHandshakes: 3`, monotonic, zero at boot, and the field is always present. **Mutation:** count matching handshakes too - red (5). **False-pass:** reading the counter through the same atomic the test increments.

### AC-6 (client debug files carry the mismatch)
**Scenario:** the webconsole connects to a live engine whose build differs. **Check:** `buildDebugJson()` output has `meta.engineBuild` = the server's string, `meta.clientBuild` = `versionRaw`, `meta.engineBuildMismatch === true`; with a matching build `false`; with NO live-engine session in this run all three keys are ABSENT (not null). The GR#27 pre-wipe capture contains the same three values. **Mutation:** write `clientBuild` into `engineBuild` - red; omit the mismatch flag - red; write `false` when no session - red (absent required).

### AC-7 (badge warning state, non-blocking)
**Scenario:** tsx mount of `LiveEngineBadge` with a fake socket answering a handshake whose serverVersion differs. **Check:** the badge renders the connected tick AND a warning class/attribute with the tooltip naming both builds; speed control still dispatches. Matching build: no warning class. **Mutation:** warning shown on match - red; connection refused on mismatch - red.

### AC-8 (converge stamps)
**Scenario:** `Compare` for the finance domain with `ReferenceBuild`/`CandidateBuild` set. **Check:** the `Report` carries both and the rendered report text prints both; empty stamp = validation error. **Mutation:** stamps swapped - red (the test uses two distinct strings and asserts placement).

### AC-9 (MET-P010 retired honestly)
**Check:** no production code path emits `MET-P010` after this change (grep pin over `internal/` and `cmd/`, excluding tests and the registry); its registry message says it is retired by Q100145. **Mutation:** re-add an emit - red.

### AC-10 (byte-identity of everything else)
**Check:** with matching build strings the handshake response JSON is byte-identical to trunk's (recorded golden); the wire codec, envelopes and command path unchanged (existing wsserver, protocol and metroserve suites green); determinism gate green.

### AC-11 (determinism / GR#21)
No `time.Now` in the audit field `at` other than through the server's existing clock source used for correlation ids; no map-range iteration in the new code paths; `-race -count=2` on `internal/protocol/wsserver` and `cmd/metroserve`.

### AC-12 (deploy-on-green-main is allowed but NOT switched on here)
This change only removes the reason the workflow's header gives for staying manual. Switching `azure-deploy.yml` to trigger on green `main` is a separate, explicitly reviewed change (it also needs BUG-1095 committed).

## Files expected to change
`internal/protocol/wsserver/server.go` (+tests), `data/errors.json` (new warn code, MET-P010 message), `cmd/metroserve/health.go` (+tests), `internal/converge/compare.go` + report rendering (+tests), `webconsole/src/sim/protocolClient.ts`, `webconsole/src/sim/debugjson.ts`, `webconsole/src/sim/captureBeforeWipe.ts`, `webconsole/src/components/LiveEngineBadge.tsx` (+ tsx test), docs: `docs/planning/azure-cloud-engine-design.md` §1.3 amendment, `docs/planning/azure-runbook.md` §4.

## Out of scope
Deploy-on-green-main trigger (AC-12); full-hash normalisation on both sides (moot once the gate is gone); the one-replica-per-city lease (Q100146).

## Open questions for Aaron
None; the ruling was explicit. Placeholder: the audit warning's severity is `warn`, not `error`, so a mismatch never trips error-rate alarms - say if you want it louder.
