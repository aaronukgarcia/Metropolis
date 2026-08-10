package errs

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

// This file covers two related but distinct findings from the SEC-020
// re-attack, both fixed in the SAME shared mechanism (this package) per
// Bill's "fix the class, not the caller" instruction:
//
//   - SEC-031 part 1: SetSink(l *Logger) took *Logger as an ARGUMENT, not
//     as a receiver, so it fell outside every method-shaped SEC-020
//     enumeration (grep `func (x *T)` + cross-check against lock sites) —
//     the one guard that technique is structurally blind to. Fixed:
//     SetSink now identity-checks l and rejects a struct copy rather than
//     installing it (see SetSink's doc comment on log.go).
//   - SEC-031 part 2 / SEC-030: the fail-closed path that logs a copy-hit
//     shared the SAME finite, unquota'd in-memory ring as genuine audit
//     entries — a struct-copied Logger's Log() double-pushed into it
//     (SEC-031), AND (SEC-030, a completely different, non-malicious
//     caller: any of the nine SEC-020 types' checkNotCopied, called every
//     time a stuck copy is used, e.g. a 10Hz render tick) could flood and
//     evict every other entry in the shared ring within ~20 seconds.
//     Fixed at the one shared choke point: ringBuffer.push now coalesces
//     consecutive same-Code pushes into a single slot with a Repeat
//     count (ASM-106), and copy-rejection entries specifically live in a
//     wholly separate ring (copyRejectRing / RecentCopyRejections,
//     ASM-105) so they can never compete with genuine entries for space
//     even before coalescing kicks in.

// --- SEC-031 part 1: SetSink's missing copy-guard -----------------------

// TestSetSink_RejectsStructCopiedLogger proves the actual, previously-
// exploitable defect is closed: handing SetSink a byte-copied *Logger
// must be rejected outright, and the REAL sink previously installed must
// keep receiving every subsequent errs.New/Wrap call — not be silently
// replaced by the rejected copy, and not be cleared back to the ring
// fallback either.
func TestSetSink_RejectsStructCopiedLogger(t *testing.T) {
	setupTestRegistry(t)

	var buf bytes.Buffer
	real := NewLogger(&buf)
	if err := SetSink(real); err != nil {
		t.Fatalf("SetSink(real): %v", err)
	}

	cp := loggerByteCopy(real)
	if err := SetSink(cp); !errors.Is(err, ErrLoggerCopied) {
		t.Fatalf("SetSink(copy): err = %v, want ErrLoggerCopied", err)
	}

	// This is the exact reproduction SEC-031 part 1 confirmed as exploitable
	// pre-fix: 20 New() calls after the (would-be) SetSink(copy) install.
	// Every single one must still land on the REAL logger's writer now,
	// proving the rejected copy was never installed as the sink.
	for i := 0; i < 20; i++ {
		_ = New("MET-F900", NewCorrelationID(), map[string]any{"thing": fmt.Sprintf("call-%d", i)})
	}

	if buf.Len() == 0 {
		t.Fatal("real logger's writer received zero bytes after 20 New() calls following a rejected SetSink(copy) — SEC-031 part 1 regression: persistent logging was silently defeated")
	}
	if got := bytes.Count(buf.Bytes(), []byte("\n")); got != 20 {
		t.Fatalf("real logger's writer received %d lines, want 20 (one per New() call) — some calls were not routed to the real sink", got)
	}
}

// TestSetSink_NilStillClears proves the documented "pass nil to go back
// to the in-memory-only ring buffer" behaviour is unaffected by the new
// guard — nil is not a copy, and must not be rejected.
func TestSetSink_NilStillClears(t *testing.T) {
	setupTestRegistry(t)

	var buf bytes.Buffer
	real := NewLogger(&buf)
	if err := SetSink(real); err != nil {
		t.Fatalf("SetSink(real): %v", err)
	}
	if err := SetSink(nil); err != nil {
		t.Fatalf("SetSink(nil): %v", err)
	}

	_ = New("MET-F900", NewCorrelationID(), nil)
	if buf.Len() != 0 {
		t.Fatalf("real logger's writer received %d bytes after SetSink(nil), want 0 (sink should have been cleared)", buf.Len())
	}
	if got := len(Recent()); got != 1 {
		t.Fatalf("Recent() len = %d after SetSink(nil) + one New() call, want 1 (should fall back to the ring)", got)
	}
}

