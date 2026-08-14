package data

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// specUnlockCategories are the twelve Development-Point progression
// categories §22 names verbatim — a checked-in reference list, not an
// invented total (GR#15), used by the real-file test the way
// naming_corpus_test.go's specCitedPlaceNames is.
var specUnlockCategories = []string{
	"Roads", "Electricity", "Water & Gas", "Health & Deathcare",
	"Education", "Fire", "Police", "Garbage", "Parks & Rec",
	"Transport", "Communications", "Welfare",
}

// --- fixture helpers -------------------------------------------------------
// The loader derives the expected category count from meta.categories
// and requires each tree to cover all thirteen tiers, so a synthetic
// valid fixture has to be built programmatically rather than inlined
// (a 12-category × 13-tier fixture would be hundreds of lines). These
// helpers produce a minimal single-category fixture and its mutations.

// unlockNodeJSON renders one kind:"unlock" node (the common case the
// validator's unlock path exercises). extra, if non-empty, is appended
// as extra fields (e.g. a prereqNodeIds edge).
func unlockNodeJSON(id string, tier int, extra string) string {
	s := fmt.Sprintf(`{"id":%q,"name":%q,"kind":"unlock","tier":%d,"specRef":"§10","description":"fixture","dpCost":%d,"prereqTier":%d`,
		id, id, tier, tier, tier)
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

// singleTreeFixture wraps nodes into a minimal valid single-category
// ("Roads") tree with a matching meta.categories list.
func singleTreeFixture(nodes ...string) string {
	return `{"version":1,"meta":{"categories":["Roads"]},"trees":[{"id":"roads","name":"Roads","nodes":[` +
		strings.Join(nodes, ",") + `]}]}`
}

// validRoadNodes returns thirteen kind:"unlock" nodes covering tiers
// 1..13 — a minimal schema-valid tree body.
func validRoadNodes() []string {
	nodes := make([]string, 0, 13)
	for tier := 1; tier <= 13; tier++ {
		nodes = append(nodes, unlockNodeJSON(fmt.Sprintf("roads_t%d", tier), tier, ""))
	}
	return nodes
}

// --- real-file test --------------------------------------------------------

// TestUnlockTrees_RealFile_LoadsAndPopulates proves the committed
// data/unlock_trees.json (not a synthetic fixture) round-trips through
// the rich UnlockTrees type: the twelve categories, every tree's
// thirteen-tier coverage, the node id/kind/dpCost/prereq fields, and a
// cross-category prereq edge are all captured.
func TestUnlockTrees_RealFile_LoadsAndPopulates(t *testing.T) {
	dir := realDataDir(t)
	u, err := LoadUnlockTrees(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadUnlockTrees(real data/unlock_trees.json): %v", err)
	}

	if u.Version != 1 {
		t.Errorf("Version = %d, want 1", u.Version)
	}
	if u.Comment == "" {
		t.Error("top-level $comment not captured")
	}
	if u.Meta.PlaceholderDisclosure == "" || u.Meta.NoopConvention == "" || u.Meta.FloorRule == "" {
		t.Error("meta disclosures not captured")
	}

	if len(u.Meta.Categories) != len(specUnlockCategories) {
		t.Errorf("len(meta.categories) = %d, want %d", len(u.Meta.Categories), len(specUnlockCategories))
	}
	metaHave := make(map[string]bool, len(u.Meta.Categories))
	for _, c := range u.Meta.Categories {
		metaHave[c] = true
	}
	treeHave := make(map[string]bool, len(u.Trees))
	for _, tr := range u.Trees {
		treeHave[tr.Name] = true
	}
	for _, c := range specUnlockCategories {
		if !metaHave[c] {
			t.Errorf("spec-cited category %q not declared in meta.categories", c)
		}
		if !treeHave[c] {
			t.Errorf("spec-cited category %q has no tree", c)
		}
	}

	// Every tree covers all thirteen §4 milestone tiers; the 13-node
	// floor is the file's own meta.floorRule ("at least 13 nodes — one
	// per tier minimum"), not an invented count.
	for _, tr := range u.Trees {
		covered := make(map[int]bool, 13)
		for _, n := range tr.Nodes {
			covered[n.Tier] = true
		}
		for tier := 1; tier <= 13; tier++ {
			if !covered[tier] {
				t.Errorf("%s does not cover milestone tier %d", tr.ID, tier)
			}
		}
		if len(tr.Nodes) < 13 {
			t.Errorf("%s has %d nodes, want >= 13 (meta.floorRule)", tr.ID, len(tr.Nodes))
		}
	}

	// Spot-check a cross-category prereq edge round-trips: the gas
	// peaker depends on the water & gas tree's network node.
	peaker := findUnlockNode(t, &u, "electricity_gas_peaker")
	if len(peaker.PrereqNodeIDs) != 1 || peaker.PrereqNodeIDs[0] != "water_gas_gas_network" {
		t.Errorf("electricity_gas_peaker.prereqNodeIds = %v, want [water_gas_gas_network]", peaker.PrereqNodeIDs)
	}
}

// findUnlockNode returns the node with the given id, failing the test if
// absent.
func findUnlockNode(t *testing.T, u *UnlockTrees, id string) UnlockNode {
	t.Helper()
	for _, tr := range u.Trees {
		for _, n := range tr.Nodes {
			if n.ID == id {
				return n
			}
		}
	}
	t.Fatalf("node %q not found", id)
	return UnlockNode{}
}

// TestUnlockTrees_RepeatedLoadDeepEqual is the GR#21 determinism check.
func TestUnlockTrees_RepeatedLoadDeepEqual(t *testing.T) {
	dir := realDataDir(t)
	u1, err := LoadUnlockTrees(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	u2, err := LoadUnlockTrees(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(u1, u2) {
		t.Error("repeated LoadUnlockTrees of the same file produced non-equal structs")
	}
}

// --- mutation tests: each proves Validate() rejects a specific
// malformation the old flat skeleton silently accepted ---------------------

// TestUnlockTrees_MissingTierRejected proves a tree that omits a single
// milestone tier (here tier 13) is rejected — the exact "category
// missing a tier" failure the brief calls out.
func TestUnlockTrees_MissingTierRejected(t *testing.T) {
	dir := t.TempDir()
	nodes := validRoadNodes()[:12] // tiers 1..12, tier 13 missing
	writeFixture(t, dir, FileUnlockTrees, singleTreeFixture(nodes...))

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "tier 13")
}

// TestUnlockTrees_DuplicateNodeIDRejected proves a globally-duplicated
// node id is rejected.
func TestUnlockTrees_DuplicateNodeIDRejected(t *testing.T) {
	dir := t.TempDir()
	nodes := validRoadNodes()
	nodes[12] = unlockNodeJSON("roads_t1", 13, "") // duplicate of tier-1 node's id
	writeFixture(t, dir, FileUnlockTrees, singleTreeFixture(nodes...))

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "duplicate node id")
}

// TestUnlockTrees_PrereqCycleRejected proves a prereq graph with a
// two-node cycle is rejected.
func TestUnlockTrees_PrereqCycleRejected(t *testing.T) {
	dir := t.TempDir()
	nodes := validRoadNodes()
	nodes[0] = unlockNodeJSON("roads_t1", 1, `"prereqNodeIds":["roads_t2"]`)
	nodes[1] = unlockNodeJSON("roads_t2", 2, `"prereqNodeIds":["roads_t1"]`)
	writeFixture(t, dir, FileUnlockTrees, singleTreeFixture(nodes...))

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "cyclic")
}

