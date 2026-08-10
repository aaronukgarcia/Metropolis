package uitest

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// copyHarnessBytes performs a raw byte-for-byte memcpy of a Harness —
// identical in effect to the illegal-but-compilable `h2 := *h` (both
// alias every reference field: buffers, channels, the fixture pump), but
// via unsafe rather than a literal struct-copy expression, since Harness
// embeds a sync.Mutex and sync.WaitGroup and a literal copy would fail
// `go vet ./...`'s copylocks check (the VERIFY step requires vet to
// pass). Same technique and same reason as
// internal/harness/replay/copy_test.go's copyRecorderBytes.
func copyHarnessBytes(h *Harness) *Harness {
	c := new(Harness)
	*(*[unsafe.Sizeof(Harness{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Harness{})]byte)(unsafe.Pointer(h))
	return c
}

// TestHarnessCopyRejected demonstrates the SEC-020-class guard this
// package's quality bar requires: a struct-copied *Harness must be
// rejected on every exported method, never allowed to run its own
// independent mutex over buffers/channels ALIASED with the original —
// and the original must be entirely unaffected by the misuse attempt.
func TestHarnessCopyRejected(t *testing.T) {
	h := NewHarness(errs.NewCorrelationID(), nil)
	defer h.Stop()

	cp := copyHarnessBytes(h) // byte-for-byte copy — the misuse SEC-020 guards against

	if err := cp.SendKeys("b"); err == nil {
		t.Error("SendKeys on a struct-copied Harness: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeHarnessCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeHarnessCopied)
	}

	if err := cp.AttachFixture(buildFixture(t, 1)); err == nil {
		t.Error("AttachFixture on a struct-copied Harness: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeHarnessCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeHarnessCopied)
	}

	if _, err := cp.Render(); err == nil {
		t.Error("Render on a struct-copied Harness: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeHarnessCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeHarnessCopied)
	}

	if _, err := cp.Capture(); err == nil {
		t.Error("Capture on a struct-copied Harness: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeHarnessCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeHarnessCopied)
	}

	// Stop on a copy must be a silent no-op (no return value to carry a
	// rejection through, matching InProcTransport.Close's precedent) and
	// — critically — must NOT close the ORIGINAL's stopCh/src, which are
	// ALIASED with the copy's.
	cp.Stop()

	// The original must be entirely unaffected: it can still accept keys.
	if err := h.SendKeys("r"); err != nil {
		t.Errorf("original Harness rejected SendKeys after a copy was misused (including cp.Stop()): %v", err)
	}
}