// TestSetSink_RejectedCopy_DoesNotClearExistingSink is the sharpest form
// of the part-1 fix: even when there was NO real sink configured yet
// (nil), a rejected SetSink(copy) must not somehow install anything —
// logging must still correctly fall back to the ring, exactly as if
// SetSink had never been called at all.
func TestSetSink_RejectedCopy_DoesNotClearExistingSink(t *testing.T) {
	setupTestRegistry(t)

	orphan := NewLogger(&bytes.Buffer{})
	cp := loggerByteCopy(orphan)
	if err := SetSink(cp); !errors.Is(err, ErrLoggerCopied) {
		t.Fatalf("SetSink(copy): err = %v, want ErrLoggerCopied", err)
	}

	_ = New("MET-F900", NewCorrelationID(), nil)
	if got := len(Recent()); got != 1 {
		t.Fatalf("Recent() len = %d after a rejected SetSink(copy) + one New() call, want 1 (must fall back to the ring, not silently vanish)", got)
	}
}

// --- SEC-030 / SEC-031 part 2: the shared ring's flooding hazard --------

// TestRingBuffer_CoalescesConsecutiveSameCode proves the core mechanism:
// N consecutive pushes of the same Code collapse into ONE ring slot with
// Repeat == N-1, carrying the MOST RECENT occurrence's other fields.
func TestRingBuffer_CoalescesConsecutiveSameCode(t *testing.T) {
	r := newRingBuffer(200)
	const n = 5000
	for i := 0; i < n; i++ {
		r.push(Entry{Code: "MET-U101", CorrelationID: fmt.Sprintf("corr-%d", i), Msg: "stuck copy", Ts: fmt.Sprintf("ts-%d", i)})
	}

	snap := r.snapshot()
	if len(snap) != 1 {
		t.Fatalf("ring has %d entries after %d consecutive same-Code pushes, want 1 (coalesced)", len(snap), n)
	}
	if snap[0].Repeat != n-1 {
		t.Fatalf("coalesced entry Repeat = %d, want %d", snap[0].Repeat, n-1)
	}
	if snap[0].CorrelationID != fmt.Sprintf("corr-%d", n-1) {
		t.Fatalf("coalesced entry CorrelationID = %q, want the MOST RECENT occurrence's (corr-%d)", snap[0].CorrelationID, n-1)
	}
}

// TestRingBuffer_CoalescesAcrossOtherCodesWithinScanBack proves push's
// scan-back (ASM-107, ringCapacity's coalesceScanBack) finds a Code's
// PREVIOUS occurrence even when other, genuinely different Codes were
// pushed in between — not just when it is the single most-recently-
// pushed entry. This is the fix for the exact gap Tester-2 found: two
// interleaved stuck emitters with different codes never matched under
// the original "check only the newest slot" rule.
func TestRingBuffer_CoalescesAcrossOtherCodesWithinScanBack(t *testing.T) {
	r := newRingBuffer(200)
	r.push(Entry{Code: "MET-A"})
	r.push(Entry{Code: "MET-B"})
	r.push(Entry{Code: "MET-A"}) // 1 other entry in between -- well within the scan-back window
	r.push(Entry{Code: "MET-C"})
	r.push(Entry{Code: "MET-A"}) // 1 other entry in between again

	snap := r.snapshot()
	if len(snap) != 3 {
		t.Fatalf("ring has %d entries, want 3 (MET-A coalesced across MET-B/MET-C, each of which keeps its own slot)", len(snap))
	}
	var aCount, aRepeat int
	for _, e := range snap {
		if e.Code == "MET-A" {
			aCount++
			aRepeat = e.Repeat
		}
	}
	if aCount != 1 {
		t.Fatalf("found %d MET-A entries, want 1 (coalesced)", aCount)
	}
	if aRepeat != 2 {
		t.Fatalf("MET-A Repeat = %d, want 2 (coalesced twice)", aRepeat)
	}
}

