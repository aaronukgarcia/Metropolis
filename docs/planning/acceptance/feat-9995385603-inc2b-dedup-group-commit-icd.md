# ICD: inc2b server-side command dedup and journal group commit (FEAT-9995385603)

**Status:** DRAFT for lead/Aaron review, contract only, no code. Written 2026-11-17 by BA/designer (Sonnet). GR#20 contract-first: nothing below is built until this is accepted and the OQs are ruled.
**Split from:** FEAT-9995385594 inc2, AC-J4..J7 (rejected in review 2026-11-17, see `feat-9995385594-inc2-r2-store.md` section D and review notes).
**Builds on:** inc2a (the Store: S3/R2, journal v2 envelope, segments, epoch fencing) and FEAT-9995385597 (`clientSessionId`, `command.dedup` capability, client retry).
**Lead rulings applied (BOW item FEAT-9995385603, 2026-11-17):** session source is `clientSessionId` from the handshake; dedup window is rebuilt from journal v2 records after restart; a retried already-applied command returns "duplicate, already applied" (no stored original results); the journaler/result-timing contract is this ICD; the batch timer is below the 160 ms turbo tick (Q100147). A2Bev001 v23.3: strict head-CAS per flush (Q-2), cheap save cadence.
**Facts checked in code (2026-11-17):** `core.CommandJournaler.ObserveCommand(cmd) error` is synchronous, called from `Engine.accept` via `journalAccepted` inside the tick (`internal/engine/core/commands.go:359,464`); only ACCEPTED commands are journaled, rejected ones never are; a failed append latches the BUG-472 persist halt (MET-E023); `protocol.Command` has no session field (`internal/protocol/envelope.go:103`); the wsserver `handleCommand` calls `transport.SendCommand(cmd)` (non-blocking enqueue) and answers `{"queued":true}` immediately, and the real `CommandResult` arrives later on `Transport.Results()` (`wsserver/server.go:914-990`); `CommandResult` has `{correlationId, tick, accepted, error?, result?}` and requires Error iff not Accepted (`envelope.go:214`); disk journal frame is `[4B len][payload][4B CRC32(len||payload)]`, one `Sync` per record (`persist/journal.go`, `diskstore.go:271`); BUG-1197 repair cuts a torn tail only when no complete valid frame follows (else MET-P063 fail closed).

---

## 1. Summary of the contract (ten lines)

