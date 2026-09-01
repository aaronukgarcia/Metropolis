package logistics

import (
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// logisticsAPIByteCopy performs SEC-020's attack — a plain LogisticsAPI
// struct copy — via a raw byte-for-byte memcpy through unsafe.Pointer,
// mirroring internal/engine/chemicals/security_test.go's refineryByteCopy
// (the reference that closed this class): a literal `cp := *l` is legal Go
// but go vet's copylocks check statically flags it, and this package must
// pass `go vet ./...`. The byte copy produces identical runtime semantics
// (self's pointer bytes copied unchanged, mu's bytes copied byte-for-byte)
// without a statically-flaggable copy expression.
func logisticsAPIByteCopy(l *LogisticsAPI) *LogisticsAPI {
	cp := new(LogisticsAPI)
	*(*[unsafe.Sizeof(LogisticsAPI{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(LogisticsAPI{})]byte)(unsafe.Pointer(l))
	return cp
}

// TestFEAT1972079946_SubscribeShortfalls_RejectsStructCopy proves
// SubscribeShortfalls — the void-mutating method FEAT-1972079946 (Aaron,
// 2026-09-01) converted from a bare-return guard swallow to an error
// return — REJECTS a struct-copied receiver with ErrCopiedValue rather than
// silently dropping the subscription (a caller believing it is wired to
// shortfall events when it never was). Revert its checkNotCopied branch
// back to a bare `return` and this test goes red — it asserts the ERROR
// VALUE returned, not merely "no panic".
func TestFEAT1972079946_SubscribeShortfalls_RejectsStructCopy(t *testing.T) {
	api := fixtureAPI(t, 1000, 1.0)
	cp := logisticsAPIByteCopy(api)

	fired := 0
	if err := cp.SubscribeShortfalls(func(ShortfallEvent) { fired++ }); !errors.Is(err, &errs.E{Code: ErrCopiedValue}) {
		t.Fatalf("SubscribeShortfalls on a struct-copied LogisticsAPI: err = %v, want ErrCopiedValue", err)
	}
	if len(cp.subs) != 0 {
		t.Fatalf("copy's subs = %d entries after a rejected SubscribeShortfalls, want 0", len(cp.subs))
	}

	// The ORIGINAL must be completely unaffected: its subscriber list must
	// not have gained the handler the copy's rejected call tried to add.
	if len(api.subs) != 0 {
		t.Fatalf("original LogisticsAPI.subs = %d entries after copy-attack call, want 0 (unaffected)", len(api.subs))
	}
}
