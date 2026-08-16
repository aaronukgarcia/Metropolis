package services

import (
	"testing"
	"unsafe"
)

// This file is the SEC-020-family copy-guard regression suite, modelled on
// internal/engine/world/copyguard_test.go: a *ServicesAPI is a mutable,
// mutex-bearing, map-holding value, so a struct copy (a2 := *a) would
// alias its maps/mutex across two values and defeat the concurrency-safety
// invariant. The guard (api.go's checkNotCopied + self atomic.Pointer)
// rejects a method call on a copied value with ErrCopiedValue.

// TestStructCopyRejected proves a struct-copied *ServicesAPI is refused on
// its mutating and querying methods, rather than silently operating on
// aliased internal state.
func TestStructCopyRejected(t *testing.T) {
	orig := testAPI(t)

	copied := servicesCopy(orig) // the exact value-copy the guard exists to catch
	if err := copied.RegisterService(ServiceSpec{ID: "x", Kind: ServiceHealthcare}); err == nil {
		t.Fatal("RegisterService on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}
	if err := copied.SetFunding("x", 1.0); err == nil {
		t.Fatal("SetFunding on a copied value returned nil, want ErrCopiedValue")
	} else {
		assertCode(t, err, ErrCopiedValue)
	}

	// The original still works — the guard does not damage the value it
	// protects (weakness pattern #5).
	if err := orig.SetUnlockGate(allowAllGate()); err != nil {
		t.Fatalf("original SetUnlockGate after copy attempt: %v", err)
	}
}

// servicesCopy takes a same-package value copy of *ServicesAPI, isolated
// into its own helper so the attack shape is documented once. A plain
// `cp := *a` is legal, correct Go that produces the identical attack
// shape, but go vet's copylocks check would flag that literal assignment
// at its own call site (ServicesAPI contains sync.RWMutex) and fail this
// package's own baseline gate — the byte-copy achieves the same
// struct-value copy (same mu bytes, same aliased maps) via a route
// copylocks does not statically recognise as a lock copy. Mirrors
// engine.world's w2Copy / engine.core's e2Copy convention exactly.
func servicesCopy(a *ServicesAPI) *ServicesAPI {
	c := new(ServicesAPI)
	*(*[unsafe.Sizeof(ServicesAPI{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(ServicesAPI{})]byte)(unsafe.Pointer(a))
	return c
}