1. Identity of a command for dedup is `(tenant, city, clientSessionId, correlationId)`; tenant/city come from the journal's CityKey, so the in-city key is `(session, correlationId)`.
2. The server stamps the connection's `clientSessionId` onto each inbound command in memory only (never on the client wire, never inside the engine's state); an absent session means no dedup (legacy), exactly today's behaviour.
3. The journal v2 record carries `session` and the Command's `correlationId`; the dedup table is derived state, rebuilt on wake from the tail plus a bounded look-back before the snapshot's sealed position. No new durable structure.
4. A duplicate (key already applied) is never sent to the engine, never journaled, never advances `seq`; the client gets `CommandResult{accepted:true, result:{"duplicate":true,"seq":N}}` (duplicate, already applied; the original result body is NOT reproduced).
5. A duplicate of a command that is still waiting for its batch is held and released only when that original's batch is durable.
6. Group commit: `ObserveCommand` becomes a non-blocking in-memory enqueue that assigns `seq`; a host-owned flusher goroutine writes one batch (one fsync on disk; one create-only object PUT plus one head CAS on S3), one flush in flight at a time.
7. Ack means durable: no `CommandResult` (and, per OQ-3, no Delta/Event derived from it) leaves the host until `durableSeq >= the command's seq`. The gate sits between the engine's outputs and `Transport.Results()/Deltas()/Events()`.
8. Batch triggers: first-record timer `P-batch-ms` = 40 ms (hard max 80 ms), `P-batch-records` = 64, `P-batch-bytes` = 1 MiB, tick-boundary hint, and "flush again immediately when the previous flush returns and records are waiting". Timer is an injected clock and never reaches the engine (GR#21).
9. On disk a batch is ONE outer frame (one CRC over the whole batch) so a torn batch is all-or-nothing and can never look like mid-file corruption (section 4.3); v1 frames stay readable; v2 is selected by a magic byte.
10. A flush failure or a fenced head CAS halts the city under BUG-472 policy from the flusher goroutine; every held result becomes the MET-E023 rejection; no later command is acknowledged; only un-ACKed commands can be lost.

---

## 2. Naming and parameters

Error-code reservation owner for new codes is the persist/compose module key per the add-error rule (claim-range, add, check; never `FEAT-`/`BUG-` owners, which redden the Go scanner). Codes below are PROPOSALS; nothing is registered by this document.

| Parameter | Default | Bound | Note |
|---|---|---|---|
| `P-batch-ms` | 40 ms | max 80 ms, injected clock | timer starts at the first record into an empty batch; below the 160 ms tick |
| `P-batch-records` | 64 | 1..1024 | |
| `P-batch-bytes` | 1 MiB | 64 KiB..4 MiB | a single record larger than this is a batch of one |
| `P-inflight-max` | 4096 records not yet durable | | admission control, section 3.5 |
| `P-dedup-keys-per-session` | 4096 | | ASM-1564 default kept |
| `P-dedup-sessions` | 64 | | LRU by last applied seq |
| `P-dedup-keys-total` | 16384 | | hard city cap, memory about 1 MB |
| `P-dedup-lookback` | 16384 records | | records read before the snapshot's sealed position at wake |
| `P-client-inflight-max` | 256 | client side | must stay well under `P-dedup-keys-per-session` so a retried in-flight command is always inside the window |

All are builder-tunable constants with named defaults (no magic numbers); tests inject smaller values.

---

## 3. Dedup contract

### 3.1 Key and session source

- Key: `(session, correlationId)` within one `CityKey`. Two sessions with the same correlationId never collide; an absent session never matches anything, including another absent one (matches FEAT-9995385597 AC-P5).
- Session source: the handshake `clientSessionId` (random 128-bit, page lifetime, reused by every reconnect of that page; FEAT-9995385597 AC-P5). It is unauthenticated (Q-3 ruling). Anyone holding the token can replay under it; accepted while Cloudflare Access gates every connection and there is one user. Revisit with real tenants.
- Carrying it to the dedup point: the wire `Command` is unchanged (no wire-version change, no new client field). wsserver stamps the connection's session into the decoded command in memory only, as a non-serialised field (`Command.Session`, `json:"-"`), set after `DecodeCommand` and before `SendCommand`. The journal v2 envelope carries it durably (section 5). OQ-1 asks whether to prefer an explicit `SendCommandFrom(session, cmd)` transport method instead.
- A legacy client (no `clientSessionId`) or the in-process TUI/headless transport has an empty session: commands skip dedup, still go through group commit.

### 3.2 Scope: which kinds are deduped

Only commands that were ACCEPTED and journaled enter the table (a rejected command is never journaled, so a retry is simply evaluated again, and the second evaluation is the authoritative answer). Read-only query kinds (`KindQueryCitizen`, `KindQueryOccupants`) and Subscribe/Unsubscribe are NOT deduped: their `CommandResult` carries a body the server does not store, and a deduped reply would drop the answer or the SubscriptionID. The client must use a FRESH correlationId for them (already required by FEAT-9995385597 AC-R5; AC-R9 must exclude queries as well, see section 10). Exemption list is a registry-driven predicate `protocol.IsDedupable(kind)` (data, not an inline switch, GR#15).

### 3.3 What is stored and where

- Durable: nothing new. The key is the `session` field plus the body's `correlationId` of each journal v2 record (section 5). No result is stored (lead ruling).
- Derived (memory): `dedupTable`: per session an LRU map `correlationId -> seq`, bounded by the three caps in section 2. Owned by the journaler/host layer, not by the engine's simulated state; it is excluded from `PopulationHash`, snapshots and the determinism gate.
- Eviction affects only whether a very old duplicate would be re-applied; it never changes any state that replay depends on (replay applies only journaled records, never re-evaluates dedup).

### 3.4 Where the check runs, and the reply

- Check point: in the engine's single-threaded command path, after decode/validate and BEFORE any handler mutates state: `deduper.Check(session, corrID) (seq, hit)`. Because the tick goroutine is the only caller, check and insert are serial and need no cross-thread locking beyond the table's own mutex for the wake rebuild. The builder MUST verify that every handler reaches `accept()` only after its own mutation or that the check precedes dispatch; this ICD requires the latter (no handler runs for a duplicate). If handlers mutate before `accept`, the check moves to the top of `dispatchCommand`.
- Insert: at `ObserveCommand` enqueue time (seq assigned), state `pending(seq)`; it flips to `durable` when `durableSeq >= seq`. If the flush fails the city halts, so the table is discarded with the engine.
- Duplicate reply (hit): `CommandResult{correlationId, tick: <current tick>, accepted:true, result:{"duplicate":true,"seq":N}}`. Not appended, `seq` not advanced, engine state untouched, no events. The reply goes through the same durability gate: if the original is still `pending(seq)`, the reply is released when `durableSeq >= seq` (never confirm "applied" before it is durable).
- Not byte-identical to the original result, by ruling. The client contract: a result with `result.duplicate == true` means "this exact command was already applied; resolve the caller's promise as success without a result body".
- Capability: `command.dedup` is advertised only when the server has journal v2 + dedup live (FEAT-9995385597 AC-P1; its test that the default set lacks it stays true until then). A connection that did not negotiate it never receives `duplicate` replies and its commands are not deduped (no behaviour surprise for older clients).

### 3.5 Restart, restore, snapshot

- Wake: restore loads the snapshot, replays the tail (records after `JournalSeq`), and during that same pass inserts each record's key into the table (replay and live use the same insert function). Because a snapshot seals the segment (inc2a AC-J9), the tail alone can be empty right after a snapshot, which is exactly when a client that never saw its result retries (hibernate and wake). So restore ALSO reads the last `P-dedup-lookback` records strictly before the snapshot's `JournalSeq` from the sealed segment(s) (kept in hot storage for the retained snapshots, A2Bev001 Q100149) and inserts only their keys (no re-apply). This keeps the lead's "rebuilt from journal records" with no new sidecar. Cost: one ranged read of about 16k records, bounded and independent of history.
- Records older than the look-back are outside the window by design; the client in-flight cap (`P-client-inflight-max`) keeps legitimate retries inside it.
- Rewind/hard-reset genesis replay (FEAT-1972079897) replays from journal records: sessions and correlation ids ride along; the table is rebuilt, nothing else changes.
- v1 records (bare Command JSON) have no session: they never enter the table.

### 3.6 Determinism (GR#21)

1. The journal contains each applied command exactly once; a deduped command is absent from it. Replay of the journal therefore yields the same state with or without dedup.
2. The dedup decision depends only on the order of journaled records and caps; it uses no wall clock and no randomness. The engine sees no session and no timer.
3. `seq` order equals engine application order (assigned inside the tick at enqueue); ack timing and batch boundaries cannot change that order or any state. A property test proves state hash equality across arbitrary batch splittings.

---

## 4. Group commit contract

### 4.1 The seam (GR#20 contract change)

Today `ObserveCommand` fsyncs inline. New contract:

- `core.CommandJournaler.ObserveCommand(cmd) error` keeps its signature but changes meaning: it copies the encoded record into the batcher, assigns the next `seq`, returns nil immediately (or an error only for encode failure, an already latched halt, or full admission control). It no longer waits for durability. In-memory inner journaler order is unchanged.
- New host-owned `journal.Batcher` (package named by the builder, wired in `compose` where `persistCommandJournaler` is built) owns: the queue, the flusher goroutine, the injected clock/timer, `durableSeq` (atomic), and the halt callback.
- New `core.PersistHalter` seam (one method, `HaltPersist(err error)`) so the flusher can latch the BUG-472 halt asynchronously; today the latch is called only from inside `journalAccepted`.
- New `ResultGate` between the engine's outbound channels and the transport the wsserver reads: each outbound `CommandResult` is tagged with the highest `seq` the engine had assigned when the result was produced (the command's own seq for a journaled command; for a non-journaled result such as a rejection or a query, the current `lastAssignedSeq`, so it never overtakes an earlier un-durable command's result: ordering guarantee 4.4). The gate releases in order when `durableSeq >= tag`. With nothing pending (`lastAssignedSeq == durableSeq`) it is a pass-through with no added latency.
- Shape and package of the gate are the builder's; the properties above are the contract. Whether the gate wraps `InProcTransport` or lives in `cityhost` is for the lead (it must cover the in-process transport as well, since that is what wsserver reads).

### 4.2 Flush protocol

- One flush in flight at a time (the S3 head CAS is strictly serial per writer).
- Trigger (first to fire): record count, byte count, timer `P-batch-ms` from the first record into an empty batch, tick-boundary hint (the engine calls `Batcher.Hint()` at end of tick; it never waits), or "previous flush finished and the queue is non-empty" (no idle timer wait while a backlog exists). An empty batch writes nothing, ever.
- Disk: build one outer batch frame (4.3), one `Write`, one `Sync`; then `durableSeq = lastSeq`, release the gate.
- S3/R2: create-only PUT of `journal/<epoch>-<firstSeq>/<seq>.blk` (inc2a AC-J8), then head CAS (AC-F4, strict), then `durableSeq = lastSeq`. A 412 on the head CAS is `E-WRITER-FENCED`: halt, no retry (AC-F5). A lost-response head CAS follows AC-A7(b).
- `durableSeq` advances only after the whole flush (both writes on S3). The PUT alone is NOT durable: objects past `head.seq` are ignored on restore (section 4.6).

### 4.3 On-disk batch frame (why one outer frame)

BUG-1197's repair rule fails closed (MET-P063) when a complete valid frame follows a bad one. A batch written as N independent frames in one `write` can, after a power loss, persist later pages but not earlier ones; the file then holds a hole followed by valid frames, which the repair rule reads as mid-file corruption and refuses to open a city whose only damage is an un-acknowledged batch. Therefore a v2 batch is ONE frame in the existing framing: `[4B len][payload][4B CRC32(len||payload)]` where the payload is `0x02` (magic) | epoch u64 | firstSeq u64 | count u32 | records (uvarint length + record JSON each). Inner records carry no frame headers or CRCs, so no valid inner frame can exist after a damaged outer one. A torn batch is cut whole by the existing repair; a partial batch is by construction un-ACKed.

### 4.4 Ordering guarantees

1. `seq` is dense, assigned inside the tick in engine application order; journal order equals apply order.
2. Batches are written and made durable strictly in seq order (one in flight).
3. A `CommandResult` for seq s is never released before every seq <= s is durable.
4. Results are released to a given client in the order the engine produced them (non-journaled results are tagged with `lastAssignedSeq`, 4.1), so a reject never overtakes the ack of an earlier accepted command from the same connection.

### 4.5 Admission control, latency and fairness

- If un-durable records reach `P-inflight-max` (or the flusher is blocked on a slow store), `SendCommand` returns the existing busy-style error (`ErrCommandSendFailed` path); the tick goroutine never blocks on the store. The client treats it as "not sent" and may retry under the same correlationId (it never reached the engine).
- Ack latency per command <= `P-batch-ms` (timer) + the in-flight flush time + its own flush time. Disk target: a few ms plus 40 ms. S3/R2: expect 100 to 300 ms per flush for the two writes; worst case about 2 flush times + 80 ms. The ICD promises the ordering and the 80 ms cap on the wait to start a flush, not an S3 latency (measure in inc3, A2Bev001 cost line).
- Fairness: strict FIFO by seq across all sessions; no per-session priority or starvation is possible because a batch takes the queue head up to the caps.
- `Flush(ctx)` is exposed for the drain path: BUG-1197's stop order becomes drain notification (FEAT-9995385597) -> stop accepting -> `Flush` -> release gate -> join pump -> final snapshot. Results released after the transport closed are dropped; the client's retry then hits dedup (`duplicate`) after wake.

### 4.6 Crash semantics

- A crash can lose only commands whose results were never released (un-ACKed). After wake, the client retries with the same correlationId: if the record survived (a torn batch is all-or-nothing on disk; on S3 the head says) the retry is a `duplicate`; if not, it applies once. Either way exactly once.
- S3 crash after the batch PUT, before the head CAS: the object is an orphan. The new writer's epoch is higher, its objects live under a different prefix, restore trusts `head.seq` and ignores batch objects whose range extends past it (they are un-ACKed). GC of orphans is an inc2a retention concern.
- Torn-tail repair (BUG-1197) is unchanged in rule: cut a genuine torn tail, fail closed (MET-P063, file byte-identical) when a valid frame follows damage. With 4.3 a legitimate un-ACKed torn batch can never trigger P063. The MET-P060 remedy text must be corrected ("fsyncs per record" becomes "per batch"; the damaged-final-frame caveat now covers any command in the final batch whose ack was already released only if the disk lied about fsync).
- Fenced zombie: its un-ACKed batches are never acknowledged (the head CAS fails first, section 4.2) so nothing it holds was promised to a client.

---

## 5. Journal v2 record layout (backward compatible)

- Record body (inc2a AC-J1, golden-pinned): `{"v":2,"seq":N,"epoch":E,"kind":"cmd"|"ext","session":"<clientSessionId or empty>","body":<Command or ExternalEvent JSON>}`.
- Dedup key extraction: `session` and `body.correlationId` for `kind:"cmd"` only. `kind:"ext"` is never deduped (AC-J3).
- On disk, frames are discriminated by the first payload byte: `{` (0x7B) is a v1 bare-Command frame (read exactly as today, no session, seq assigned from position per AC-J2); `0x02` is a v2 batch frame (4.3). Any other first byte is `E-JOURNAL-CORRUPT`-class (proposed MET-P066, below).
- S3 batch objects carry the same payload bytes as the disk batch frame inside the object (inc2a AC-J8 said "CRC-framed v2 records"; this ICD asks for one outer frame per object too, for one reader code path, see section 9 conflict C3).
- Mixed journals (v1 then v2) restore; the first v2 seq continues from the v1 record count. A city that has written any v2 batch is marked `journalVersion:2` in its meta/manifest and an older binary must refuse it (downgrade guard, OQ-6) because it would misread a batch frame as a Command.
- Read API: `ReadJournal` returns typed records (seq, epoch, session, kind, body) for both versions; `Store.AppendBatch(ctx, city, records) (lastSeq, error)` replaces per-record `AppendJournal` for the persist journaler (the old method stays for v1 callers and tests).

---

## 6. Error and failure table (codes are PROPOSALS, registry untouched)

| # | Condition | Proposed code | Client-visible behaviour |
|---|---|---|---|
| E1 | Flush fails (disk write/sync error, S3 5xx exhausted) | existing MET-E023 (persist halt); store error as cause | Held results become the rejected CommandResult carrying MET-E023; city halted; later commands rejected; no snapshot (dirty latch); client treats as outcome unknown for in-flight, retries after wake with same correlationId |
| E2 | Head CAS 412 (fenced writer) | `E-WRITER-FENCED` (inc2a AC-F5) | Same as E1; city halted; never retried |
| E3 | Admission full (`P-inflight-max`) | MET-P064 `PERSIST-JOURNAL-BACKPRESSURE` (retryable) | `SendCommand` error, reply as busy; the command was never accepted; safe to retry with the same correlationId |
| E4 | Duplicate hit | none (success path) | `accepted:true, result:{"duplicate":true,"seq":N}` |
| E5 | Duplicate of a pending command and the original's batch then fails | MET-E023 / E-WRITER-FENCED | Both the original's and the duplicate's replies become the halt rejection |
| E6 | Dedup requested but session empty or `command.dedup` not negotiated | none | Command processed with no dedup, as today; never a `duplicate` reply |
| E7 | Dedup table over cap (eviction) | MET-P065 `PERSIST-DEDUP-EVICTED` (log, info, rate-limited once per city per minute) | None; a retry of an evicted key re-applies (documented bound) |
| E8 | Unknown frame discriminator / v2 batch inner decode failure | MET-P066 `PERSIST-JOURNAL-FRAME-UNKNOWN` (fail closed) | City refuses to load, same as MET-P063; no truncation |
| E9 | Journal v2 city opened by an older binary | MET-P067 `PERSIST-JOURNAL-VERSION-TOO-NEW` (fail closed) | City refuses to load |
| E10 | Torn tail on open (v2 batch cut whole) | existing MET-P060 (remedy text amended) | Logged; un-ACKed commands gone; client retries apply once |
| E11 | Mid-file damage with valid frame after | existing MET-P063 | Unchanged fail-closed |
| E12 | Look-back read fails at wake | MET-P068 `PERSIST-DEDUP-REBUILD-FAILED` | Wake fails closed (an unknown dedup window would risk double-apply), city does not load; BOW alert |
| E13 | `Flush` exceeds the drain deadline | existing BUG-1197 deadline path | Final snapshot skipped if engine ahead of journal; un-ACKed commands lost, retried after wake |

Builder: claim-range first (parallel lanes collide on codes), then `tools/plan/add-error.js`.

---

## 7. Tests the contract implies

Property and fault tests (all with injected clock, no wall-clock bounds, no sleeps for ordering):

1. State-hash equality: any partition of a command sequence into batches (random sizes, random timer fires) yields the same final state hash and the same journal seq/order; also vs. unbatched.
2. Ack-never-before-durable with a blocking fake store, and a MUTATION (release before flush) turns it red (inc2 AC-J7 requirement kept); same for the S3 sequence (PUT done, head CAS blocked: no ack).
3. Dedup exactly-once: for random duplicate injections (same session+corr, at any point: pending, durable, after restart, after snapshot+wake), the engine mutation count equals the distinct key count, journal has no duplicates, `seq` dense.
4. Two sessions, same correlationId: both apply.
5. Eviction: key beyond each cap re-applies; keys inside never do (boundary at cap, cap+1).
6. Crash-point matrix over a batch of N records (disk and S3 fake): crash at (a) before enqueue, (b) enqueued not flushed, (c) mid-write (every byte offset in a sampled set, including hole-then-valid-bytes layouts), (d) after write before sync, (e) after sync before release, (f) S3: after PUT before CAS, after CAS before release, CAS response lost, (g) mid-release. For each: restart; assert (i) every command whose result was released is present, (ii) no command applied twice after client retry, (iii) the city loads (no MET-P063 on any legitimate torn batch), (iv) state hash equals replay of the surviving journal.
7. Torn-batch variants: valid frame after hole inside a v2 batch cannot exist by construction (test constructs the pages-reordered file and asserts load succeeds and the batch is cut whole); a v1 journal with mid-file damage still fails closed with MET-P063 and a byte-identical file.
8. v1/v2 compat: v1-only, v2-only, v1 then v2 restore; golden bytes for the batch frame and the record JSON; unknown magic fails closed; downgrade guard.
9. Halt: flush failure turns all held results into MET-E023, later `SendCommand` refused, no snapshot written; fenced writer sends nothing after the CAS failure.
10. Admission: at `P-inflight-max` `SendCommand` fails fast and the tick goroutine is never blocked (assert tick completes with a store that never returns).
11. Drain: `Flush` then final snapshot; commands in flight at stop are either durable and acked, or un-acked and retriable; retry after wake yields `duplicate`.
12. Rebuild: wake with empty tail right after a snapshot still dedups a key from before the snapshot (look-back), and fails closed when the look-back read errors (E12).
13. Capability: `command.dedup` absent before live; legacy/empty-session path unchanged byte-for-byte (existing fixtures and `PopulationHash` unchanged).
14. Race: `-race` over concurrent SendCommand, flusher, gate release; 16 concurrent clients one writer.
15. Determinism gate and perf: with batching on, the 1M perf gate and detgate stay green; per-record allocations on the enqueue path bounded (pinned test).
16. Gate/seam: engine outbound ordering test (reject never overtakes an earlier un-durable ack).
Scoped runs only for agents (test-scope contract); 30-minute watchdog applies.

---

## 8. What inc2a must expose (dependency list)

1. `Store.AppendBatch(ctx, city, []JournalRecord) (lastSeq uint64, err error)` with the 4.3 on-disk frame and the S3 object+head CAS, `JournalRecord{Seq, Epoch, Kind, Session, Body}`; seq assignment owned by the single writer (inc2a AC-J1).
2. A typed, version-aware `ReadJournal` and a ranged read for the look-back (`ReadJournalRange(city, fromSeq, toSeq)` or `ReadJournalBefore(city, seq, n)`), both versions, usable across a sealed segment boundary.
3. `JournalRepair` extended for the batch frame (cut whole batch, keep BUG-1197 mid-file rule and MET-P063).
4. A writer-epoch handle: current epoch, a fenced signal/callback (`E-WRITER-FENCED`) the batcher can wire to `HaltPersist`.
5. `HeadSeq`/`DurableSeq` accessor so restore trusts the head and ignores orphan batch objects.
6. Segment seal at snapshot commit with `JournalSeq` in the manifest (AC-J9), and hot retention of at least the segment before the snapshot (needed by the look-back).
7. Injected clock and an in-process fake store with blockable PUT/CAS and write-then-drop-response (AC-A7) for the crash matrix; conformance cases for MemStore, DiskStore, S3Store.
8. `journalVersion` marker in city meta/manifest and the downgrade guard (E9).
9. Seq continuity from v1 record count (AC-J2).
Outside persist: `compose` wiring (Batcher, PersistHalter, ResultGate), `cmd/metroserve/cityhost.go` drain order, wsserver session stamping and `command.dedup` advertisement, client handling of `result.duplicate` (FEAT-9995385597).

---

## 9. Conflicts with sibling documents

- **C1 (inc2 AC-J4/J5 and a2 s1.4):** both say a duplicate returns "the original `CommandResult` unchanged". Lead ruling and this ICD: a duplicate returns a marker reply, no stored results. Reword AC-J4/J5 when re-specified from this ICD.
- **C2 (FEAT-9995385597 AC-R9, AC-R5 prose):** AC-R9 says "journal dedup returns the original result"; must say "returns `duplicate`, resolve as success without a body". AC-R9 also excludes only Subscribe/Unsubscribe; queries must be excluded too (section 3.2) and never retried under the old id.
- **C3 (inc2 AC-J8):** S3 object "containing CRC-framed v2 records (existing journal.go framing)" per record. This ICD requires one outer frame per batch (4.3), at least on disk, and recommends the same for S3 for one decoder. Needs inc2a agreement.
- **C4 (inc2 AC-J6):** `P-batch-ms` 200 ms is longer than the 160 ms tick; lead ruling lowers it (this ICD: 40 ms, max 80 ms).
- **C5 (a2 s1.4 / lead "rebuilt from journal v2 records"):** a tail-only rebuild is empty right after a snapshot seals the segment (inc2a AC-J9), so it cannot cover the exact hibernate-then-retry case. This ICD adds the look-back read (3.5); inc2a must keep the preceding segment hot.
- **C6 (MET-P060 remedy):** text asserts "the journal fsyncs per record"; false after group commit; amend.
- **C7 (BUG-472 halt):** currently latched only from inside `journalAccepted` synchronously; asynchronous latching from the flusher is new (PersistHalter seam, 4.1).
- **C8 (inc2 scope line):** already flagged by its review: the engine command-result path changes; this ICD confirms it (core, compose, wsserver, cityhost).

---

## 10. Open questions for Aaron (each with a recommended answer)

- **OQ-1 Session carrier.** In-memory `Command.Session` field (`json:"-"`) stamped by wsserver, or a new `SendCommandFrom(session, cmd)` on `Transport`? Recommend the non-serialised field: smallest diff, wire and fixtures unchanged. Risk: any code comparing/hashing `Command` by value; builder greps first.
- **OQ-2 Duplicate reply shape.** `accepted:true` with `result:{"duplicate":true,"seq":N}`, or `accepted:false` with a registry "already applied" code? Recommend accepted:true (the command WAS accepted; only accepted commands are journaled, so duplicates are by definition accepted ones) and capability-gated so no old client sees it.
- **OQ-3 Hold Deltas/Events too?** Results alone are ruled held. A delta derived from an un-durable command shows the player an effect a crash can erase (visible, never acknowledged, then lost). Recommend hold deltas/events with the same watermark (tag by seq at emission); cost is at most one batch window of extra visual latency (40 ms disk, a few hundred ms S3), and the "never lose a player-visible action" promise stays true. Alternative: results only, accept a rare visible-then-gone effect.
- **OQ-4 Queries and Subscribe not deduped.** Confirm they stay out of dedup and the client always uses a fresh correlationId for them (loses nothing: they are idempotent reads/handles). Recommend yes.
- **OQ-5 Batch timer.** 40 ms default, 80 ms cap, flush-again-immediately under backlog. Recommend; confirm 40 ms is acceptable given S3 flushes will take 100-300 ms regardless (the timer is not the dominant term).
- **OQ-6 Downgrade.** Refuse to open a v2 journal with an older binary (E9, fail closed) versus attempting to read it. Recommend refuse; there is no safe partial read of a batch frame.
- **OQ-7 Look-back size.** 16384 records before the snapshot (about 1-4 MB read at wake). Recommend; wake-time impact to be measured in the inc3 wake budget (Q100153 15 s).
- **OQ-8 Rejected commands retried.** Not journaled, so not deduped; a retry is re-evaluated and may flip from reject to accept if state changed. Recommend accept as is (idempotence for rejects is not needed; a client wants the current answer).
- **OQ-9 Un-ACKed prefix of a torn S3/disk batch.** Disk is all-or-nothing by 4.3; on S3 an un-CAS'd object is ignored. Confirm there is no requirement to salvage a partial batch. Recommend none.
- **OQ-10 Window loss on eviction.** Eviction can cause a re-apply of a very old retried command. Recommend accept with the client in-flight cap (256) as the mitigation and the MET-P065 log; the alternative (unbounded table) is not affordable at 100M scale.
