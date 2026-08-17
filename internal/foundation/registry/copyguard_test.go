package registry

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// guarded is the fresh, embed-by-value type AC-10 asks for: a single
// CopyGuard[guarded] field gives it the whole SEC-020 pattern, nothing
// hand-rolled.
type guarded struct {
	mu sync.Mutex
	g  CopyGuard[guarded]
	n  int
}

func newGuarded() *guarded {
	g := &guarded{}
	g.g.Bind() // identity captured exactly once, at the end of construction
	return g
}

func (g *guarded) setN(correlationID string, n int) error {
	if err := g.g.Check(correlationID, map[string]any{"method": "setN"}); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n = n
	return nil
}

func (g *guarded) getN(correlationID string) (int, error) {
	if err := g.g.Check(correlationID, map[string]any{"method": "getN"}); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n, nil
}

// guardedRW exercises the read-lock-held copy case (AC-11): the same
// wrapper over a sync.RWMutex, whose RLock path the pre-lock guard must
// protect exactly like a write Lock.
type guardedRW struct {
	mu sync.RWMutex
	g  CopyGuard[guardedRW]
	n  int
}

func newGuardedRW() *guardedRW {
	g := &guardedRW{}
	g.g.Bind()
	return g
}

func (g *guardedRW) setN(correlationID string, n int) error {
	if err := g.g.Check(correlationID, map[string]any{"method": "setN"}); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n = n
	return nil
}

