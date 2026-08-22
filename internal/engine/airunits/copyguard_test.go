package airunits

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SEC-020 copy guard: an *AirUnitsAPI is a mutable, mutex-bearing, map-
// holding value, so a struct copy would alias its fleet map/mutex across two
// values and defeat the concurrency-safety invariant. checkNotCopied (armed
// via self atomic.Pointer in New) rejects any method call on a copied value.
//
// PR #58 astgate reconciliation (lane/bob sweep 2026-08-20): PoliceEffect,
// FireEffect, AmbulanceEffect, and VIPEffect were newly guarded (they
// previously called only the already-guarded RoleEffect, but astgate's
// syntactic per-function analysis requires each exported entry point to
// carry its own checkNotCopied call — see the GR#23/SEC-020 house rule).
// This test proves the guard is genuinely live on those four methods, not
// merely present in the source.

// airUnitsCopy performs the byte-for-byte struct copy (go vet's copylocks
// check would flag a literal `cp := *a`), mirroring engine.crime's
// crimeCopy / engine.services' servicesCopy / engine.world's w2Copy
// convention.
func airUnitsCopy(a *AirUnitsAPI) *AirUnitsAPI {
	c := new(AirUnitsAPI)
	*(*[unsafe.Sizeof(AirUnitsAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(AirUnitsAPI{})]byte)(unsafe.Pointer(a))
	return c
}

func TestAirUnitsAPICopyGuard(t *testing.T) {
	orig := newAPIWithSeed(t, 1, defaultData())
	cp := airUnitsCopy(orig)

	// The four Effect accessors newly guarded by this sweep (PR #58).
	if _, err := cp.PoliceEffect(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("PoliceEffect on a copied value: want ErrCopiedValue, got %v", err)
	}
	if _, err := cp.FireEffect(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("FireEffect on a copied value: want ErrCopiedValue, got %v", err)
	}
	if _, err := cp.AmbulanceEffect(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("AmbulanceEffect on a copied value: want ErrCopiedValue, got %v", err)
	}
	if _, err := cp.VIPEffect(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("VIPEffect on a copied value: want ErrCopiedValue, got %v", err)
	}

	// A representative sample of the pre-existing guarded surface, so a
	// regression that disarms the shared self atomic.Pointer is caught here
	// too, not just on the four methods this sweep touched.
	if _, err := cp.RoleEffect(UnitPolice); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("RoleEffect on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.AssignPilot(1, 1); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("AssignPilot on a copied value: want ErrCopiedValue, got %v", err)
	}

	// The original is unaffected and still usable.
	if _, err := orig.PoliceEffect(); err != nil {
		t.Fatalf("original API corrupted by the copy guard test: %v", err)
	}
}
