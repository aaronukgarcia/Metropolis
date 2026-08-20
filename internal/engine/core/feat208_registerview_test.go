package core

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-208 increment 1, step 1: RegisterView generalises Subscribe/
// Publish from one hardcoded view ("engine.status") to a registered
// table. This file's tests prove the table itself — two independently
// registered views, each subscribed separately, each getting its OWN
// patch content and its OWN monotonically increasing Seq sequence,
// entirely independent of the other's.

// TestRegisterView_TwoViews_IndependentPatchesAndSeq is the increment
// plan's own step-1 unit test: register two views on a bare
// SubscriptionServer (a fresh NewEngine already carries "engine.status"
// pre-registered — see engine.go — so this test uses two DIFFERENT new
// names to prove the table generalises beyond that one always-present
// entry), subscribe to both, publish twice, and assert each
// subscription's delta stream carries its own view's content and its
// own Seq starting at 1 and incrementing by exactly 1 per Publish call.
func TestRegisterView_TwoViews_IndependentPatchesAndSeq(t *testing.T) {
	s := NewSubscriptionServer()

	aCalls := 0
	if err := s.RegisterView("test.viewa", func() (json.RawMessage, error) {
		aCalls++
		return json.Marshal(map[string]int{"a": aCalls})
	}); err != nil {
		t.Fatalf("RegisterView(test.viewa): %v", err)
	}
	bCalls := 0
	if err := s.RegisterView("test.viewb", func() (json.RawMessage, error) {
		bCalls++
		return json.Marshal(map[string]int{"b": bCalls * 100})
	}); err != nil {
		t.Fatalf("RegisterView(test.viewb): %v", err)
	}

	idA, err := s.Subscribe("test.viewa", nil, "", "corr-a")
	if err != nil {
		t.Fatalf("Subscribe(test.viewa): %v", err)
	}
	idB, err := s.Subscribe("test.viewb", nil, "", "corr-b")
	if err != nil {
		t.Fatalf("Subscribe(test.viewb): %v", err)
	}
	if idA == idB {
		t.Fatalf("Subscribe allocated the same SubscriptionID (%s) for two distinct views", idA)
	}

	sink := &recordingSink{}
	s.Publish(sink, protocol.Tick(1))
	s.Publish(sink, protocol.Tick(2))

	deltasFor := func(id protocol.SubscriptionID) []protocol.Delta {
		var out []protocol.Delta
		for _, d := range sink.deltas {
			if d.SubscriptionID == id {
				out = append(out, d)
			}
		}
		return out
	}

	da := deltasFor(idA)
	db := deltasFor(idB)
	if len(da) != 2 {
		t.Fatalf("view A deltas = %d, want 2", len(da))
	}
	if len(db) != 2 {
		t.Fatalf("view B deltas = %d, want 2", len(db))
	}

	// Seq is monotonic per SubscriptionID, starting at 1, independent of
	// the other subscription's own counter.
	if da[0].Seq != 1 || da[1].Seq != 2 {
		t.Errorf("view A Seqs = [%d %d], want [1 2]", da[0].Seq, da[1].Seq)
	}
	if db[0].Seq != 1 || db[1].Seq != 2 {
		t.Errorf("view B Seqs = [%d %d], want [1 2]", db[0].Seq, db[1].Seq)
	}

	// Each subscription's patch content came from ITS OWN registered
	// ViewPatchFunc, never the other's — proves the table dispatches by
	// view name, not by registration order or some shared single patch.
	var pa map[string]int
	if err := json.Unmarshal(da[0].Patch, &pa); err != nil {
		t.Fatalf("unmarshal view A patch: %v", err)
	}
	if pa["a"] != 1 {
		t.Errorf("view A first patch = %v, want {a:1}", pa)
	}
	var pb map[string]int
	if err := json.Unmarshal(db[1].Patch, &pb); err != nil {
		t.Fatalf("unmarshal view B patch: %v", err)
	}
	if pb["b"] != 200 {
		t.Errorf("view B second patch = %v, want {b:200}", pb)
	}

	// Tick is stamped once per Publish call, identically across every
	// delta produced by that cycle (§4's "Tick-consistent by
	// construction" — never a per-delta value).
	if da[0].Tick != 1 || db[0].Tick != 1 {
		t.Errorf("cycle 1 Tick = [%d %d], want [1 1]", da[0].Tick, db[0].Tick)
	}
	if da[1].Tick != 2 || db[1].Tick != 2 {
		t.Errorf("cycle 2 Tick = [%d %d], want [2 2]", da[1].Tick, db[1].Tick)
	}
}

// TestRegisterView_DuplicateName_Rejected proves RegisterView refuses a
// second registration of the same view name rather than silently
// replacing the first registration's ViewPatchFunc.
func TestRegisterView_DuplicateName_Rejected(t *testing.T) {
	s := NewSubscriptionServer()
	fn := func() (json.RawMessage, error) { return json.RawMessage("{}"), nil }
	if err := s.RegisterView("test.dup", fn); err != nil {
		t.Fatalf("first RegisterView(test.dup): %v", err)
	}
	err := s.RegisterView("test.dup", fn)
	if err == nil {
		t.Fatal("second RegisterView(test.dup): accepted, want ErrViewAlreadyRegistered")
	}
	if !errors.Is(err, &errs.E{Code: ErrViewAlreadyRegistered}) {
		t.Errorf("second RegisterView(test.dup): err = %v, want ErrViewAlreadyRegistered", err)
	}
}

// TestRegisterView_NilFunc_Rejected proves RegisterView refuses a nil
// ViewPatchFunc rather than storing it and only failing later, mid-
// Publish.
func TestRegisterView_NilFunc_Rejected(t *testing.T) {
	s := NewSubscriptionServer()
	err := s.RegisterView("test.nilfunc", nil)
	if err == nil {
		t.Fatal("RegisterView(nil fn): accepted, want ErrNilViewPatchFunc")
	}
	if !errors.Is(err, &errs.E{Code: ErrNilViewPatchFunc}) {
		t.Errorf("RegisterView(nil fn): err = %v, want ErrNilViewPatchFunc", err)
	}
}

// TestSubscribe_UnregisteredView_StillRejected proves the generalised,
// table-driven Subscribe still rejects a well-formed but never-
// registered view name exactly as the old hardcoded check did — no
// regression in the "unknown view" rejection path (mirrors
// subscribe_test.go's TestSubscribe_UnknownView_Rejected, using a
// distinct view name so it stays independent of that test's own
// evidence).
func TestSubscribe_UnregisteredView_StillRejected(t *testing.T) {
	s := NewSubscriptionServer()
	_, err := s.Subscribe("test.neverregistered", nil, "", "corr")
	if err == nil {
		t.Fatal("Subscribe(never-registered view): accepted, want ErrUnknownView")
	}
	if !errors.Is(err, &errs.E{Code: ErrUnknownView}) {
		t.Errorf("Subscribe(never-registered view): err = %v, want ErrUnknownView", err)
	}
}

// recordingSink is a minimal DeltaSink recording every delta sent, in
// send order — this file's tests need the full delta content, not just
// a count (unlike sec019_poc_test.go's countingSink).
type recordingSink struct {
	deltas []protocol.Delta
}

func (r *recordingSink) SendDelta(d protocol.Delta) bool {
	r.deltas = append(r.deltas, d)
	return true
}
