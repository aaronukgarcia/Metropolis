package compose

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-1145 (social half): socialWiring is a mutex-bearing candidate type for
// the astgate copy-guard ratchet. A struct copy gets its own independent mu
// while still aliasing api, so each lock-taking entry point must reject a copy
// BEFORE touching mu and fail closed. These tests prove the guard can fail:
// they take a raw byte copy (sidestepping vet's copylocks) and assert the
// rejection.

func newCopyGuardSocialWiring() *socialWiring {
	w := &socialWiring{lastMonth: -1, posted: 5}
	w.self.Store(w)
	return w
}

func socialByteCopy(w *socialWiring) *socialWiring {
	c := new(socialWiring)
	*(*[unsafe.Sizeof(socialWiring{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(socialWiring{})]byte)(unsafe.Pointer(w))
	return c
}

func TestSocialWiring_StructCopy_FailsClosed(t *testing.T) {
	orig := newCopyGuardSocialWiring()
	if !orig.checkNotCopied() {
		t.Fatal("constructed socialWiring rejected as a copy")
	}
	// Copy while orig.mu is HELD: the copy captures a "locked" mutex, so a
	// guard that reached cp.mu would park forever (SEC-016).
	orig.mu.Lock()
	cp := socialByteCopy(orig)
	orig.mu.Unlock()
	if cp.checkNotCopied() {
		t.Fatal("struct copy accepted by checkNotCopied")
	}
	if (&socialWiring{}).checkNotCopied() {
		t.Fatal("bare literal accepted by checkNotCopied")
	}
	if got := cp.unemploymentMonths(5, 10); got != 0 {
		t.Fatalf("copy.unemploymentMonths = %d, want 0 (fail closed)", got)
	}
	if got := cp.caseDuration(0.5); got != 1 {
		t.Fatalf("copy.caseDuration = %d, want 1 (fail closed)", got)
	}
	cp.digestInto(sha256.New()) // must return without parking on the captured-locked mu
	err := errSocialCopied("c", "x")
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrSocialStateInvalid {
		t.Fatalf("errSocialCopied = %v, want code %s", err, ErrSocialStateInvalid)
	}
}

func TestSocialWiring_StructCopy_SimStateEntryPointsFailClosed(t *testing.T) {
	orig := newCopyGuardSocialWiring()
	cp := socialByteCopy(orig)
	st := &simState{socialW: cp}
	if st.postSocialSupport(1, 100) {
		t.Fatal("copy postSocialSupport reported success, want fail closed")
	}
	if err := st.handleSocialFunding("social.x", 1); err == nil {
		t.Fatal("copy handleSocialFunding returned nil, want the copy rejection")
	}
	if err := st.resetSocialIfNoShard(nil); err == nil {
		t.Fatal("copy resetSocialIfNoShard returned nil, want the copy rejection")
	}
	raw, err := st.buildSocialPatch()
	if err != nil || len(raw) == 0 {
		t.Fatalf("copy buildSocialPatch = %q, %v, want the honest empty patch", raw, err)
	}
	c := &Composition{state: st}
	if c.Social() != nil || c.SocialPostedMicropounds() != 0 {
		t.Fatal("copy Composition accessors leaked state")
	}
	p := newSocialParticipant(st)
	rec, ok, serr := p.Source()()
	if ok || serr != nil || rec.Kind != "" {
		t.Fatalf("copy Source emitted a record: %v %v %v", rec, ok, serr)
	}
	if herr := p.Handler()(serialize.Record{Kind: recComposeSocial}); herr == nil {
		t.Fatal("copy Handler returned nil, want the copy rejection")
	}
	// The genuine value still posts its own state (the guard is not a blanket reject).
	st.socialW = orig
	if c.SocialPostedMicropounds() != 5 {
		t.Fatalf("original SocialPostedMicropounds = %d, want 5", c.SocialPostedMicropounds())
	}
}

// TestRunSocial_StructCopy_FailsClosed covers runSocial's own pre-lock guard
// (r1 advisory): a copied wiring must run NO month (lastMonth stays at its
// sentinel, nothing posted) and must not park on the captured-locked mu.
func TestRunSocial_StructCopy_FailsClosed(t *testing.T) {
	orig := newCopyGuardSocialWiring()
	orig.mu.Lock()
	cp := socialByteCopy(orig)
	orig.mu.Unlock()
	st := &simState{socialW: cp, citizens: &citizens.CitizensAPI{}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		st.runSocial(socialEffect{month: 7})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runSocial parked on the copied wiring's mutex (guard missing or after the lock)")
	}
	if cp.lastMonth != -1 || cp.posted != 5 || cp.failedMonths != 0 {
		t.Fatalf("copied wiring was advanced: lastMonth=%d posted=%d failed=%d", cp.lastMonth, cp.posted, cp.failedMonths)
	}
}
