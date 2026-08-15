package unlocks

import "testing"

// --- AC-2: XP accrues continuously from four named, per-source sources --

// TestXPIncreasesFromEachSource asserts that each of the four per-source
// award functions (construction, population, service performance,
// milestone progress) increases the XP counter. If any source were wired
// to a shared no-op or a single gainXP(amount), the per-source
// functions would not each move XP, and this test fails. The rates are
// placeholders (see xp.go), so the assertion is direction (XP grew), not
// a pinned number.
func TestXPIncreasesFromEachSource(t *testing.T) {
	api := realAPI(t)
	before := api.XP()

	if err := api.AwardConstructionXP(3_000_000, testCorrelationID()); err != nil {
		t.Fatalf("AwardConstructionXP: %v", err)
	}
	if api.XP() <= before {
		t.Errorf("AwardConstructionXP did not increase XP (before %d, after %d)", before, api.XP())
	}

	before = api.XP()
	if err := api.AwardPopulationXP(10, testCorrelationID()); err != nil {
		t.Fatalf("AwardPopulationXP: %v", err)
	}
	if api.XP() <= before {
		t.Errorf("AwardPopulationXP did not increase XP (before %d, after %d)", before, api.XP())
	}

	before = api.XP()
	if err := api.AwardServiceXP(5, testCorrelationID()); err != nil {
		t.Fatalf("AwardServiceXP: %v", err)
	}
	if api.XP() <= before {
		t.Errorf("AwardServiceXP did not increase XP (before %d, after %d)", before, api.XP())
	}

	before = api.XP()
	if err := api.AwardMilestoneProgressXP(25, testCorrelationID()); err != nil {
		t.Fatalf("AwardMilestoneProgressXP: %v", err)
	}
	if api.XP() <= before {
		t.Errorf("AwardMilestoneProgressXP did not increase XP (before %d, after %d)", before, api.XP())
	}
}

// TestXPAwardRejectsNegative guards the GR#16 boundary: a negative
// source input is rejected rather than silently subtracting XP.
func TestXPAwardRejectsNegative(t *testing.T) {
	api := realAPI(t)
	before := api.XP()

	for name, call := range map[string]func() error{
		"construction": func() error { return api.AwardConstructionXP(-1, testCorrelationID()) },
		"population":   func() error { return api.AwardPopulationXP(-1, testCorrelationID()) },
		"service":      func() error { return api.AwardServiceXP(-1, testCorrelationID()) },
		"milestone":    func() error { return api.AwardMilestoneProgressXP(-1, testCorrelationID()) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: negative input returned nil error, want ErrNegativeAmount", name)
		} else {
			assertCode(t, err, ErrNegativeAmount)
		}
	}
	if api.XP() != before {
		t.Errorf("XP changed after rejected negative awards: before %d, after %d", before, api.XP())
	}
}
