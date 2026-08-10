package mapscreen

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// SEC-039 regression (Destructive-1, 2026-08-10; fix landed the same
// day, corrected after an initial brief that only bounded len(p.Cells)
// AFTER decode — see limits.go's maxPatchWireBytes and patch.go's
// decodeWirePatch doc comments for the full history). SEC-009's clamp
// in applyFullLocked bounds ONLY the derived grid slab (w*h against
// maxGridSide/maxGridCells). It did nothing about the size of the
// incoming "f1.viewport" patch itself: ApplyPatch called
// json.Unmarshal(raw, &p) — fully decoding p.Cells, a []wireCell whose
// length and per-string field sizes are entirely wire-controlled —
// BEFORE the Extent was ever inspected. applyFullLocked's per-cell loop
// then ranged over the FULL p.Cells slice regardless of how many of
// those entries actually land in-bounds for a tiny Extent, so a patch
// declaring extent 1x1 could still carry an arbitrarily large Cells
// array and pay full unmarshal + full O(len(Cells)) iteration cost.
//
// This test now asserts the FIXED behaviour (AC-10): the oversized wire
// payload is rejected by byte size BEFORE json.Unmarshal ever runs —
// proven mechanically via unmarshalAttempts (patch.go), not just by
// timing, plus a generous timing bound as a second, human-readable
// signal.
func TestSEC039_ApplyPatchRejectsOversizedWirePayloadBeforeDecode(t *testing.T) {
	m := NewMapScreen("poc-corr", widgets.Palette{})

	const nCells = 300_000
	const junkLen = 200 // bytes of filler per string field

	junk := strings.Repeat("X", junkLen)

	var b strings.Builder
	b.WriteString(`{"schemaVersion":1,"full":true,"origin":{"x":0,"y":0},"extent":{"width":1,"height":1},"cells":[`)
	for i := 0; i < nCells; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		// Every cell coordinate is WAY outside the declared 1x1 extent, so
		// none of them would ever be written into the grid even if this
		// patch WERE decoded — applyFullLocked's bounds check
		// (`c.X < 0 || c.X >= w || ...`) would silently skip every single
		// one, but only AFTER visiting it. The fix stops this patch from
		// ever reaching that loop, or even ever reaching json.Unmarshal.
		b.WriteString(`{"x":999999,"y":999999,"terrain":"` + junk + `","road":"` + junk + `","building":"` + junk + `"}`)
	}
	b.WriteString(`]}`)
	raw := json.RawMessage(b.String())

	if len(raw) <= maxPatchWireBytes {
		t.Fatalf("test setup: forged patch is %d bytes, want > maxPatchWireBytes (%d) so this test actually exercises the AC-10 gate", len(raw), maxPatchWireBytes)
	}
	t.Logf("forged f1.viewport patch: declared extent 1x1, raw wire size = %d bytes (%d cell entries) — %.1fx over the %d-byte maxPatchWireBytes ceiling", len(raw), nCells, float64(len(raw))/float64(maxPatchWireBytes), maxPatchWireBytes)

	before := unmarshalAttempts.Load()
	start := time.Now()
	m.ApplyPatch(raw)
	elapsed := time.Since(start)
	after := unmarshalAttempts.Load()

	// The decisive, non-timing proof: decodeWirePatch's json.Unmarshal
	// must never have been reached for this patch.
	if after != before {
		t.Fatalf("decodeWirePatch called json.Unmarshal on an oversized patch (unmarshalAttempts %d -> %d) — the SEC-039/AC-10 byte-size gate did not run before decoding", before, after)
	}

	// Elapsed time is REPORTED, never asserted on. The first version of
	// this test failed it against a 100ms ceiling, on the stated grounds
	// that 100ms was "generous headroom on any machine" — and CI disproved
	// that on the first run, taking 707ms for a rejection that costs 3.9ms
	// locally. A shared runner under -race, moving a 198MB payload and
	// collecting it, is simply not a stable clock.
	//
	// The rule this broke is one this project already had in writing:
	// concurrency and performance tests must be DETERMINISTIC, not
	// probable. A wall-clock threshold is a probabilistic assertion about
	// the machine, and it turns a correct fix into a red gate — which,
	// under GR#21, stops the line for everyone.
	//
	// The deterministic proof is the unmarshalAttempts check above: zero
	// decode attempts is incompatible with a 198MB json.Unmarshal having
	// run, and it is true on any machine at any speed. That is the
	// assertion; this is the human-readable colour beside it (Destructive-1
	// measured 1.43s to decode+iterate this exact shape unfixed).
	t.Logf("ApplyPatch rejected the %d-byte oversized patch in %v with zero Unmarshal attempts (PoC measured 1.43s unfixed; timing is informational, the zero-attempt count above is the proof)", len(raw), elapsed)

	// The grid must never have been touched — same "reject, never
	// clamp, keep last-known-good state" posture as every other
	// malformed-patch path in this package.
	if m.haveSnapshot || m.width != 0 || m.height != 0 || len(m.grid) != 0 {
		t.Fatalf("state changed after a rejected oversized patch: haveSnapshot=%v width=%d height=%d len(grid)=%d, want all zero/false", m.haveSnapshot, m.width, m.height, len(m.grid))
	}
}

