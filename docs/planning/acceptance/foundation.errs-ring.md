BOW code: SEC-033

# Acceptance criteria — resolve SEC-033: the ring's coalescing bound must not rest on a re-derivable population count (foundation.errs)

**BOW code:** SEC-033 (P2, open)
**Spec refs:** GR#1 (log, don't lose); GR#15 (validators/bounds derive from data or runtime queries, never a hardcoded/prose constant); dev-team-process.md Weakness pattern #5 ("a guard must not damage what it protects" — SEC-030/SEC-031(b), the direct ancestors of this ring); dev-team-process.md Weakness pattern #3 ("fix the class, not the demonstrated instance").
**Date:** 2026-08-10
**Status:** active
**Package under test:** `internal/foundation/errs` (`log.go`'s `ringBuffer`/`push`/`coalesceScanBack`, and `sec030_sec031_test.go` plus this item's new test file).
**Standard gates:** `go build ./...`, `go vet ./...`, `golangci-lint run ./...`, `go test ./internal/foundation/errs/... -race -count=1`, `gofmt -l .` empty, forbidden-touch check (no file outside `internal/foundation/errs` changes; `Entry`'s JSON shape and `Logger`'s NDJSON path are explicitly untouched — see AC-8).

## User stories

- As **an operator reading the F12 debug tail during a real incident**, I need the in-memory error ring to coalesce every genuinely repeating code it sees, not just the first K interleaved codes, so that a multi-subsystem event (e.g. a DB outage tripping several modules' health-checks and retries at once) does not silently evict earlier genuine entries the way SEC-030 originally did.
- As **the maintainer who next touches `coalesceScanBack`** (or its replacement), I need the design to no longer depend on a population count that has already been shown wrong twice (stale numerator, wrong denominator — SEC-033) and is expected to keep changing as the module sweep continues, so that "keeping the bound accurate" stops being a recurring, easy-to-forget chore.
- As **the no-sink dev session** (SEC-030's own original scenario, where the ring is not a fallback but the *only* record), I need coalescing to hold under the same interleaved-flood conditions that broke it before, because there is no NDJSON file behind this session to fall back on.

## Decision this item resolves

**Required: option (b) — key coalescing by `Code` (e.g. `map[string]*Entry` plus an eviction order), removing `coalesceScanBack`/K from the design entirely.** Raising K (option (a)) is explicitly **rejected** as the fix.

Reasoning, for the record:

1. **The finding is not "K is too small," it is "no fixed K is the right shape."** SEC-033 showed both halves of the original K=16 justification were wrong: the numerator (9 guarded types) was stale (actually 14, still rising) and the denominator was the wrong population — the ring is the fallback for every registry-sourced error, currently **84** distinct `MET-*` codes in `data/errors.json` (itself already lower than the 86 cited in the finding two days ago — the count moved again in the time it took to write this file, which is the drift SEC-033 is about, demonstrated rather than argued). A population that has been wrong-then-stale twice in one week is not a number a fixed K can safely track; raising it to any chosen value just schedules the next re-derivation.
2. **GR#15 makes option (a) structurally expensive forever, not just once.** If a numeric K survives, GR#15 requires its justification to derive from data and be drift-tested — meaning a permanent test that reads `len(data/errors.json's codes)` (or walks the AST for guarded types, whichever population is claimed) and asserts K still exceeds it, re-litigated every time either count changes. Option (b) removes the bound from the design, which removes the obligation, rather than institutionalising it.
3. **Constraints line up with (b), not (a).** The Repeat count is the signal that must survive (AC-2/AC-3) — exact map-keyed coalescing preserves it *more* faithfully than scan-back-K ever did (K coalescing has always had a "beyond the window, a genuine repeat looks like a new occurrence" gap; a map has none). The file-backed NDJSON audit trail is explicitly out of scope for coalescing either way (AC-8) and stays untouched under (b) exactly as it did under (a). `push()` sits on every guard's rejection path, so its cost matters under flood (AC-6) — a map lookup/insert is the standard, well-understood cost of exact coalescing, and this is a diagnostic, non-security-boundary buffer (Bill's own framing, carried over from ASM-106/ASM-107), so trading O(K) prose-bounded scanning for O(1) amortized map operations is a reasonable, not reckless, cost increase.
4. **This is fixing the class, not the instance (Weakness pattern #3).** Option (a) fixes today's instance (14-vs-16) and leaves the exact same shape of defect standing for whoever crosses the next chosen K — a map removes the shape of the defect (K-vs-population) rather than moving its threshold.

If a future Destructive/Tester finding shows (b)'s allocation/bookkeeping cost is unacceptable under a measured flood, that is a new finding against (b), to be raised and logged as its own item — not a reason to fall back to (a) inside this one.

**ASM-116** logs this BA's judgement call (option (b) over (a)) per v1.7's mandatory assumption logging.

## Acceptance criteria

### Functional

- **AC-1 (map-keyed exact coalescing replaces scan-back).** `ringBuffer.push` coalesces a pushed `Entry` into an existing slot whenever **any** currently-held entry (not just one within a bounded scan-back window) has the same `Code` — implemented via a `Code`-keyed index (e.g. `map[string]int`/`map[string]*Entry`) rather than the `for k := 1; k <= scanBack; k++` reverse scan. Check: `grep -n "coalesceScanBack" internal/foundation/errs/log.go` finds no matches (the constant and every reference to it are removed, not just unused); `grep -n "map\[string\]" internal/foundation/errs/log.go` finds the new index.
- **AC-2 (Repeat count is preserved exactly, including across evictions).** Every existing `sec030_sec031_test.go` test that asserts a `Repeat` value continues to pass unmodified in its *assertions* (test setup may be adjusted only where it specifically exercised the old scan-back boundary — see AC-4) — `TestRingBuffer_CoalescesConsecutiveSameCode` (Repeat == n-1 after n consecutive same-Code pushes) and `TestRingBuffer_CoalescesAcrossOtherCodesWithinScanBack` (Repeat == 2 after two coalesced hits with other codes interleaved) must still pass with their existing expected values.
- **AC-3 (the actual gap SEC-033 identified is closed and proven).** A new test proves what scan-back-K structurally could not: **more than any previously-chosen K** distinct codes interleaved (e.g. 20 distinct codes, comfortably past the old `coalesceScanBack = 16`) round-robin-pushed well beyond the ring's capacity still coalesces each code into exactly one slot with the correct `Repeat`, and a genuine entry seeded before the flood survives — i.e. `TestRing_InterleavedFlood_BeyondScanBack_AcceptedTradeOff`'s old assertion (ring fills to capacity and a boundary-crossing pattern degrades toward pre-fix behaviour) is either deleted or **inverted** to prove the new code coalesces exactly where the old code was documented to fail. Check: the new/updated test name makes clear it targets the population SEC-033 named (registry-sourced codes generally), not guarded types specifically, and passes.
- **AC-4 (existing boundary-specific tests are retired, not silently orphaned).** `TestRingBuffer_ScanBackBoundary_ExactlyAtLimitStillCoalesces` and `TestRingBuffer_ScanBackBoundary_JustBeyondLimitDoesNotCoalesce` (which exist solely to pin `coalesceScanBack`'s off-by-one edge) are removed, with a one-line comment at the removal site (or in the same commit's test-file header) stating why: the scan-back boundary they pinned no longer exists after SEC-033. Silently deleting them with no trace is not acceptable — a future reader diffing test history must be able to see this was a deliberate design change, not an accidental coverage loss.
- **AC-5 (eviction under capacity remains bounded and correct).** `ringCapacity` (200) is unchanged and still enforced: when the ring is at capacity and a **new** (non-coalescing) `Code` is pushed, the oldest entry is evicted exactly as before, and the `Code`-keyed index is kept consistent with `buf`/`start`/`count` (no stale index entries pointing at overwritten slots — a targeted test pushes past capacity with a mix of new and repeating codes and asserts `snapshot()`'s length never exceeds 200 and every returned entry's `Code` is independently coalesce-correct).

### Error handling

- **AC-6 (cost under flood is measured, not assumed — Pattern #5).** Because `push()` sits on every guarded type's rejection path (SEC-030's own scenario: a stuck copy hammering `checkNotCopied` at render-tick rate), a benchmark or timed test demonstrates the map-keyed `push` does not regress flood behaviour into a new denial-of-service shape: pushing at least 10,000 entries (mix of repeating and 20+ distinct codes, matching AC-3's population) completes in a bounded, logged time and the process does not exhibit unbounded memory growth (the map's size is bounded by however many **distinct** codes are live in the ring at once, which is itself bounded by `ringCapacity`, and a comment or assertion states this bound explicitly).
- **AC-7 (the no-sink session — SEC-030's original scenario — is the one case checked directly).** `TestRing_StuckCopyAtRenderTickRate_DoesNotEvictGenuineEntry` and `TestRing_InterleavedFlood_DoesNotEvictGenuineEntry` (both already exist, exercising `logEntry`'s no-sink-configured path) continue to pass, proving the fix holds in the exact scenario that has no NDJSON fallback behind it at all.

### Documentation

- **AC-8 (the file-backed audit trail's non-involvement is stated, not just true).** `ringBuffer`'s doc comment (and `push`'s) is updated to describe the new map-keyed mechanism, explicitly restates that `Logger.Log`'s NDJSON output has no coalescing and is unaffected by this change (carrying forward the existing sentence to that effect rather than dropping it), and removes the now-obsolete "16 comfortably exceeds..." / "if this needs closing... raise K or key a map by Code" language that SEC-033 was about — the design decision this item resolves is documented as resolved, not left as an open question the old comment still poses.
- **AC-9 (SEC-033 is closed with a note pointing at the chosen design).** The BOW item SEC-033 receives a comment (at build/verification time, not by this BA) summarizing that the map-keyed fix landed, referencing the commit and this acceptance file, so a future reader hitting SEC-033 in `claude-bow.js list` sees the resolution path without re-reading the whole thread.

### Determinism & safety

- **AC-10 (no new nondeterminism).** The map-keyed implementation introduces no dependency on Go map iteration order for any externally observable behaviour — `snapshot()`'s returned order remains the existing ring insertion order (via `start`/`count`/`buf`, not via ranging the new `Code` map), so `Recent()`/`RecentCopyRejections()` output ordering is unchanged. Check: a test asserts `snapshot()` order is stable across repeated calls with no intervening pushes, and `grep -n "range.*map\[string\]" internal/foundation/errs/log.go` (or equivalent) confirms the map is never ranged to produce ordered output.

## Out of scope

- Any change to `Logger`/`Log`/NDJSON rotation — this item touches only the in-memory `ringBuffer` mechanism (`ring` and `copyRejectRing` both use the same `ringBuffer` type, so both benefit, but neither's *capacity* or *separation* from the other changes here — that split is SEC-031(b)'s finding, not this one).
- Re-deriving or re-justifying any specific numeric K — that is precisely the pattern this item retires (see "Decision this item resolves" above).
- `copyRejectRingCapacity`'s value or the two-ring split itself (SEC-031(b)) — unaffected; this item changes *how* a single `ringBuffer` coalesces internally, not how many rings exist or their sizes.

## Escalations

- **Likely assumption needed at build time:** the exact index data structure (`map[string]int` storing a slot index that must be kept valid across eviction/wraparound, vs. `map[string]*Entry` pointing directly at ring elements, vs. some other shape) is an implementation choice the criteria above do not dictate — the junior must log an `ASM-` for the chosen shape, citing why it keeps the index correct across the ring's circular-buffer eviction (AC-5) and produces the ordering guarantee (AC-10).
- **Likely assumption needed at build time:** AC-6's specific "bounded, logged time" threshold for the 10,000-entry flood benchmark is a tuning decision no spec dictates — the junior must log an `ASM-` for the concrete number chosen and what machine/conditions it was measured under.

---

BOW code: SEC-042

# Acceptance criteria — resolve SEC-042: ring-capacity literal duplicated at construction and assertion (foundation.errs)

**BOW code:** SEC-042 (P3, minor, open)
**Spec refs:** GR#15 (validators'/bounds' expected values must derive from a single source, never independently re-typed constants) — low-severity flavour, since the value is intrinsic to the test's own setup rather than sourced from external data.
**Date:** 2026-08-12
**Status:** active
**Package under test:** `internal/foundation/errs` (`sec030_sec031_test.go` only — read-only test-file change, no production code touched).
**Standard gates:** `go build ./...`, `go vet ./...`, `go test ./internal/foundation/errs/... -race -count=1`, `gofmt -l .` empty.

## Finding status — the original citation is stale; the underlying pattern is not

Re-read against the current tree (SEC-033/ASM-116's map-keyed `coalesceScanBack` removal has landed — the file builds clean, `go vet` and `-race` tests pass):

- The specific test SEC-042 named, `TestRing_InterleavedFlood_BeyondScanBack_AcceptedTradeOff`, **no longer exists**. It was deliberately removed by SEC-033 itself (see the removal-note comment at `sec030_sec031_test.go:175-189`) and replaced by `TestRing_ManyDistinctCodes_ExactCoalescing`, which does not reproduce the duplicated-literal shape (its `len(snap) != wantSlots` check compares against `distinctCodes + 1`, not a re-typed `200`). The `~line 298`/`~line 312` citation is stale — those line numbers now fall inside `TestRing_ManyDistinctCodes_ExactCoalescing`, an unrelated test.
- The sibling test SEC-042 cites as the already-fixed pattern to mirror, `TestRingBuffer_ScanBackBoundary_JustBeyondLimitDoesNotCoalesce`, **also no longer exists** — same SEC-033 removal. Its replacement fix pattern (name a shared value once, reference it at both sites) is however still present and provable: `TestRingBuffer_EvictionKeepsIndexConsistent` (`sec030_sec031_test.go:312-348`) already declares `const capacity = 200` once (line 313) and references `capacity` at both the construction site (`newRingBuffer(capacity)`, line 314) and every assertion site (line 332), so the pattern SEC-042 asks for is already established practice in this file — just not at the site SEC-042 pointed at.
- **The anti-pattern itself is still live, relocated.** `TestRing_FloodCost_10kEntries_BoundedTime` (`sec030_sec031_test.go:393-435`) constructs `r := newRingBuffer(200)` (line 394, bare literal) and later asserts `len(r.index) > 200` / `want <= 200` (lines 429-430, the same bare literal re-typed, not derived from the line-394 value). This is the exact same-shape duplication SEC-042 describes — construction and assertion sites that could independently drift — just at a different test than the one filed against.

**Conclusion: the finding is not resolved.** Its specific line citations are dead (the cited test was removed by unrelated SEC-033 work), but the class of defect it describes still exists in the file, now at `TestRing_FloodCost_10kEntries_BoundedTime`. Criteria below are written against the current, live instance rather than the stale one, proportionate to the finding's own stated minor/P3 severity — two ACs, no rigor treatment.

## Acceptance criteria

- **AC-1 (single named value for the flood-cost test's ring capacity).** In `TestRing_FloodCost_10kEntries_BoundedTime`, the ring's capacity is declared once as a named constant (e.g. `const capacity = 200`, mirroring `TestRingBuffer_EvictionKeepsIndexConsistent`'s established pattern) and both the construction site (`newRingBuffer(capacity)`, currently line 394) and every assertion comparing `len(r.index)` against the capacity (currently lines 429-430) reference that same named constant — no bare `200` literal remains at either site. Check: `grep -n "200" internal/foundation/errs/sec030_sec031_test.go` shows no bare-literal `200` inside `TestRing_FloodCost_10kEntries_BoundedTime`'s body (comments referencing `ringCapacity` descriptively are fine; only construction/assertion literals are in scope).
- **AC-2 (no behavioural change).** The edit is a pure refactor: `go test ./internal/foundation/errs/... -race -count=1` passes with output identical in substance to the pre-change run (same pass/fail set, same assertions), proving the named constant carries the same value (200) the bare literals previously encoded and nothing else in the test's behaviour shifted.

## Out of scope

- Any other `newRingBuffer(200)` call site in this file that is not later re-compared against a bare `200` (e.g. lines 123, 149, 252, 442) — those construct-only sites have no duplicated literal to drift against and are not this finding's target.
- Production code (`log.go`'s `ringCapacity`) — untouched; this is a test-file-only cleanup.
- Re-opening SEC-033's design (map-keyed coalescing, the removed tests, `ASM-116`) — settled, out of scope here.
