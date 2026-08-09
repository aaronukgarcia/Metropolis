package core

import (
	"errors"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestSEC014_StructCopyRejected_NoRace reproduces SEC-014 (Destructive-2,
// P1) against the FIXED engine and asserts the struct-copy attack is now
// refused rather than racing.
//
// The attack: `e2 := *e` is a legal, unsafe-free, reflect-free struct
// copy from outside package core (every field is unexported, but Go
// does not forbid copying an addressable struct value obtained from a
// dereferenced exported *Engine — only `go vet`'s copylocks check flags
// it, and only at build time, on a directly-visible copy, never a
// runtime one). Engine.hooks is a map (reference type), so e2.hooks
// ALIASES e.hooks. But mu and sealed were plain values, so pre-fix, e2
// got its OWN mu and its OWN sealed flag, independent of e's.
//
// Pre-fix, driving e2.AdvanceTicks (which called e2.seal() -> took
// e2.mu -> never observed e.sealed -> proceeded to read the SHARED
// e2.hooks map via runPhase with no lock, exactly like the original
// SEC-003 shape) concurrently with e.RegisterPhaseHook (which took e.mu
// -> mutated the SAME shared map) reproduced SEC-003's crash again,
// wearing a different hat: two different mutexes guarding one map. This
// was verified live against the code as it stood immediately before
// this fix (this exact test, run before the Engine.self/checkNotCopied
// change below existed) and produced:
//
//	WARNING: DATA RACE
//	Read at 0x... by goroutine 9: ... Engine.runPhase() phase.go:250
//	Previous write at 0x... by goroutine 8: ... Engine.RegisterPhaseHook() engine.go:285
//
// The fix (Engine.self, checkNotCopiedLocked — see engine.go) rejects a
// struct-copied Engine's very first call to either RegisterPhaseHook or
// seal() (called from AdvanceTicks) with ErrEngineCopied, before that
// call can touch the shared hooks map at all — so there is nothing left
// to race.
func TestSEC014_StructCopyRejected_NoRace(t *testing.T) {
	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("seed RegisterPhaseHook: %v", err)
	}

	// The attack: a plain struct copy. Legal Go, no unsafe, no reflect.
	e2 := e2Copy(e)

	// Every AdvanceTicks call on the copy must be rejected outright, not
	// merely "eventually rejected after a partial tick" — assert the
	// very first call already fails, and that the clock never moves.
	if err := e2.AdvanceTicks("sec014-first-call", 1); !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("e2.AdvanceTicks (copy) first call: err = %v, want ErrEngineCopied", err)
	}
	if got := e2.TicksCompleted(); got != 0 {
		t.Fatalf("e2.TicksCompleted() after rejected AdvanceTicks = %d, want 0 — a copy must never advance", got)
	}
	if err := e2.RegisterPhaseHook(PhaseConsumptionShortfall, noopHook{}); !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("e2.RegisterPhaseHook (copy): err = %v, want ErrEngineCopied", err)
	}

	// Now the concurrent-hammer shape from the original PoC: this must
	// no longer race, and every call on the copy must resolve to
	// ErrEngineCopied (never a silent success, never a crash).
	stop := make(chan struct{})
	var wgA sync.WaitGroup
	var badResults int
	var resMu sync.Mutex

	wgA.Add(1)
	go func() {
		defer wgA.Done()
		for {
			select {
			case <-stop:
				return
			default:
				err := e2.AdvanceTicks("sec014-hammer-advance", 1)
				if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
					resMu.Lock()
					badResults++
					resMu.Unlock()
				}
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		if err := e.RegisterPhaseHook(PhaseProduction, noopHook{}); err != nil {
			t.Fatalf("original e.RegisterPhaseHook call %d: unexpected error %v (the original Engine must be unaffected by the rejected copy)", i, err)
		}
	}

	close(stop)
	wgA.Wait()

	if badResults != 0 {
		t.Fatalf("%d of the copy's AdvanceTicks calls returned something other than ErrEngineCopied", badResults)
	}

	// The ORIGINAL Engine must still work normally after all this — the
	// copy attack must not have corrupted it.
	if err := e.AdvanceTicks("sec014-original-still-works", 30); err != nil {
		t.Fatalf("original Engine AdvanceTicks after copy attack: %v", err)
	}
	if got := clockOrFatal(t, e).Month(); got != 1 {
		t.Fatalf("original Engine Month() after 30 ticks = %d, want 1", got)
	}
}

// e2Copy performs SEC-014's attack — a plain Engine struct copy — via a
// raw byte-for-byte memcpy through unsafe.Pointer, rather than the
// literal `c := *e` a real attacker would write. Both produce IDENTICAL
// bytes and therefore identical runtime semantics (mu's zero-derived
// bytes copied as-is, hooks' map header copied — aliasing the same map
// — sealed's bool byte copied, self's pointer bytes copied unchanged);
// the only difference is that `c := *e` is a typed Go assignment `go
// vet`'s copylocks check statically flags (confirmed above — this
// package cannot contain that literal line and still pass `go vet ./...`,
// which this fix's VERIFY step requires), while this byte-level copy
// operates on an untyped [N]byte array vet does not analyze for lock
// content. This is a TEST-ONLY mechanism to keep exercising the
// regression once the literal attack line can no longer live in this
// repository; it changes nothing about what SEC-014 or its fix are
// about. The live, unsuppressed `go vet`-catchable form of the attack
// was run and captured (WARNING: DATA RACE, see this file's top
// comment) against the code exactly as it stood before Engine.self
// existed.
func e2Copy(e *Engine) *Engine {
	c := new(Engine)
	*(*[unsafe.Sizeof(Engine{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Engine{})]byte)(unsafe.Pointer(e))
	return c
}

// TestEngine_SelfIdentity_FreshEnginesAreDistinct is a sanity check that
// two independently constructed Engines never collide on identity (the
// non-attack path checkNotCopiedLocked must never reject).
func TestEngine_SelfIdentity_FreshEnginesAreDistinct(t *testing.T) {
	e1 := NewEngine()
	e2 := NewEngine()
	if err := e1.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("e1.RegisterPhaseHook: %v", err)
	}
	if err := e2.RegisterPhaseHook(PhaseDailyTick, noopHook{}); err != nil {
		t.Fatalf("e2.RegisterPhaseHook: %v", err)
	}
	if err := e1.AdvanceTicks("corr-e1", 1); err != nil {
		t.Fatalf("e1.AdvanceTicks: %v", err)
	}
	if err := e2.AdvanceTicks("corr-e2", 1); err != nil {
		t.Fatalf("e2.AdvanceTicks: %v", err)
	}
}
