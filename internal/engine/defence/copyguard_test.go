package defence

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// defenceByteCopy performs SEC-020's attack — a plain DefenceAPI struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/foundation/registry/sec020_test.go's registryByteCopy (a literal
// `cp := *d` is forbidden here because go vet's copylocks check flags the
// copied sync.RWMutex, and VERIFY runs go vet).
func defenceByteCopy(d *DefenceAPI) *DefenceAPI {
	cp := new(DefenceAPI)
	*(*[unsafe.Sizeof(DefenceAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(DefenceAPI{})]byte)(unsafe.Pointer(d))
	return cp
}

// TestDefenceAPICopyGuard (SEC-020 family): a command issued against a
// struct-copied DefenceAPI is rejected with ErrCopiedValue rather than racing
// the copy's own mutex over the original's aliased maps.
func TestDefenceAPICopyGuard(t *testing.T) {
	d := newDefence(t, validConfig(), 1)
	cp := defenceByteCopy(d)

	err := cp.SetPlanningQuality(0.5)
	if err == nil {
		t.Fatal("expected ErrCopiedValue from a struct-copied DefenceAPI, got nil")
	}
	if !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("expected ErrCopiedValue (%s), got %v", ErrCopiedValue, err)
	}

	// The original is unaffected and still usable.
	if got := d.ReputationPenalty(); got != 0 {
		t.Fatalf("original API corrupted by the copy guard test: penalty %d", got)
	}
}
