package freight

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// containerPortByteCopy performs SEC-160's attack — a plain ContainerPort
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer, the
// sanctioned TEST-ONLY mechanism (mirroring
// internal/engine/chemicals/security_test.go's refineryByteCopy and
// internal/foundation/errs/copyguard_test.go's loggerByteCopy): a literal
// `c := *cp` is legal Go but go vet's copylocks check statically flags it,
// and this package must pass `go vet ./...`. The byte copy produces identical
// runtime semantics (self's pointer bytes copied unchanged, still pointing at
// the original) without a statically-flaggable copy expression.
func containerPortByteCopy(cp *ContainerPort) *ContainerPort {
	out := new(ContainerPort)
	*(*[unsafe.Sizeof(ContainerPort{})]byte)(unsafe.Pointer(out)) = *(*[unsafe.Sizeof(ContainerPort{})]byte)(unsafe.Pointer(cp))
	return out
}

// ---- ASM-1286/SEC-160: Wire* setters reject a struct-copied ContainerPort,
// they no longer silently no-op on the copy ----
//
// This is the prove-can-fail regression for the fix: before the fix, WireRail/
// WirePermit/WireDecommission were void — checkNotCopied's error was computed
// and then discarded, so a struct-copied port's Wire* calls returned nothing
// observable at all (a silent no-op). If any of these three methods reverts
// to swallowing the guard error (i.e. loses its `return err`), this test goes
// RED because errors.Is on a nil error is false.
func TestCopiedContainerPortWireReturnsError(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	copied := containerPortByteCopy(cp)

	if err := copied.WireRail(newStubRail()); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.WireRail: expected ErrCopiedValue, got %v", err)
	}
	if err := copied.WirePermit(&stubPermit{grant: true}); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.WirePermit: expected ErrCopiedValue, got %v", err)
	}
	if err := copied.WireDecommission(&stubDecom{}); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.WireDecommission: expected ErrCopiedValue, got %v", err)
	}

	// The rejection must leave the copy's seams unwired — checkNotCopied runs
	// before the lock, so no write happens on a rejected call (no side effect
	// on the copy distinguishes "rejected" from "wired then rejected").
	if copied.rail != nil || copied.permit != nil || copied.decom != nil {
		t.Fatalf("rejected Wire* left a seam wired on the copy: rail=%v permit=%v decom=%v",
			copied.rail, copied.permit, copied.decom)
	}

	// Sanity: the original's Wire* still wires — the guard rejects the copy,
	// not legitimate wiring.
	if err := cp.WireRail(newStubRail()); err != nil {
		t.Fatalf("original.WireRail: unexpected error %v", err)
	}
	if err := cp.WirePermit(&stubPermit{grant: true}); err != nil {
		t.Fatalf("original.WirePermit: unexpected error %v", err)
	}
	if err := cp.WireDecommission(&stubDecom{}); err != nil {
		t.Fatalf("original.WireDecommission: unexpected error %v", err)
	}
}

// ---- ASM-1286/SEC-136: guarded query methods return the error, not a
// nil/zero sentinel ----
//
// Before the fix, Tiers() returned a bare nil on a copied port (the swallow
// the ASM names), BalanceOfTrade() returned a bare zero BalanceOfTrade{}, and
// ActiveTier() returned a bare "" — all three indistinguishable from a
// legitimate empty/zero result. If any of these reverts to swallowing, this
// test goes RED (the error assertion fails because the returned error is
// nil).
func TestCopiedContainerPortQueriesReturnError(t *testing.T) {
	cp := loadContainerPortFixture(t, nil)
	copied := containerPortByteCopy(cp)

	if _, err := copied.Tiers(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.Tiers: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.Tier("deep_sea_terminal"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.Tier: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.DeepSeaTier(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.DeepSeaTier: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.TierPhysicalCapacity("deep_sea_terminal"); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.TierPhysicalCapacity: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.PhysicalCapacity(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.PhysicalCapacity: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.CustomsCapacity(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.CustomsCapacity: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.CustomsSaturation(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.CustomsSaturation: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.SmugglingRisk(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.SmugglingRisk: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.IntermodalTransfer(ModeSea, ModeRail, 100); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.IntermodalTransfer: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.IntermodalAccount(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.IntermodalAccount: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.BalanceOfTrade(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.BalanceOfTrade: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.Import("concrete", 5, ModeRoad); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.Import: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.Export("concrete", 5, ModeRoad); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.Export: expected ErrCopiedValue, got %v", err)
	}
	if _, err := copied.ActiveTier(); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.ActiveTier: expected ErrCopiedValue, got %v", err)
	}
	if err := copied.Build("deep_sea_terminal", 9); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("copied.Build: expected ErrCopiedValue, got %v", err)
	}

	// Positive control: the original (non-copied) port still queries fine
	// with a nil error.
	if _, err := cp.Tiers(); err != nil {
		t.Fatalf("original.Tiers: unexpected error %v", err)
	}
	if _, err := cp.ActiveTier(); err != nil {
		t.Fatalf("original.ActiveTier: unexpected error %v", err)
	}
	if _, err := cp.BalanceOfTrade(); err != nil {
		t.Fatalf("original.BalanceOfTrade: unexpected error %v", err)
	}
}
