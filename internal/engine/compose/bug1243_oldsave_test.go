package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1243: choosing an option changes what the daily simulation computes, not
// what a player's command does, so by Aaron's ruling it carries no rule
// version. A save made under one setting must still load under another and
// simply continue under the new numbers, deterministically.
//
// world.aWorld in particular is re-read from data on every load and is NOT
// stored in a save: a save made under one value and loaded under another
// continues under the new value, and two loads give identical runs.
func TestBUG1243_SaveLoadsUnderAnotherSetting_ContinuesUnderTheNewNumbers(t *testing.T) {
	const seed = uint64(424242)
	const saveTick = int64(6 * 30)
	const moreTicks = 6 * 30

	// The save is made under today's shipped data (both modifiers built, 40).
	eOld, cOld := newUncappedTestEngine(t, seed)
	if err := eOld.AdvanceTicks(errs.NewCorrelationID(), saveTick); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := cOld.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	savedPop := cOld.Population()

	// load continues the save under whatever data is currently selected.
	load := func() *Composition {
		e, c := newUncappedTestEngine(t, seed)
		if err := c.LoadAt(root, saveTick); err != nil {
			t.Fatalf("LoadAt: %v", err)
		}
		if got := c.Population(); got != savedPop {
			t.Fatalf("loaded population %d, saved %d", got, savedPop)
		}
		if err := e.AdvanceTicks(errs.NewCorrelationID(), moreTicks); err != nil {
			t.Fatalf("continue: %v", err)
		}
		return c
	}
	same := load() // data unchanged: today
	if again := load(); again.StateDigest() != same.StateDigest() {
		t.Fatalf("two loads of the same save under the same data diverged")
	}

	for _, v := range []struct {
		name string
		edit func(doc map[string]any)
	}{
		{"option A (neutral)", bug1243Neutral},
		{"aWorld 20 (option B)", bug1243Baseline(20)},
	} {
		bug1243DataVariant(t, v.edit)
		first := load()
		if first.StateDigest() == same.StateDigest() {
			t.Fatalf("%s: the loaded city continued identically to today's data: the setting had no effect on a loaded save", v.name)
		}
		if second := load(); second.StateDigest() != first.StateDigest() {
			t.Fatalf("%s: two loads of the same save diverged", v.name)
		}
	}
}

// A data file with no built block (an older file) means built: the behaviour
// before BUG-1243, which is also what the shipped file spells out.
func TestBUG1243_BuiltBlock_MissingMeansBuilt_ShippedMeansBuilt(t *testing.T) {
	b, _, err := readAttractTermsFile("bug1243-test")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := parseAttractTermsData(b, "bug1243-test")
	if err != nil {
		t.Fatal(err)
	}
	if !shipped.Built.safetyBuilt() || !shipped.Built.environmentBuilt() {
		t.Fatalf("shipped data/attract_terms.json must hold both modifiers built (today's behaviour)")
	}
	var old attractTermsBuilt
	if !old.safetyBuilt() || !old.environmentBuilt() {
		t.Fatalf("a missing flag must mean built (the behaviour before BUG-1243)")
	}
}
