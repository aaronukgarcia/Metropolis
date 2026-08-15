package unlocks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

// realAPI loads a *UnlocksAPI against the repository's own
// data/unlock_trees.json (via ResolveDataDir), for tests that check the
// actual spec-transcribed data (AC-3/AC-6/AC-19) rather than a synthetic
// fixture.
func realAPI(t *testing.T) *UnlocksAPI {
	t.Helper()
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	api, err := Load(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("Load real data/unlock_trees.json: %v", err)
	}
	return api
}

// realAPIWithFinance is realAPI plus a wired, fresh engine.finance, for
// tests that exercise milestone crossings (which post cash awards).
func realAPIWithFinance(t *testing.T) (*UnlocksAPI, *finance.FinanceAPI) {
	t.Helper()
	api := realAPI(t)
	f := finance.NewFinanceAPI(testCorrelationID())
	if err := api.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	return api, f
}

func assertCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %s, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != wantCode {
		t.Errorf("e.Code = %s, want %s (err: %v)", e.Code, wantCode, err)
	}
}

// writeUnlockTreesFixture writes content to dir/unlock_trees.json (the
// filename foundation/data.LoadUnlockTrees resolves).
func writeUnlockTreesFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, data.FileUnlockTrees), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// minimalUnlockTreesJSON returns a minimal-but-valid single-tree
// unlock_trees.json covering all thirteen tiers, so tests that exercise a
// specific later validation (a cyclic prereq graph, an unknown prereq
// edge) can reach it without authoring the full twelve-category file.
// Node ids match foundation.data's buildingIDPattern and every tier is
// covered exactly once, satisfying Validate's structural checks up to the
// prereq-graph stage.
func minimalUnlockTreesJSON(extraPrereqs map[string][]string) string {
	var b strings.Builder
	b.WriteString(`{"version": 1, "meta": {"categories": ["Roads"]}, "trees": [{"id": "roads", "name": "Roads", "nodes": [`)

	nodes := make([]string, 0, 13)
	for tier := 1; tier <= 13; tier++ {
		id := "roads_t" + itoa(tier)
		prereq := ""
		if extraPrereqs != nil {
			if ids, ok := extraPrereqs[id]; ok && len(ids) > 0 {
				prereq = `, "prereqNodeIds": ["` + strings.Join(ids, `", "`) + `"]`
			}
		}
		nodes = append(nodes, `{"id": "`+id+`", "name": "node `+id+`", "kind": "unlock", "tier": `+itoa(tier)+`, "specRef": "§10", "description": "test node", "dpCost": `+itoa(tier)+`, "prereqTier": `+itoa(tier)+`}`+prereq)
	}
	b.WriteString(strings.Join(nodes, ","))
	b.WriteString(`]}]}`)
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
