package crime

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// SEC-020 copy guard: a *CrimeAPI is a mutable, mutex-bearing, map-holding
// value, so a struct copy would alias its maps/mutex across two values and
// defeat the concurrency-safety invariant. checkNotCopied (armed via
// self atomic.Pointer in New) rejects any method call on a copied value.

// crimeCopy performs the byte-for-byte struct copy (go vet's copylocks
// check would flag a literal `cp := *a`), mirroring engine.services'
// servicesCopy / engine.world's w2Copy convention.
func crimeCopy(a *CrimeAPI) *CrimeAPI {
	c := new(CrimeAPI)
	*(*[unsafe.Sizeof(CrimeAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(CrimeAPI{})]byte)(unsafe.Pointer(a))
	return c
}

func TestCrimeAPICopyGuard(t *testing.T) {
	orig := testAPI(t)
	advance(t, orig, 0, defaultDistrict(1))
	cp := crimeCopy(orig)

	if _, err := cp.Generation(1, CrimePettyTheft); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("Generation on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.SetStrategyMix(StrategyMix{Patrol: 0.5, Detective: 0.3, Community: 0.2}); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SetStrategyMix on a copied value: want ErrCopiedValue, got %v", err)
	}
	if err := cp.AdvanceMonth(1, []DistrictInput{defaultDistrict(1)}, SecurityInput{}); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("AdvanceMonth on a copied value: want ErrCopiedValue, got %v", err)
	}

	// The original is unaffected and still usable.
	if _, err := orig.Generation(1, CrimePettyTheft); err != nil {
		t.Fatalf("original API corrupted by the copy guard test: %v", err)
	}
}
