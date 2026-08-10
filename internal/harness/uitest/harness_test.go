package uitest

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestHarnessSendKeysDrivesOnKey is AC-1: NewHarness constructs the
// widget layer headlessly (no real terminal — no tcell.Screen is ever
// constructed by this package) and SendKeys injects synthetic key events
// that reach the caller's onKey handler on the harness's own T-INPUT
// goroutine, in order.
func TestHarnessSendKeysDrivesOnKey(t *testing.T) {
	var mu sync.Mutex
	var got []rune

	h := NewHarness(errs.NewCorrelationID(), func(msg uicore.InputMsg) {
		if msg.Kind != uicore.KeyInput {
			return
		}
		mu.Lock()
		got = append(got, msg.Rune)
		mu.Unlock()
	})
	defer h.Stop()

	if err := h.SendKeys("b r s"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []rune{'b', 'r', 's'}
	if len(got) != len(want) {
		t.Fatalf("got %d key events %q, want %q", len(got), string(got), string(want))
	}
	for i, r := range want {
		if got[i] != r {
			t.Errorf("event %d = %q, want %q", i, got[i], r)
		}
	}
}

// TestHarnessAttachFixtureFeedsViewModels is AC-3: attaching a
// harness.replay fixture (via replay.UIPlayer, see transport.go) feeds
// the widget layer's ViewModels in place of a live Transport — Render()
// against a fixture-driven ViewStore produces content derived from the
// fixture, not a blank/zero buffer.
func TestHarnessAttachFixtureFeedsViewModels(t *testing.T) {
	fx := buildFixture(t, 3)

	h := NewHarness(errs.NewCorrelationID(), nil, countDraw)
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("AttachFixture: %v", err)
	}
	if err := h.AwaitDeltas(3, 2*time.Second); err != nil {
		t.Fatalf("AwaitDeltas: %v", err)
	}
	if _, err := h.Render(); err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := h.Capture()
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if !strings.HasPrefix(got, "3.") {
		t.Fatalf("Capture()'s first two cells = %q, want \"3.\" (last patch n=3, not stale)", got[:2])
	}
}

// TestHarnessAttachFixtureTwiceRejected confirms AttachFixture is
// one-shot per Harness (documented behaviour, not silently allowing a
// second fixture to race the first's ViewsLoop over the same ViewStore).
func TestHarnessAttachFixtureTwiceRejected(t *testing.T) {
	fx := buildFixture(t, 1)
	h := NewHarness(errs.NewCorrelationID(), nil)
	defer h.Stop()

	if err := h.AttachFixture(fx); err != nil {
		t.Fatalf("first AttachFixture: %v", err)
	}
	if err := h.AttachFixture(fx); err == nil {
		t.Fatal("second AttachFixture: got nil error, want rejection")
	}
}
