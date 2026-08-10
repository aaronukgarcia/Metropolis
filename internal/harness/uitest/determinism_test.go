package uitest

import (
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestDeterministicCapture is AC-9 (GR#21): running the same scripted
// sequence against the same fixture twice produces byte-identical
// captured cell-buffer output both times. AwaitDeltas is driven by real
// completion signals (transport.go's exhausted channel / deltasSeen
// count), never a sleep, so this holds regardless of scheduling — see
// doc.go's "Determinism" section.
func TestDeterministicCapture(t *testing.T) {
	fx := buildFixture(t, 4)

	run := func() string {
		h := NewHarness(errs.NewCorrelationID(), nil, countDraw)
		defer h.Stop()
		if err := h.AttachFixture(fx); err != nil {
			t.Fatalf("AttachFixture: %v", err)
		}
		if err := h.RunScript("b r s d", 4, 2*time.Second); err != nil {
			t.Fatalf("RunScript: %v", err)
		}
		if _, err := h.Render(); err != nil {
			t.Fatalf("Render: %v", err)
		}
		got, err := h.Capture()
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		return got
	}

	a := run()
	b := run()
	if a != b {
		t.Fatalf("uitest: Capture() differs between two runs of the same script+fixture:\n--- run 1 ---\n%s--- run 2 ---\n%s", a, b)
	}
}
