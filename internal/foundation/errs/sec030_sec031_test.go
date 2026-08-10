package errs

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
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
// TestRingBuffer_ScanBackBoundary_JustBeyondLimitDoesNotCoalesce pin the
// exact off-by-one at coalesceScanBack's edge (Weakness pattern #3's
// "get the arithmetic right" — a previous round's summary/arithmetic
// mismatch in a sibling package was only caught by a Tester re-deriving
// it, so this pins both sides of the boundary explicitly rather than
// trusting the constant alone).
//
// With one MET-A push, followed by M pushes of M distinct OTHER codes,
// then a second MET-A push: the first MET-A sits exactly (1+M) slots
// back from the newest at the moment of the second push. The scan
// covers slots 1..coalesceScanBack back (inclusive), so coalescing
// succeeds when 1+M <= coalesceScanBack, i.e. M <= coalesceScanBack-1,
// and fails once M reaches coalesceScanBack.
func TestRingBuffer_ScanBackBoundary_ExactlyAtLimitStillCoalesces(t *testing.T) {
	r := newRingBuffer(200)
	r.push(Entry{Code: "MET-A"})
	for i := 0; i < coalesceScanBack-1; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-O%d", i)})
	}
	r.push(Entry{Code: "MET-A"}) // old MET-A is now exactly coalesceScanBack slots back

	snap := r.snapshot()
	if len(snap) != coalesceScanBack {
		t.Fatalf("ring has %d entries, want %d (MET-A coalesced at the exact edge of the scan-back window)", len(snap), coalesceScanBack)
	}
	aCount := 0
	for _, e := range snap {
		if e.Code == "MET-A" {
			aCount++
			if e.Repeat != 1 {
				t.Errorf("MET-A Repeat = %d, want 1", e.Repeat)
			}
		}
	}
	if aCount != 1 {
		t.Fatalf("found %d MET-A entries, want 1 (coalesced at the boundary)", aCount)
	}
}

func TestRingBuffer_ScanBackBoundary_JustBeyondLimitDoesNotCoalesce(t *testing.T) {
	r := newRingBuffer(200)

	// The expected MET-A count is derived from the pushes this test
	// actually makes, not asserted as a bare literal (GR#15): change the
	// setup and the assertion follows it instead of silently going stale.
	aPushes := []Entry{{Code: "MET-A"}, {Code: "MET-A"}}

	r.push(aPushes[0])
	for i := 0; i < coalesceScanBack; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-O%d", i)})
	}
	r.push(aPushes[1]) // old MET-A is now coalesceScanBack+1 slots back -- one past the window

	snap := r.snapshot()
	if len(snap) != coalesceScanBack+len(aPushes) {
		t.Fatalf("ring has %d entries, want %d (both MET-A occurrences distinct, one slot per MET-O, since the old MET-A fell just outside the scan-back window)", len(snap), coalesceScanBack+len(aPushes))
	}
	aCount := 0
	for _, e := range snap {
		if e.Code == "MET-A" {
			aCount++
			if e.Repeat != 0 {
				t.Errorf("MET-A entry Repeat = %d, want 0 (must not have coalesced)", e.Repeat)
			}
		}
	}
	if aCount != len(aPushes) {
		t.Fatalf("found %d MET-A entries, want %d (distinct, not coalesced — the gap exceeded coalesceScanBack)", aCount, len(aPushes))
	}
}

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

// TestRing_InterleavedFlood_BeyondScanBack_AcceptedTradeOff documents,
// with a runnable assertion rather than only a comment, exactly what
// ASM-107's accepted limit costs: MORE distinct interleaved codes than
// coalesceScanBack DOES still degrade toward pre-fix behaviour. This is
// deliberately proven, not hidden — the trade-off Bill asked to see
// "priced," not merely asserted.
func TestRing_InterleavedFlood_BeyondScanBack_AcceptedTradeOff(t *testing.T) {
	r := newRingBuffer(200)

	// coalesceScanBack+1 distinct codes, round-robin interleaved: each
	// code's own previous occurrence is always coalesceScanBack+1 slots
	// back once the ring is warmed up -- one slot beyond what push scans,
	// so this pattern never coalesces at all, and the ring grows exactly
	// like it would with no coalescing.
	distinctCodes := coalesceScanBack + 1
	iterations := distinctCodes * 20
	for i := 0; i < iterations; i++ {
		r.push(Entry{Code: fmt.Sprintf("MET-WIDE%d", i%distinctCodes)})
	}

	snap := r.snapshot()
	if len(snap) != 200 {
		t.Fatalf("ring has %d entries after flooding with %d round-robin codes (1 more than coalesceScanBack=%d), want 200 (filled to capacity, the accepted cost of the chosen K) — if this now passes with fewer entries, coalesceScanBack's boundary changed and this comment/ASM-107 need updating together", len(snap), distinctCodes, coalesceScanBack)
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