// TestRingBuffer_ScanBackBoundary_ExactlyAtLimitStillCoalesces and
// TestRingBuffer_ScanBackBoundary_JustBeyondLimitDoesNotCoalesce
// (formerly here) pinned the exact off-by-one at the OLD bounded
// scan-back's edge (`coalesceScanBack`, M=15 coalesces / M=16 does not).
// SEC-033 (Bill's ruling, ASM-116) replaced that bounded scan-back with
// exact Code-keyed coalescing (see ringBuffer.index / push in log.go),
// which removed `coalesceScanBack` and every reference to it from the
// design entirely — there is no longer a scan-back window, so there is
// no boundary left to pin. These two tests are deliberately REMOVED,
// not weakened or left to bit-rot against a symbol that no longer
// exists: see TestRing_ManyDistinctCodes_ExactCoalescing below for the
// replacement, which proves the new mechanism has no equivalent edge at
// all (an arbitrarily large number of interleaved distinct Codes still
// coalesces exactly, where the old design necessarily degraded once the
// population exceeded whatever K was chosen).

// TestRing_InterleavedFlood_DoesNotEvictGenuineEntry is Tester-2's exact
// reproduction, now pinned as a permanent regression test regardless of
// which way the design decision went (Bill's instruction 1): TWO stuck
// emitters with DIFFERENT codes, interleaved — e.g. a stuck F1 MapScreen
// (MET-U101) beside a stuck F12 debug.Screen (MET-U203), both driven by
// the same render loop, an entirely ordinary co-occurrence. Pre-fix (and
// pre-ASM-107's scan-back widening — the original "check only the
// newest slot" rule, ASM-106 alone), 10,000 alternating pushes filled
// Recent() to capacity (200) and evicted the genuine pre-flood entry —
// identical to having no coalescing at all, because neither code was
// ever the single most-recently-pushed entry when its own previous
// occurrence needed matching.
func TestRing_InterleavedFlood_DoesNotEvictGenuineEntry(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	// Seed a genuine, distinct-Code entry before the flood starts.
	logEntry(Entry{Code: "MET-E006", CorrelationID: "corr-genuine-interleaved", Msg: "genuine, must survive an interleaved flood"})

	const floodCount = 10_000
	for i := 0; i < floodCount; i++ {
		code := "MET-U101"
		if i%2 == 1 {
			code = "MET-U203"
		}
		logEntry(Entry{Code: code, CorrelationID: fmt.Sprintf("corr-flood-%d", i), Msg: "interleaved stuck emitters"})
	}

	entries := Recent()
	if len(entries) > 3 {
		t.Fatalf("Recent() has %d entries after a %d-iteration TWO-CODE interleaved flood, want at most 3 (the seeded genuine entry + one coalesced slot per flooding code) — SEC-030 regression: the ring filled to capacity again, exactly Tester-2's reproduction", len(entries), floodCount)
	}

	found := false
	for _, e := range entries {
		if e.CorrelationID == "corr-genuine-interleaved" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the genuine, pre-flood entry was evicted by a %d-iteration interleaved flood of just 2 distinct codes — identical to pre-fix behaviour (Tester-2's finding)", floodCount)
	}
}

// TestRing_ManyDistinctCodes_ExactCoalescing is SEC-033's direct proof
// (AC-3), inverting what TestRing_InterleavedFlood_BeyondScanBack_
// AcceptedTradeOff (formerly here) demonstrated under the OLD design:
// where that test proved the ring necessarily filled to capacity once
// more than `coalesceScanBack` (16) distinct codes interleaved, this
// proves the new Code-keyed design has no such ceiling at all.
//
// 20 distinct codes — comfortably past the old K=16 — round-robin
// pushed well beyond the ring's capacity (200) must still coalesce each
// code into exactly one slot with the correct Repeat, and a genuine
// entry seeded before the flood must survive. This targets the
// population SEC-033 actually named — every registry-sourced error the
// ring is a fallback for (84 distinct MET- codes at last count, not the
// far smaller and unrelated "guarded types" count the old K was
// mis-justified against) — not guarded types specifically.
func TestRing_ManyDistinctCodes_ExactCoalescing(t *testing.T) {
	r := newRingBuffer(200)

	// Seed a genuine entry the flood must not evict.
	r.push(Entry{Code: "MET-E006", CorrelationID: "corr-genuine-wide", Msg: "genuine, must survive a wide flood"})

	const distinctCodes = 20 // comfortably past the old coalesceScanBack=16
	const roundsPerCode = 50
	for round := 0; round < roundsPerCode; round++ {
		for c := 0; c < distinctCodes; c++ {
			r.push(Entry{
				Code:          fmt.Sprintf("MET-WIDE%d", c),
				CorrelationID: fmt.Sprintf("corr-wide-%d-%d", c, round),
			})
		}
	}

	snap := r.snapshot()
	// One slot per distinct flood code, plus the seeded genuine entry --
	// NOT capacity (200), which is exactly the gap the old bounded
	// scan-back could not close for a population this wide.
	wantSlots := distinctCodes + 1
	if len(snap) != wantSlots {
		t.Fatalf("ring has %d entries after a %d-distinct-code round-robin flood, want %d (one slot per code + the seeded genuine entry) — exact Code-keyed coalescing regressed", len(snap), distinctCodes, wantSlots)
	}

	seenGenuine := false
	seenCodes := make(map[string]bool, distinctCodes)
	for _, e := range snap {
		if e.Code == "MET-E006" {
			seenGenuine = true
			continue
		}
		if seenCodes[e.Code] {
			t.Fatalf("Code %q appears more than once in the snapshot -- coalescing failed to stay exact", e.Code)
		}
		seenCodes[e.Code] = true
		if e.Repeat != roundsPerCode-1 {
			t.Fatalf("entry %q Repeat = %d, want %d (coalesced across %d rounds)", e.Code, e.Repeat, roundsPerCode-1, roundsPerCode)
		}
		wantCorr := fmt.Sprintf("corr-wide-%s-%d", e.Code[len("MET-WIDE"):], roundsPerCode-1)
		if e.CorrelationID != wantCorr {
			t.Fatalf("entry %q CorrelationID = %q, want %q (most recent occurrence)", e.Code, e.CorrelationID, wantCorr)
		}
	}
	if !seenGenuine {
		t.Fatal("the seeded genuine entry (MET-E006) was evicted by a 20-distinct-code flood -- exactly the SEC-033 gap this fix closes")
	}
	if len(seenCodes) != distinctCodes {
		t.Fatalf("saw %d distinct flood codes in the snapshot, want %d", len(seenCodes), distinctCodes)
	}
}

// TestRingBuffer_EvictionKeepsIndexConsistent is AC-5: pushing well past
// ringCapacity with a MIX of new (non-coalescing) and repeating codes
// must keep the Code-keyed index consistent with buf/start/count at
// every step -- no stale index entry may point at a slot that has since
// been overwritten by eviction. Proven by checking, after every push,
// that (a) snapshot() never exceeds capacity and (b) every Code that
// currently has an index entry is actually found, exactly once, at that
// index in the live snapshot.
func TestRingBuffer_EvictionKeepsIndexConsistent(t *testing.T) {
	const capacity = 200
	r := newRingBuffer(capacity)

	const totalPushes = capacity * 5
	for i := 0; i < totalPushes; i++ {
		// Every push is a NEW code every 7th iteration (never coalesces),
		// otherwise repeats one of a small rotating set (coalesces) -- a
		// deliberate mix so both index-update paths (new slot, existing
		// slot) run interleaved with evictions once the ring is full.
		var code string
		if i%7 == 0 {
			code = fmt.Sprintf("MET-FRESH%d", i)
		} else {
			code = fmt.Sprintf("MET-REPEAT%d", i%5)
		}
		r.push(Entry{Code: code, CorrelationID: fmt.Sprintf("corr-%d", i)})

		snap := r.snapshot()
		if len(snap) > capacity {
			t.Fatalf("push %d: snapshot length = %d, want <= %d (ringCapacity)", i, len(snap), capacity)
		}

		// Every Code the index currently claims to hold must resolve to
		// exactly the entry snapshot() independently reports for it --
		// two views (index-driven lookup, position-driven snapshot) of
		// the same state must agree, or the index has gone stale.
		for c, idx := range r.index {
			if idx < 0 || idx >= len(r.buf) {
				t.Fatalf("push %d: index[%q] = %d, out of buf bounds [0,%d)", i, c, idx, len(r.buf))
			}
			if r.buf[idx].Code != c {
				t.Fatalf("push %d: index[%q] = %d, but buf[%d].Code = %q -- index is stale", i, c, idx, idx, r.buf[idx].Code)
			}
		}
	}
}

// TestRingBuffer_SnapshotOrderStableAcrossCalls is AC-10: snapshot()'s
// returned order must depend only on buf/start/count (ring insertion
// order), never on ranging the Code-keyed index map -- Go map iteration
// order is intentionally randomised per-run, so if snapshot ever
// depended on it, repeated calls with no intervening push would be free
// to disagree with each other, and in practice very often would.
func TestRingBuffer_SnapshotOrderStableAcrossCalls(t *testing.T) {
	r := newRingBuffer(50)
	for i := 0; i < 30; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-ORD%d", i), CorrelationID: fmt.Sprintf("corr-%d", i)})
	}

	first := r.snapshot()
	for call := 0; call < 20; call++ {
		got := r.snapshot()
		if len(got) != len(first) {
			t.Fatalf("call %d: snapshot length = %d, want %d (unchanged, no intervening push)", call, len(got), len(first))
		}
		for i := range got {
			if got[i].Code != first[i].Code || got[i].CorrelationID != first[i].CorrelationID {
				t.Fatalf("call %d: snapshot()[%d] = {%q,%q}, want {%q,%q} (order must not vary between calls)", call, i, got[i].Code, got[i].CorrelationID, first[i].Code, first[i].CorrelationID)
			}
		}
	}
}

