package dash

import (
	"testing"
)

func TestSEC071_LayoutValueCopyAliasing(t *testing.T) {
	l1 := NewLayout("f1")
	tileA, _ := NewBignumTile("a", DrillTarget{ViewName: "f1.viewport"}, BignumSpec{})
	tileB, _ := NewBignumTile("b", DrillTarget{ViewName: "f1.viewport"}, BignumSpec{})
	_ = l1.AddTile(tileA)
	_ = l1.AddTile(tileB)

	// Layout value copy
	l2 := l1

	// Mutate l2: remove "a", which shifts "b" into index 0
	_ = l2.RemoveTile("a")

	// Because l1 and l2 share a backing array, the shift inside l2 overwrote l1's index 0.
	// l1 still has length 2, so it now contains ["b", "b"].

	// Let's see what happens to l1.
	if _, ok := l1.FindTile("a"); !ok {
		t.Errorf("SEC-071: Layout value copy aliased the mutable tile slice, mutating l2 corrupted l1")
	}
}
