package spiral

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// decayAPIByteCopy performs SEC-020's attack — a plain DecayAPI struct copy
// — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/chemicals/security_test.go's refineryByteCopy (the
// reference that closed this class): a literal `cp := *d` is legal Go but
// go vet's copylocks check statically flags it, and this package must pass
// `go vet ./...`. The byte copy produces identical runtime semantics (self's
// pointer bytes copied unchanged, mu's bytes copied byte-for-byte) without a
// statically-flaggable copy expression.
func decayAPIByteCopy(d *DecayAPI) *DecayAPI {
	cp := new(DecayAPI)
	*(*[unsafe.Sizeof(DecayAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(DecayAPI{})]byte)(unsafe.Pointer(d))
	return cp
}

// TestFEAT1972079946_RecordPopulationLocked_RejectsStructCopy proves
// recordPopulationLocked — the void-mutating helper FEAT-1972079946 (Aaron,
// 2026-09-01) converted from a bare-return guard swallow to an error return
// — REJECTS a struct-copied receiver with ErrCopiedValue rather than
// silently no-op'ing the population/historic-peak record. Revert its
// checkNotCopied branch back to a bare `return` and this test goes red — it
// asserts the ERROR VALUE returned, not merely "no panic".
func TestFEAT1972079946_RecordPopulationLocked_RejectsStructCopy(t *testing.T) {
	d, err := New("corr-setup")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cp := decayAPIByteCopy(d)

	if err := cp.recordPopulationLocked(0, 12345); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("recordPopulationLocked on a struct-copied DecayAPI: err = %v, want ErrCopiedValue", err)
	}

	// The ORIGINAL must be completely unaffected by the rejected call above.
	if len(d.popHistory) != 0 {
		t.Fatalf("original DecayAPI.popHistory = %v after copy-attack call, want empty", d.popHistory)
	}
	if d.historicPeak != 0 {
		t.Fatalf("original DecayAPI.historicPeak = %d after copy-attack call, want 0", d.historicPeak)
	}
}
