package unlocks

import "testing"

// --- AC-3: all 13 milestone tiers match §4's table exactly --------------

// TestMilestoneLadderMatchesSpec asserts the thirteen-tier ladder
// (thresholds and names) matches §4's table verbatim. The expected values
// are transcribed from §4 (GR#15: "transcribed from §4, not invented") —
// the ladder is spec data, not config data, so the test pins the Go table
// to §4 rather than to another data file. This test FAILS if a threshold
// or name diverges from §4, which is the whole point: a build that invents
// or mutates a threshold cannot pass.
func TestMilestoneLadderMatchesSpec(t *testing.T) {
	// §4's table, second and first columns, in order (see
	// docs/METROPOLIS-MASTER-v2.1.md §4).
	want := []Milestone{
		{Tier: 1, Name: "Wilderness", Population: 0},
		{Tier: 2, Name: "Hamlet", Population: 100},
		{Tier: 3, Name: "Village", Population: 500},
		{Tier: 4, Name: "Small Town", Population: 5_000},
		{Tier: 5, Name: "Town", Population: 20_000},
		{Tier: 6, Name: "Large Town", Population: 50_000},
		{Tier: 7, Name: "Small City", Population: 100_000},
		{Tier: 8, Name: "City", Population: 250_000},
		{Tier: 9, Name: "Metropolis", Population: 1_000_000},
		{Tier: 10, Name: "Conurbation", Population: 5_000_000},
		{Tier: 11, Name: "Megacity", Population: 10_000_000},
		{Tier: 12, Name: "Megalopolis", Population: 50_000_000},
		{Tier: 13, Name: "Centopolis", Population: 100_000_000},
	}

	if len(milestoneLadder) != len(want) {
		t.Fatalf("milestoneLadder has %d tiers, want exactly %d (§4's table)", len(milestoneLadder), len(want))
	}
	for i, w := range want {
		got := milestoneLadder[i]
		if got.Tier != w.Tier || got.Name != w.Name || got.Population != w.Population {
			t.Errorf("milestoneLadder[%d] = %+v, want %+v (§4's table, transcribed verbatim)", i, got, w)
		}
	}
}

// TestMilestoneAtBounds guards the tier-domain helper: tier 0 (the "no
// milestone reached" sentinel) and tier 14 are not real milestones.
func TestMilestoneAtBounds(t *testing.T) {
	if _, ok := milestoneAt(0); ok {
		t.Error("milestoneAt(0) reported ok; tier 0 is the no-milestone sentinel, not a real tier")
	}
	if _, ok := milestoneAt(len(milestoneLadder) + 1); ok {
		t.Error("milestoneAt(14) reported ok; the ladder has 13 tiers")
	}
	for tier := 1; tier <= len(milestoneLadder); tier++ {
		if m, ok := milestoneAt(tier); !ok || m.Tier != tier {
			t.Errorf("milestoneAt(%d) = %+v, %v; want tier %d present", tier, m, ok, tier)
		}
	}
}
