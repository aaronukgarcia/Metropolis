package compose

import (
	"crypto/sha256"
	"errors"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1145 family: defenceWiring is a mutex-bearing candidate type for the
// astgate copy-guard ratchet. A struct copy gets its own independent mu while
// still aliasing api, so each lock-taking entry point must reject a copy
// BEFORE touching mu and fail closed. These tests prove the guard can fail:
// they take a raw byte copy (the same shape as coastalByteCopy, sidestepping
// vet's copylocks) and assert the rejection.

func newCopyGuardDefenceWiring() *defenceWiring {
	w := &defenceWiring{lastMonth: -1, popFn: func() int64 { return 777 }}
	w.self.Store(w)
	return w
}

func defenceByteCopy(w *defenceWiring) *defenceWiring {
	c := new(defenceWiring)
	*(*[unsafe.Sizeof(defenceWiring{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(defenceWiring{})]byte)(unsafe.Pointer(w))
	return c
}

func requireDefenceCopyRejected(t *testing.T, what string, err error) {
	t.Helper()
	var e *errs.E
	if err == nil || !errors.As(err, &e) || e.Code != ErrDefenceStateInvalid {
		t.Fatalf("%s = %v, want code %s", what, err, ErrDefenceStateInvalid)
	}
}

func TestDefenceWiring_StructCopy_FailsClosed(t *testing.T) {
	orig := newCopyGuardDefenceWiring()
	if !orig.checkNotCopied() {
		t.Fatal("constructed defenceWiring rejected as a copy")
	}
	// Take the copy while orig.mu is HELD: the copy captures a "locked" mutex,
	// so a guard that reached cp.mu would park forever (SEC-016). The
	// pre-lock guard must return without touching it.
	orig.mu.Lock()
	cp := defenceByteCopy(orig)
	orig.mu.Unlock()
	if cp.checkNotCopied() {
		t.Fatal("struct copy accepted by checkNotCopied")
	}
	requireDefenceCopyRejected(t, "copy.bind()", cp.bind(&simState{}))
	cp.digestInto(sha256.New()) // would deadlock on the captured-locked mu if unguarded
	// A bare literal never went through newDefenceWiring: nil self == rejected.
	if (&defenceWiring{}).checkNotCopied() {
		t.Fatal("bare literal accepted by checkNotCopied")
	}
}

func TestDefenceWiring_StructCopy_SimStateEntryPointsFailClosed(t *testing.T) {
	orig := newCopyGuardDefenceWiring()
	orig.mu.Lock()
	cp := defenceByteCopy(orig) // captured locked: any unguarded mu use hangs
	orig.mu.Unlock()
	st := &simState{defenceW: cp, finance: &finance.FinanceAPI{}}

	// handleDefenceCommand: rejected before the month/mu/api are touched.
	requireDefenceCopyRejected(t, "copy handleDefenceCommand",
		st.handleDefenceCommand(protocol.Command{Kind: protocol.KindBidForGrant}))
	// defencePopulation: the copy's fixture seam (777) must NOT be honoured.
	if got := st.defencePopulation(); got == 777 {
		t.Fatalf("copy defencePopulation honoured the copy's popFn (%d)", got)
	}
	// runDefenceMonth: no month is recorded on the copy.
	st.runDefenceMonth(3)
	if cp.lastMonth != -1 {
		t.Fatalf("copy runDefenceMonth recorded month %d, want untouched -1", cp.lastMonth)
	}
	// postDefenceLine: reported unposted, counter untouched.
	if st.postDefenceLine(3, "procurement", finCatDefenceProcurement, finance.AcctFirms, 10) {
		t.Fatal("copy postDefenceLine reported posted")
	}
	// buildDefencePatch: honest empty patch, no panic on the nil api.
	if raw, err := st.buildDefencePatch(); err != nil || len(raw) == 0 {
		t.Fatalf("copy buildDefencePatch = %q, %v", raw, err)
	}
	// Composition.Defence: no module handle from a copy.
	if (&Composition{state: st}).Defence() != nil {
		t.Fatal("copy Composition.Defence returned a module handle")
	}
	// Save participant: Source emits nothing, Handler refuses.
	p := newDefenceParticipant(st)
	if _, ok, err := p.Source()(); ok || err != nil {
		t.Fatalf("copy participant Source emitted (ok=%v err=%v)", ok, err)
	}
	requireDefenceCopyRejected(t, "copy participant Handler",
		p.Handler()(serialize.Record{Kind: recComposeDefence, Data: []byte(`{}`)}))
	// The genuine value is not blanket-rejected.
	st.defenceW = orig
	if got := st.defencePopulation(); got != 777 {
		t.Fatalf("original defencePopulation = %d, want 777", got)
	}
}
