package protocol

import "testing"

func TestValidateViewName(t *testing.T) {
	valid := []string{
		"f1.viewport",
		"f2.ledger",
		"junction.14.approaches",
		"citizen.482913.detail",
	}
	for _, name := range valid {
		if err := ValidateViewName(name); err != nil {
			t.Errorf("ValidateViewName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"NoDotsAtAll",
		"Uppercase.Segment",
		".leadingdot",
		"trailingdot.",
		"f1",
	}
	for _, name := range invalid {
		if err := ValidateViewName(name); err == nil {
			t.Errorf("ValidateViewName(%q) = nil, want an error", name)
		}
	}
}

func TestSubscriptionAllocator_Unique(t *testing.T) {
	a := NewSubscriptionAllocator()
	seen := make(map[SubscriptionID]bool)
	for i := 0; i < 1000; i++ {
		id := a.Allocate()
		if seen[id] {
			t.Fatalf("SubscriptionAllocator produced a duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestSeqTracker_InOrder(t *testing.T) {
	tr := NewSeqTracker()
	sub := SubscriptionID("sub-1")

	for seq := uint64(1); seq <= 5; seq++ {
		gap, ok := tr.Observe(sub, seq)
		if !ok {
			t.Fatalf("Observe(%d) ok = false, want true", seq)
		}
		if gap != 0 {
			t.Fatalf("Observe(%d) gap = %d, want 0", seq, gap)
		}
	}
}

func TestSeqTracker_DetectsGap(t *testing.T) {
	tr := NewSeqTracker()
	sub := SubscriptionID("sub-1")

	if gap, ok := tr.Observe(sub, 1); !ok || gap != 0 {
		t.Fatalf("Observe(1) = (%d, %v), want (0, true)", gap, ok)
	}
	// Deltas 2, 3, 4 were dropped; 5 is the next one to arrive.
	gap, ok := tr.Observe(sub, 5)
	if !ok {
		t.Fatal("Observe(5) after Observe(1) ok = false, want true")
	}
	if gap != 3 {
		t.Fatalf("Observe(5) after Observe(1) gap = %d, want 3", gap)
	}
}

func TestSeqTracker_DuplicateOrOutOfOrder(t *testing.T) {
	tr := NewSeqTracker()
	sub := SubscriptionID("sub-1")

	tr.Observe(sub, 10)

	if _, ok := tr.Observe(sub, 10); ok {
		t.Fatal("Observe with a duplicate seq reported ok = true, want false")
	}
	if _, ok := tr.Observe(sub, 3); ok {
		t.Fatal("Observe with an out-of-order (lower) seq reported ok = true, want false")
	}
}

func TestSeqTracker_IndependentPerSubscription(t *testing.T) {
	tr := NewSeqTracker()
	subA := SubscriptionID("sub-a")
	subB := SubscriptionID("sub-b")

	tr.Observe(subA, 100)
	// subB starts its own stream at 1; must not be compared against subA.
	if gap, ok := tr.Observe(subB, 1); !ok || gap != 0 {
		t.Fatalf("Observe(subB, 1) = (%d, %v), want (0, true)", gap, ok)
	}
}

func TestSeqTracker_ResetStartsFresh(t *testing.T) {
	tr := NewSeqTracker()
	sub := SubscriptionID("sub-1")

	tr.Observe(sub, 50)
	tr.Reset(sub)

	if gap, ok := tr.Observe(sub, 1); !ok || gap != 0 {
		t.Fatalf("Observe after Reset = (%d, %v), want (0, true)", gap, ok)
	}
}