// TestSEC039_ApplyPatchRejectsExcessCellsEvenUnderByteBudget is AC-11:
// defense in depth. A patch can pass the AC-10 byte-size gate (small
// per-cell payloads keep total bytes low) while still carrying more
// Cells entries than this screen could ever legitimately need — this
// must still be rejected, AFTER decode this time, before either
// applyFullLocked's or applySparseLocked's per-cell loop runs. Proven
// via unmarshalAttempts too, the other way round this time: THIS gate
// necessarily runs AFTER a real Unmarshal, unlike AC-10's.
func TestSEC039_ApplyPatchRejectsExcessCellsEvenUnderByteBudget(t *testing.T) {
	m := NewMapScreen("poc-corr-2", widgets.Palette{})

	// One cell entry per line is small (~13 bytes: `{"x":0,"y":0},`), so
	// maxGridCells+1 of them stays comfortably under maxPatchWireBytes —
	// this is the point: AC-10's byte gate alone would NOT catch this
	// shape, only AC-11's Cells-length gate does.
	nCells := maxGridCells + 1

	var b strings.Builder
	b.Grow(nCells*14 + 128)
	b.WriteString(`{"schemaVersion":1,"full":true,"origin":{"x":0,"y":0},"extent":{"width":1,"height":1},"cells":[`)
	for i := 0; i < nCells; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"x":0,"y":0}`)
	}
	b.WriteString(`]}`)
	raw := json.RawMessage(b.String())

	if len(raw) >= maxPatchWireBytes {
		t.Fatalf("test setup: forged patch is %d bytes, want < maxPatchWireBytes (%d) so this test actually isolates the AC-11 gate from AC-10's", len(raw), maxPatchWireBytes)
	}
	t.Logf("forged f1.viewport patch: %d cell entries (maxGridCells+1), raw wire size = %d bytes (under the %d-byte AC-10 ceiling)", nCells, len(raw), maxPatchWireBytes)

	before := unmarshalAttempts.Load()
	m.ApplyPatch(raw)
	after := unmarshalAttempts.Load()

	if after != before+1 {
		t.Fatalf("unmarshalAttempts moved %d -> %d, want exactly +1 — this patch should pass AC-10's byte gate and reach json.Unmarshal exactly once", before, after)
	}
	if m.haveSnapshot || m.width != 0 || m.height != 0 || len(m.grid) != 0 {
		t.Fatalf("state changed after a rejected excess-Cells patch: haveSnapshot=%v width=%d height=%d len(grid)=%d, want all zero/false", m.haveSnapshot, m.width, m.height, len(m.grid))
	}
}