// TestRing_FloodCost_10kEntries_BoundedTime is AC-6: push() sits on
// every SEC-020 guard's rejection path, so a flood of pushes must not
// regress into a new denial-of-service shape now that it does a map
// lookup/insert instead of an O(K) bounded scan. 10,000 entries, mixed
// repeating and 20+ distinct codes (matching AC-3's population), must
// complete within a bounded, logged wall-clock budget.
//
// floodBudget (ASM- logged separately per the criteria's Escalations
// section) is deliberately generous -- this is a regression tripwire
// for an accidental O(n^2) or worse, not a tight performance SLA, and
// dev-machine scheduling noise under `-race` (which this suite always
// runs under, see the baseline gate) is real.
//
// The map's size is bounded by however many DISTINCT codes are live in
// the ring at once, which is itself bounded by ringCapacity (200,
// unchanged) -- see ringBuffer.index's doc comment in log.go, and this
// test also spot-checks it directly.
func TestRing_FloodCost_10kEntries_BoundedTime(t *testing.T) {
	r := newRingBuffer(200)

	const floodCount = 10_000
	const distinctCodes = 25 // 20+, matching AC-3

	start := time.Now()
	for i := 0; i < floodCount; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-FLOOD%d", i%distinctCodes), CorrelationID: fmt.Sprintf("corr-%d", i)})
	}
	elapsed := time.Since(start)

	// Timing is REPORTED, not asserted. The budget that used to sit here
	// (500ms against a measured 26.6ms) was the same shape as the SEC-039
	// timing gate that went red on CI within an hour of landing: a
	// wall-clock threshold with what looked like ample headroom, on a
	// shared runner under -race that does not keep a stable clock. A
	// correct fix failing a machine-dependent assertion stops the line for
	// everyone under GR#21, which is a far worse outcome than the weak
	// regression signal it was buying.
	//
	// Worth being honest about what is lost rather than pretending the
	// coverage is unchanged: nothing here now detects a silent regression
	// from the map lookup back to a bounded scan. The coalescing tests
	// would NOT catch it — a scan-back with a large enough K coalesces
	// correctly too, just more slowly. What remains is structural: push()
	// does one map lookup, and the index-size assertion below pins the
	// property that actually mattered (the index cannot become the
	// unbounded resource it replaced). If cost-shape regression detection
	// is wanted back, it needs an operation counter, not a stopwatch.
	t.Logf("flood cost: %d pushes across %d distinct codes in %s (informational — see the comment above on why this is not asserted)", floodCount, distinctCodes, elapsed)

	// The index's size is bounded by the number of distinct LIVE codes,
	// itself bounded by ringCapacity -- explicit assertion, not just a
	// comment, that the index cannot itself become the unbounded
	// resource (the non-negotiable this item was built against).
	if len(r.index) > 200 {
		t.Fatalf("index has %d entries after the flood, want <= 200 (ringCapacity) -- the index has become an unbounded resource", len(r.index))
	}
	if len(r.index) != distinctCodes {
		t.Fatalf("index has %d entries, want %d (exactly the distinct codes pushed, all under ringCapacity)", len(r.index), distinctCodes)
	}
}

