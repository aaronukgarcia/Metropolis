package coastal

import (
	"testing"
	"unsafe"
)

// TestStructCopyRejected (SEC-020 family): a method call on a struct-copied
// *CoastalAPI is rejected with ErrCopiedValue before the lock is touched,
// mirroring engine.services/engine.comms/engine.world.
func TestStructCopyRejected(t *testing.T) {
	orig := mustAPI(t, testConfig(), newFakeShore(oneCell))

	cp := coastalCopy(orig) // the exact value-copy the guard exists to catch
	if err := cp.SetProcessingFunding(0.2); err == nil {
		t.Fatal("SetProcessingFunding on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertRegistryCode(t, err, ErrCopiedValue)
	}
	if _, err := cp.Advance(0); err == nil {
		t.Fatal("Advance on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertRegistryCode(t, err, ErrCopiedValue)
	}

	// The original still works — the guard does not damage the value it
	// protects (weakness pattern #5).
	if err := orig.SetProcessingFunding(0.4); err != nil {
		t.Fatalf("original SetProcessingFunding after copy attempt: %v", err)
	}
}

// coastalCopy takes a same-package value copy of *CoastalAPI, isolated into
// its own helper so the attack shape is documented once. A plain `cp := *a`
// is legal Go producing the identical attack shape, but go vet's copylocks
// check flags that literal assignment (CoastalAPI contains sync.RWMutex) and
// fails this package's own baseline gate — the byte-copy achieves the same
// struct-value copy (same mu bytes, same aliased maps) via a route copylocks
// does not statically recognise as a lock copy. Mirrors engine.world's
// w2Copy / engine.services' servicesCopy convention exactly.
func coastalCopy(a *CoastalAPI) *CoastalAPI {
	c := new(CoastalAPI)
	*(*[unsafe.Sizeof(CoastalAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(CoastalAPI{})]byte)(unsafe.Pointer(a))
	return c
}
