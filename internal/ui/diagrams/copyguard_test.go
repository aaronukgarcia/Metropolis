package diagrams

import (
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// engineByteCopy performs SEC-020's attack — a plain Engine struct copy —
// via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/ui/screens/map/sec020_test.go's mapScreenByteCopy and
// internal/engine/debug/copyguard_test.go's stateByteCopy: a literal
// `e2 := *e` is legal, unsafe-free Go but is exactly what `go vet`'s
// copylocks check flags, and this package's VERIFY step requires
// `go vet ./...` clean. The byte-level copy produces IDENTICAL runtime
// semantics (mu's bytes copied as-is, cache's map header copied — aliasing
// the same backing map — self's pointer bytes copied unchanged) without a
// statically flaggable copy expression.
func engineByteCopy(e *Engine) *Engine {
	c := new(Engine)
	*(*[unsafe.Sizeof(Engine{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Engine{})]byte)(unsafe.Pointer(e))
	return c
}

// wantEngineCopied asserts err is exactly ErrEngineCopied — naming each
// call individually (rather than a shared loop) means a stripped guard on
// any ONE method identifies which site regressed.
func wantEngineCopied(t *testing.T, method string, err error) {
	t.Helper()
	if !errors.Is(err, &errs.E{Code: ErrEngineCopied}) {
		t.Fatalf("%s on a struct-copied Engine: err = %v, want ErrEngineCopied", method, err)
	}
}

// runBoundedEngineCopy runs fn (a call into a guarded *Engine method) in
// its own goroutine and asserts it completes within 3 seconds, rather than
// calling it synchronously with no bound. A regression that reintroduces a
// pre-lock guard gap on a copy taken mid-lock hangs the guarded method
// forever (SEC-016's exact failure mode: the copy's mu bytes read as
// permanently "locked" by nobody who will ever unlock THIS copy's address) —
// without a per-case bound, that regression is only caught by Go's default
// 10-minute test timeout and a goroutine-dump panic, not the guarded method
// itself. Ported from internal/ui/screens/map/sec020_test.go's
// runBoundedSEC020.
func runBoundedEngineCopy(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("SEC-020 REGRESSION: %s on a copy taken while mu was held did not return within 3s — hung, exactly the pre-fix failure mode", name)
	}
}

// copyTestTopo returns a valid minimal topology of each kind, so a
// non-copied Engine would render it successfully — proving the guard is
// what rejects the copy, not a malformed input.
func copyTestTopo() (ChainTopology, NetworkTopology, SankeyTopology) {
	chain := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "mine"}, {ID: "B", Label: "smelt"}},
		Edges: []ChainEdge{{ID: "e1", From: "A", To: "B", Figure: "9 t/day"}},
	}
	net := NetworkTopology{
		Nodes: []NetworkNode{{ID: "A", Label: "a", X: 0, Y: 0}, {ID: "B", Label: "b", X: 5, Y: 0}},
		Edges: []NetworkEdge{{ID: "e1", From: "A", To: "B", Load: 0.5}},
	}
	sankey := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 100}},
	}
	return chain, net, sankey
}

// TestEngineRejectsStructCopy proves every *Engine method rejects a
// struct-copied Engine (SEC-020) before doing any work — including that
// Render's underlying layout callback never runs on a copy.
func TestEngineRejectsStructCopy(t *testing.T) {
	orig := NewEngine()
	cp := engineByteCopy(orig)
	buf := core.NewBuffer(40, 8)
	chain, net, sankey := copyTestTopo()

	calls := 0
	_, errRender := cp.Render(buf, 1, func(b *core.Buffer) (Result, error) {
		calls++
		return Result{}, nil
	})
	wantEngineCopied(t, "Render", errRender)
	if calls != 0 {
		t.Fatalf("copy.Render ran the underlying render %d times, want 0 (the guard must reject before rendering)", calls)
	}

	_, errChain := cp.Chain(buf, chain, Options{})
	wantEngineCopied(t, "Chain", errChain)
	_, errNet := cp.Network(buf, net, Options{})
	wantEngineCopied(t, "Network", errNet)
	_, errSankey := cp.Sankey(buf, sankey, Options{})
	wantEngineCopied(t, "Sankey", errSankey)

	// The ORIGINAL must remain fully usable after every rejected call.
	if _, err := orig.Sankey(buf, sankey, Options{}); err != nil {
		t.Fatalf("original.Sankey after copy-attack calls: %v", err)
	}
}

// TestEngineCopyTakenWhileLockHeld_RejectedNotHung is the deterministic
// SEC-016 "copy taken mid-lock" attack: lock mu, take the byte copy while
// it is held (so the copy's mu bytes read as "currently locked, no
// waiters"), unlock the original, then call the copy. Every guarded call
// below must return promptly with ErrEngineCopied (never attempt to acquire
// its own permanently unrecoverable lock) because checkNotCopied is
// lock-free and runs BEFORE e.mu.Lock() in every guarded method.
// Deterministic, single-goroutine, runs under -race like everything else.
func TestEngineCopyTakenWhileLockHeld_RejectedNotHung(t *testing.T) {
	orig := NewEngine()

	orig.mu.Lock()
	cp := engineByteCopy(orig) // cp.mu's bytes now read "locked" — byte-identical to orig.mu at this instant
	orig.mu.Unlock()

	buf := core.NewBuffer(40, 8)
	chain, net, sankey := copyTestTopo()

	var errRender error
	runBoundedEngineCopy(t, "Render", func() {
		_, errRender = cp.Render(buf, 1, func(b *core.Buffer) (Result, error) { return Result{}, nil })
	})
	wantEngineCopied(t, "Render", errRender)

	var errChain error
	runBoundedEngineCopy(t, "Chain", func() { _, errChain = cp.Chain(buf, chain, Options{}) })
	wantEngineCopied(t, "Chain", errChain)

	var errNet error
	runBoundedEngineCopy(t, "Network", func() { _, errNet = cp.Network(buf, net, Options{}) })
	wantEngineCopied(t, "Network", errNet)

	var errSankey error
	runBoundedEngineCopy(t, "Sankey", func() { _, errSankey = cp.Sankey(buf, sankey, Options{}) })
	wantEngineCopied(t, "Sankey", errSankey)

	// The original must still be fully usable afterward — the abandoned,
	// permanently-"locked"-looking copy mu must not have wedged anything shared.
	if _, err := orig.Sankey(buf, sankey, Options{}); err != nil {
		t.Fatalf("original.Sankey after copy-during-lock attack: %v", err)
	}
}

// TestEngineZeroValueAndNew_FailClosed proves a zero-value Engine (var e
// Engine) and a new(Engine) — neither ever passed through NewEngine, so
// self was never stored — are rejected the same way a copy is.
func TestEngineZeroValueAndNew_FailClosed(t *testing.T) {
	buf := core.NewBuffer(40, 8)
	_, _, sankey := copyTestTopo()

	var z Engine
	_, errZ := z.Sankey(buf, sankey, Options{})
	wantEngineCopied(t, "zero-value Sankey", errZ)

	n := new(Engine)
	_, errN := n.Sankey(buf, sankey, Options{})
	wantEngineCopied(t, "new(Engine) Sankey", errN)
}