func (g *guardedRW) getN(correlationID string) (int, error) {
	if err := g.g.Check(correlationID, map[string]any{"method": "getN"}); err != nil {
		return 0, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.n, nil
}

// guardedByteCopy / guardedRWByteCopy are the sanctioned TEST-ONLY byte
// copies (see registryByteCopy's doc comment — a literal `c := *g` is
// legal but vet-flagged, and these types embed a sync.Mutex/RWMutex value).
func guardedByteCopy(g *guarded) *guarded {
	c := new(guarded)
	*(*[unsafe.Sizeof(guarded{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(guarded{})]byte)(unsafe.Pointer(g))
	return c
}

func guardedRWByteCopy(g *guardedRW) *guardedRW {
	c := new(guardedRW)
	*(*[unsafe.Sizeof(guardedRW{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(guardedRW{})]byte)(unsafe.Pointer(g))
	return c
}

func wantCopyGuardCopied(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: codeCopyGuardCopied}) {
		t.Fatalf("err = %v, want codeCopyGuardCopied (%s)", err, codeCopyGuardCopied)
	}
}

// AC-10: a byte-copied guarded value's Check rejects it fail-closed, and
// the original is unaffected.
func TestCopyGuard_RejectsStructCopy(t *testing.T) {
	orig := newGuarded()
	if err := orig.setN(errs.NewCorrelationID(), 42); err != nil {
		t.Fatalf("setup setN: %v", err)
	}

	cp := guardedByteCopy(orig)

	wantCopyGuardCopied(t, cp.setN(errs.NewCorrelationID(), 99))
	if _, err := cp.getN(errs.NewCorrelationID()); err == nil {
		t.Fatal("cp.getN on a copy = nil error, want codeCopyGuardCopied")
	} else {
		wantCopyGuardCopied(t, err)
	}

	if n, err := orig.getN(errs.NewCorrelationID()); err != nil || n != 42 {
		t.Fatalf("original getN after copy attack = (%d, %v), want (42, nil)", n, err)
	}
}

// AC-10(c): the zero value, new(T), and a hand-built literal are all
// rejected (Bind never ran), none silently usable.
func TestCopyGuard_ZeroValueNewAndLiteralRejected(t *testing.T) {
	var z guarded
	wantCopyGuardCopied(t, z.setN(errs.NewCorrelationID(), 1))
	if _, err := z.getN(errs.NewCorrelationID()); err == nil {
		t.Fatal("zero-value getN = nil error, want codeCopyGuardCopied")
	} else {
		wantCopyGuardCopied(t, err)
	}

	p := new(guarded)
	wantCopyGuardCopied(t, p.setN(errs.NewCorrelationID(), 2))

	lit := &guarded{n: 5} // hand-built literal, Bind never ran
	if _, err := lit.getN(errs.NewCorrelationID()); err == nil {
		t.Fatal("hand-built literal getN = nil error, want codeCopyGuardCopied")
	} else {
		wantCopyGuardCopied(t, err)
	}
}

// AC-11: a copy taken while the original's mutex was held is rejected
// promptly (the lock-free pre-lock Check), not hung — for both a
// write-lock-held and a read-lock-held copy, each within a 3s bound.
func TestCopyGuard_CopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	t.Run("copy taken during Lock", func(t *testing.T) {
		orig := newGuarded()
		orig.mu.Lock()
		cp := guardedByteCopy(orig) // cp.mu bytes read "locked"
		orig.mu.Unlock()

		done := make(chan error, 1)
		go func() { done <- cp.setN(errs.NewCorrelationID(), 1) }()
		select {
		case err := <-done:
			wantCopyGuardCopied(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("SEC-020 REGRESSION: setN on a copy taken while mu was held did not return within 3s")
		}

		if err := orig.setN(errs.NewCorrelationID(), 7); err != nil {
			t.Fatalf("original setN after attack: %v", err)
		}
	})

	t.Run("copy taken during RLock", func(t *testing.T) {
		orig := newGuardedRW()
		if err := orig.setN(errs.NewCorrelationID(), 1); err != nil {
			t.Fatalf("setup: %v", err)
		}
		orig.mu.RLock()
		cp := guardedRWByteCopy(orig) // cp.mu bytes read "read-locked"
		orig.mu.RUnlock()

		done := make(chan error, 1)
		go func() { _, err := cp.getN(errs.NewCorrelationID()); done <- err }()
		select {
		case err := <-done:
			wantCopyGuardCopied(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("SEC-020 REGRESSION: getN on a copy taken during RLock did not return within 3s")
		}

		if n, err := orig.getN(errs.NewCorrelationID()); err != nil || n != 1 {
			t.Fatalf("original getN after attack = (%d, %v), want (1, nil)", n, err)
		}
	})
}

// AC-12: CloneMap/CloneSlice return a defensive copy — mutating the result
// never mutates the source (SEC-066's reproduction, inverted).
func TestCloneMapAndCloneSlice_DefensiveCopy(t *testing.T) {
	src := map[string]int{"a": 1, "b": 2}
	cpy := CloneMap(src)
	cpy["a"] = 99
	cpy["c"] = 3
	if src["a"] != 1 || len(src) != 2 {
		t.Fatalf("CloneMap source mutated: %v", src)
	}
	if !reflect.DeepEqual(src, map[string]int{"a": 1, "b": 2}) {
		t.Fatalf("CloneMap source changed: %v", src)
	}

	srcS := []int{1, 2, 3}
	cpyS := CloneSlice(srcS)
	cpyS[0] = 99
	if srcS[0] != 1 {
		t.Fatalf("CloneSlice source backing mutated: %v", srcS)
	}

	if CloneMap[string, int](nil) != nil {
		t.Fatal("CloneMap(nil) != nil")
	}
	if CloneSlice[int](nil) != nil {
		t.Fatal("CloneSlice(nil) != nil")
	}
}

// AC-13: the wrapper is race-free — one goroutine mutates under the lock
// while others Check/read. Run with -race.
func TestCopyGuard_ConcurrentAccess_RaceFree(t *testing.T) {
	g := newGuarded()
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if err := g.setN(errs.NewCorrelationID(), i); err != nil {
				t.Errorf("setN: %v", err)
				return
			}
		}
	}()

	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if _, err := g.getN(errs.NewCorrelationID()); err != nil {
					t.Errorf("getN: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	if n, err := g.getN(errs.NewCorrelationID()); err != nil || n != 999 {
		t.Fatalf("final getN = (%d, %v), want (999, nil)", n, err)
	}
}
