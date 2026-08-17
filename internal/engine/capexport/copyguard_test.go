package capexport

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the SEC-020-family copy-guard regression suite, modelled on
// internal/engine/services/copyguard_test.go: a *CapExportAPI is a mutable,
// mutex-bearing, map-holding value, so a struct copy (a2 := *a) would alias
// its maps/mutex across two values and defeat the concurrency-safety
// invariant. The guard (api.go's checkNotCopied + self atomic.Pointer) rejects
// a method call on a copied value with ErrCopiedValue.

// TestCopiedValueRejected proves a struct-copied *CapExportAPI is refused on
// its mutating and querying methods, rather than silently operating on aliased
// internal state — and that the original still works afterwards (weakness
// pattern #5: the guard must not damage the value it protects).
func TestCopiedValueRejected(t *testing.T) {
	orig, _, _, _ := newTestAPI(t)

	copied := capexportCopy(orig) // the exact value-copy the guard exists to catch
	if err := copied.SetServices(services.New("other")); err == nil {
		t.Fatal("SetServices on a copied value returned nil, want ErrCopiedValue")
	} else if !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SetServices on a copied value err = %v, want ErrCopiedValue", err)
	}
	if _, err := copied.SurplusBook(ExportHospitalBeds); err == nil {
		t.Fatal("SurplusBook on a copied value returned nil, want ErrCopiedValue")
	} else if !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SurplusBook on a copied value err = %v, want ErrCopiedValue", err)
	}

	// The original still works — the guard does not damage the value it
	// protects (weakness pattern #5).
	if err := orig.SetServices(services.New("orig")); err != nil {
		t.Fatalf("original SetServices after copy attempt: %v", err)
	}
}

// capexportCopy takes a same-package value copy of *CapExportAPI, isolated
// into its own helper so the attack shape is documented once. A plain
// `cp := *a` is legal, correct Go that produces the identical attack shape,
// but go vet's copylocks check would flag that literal assignment at its own
// call site (CapExportAPI contains sync.RWMutex) and fail this package's
// baseline gate — the byte-copy achieves the same struct-value copy (same mu
// bytes, same aliased maps) via a route copylocks does not statically
// recognise as a lock copy. Mirrors engine.services' servicesCopy /
// engine.world's w2Copy convention exactly.
func capexportCopy(a *CapExportAPI) *CapExportAPI {
	c := new(CapExportAPI)
	*(*[unsafe.Sizeof(CapExportAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(CapExportAPI{})]byte)(unsafe.Pointer(a))
	return c
}