// TestUnlockTrees_DanglingPrereqRejected proves a prereqNodeIds entry
// that does not resolve to an existing node is rejected.
func TestUnlockTrees_DanglingPrereqRejected(t *testing.T) {
	dir := t.TempDir()
	nodes := validRoadNodes()
	nodes[1] = unlockNodeJSON("roads_t2", 2, `"prereqNodeIds":["nonexistent_node"]`)
	writeFixture(t, dir, FileUnlockTrees, singleTreeFixture(nodes...))

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "unknown node")
}

// TestUnlockTrees_UnknownKindRejected proves a node with a kind outside
// the unlock/none enum is rejected.
func TestUnlockTrees_UnknownKindRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileUnlockTrees, `{"version":1,"meta":{"categories":["Roads"]},"trees":[{"id":"roads","name":"Roads","nodes":[{"id":"roads_bad","name":"Bad","kind":"bogus","tier":1,"specRef":"§10","description":"x","dpCost":1,"prereqTier":1}]}]}`)

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "kind")
}

// TestUnlockTrees_CategoryCountMismatchRejected proves a tree count that
// diverges from meta.categories is rejected (the data-derived "12
// categories" check — GR#15).
func TestUnlockTrees_CategoryCountMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileUnlockTrees, `{"version":1,"meta":{"categories":["Roads","Electricity"]},"trees":[{"id":"roads","name":"Roads","nodes":[]}]}`)

	_, err := LoadUnlockTrees(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "must have exactly 2 trees")
}
