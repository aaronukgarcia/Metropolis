package maintenance

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SEC-020 copy guard: a *MaintenanceAPI is a mutable, mutex-bearing,
// map-holding value, so a struct copy would alias its maps/mutex across two
// values and defeat the concurrency-safety invariant. checkNotCopied (armed
// via the self atomic.Pointer in New) rejects any method call on a copied
// value.

// maintenanceCopy performs the byte-for-byte struct copy (go vet's copylocks
// check would flag a literal `cp := *a`), mirroring engine.crime's crimeCopy
// and engine.leisure's copy-guard convention.
func maintenanceCopy(a *MaintenanceAPI) *MaintenanceAPI {
	c := new(MaintenanceAPI)
	*(*[unsafe.Sizeof(MaintenanceAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(MaintenanceAPI{})]byte)(unsafe.Pointer(a))
	return c
}

// TestMaintenanceAPICopyGuard proves every guarded method rejects a struct
// copy with ErrCopiedValue, and the original is unaffected.
func TestMaintenanceAPICopyGuard(t *testing.T) {
	orig := newTestAPI(t)
	if err := orig.Register(1, "dwelling", RegisterOptions{}, "test"); err != nil {
		t.Fatalf("register: %v", err)
	}
	cp := maintenanceCopy(orig)

	if _, err := cp.View(1, "test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("View on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.SetDailyBudget(10, "test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SetDailyBudget on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.AdvanceMonth(1, "test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("AdvanceMonth on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.Register(2, "shop", RegisterOptions{}, "test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("Register on a copied value: want ErrCopiedValue, got %v", err)
	}
	if _, err := cp.RunCrewDay("test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("RunCrewDay on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.SetFinance(nil); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SetFinance on a copied value: want ErrCopiedValue, got %v", err)
	}
	if _, err := cp.TotalBacklog("test"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("TotalBacklog on a copied value: want ErrCopiedValue, got %v", err)
	}

	// The original is unaffected and still usable.
	if _, err := orig.View(1, "test"); err != nil {
		t.Fatalf("original API corrupted by the copy guard test: %v", err)
	}
}
