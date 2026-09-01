package policies

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// policiesAPIByteCopy performs SEC-020's attack — a plain PoliciesAPI struct
// copy — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/chemicals/security_test.go's refineryByteCopy (the
// reference that closed this class): a literal `cp := *a` is legal Go but
// go vet's copylocks check statically flags it, and this package must pass
// `go vet ./...`. The byte copy produces identical runtime semantics (self's
// pointer bytes copied unchanged, mu's bytes copied byte-for-byte) without a
// statically-flaggable copy expression.
func policiesAPIByteCopy(a *PoliciesAPI) *PoliciesAPI {
	cp := new(PoliciesAPI)
	*(*[unsafe.Sizeof(PoliciesAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(PoliciesAPI{})]byte)(unsafe.Pointer(a))
	return cp
}

// TestFEAT1972079946_VoidMutatingSwallowers_RejectStructCopy proves the
// three void-mutating helpers FEAT-1972079946 (Aaron, 2026-09-01) converted
// from a bare-return guard swallow to an error return now REJECT a
// struct-copied receiver with ErrCopiedValue, rather than silently
// no-op'ing the mutation (appendDriftEventsLocked dropping a checkpoint's
// drift events, resetTaxMovesLocked leaving a failed enactment's tax moves
// unrolled-back, raiseConflictWarningsLocked dropping an AC-11 conflict
// warning). Revert any one of these methods' checkNotCopied branch back to
// a bare `return`/`return nil` and this test goes red — it is asserting the
// ERROR VALUE returned, not merely "no panic".
func TestFEAT1972079946_VoidMutatingSwallowers_RejectStructCopy(t *testing.T) {
	a := NewPoliciesAPI("corr-setup")
	cp := policiesAPIByteCopy(a)

	if err := cp.appendDriftEventsLocked(nil); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("appendDriftEventsLocked on a struct-copied PoliciesAPI: err = %v, want ErrCopiedValue", err)
	}
	if err := cp.resetTaxMovesLocked(nil); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("resetTaxMovesLocked on a struct-copied PoliciesAPI: err = %v, want ErrCopiedValue", err)
	}
	if err := cp.raiseConflictWarningsLocked(&policyDef{}, Scope{}); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("raiseConflictWarningsLocked on a struct-copied PoliciesAPI: err = %v, want ErrCopiedValue", err)
	}

	// The ORIGINAL must be completely unaffected by every rejected call
	// above (no drift events appended, no warnings raised).
	if len(a.events) != 0 {
		t.Fatalf("original PoliciesAPI.events = %d entries after copy-attack calls, want 0", len(a.events))
	}
	if len(a.warnings) != 0 {
		t.Fatalf("original PoliciesAPI.warnings = %d entries after copy-attack calls, want 0", len(a.warnings))
	}
}
