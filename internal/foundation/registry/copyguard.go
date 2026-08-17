package registry

import (
	"maps"
	"slices"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// codeCopyGuardCopied is the registry-sourced error a CopyGuard.Check
// returns on a struct copy (SEC-020). MET-F107 is the first free code in
// the foundation.registry F100-F199 sub-range (F100-F106 are the Registry
// type's own codes, ending at codeRegistryCopied = MET-F106).
const codeCopyGuardCopied = "MET-F107"

// CopyGuard[T] is the reusable, embed-by-value copy-guard wrapper that
// generalises Registry.checkNotCopied (and the hand-rolled self
// atomic.Pointer[T] + checkNotCopied triples in engine.core,
// protocol/transport, engine/stub, engine/debug, and the ui.* SEC-020
// sites). A type with shared mutable state embeds it and gets the whole
// SEC-020 pattern from a single field — no per-module hand-rolling:
//
//	type guarded struct {
//		mu    sync.Mutex
//		guard CopyGuard[guarded]
//		n     int
//	}
//
//	func newGuarded() *guarded {
//		g := &guarded{}
//		g.guard.Bind() // identity captured exactly once, at the end of construction
//		return g
//	}
//
//	func (g *guarded) setN(correlationID string, n int) error {
//		if err := g.guard.Check(correlationID, map[string]any{"method": "setN"}); err != nil {
//			return err
//		}
//		g.mu.Lock()
//		defer g.mu.Unlock()
//		g.n = n
//		return nil
//	}
//
// feat.securehelpers (FEAT-135) — the reject-at-the-boundary discipline:
// alongside SEC-066 (return a defensive copy, never a live pointer — see
// CloneMap/CloneSlice below), SEC-080 and SEC-093 (reject non-finite and
// overflowing inputs — internal/foundation/num). The rule in one line:
// never wrap; never leak +Inf/NaN from a finite input; reject — never
// silently clamp — at the boundary; never return a live pointer.
type CopyGuard[T any] struct {
	// self holds the address of this CopyGuard field as it exists inside
	// the value its constructor returned (Bind stores it). A struct copy
	// carries the pointer bytes verbatim — so the copy's self.Load()
	// still names the ORIGINAL's field address, while Check's receiver g
	// names the COPY's field address; the mismatch is what rejects the
	// copy. atomic.Pointer (not a plain *CopyGuard[T]) for the SEC-016
	// reason documented on Registry.self: the identity check must be
	// race-safe AND run before the embedder's mutex is ever touched.
	self atomic.Pointer[CopyGuard[T]]
}

// Bind captures the guard's identity. The embedder's constructor calls it
// exactly once, at the end, before the value is returned to any caller —
// mirroring NewRegistry's self.Store(r). A value whose Bind never ran (the
// zero value, new(T), or a hand-built literal) has a nil self, which Check
// treats as a misuse and rejects fail-closed.
func (g *CopyGuard[T]) Bind() {
	g.self.Store(g)
}

// Check reports whether the receiving value is a struct copy (or an
// un-Bound zero value), returning a registry-sourced codeCopyGuardCopied
// error if so. It is deliberately lock-free — a single atomic.Pointer.Load,
// nothing else — so it is safe and correct to call BEFORE the embedder's
// own mutex is ever touched (SEC-016: a copy taken while the original's
// mutex was held captures mutex bytes that read as permanently "locked";
// acquiring the copy's own mutex in that state would park forever, so the
// guard must run before any lock attempt).
func (g *CopyGuard[T]) Check(correlationID string, ctx map[string]any) error {
	if g.self.Load() != g {
		return errs.New(codeCopyGuardCopied, correlationID, ctx)
	}
	return nil
}

// CloneMap returns a shallow copy of m — the defensive-copy discipline
// (SEC-066) in one call: an accessor that must hand back internal map
// state hands back a copy, so a caller mutating the result corrupts
// nothing. A nil map clones to nil. A thin wrapper over stdlib maps.Clone,
// kept here so "return a copy, never a live pointer" has a single
// sanctioned name.
func CloneMap[K comparable, V any](m map[K]V) map[K]V {
	return maps.Clone(m)
}

// CloneSlice returns a copy of s with its own backing array — the
// defensive-copy discipline (SEC-066) for slices: mutating the result
// never mutates the source's backing array. A nil slice clones to nil. A
// thin wrapper over stdlib slices.Clone, kept here for the same
// single-sanctioned-name reason as CloneMap.
func CloneSlice[V any](s []V) []V {
	return slices.Clone(s)
}