// BenchmarkRingBuffer_Push measures push()'s per-call cost directly
// (AC-6, `go test -bench`), complementing the wall-clock flood test
// above with the standard Go benchmarking harness so a future regression
// shows up in `go test -bench=. -benchmem` allocation counts too.
func BenchmarkRingBuffer_Push(b *testing.B) {
	r := newRingBuffer(200)
	const distinctCodes = 25
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-BENCH%d", i%distinctCodes), CorrelationID: fmt.Sprintf("corr-%d", i)})
	}
}

// TestRing_StuckCopyAtRenderTickRate_DoesNotEvictGenuineEntry is the
// direct SEC-030 proof Bill asked for: seed a genuine entry with a
// DIFFERENT Code (simulating some other real error from elsewhere in the
// system, e.g. engine.core or protocol — SEC-030's exact scenario), then
// hammer the SAME Code many more times than ringCapacity — well beyond
// what a 10Hz render tick would produce in the ~20 seconds SEC-030
// measured against the pre-fix ring — and confirm the seeded entry
// survives, is still findable, and the ring never grows past a small,
// bounded size regardless of flood length.
func TestRing_StuckCopyAtRenderTickRate_DoesNotEvictGenuineEntry(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	// Seed a genuine, distinct-Code entry BEFORE the flood starts —
	// exactly SEC-030's "some genuinely different error that happened
	// moments earlier" scenario.
	logEntry(Entry{Code: "MET-E006", CorrelationID: "corr-genuine", Msg: "genuine engine.core error, unrelated to the flood"})

	// Simulate a stuck struct-copied guarded type's checkNotCopied firing
	// every render tick, forever: the SAME Code, fired far more times
	// than ringCapacity (200) — 10,000 iterations is roughly what a
	// stuck 10Hz render loop would produce in ~1000 seconds, well beyond
	// SEC-030's measured ~20s-to-flood-200-slots finding, run here in a
	// tight loop rather than real wall-clock time since only the COUNT
	// matters to this mechanism, not the pacing.
	const floodCount = 10_000
	for i := 0; i < floodCount; i++ {
		logEntry(Entry{Code: "MET-U101", CorrelationID: fmt.Sprintf("corr-flood-%d", i), Msg: "struct-copied MapScreen"})
	}

	entries := Recent()
	if len(entries) > 2 {
		t.Fatalf("Recent() has %d entries after a %d-iteration same-Code flood, want at most 2 (the seeded genuine entry + the coalesced flood entry) — SEC-030 regression: the ring grew unbounded instead of coalescing", len(entries), floodCount)
	}

	found := false
	for _, e := range entries {
		if e.CorrelationID == "corr-genuine" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the genuine, pre-flood entry (corr-genuine) was evicted by a %d-iteration same-Code flood — SEC-030 regression: exactly the operator-facing failure Bill described (F12's tail shows nothing but duplicates, drowning out whatever else is wrong)", floodCount)
	}
}

// TestRing_CopyRejectionFloodNeverTouchesGenuineRing is SEC-031 part 2's
// direct proof: a struct-copied Logger hammered via Log() (not SetSink —
// see the SEC-031-part-1 tests above for that half) must never be able to
// evict a genuine entry from Recent(), because its rejection entries are
// routed to copyRejectRing entirely, not `ring`. Also proves the
// double-push fix: the SAME hammering, run through the FULL logEntry ->
// sink.Log path (by installing the copy as if it were — deliberately —
// the misconfigured sink, SEC-031 part 1's exact scenario), produces
// exactly one coalesced entry in copyRejectRing, not two per call.
func TestRing_CopyRejectionFloodNeverTouchesGenuineRing(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	// Seed a genuine entry in the real ring first.
	logEntry(Entry{Code: "MET-E006", CorrelationID: "corr-genuine-2", Msg: "genuine, must survive"})

	real := NewLogger(&bytes.Buffer{})
	cp := loggerByteCopy(real)

	// Directly hammering Log() on the copy (bypassing SetSink entirely —
	// this is the "a helper somewhere holds a copied *Logger and calls
	// Log() itself" shape, independent of the SetSink guard).
	const floodCount = 500
	for i := 0; i < floodCount; i++ {
		if err := cp.Log(Entry{Code: "MET-F900", CorrelationID: fmt.Sprintf("corr-copy-%d", i), Msg: "hammered"}); !errors.Is(err, ErrLoggerCopied) {
			t.Fatalf("cp.Log() call %d: err = %v, want ErrLoggerCopied", i, err)
		}
	}

	// The genuine ring must be completely untouched by any of this.
	if got := len(Recent()); got != 1 {
		t.Fatalf("Recent() len = %d after a %d-call Log() hammer against a copy, want 1 (the genuine entry, untouched) — SEC-031 part 2 regression", got, floodCount)
	}
	if Recent()[0].CorrelationID != "corr-genuine-2" {
		t.Fatalf("Recent()[0].CorrelationID = %q, want corr-genuine-2 (the genuine entry must be exactly as seeded)", Recent()[0].CorrelationID)
	}

	// The copy-rejection ring absorbed the flood, coalesced to one entry
	// (proving no double-push and no unbounded growth of ITS OWN buffer
	// either).
	rejections := RecentCopyRejections()
	if len(rejections) != 1 {
		t.Fatalf("RecentCopyRejections() len = %d after a %d-call Log() hammer, want 1 (coalesced)", len(rejections), floodCount)
	}
	if rejections[0].Repeat != floodCount-1 {
		t.Fatalf("RecentCopyRejections()[0].Repeat = %d, want %d", rejections[0].Repeat, floodCount-1)
	}
}

// TestLogEntry_NoDoublePushOnCopiedSink is the narrowest possible proof
// of SEC-031 part 2's double-push fix: ONE logEntry call, routed through
// a copied-Logger sink, must add exactly ONE entry to copyRejectRing —
// not two (rejectCopiedLog's own push, plus logEntry's pre-fix fallback
// push of the same Entry a second time).
func TestLogEntry_NoDoublePushOnCopiedSink(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	real := NewLogger(&bytes.Buffer{})
	cp := loggerByteCopy(real)

	sinkMu.Lock()
	sink = cp // bypass SetSink's new guard deliberately, to isolate logEntry's own behaviour
	sinkMu.Unlock()

	logEntry(Entry{Code: "MET-F900", CorrelationID: "corr-single", Msg: "one call"})

	rejections := RecentCopyRejections()
	if len(rejections) != 1 {
		t.Fatalf("RecentCopyRejections() len = %d after exactly one logEntry() call through a copied sink, want 1 (not double-pushed)", len(rejections))
	}
	if rejections[0].Repeat != 0 {
		t.Fatalf("RecentCopyRejections()[0].Repeat = %d, want 0 (a single call must not look like 2 occurrences)", rejections[0].Repeat)
	}
	if got := len(Recent()); got != 0 {
		t.Fatalf("Recent() len = %d, want 0 (a copy-rejected entry must never land in the genuine ring)", got)
	}
}
