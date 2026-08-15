package unlocks

import "testing"

// --- AC-13: malformed/cyclic unlock_trees.json fails loudly at load ----

// TestMalformedTreeRejected feeds several structurally-broken fixtures
// through Load and asserts each fails with ErrDataInvalid (foundation
// data's validated-load path). If Load silently defaulted a tree set, the
// first case would pass through and this test fails.
func TestMalformedTreeRejected(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name:    "missing version",
			content: `{"meta": {"categories": ["Roads"]}, "trees": []}`,
		},
		{
			name:    "empty categories",
			content: `{"version": 1, "meta": {"categories": []}, "trees": []}`,
		},
		{
			name:    "unknown node kind",
			content: `{"version": 1, "meta": {"categories": ["Roads"]}, "trees": [{"id": "roads", "name": "Roads", "nodes": [{"id": "roads_x", "name": "x", "kind": "bogus", "tier": 1}]}]}`,
		},
		{
			name:    "prereq references unknown node",
			content: `{"version": 1, "meta": {"categories": ["Roads"]}, "trees": [{"id": "roads", "name": "Roads", "nodes": [{"id": "roads_a", "name": "a", "kind": "unlock", "tier": 1, "specRef": "§10", "description": "x", "dpCost": 1, "prereqTier": 1, "prereqNodeIds": ["ghost_node"]}]}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeUnlockTreesFixture(t, dir, tc.content)
			if _, err := Load(dir, testCorrelationID()); err == nil {
				t.Fatalf("Load accepted a malformed fixture (%s); want ErrDataInvalid", tc.name)
			} else {
				assertCode(t, err, ErrDataInvalid)
			}
		})
	}
}

// TestCyclicTreeRejected feeds a tree whose prereqNodeIds edges form a
// cycle and asserts Load rejects it — the prereq graph must be acyclic
// (foundation data's cycle check). A minimal 13-tier tree is used so the
// fixture reaches the cycle stage of validation.
func TestCyclicTreeRejected(t *testing.T) {
	dir := t.TempDir()
	// roads_t2 depends on roads_t3, and roads_t3 depends on roads_t2 — a
	// 2-cycle.
	writeUnlockTreesFixture(t, dir, minimalUnlockTreesJSON(map[string][]string{
		"roads_t2": {"roads_t3"},
		"roads_t3": {"roads_t2"},
	}))

	if _, err := Load(dir, testCorrelationID()); err == nil {
		t.Fatal("Load accepted a cyclic prereq graph; want ErrDataInvalid")
	} else {
		assertCode(t, err, ErrDataInvalid)
	}
}

// TestValidMinimalFixtureLoads proves the minimal fixture generator itself
// produces a loadable file, so the malformed/cycle tests above are
// genuinely exercising the validation they name rather than tripping over
// an unrelated structural error.
func TestValidMinimalFixtureLoads(t *testing.T) {
	dir := t.TempDir()
	writeUnlockTreesFixture(t, dir, minimalUnlockTreesJSON(nil))

	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load of the minimal valid fixture: %v", err)
	}
	if got := len(api.Categories()); got != 1 {
		t.Errorf("minimal fixture loaded %d categories, want 1", got)
	}
}
