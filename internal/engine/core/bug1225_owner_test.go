package core

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func bug1225Sub(e *Engine, owner uint64, tag string) protocol.CommandResult {
	return e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("bug1225-" + tag),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
		Owner:           owner,
	})
}

// BUG-1225: ownership rules of the subscription server, independent of any
// transport. A Subscribe applied after its owner detached is dropped; a later
// epoch and the in-process owner 0 are never touched; the live-owner set is
// bounded by live owners.
func TestBug1225_OwnerDetachRules(t *testing.T) {
	e := NewEngine()
	a, b := e.NewSubscriptionOwner(), e.NewSubscriptionOwner()
	if a == 0 || b <= a {
		t.Fatalf("epochs must rise from 1: a=%d b=%d", a, b)
	}
	if r := bug1225Sub(e, 0, "inproc"); !r.Accepted {
		t.Fatalf("in-process subscribe refused: %+v", r)
	}
	if r := bug1225Sub(e, a, "a1"); !r.Accepted {
		t.Fatalf("owner a subscribe refused: %+v", r)
	}
	if r := bug1225Sub(e, b, "b1"); !r.Accepted {
		t.Fatalf("owner b subscribe refused: %+v", r)
	}
	if got := e.SubscriptionCount(); got != 3 {
		t.Fatalf("count=%d want 3", got)
	}
	if n := e.DetachSubscriptionOwner(a); n != 1 {
		t.Fatalf("detach a removed %d, want 1", n)
	}
	if got := e.SubscriptionCount(); got != 2 {
		t.Fatalf("after detach a: count=%d want 2 (in-process + b)", got)
	}
	// A Subscribe from the dead owner that arrives late is accepted off the
	// wire but creates nothing.
	if r := bug1225Sub(e, a, "a-late"); !r.Accepted {
		t.Fatalf("late subscribe from dead owner should be a quiet no-op, got %+v", r)
	}
	if got := e.SubscriptionCount(); got != 2 {
		t.Fatalf("dead owner's late subscribe leaked: count=%d want 2", got)
	}
	// Idempotent; detaching a again or owner 0 removes nothing.
	if n := e.DetachSubscriptionOwner(a) + e.DetachSubscriptionOwner(0); n != 0 {
		t.Fatalf("repeat/zero detach removed %d", n)
	}
	// Detaching b removes b's one subscription and nothing else.
	if n := e.DetachSubscriptionOwner(b); n != 1 {
		t.Fatalf("detach b removed %d, want 1", n)
	}
	if got := e.SubscriptionCount(); got != 1 {
		t.Fatalf("in-process subscriber must survive: count=%d want 1", got)
	}
	// A fresh epoch is live.
	c := e.NewSubscriptionOwner()
	if r := bug1225Sub(e, c, "c1"); !r.Accepted || e.SubscriptionCount() != 2 {
		t.Fatalf("fresh epoch could not subscribe: %+v count=%d", r, e.SubscriptionCount())
	}
}

// The owner is process-local metadata: it never reaches the wire or journal.
func TestBug1225_OwnerNotSerialised(t *testing.T) {
	base := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "bug1225-ser",
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: "engine.status"},
	}
	owned := base
	owned.Owner = 42
	x, err1 := protocol.EncodeCommand(base)
	y, err2 := protocol.EncodeCommand(owned)
	if err1 != nil || err2 != nil {
		t.Fatalf("encode: %v %v", err1, err2)
	}
	if string(x) != string(y) {
		t.Fatalf("Owner leaked into the encoded command:\n%s\n%s", x, y)
	}
}

// BUG-1225 r3: closing one connection removes ONLY its own subscriptions,
// whatever the order of binds and closes.
func TestBug1225_OrderIndependence(t *testing.T) {
	for _, closeAFirst := range []bool{true, false} {
		e := NewEngine()
		a, b := e.NewSubscriptionOwner(), e.NewSubscriptionOwner() // bind A, bind B
		for i := 0; i < 3; i++ {
			bug1225Sub(e, a, "oa"+string(rune('0'+i)))
			bug1225Sub(e, b, "ob"+string(rune('0'+i)))
		}
		bug1225Sub(e, 0, "oinproc")
		if got := e.SubscriptionCount(); got != 7 {
			t.Fatalf("setup count=%d", got)
		}
		first, second := a, b
		if !closeAFirst {
			first, second = b, a
		}
		if n := e.DetachSubscriptionOwner(first); n != 3 {
			t.Fatalf("closeAFirst=%v: first close removed %d, want 3", closeAFirst, n)
		}
		if got := e.SubscriptionCount(); got != 4 {
			t.Fatalf("closeAFirst=%v: survivor lost subscriptions: count=%d want 4", closeAFirst, got)
		}
		// the survivor can still subscribe
		if r := bug1225Sub(e, second, "more"); !r.Accepted || e.SubscriptionCount() != 5 {
			t.Fatalf("survivor cannot subscribe: %+v %d", r, e.SubscriptionCount())
		}
		if n := e.DetachSubscriptionOwner(second); n != 4 {
			t.Fatalf("second close removed %d, want 4", n)
		}
		if got := e.SubscriptionCount(); got != 1 {
			t.Fatalf("closeAFirst=%v: want only the in-process sub, got %d", closeAFirst, got)
		}
		if got := e.LiveSubscriptionOwners(); got != 0 {
			t.Fatalf("live set not empty: %d", got)
		}
	}
}

// A Subscribe for an owner that was never minted creates nothing.
func TestBug1225_UnmintedOwnerCreatesNothing(t *testing.T) {
	e := NewEngine()
	_ = e.NewSubscriptionOwner()
	if r := bug1225Sub(e, 1<<40, "ghost"); !r.Accepted {
		t.Fatalf("expected quiet no-op, got %+v", r)
	}
	if got := e.SubscriptionCount(); got != 0 {
		t.Fatalf("unminted owner created %d subscriptions", got)
	}
}
