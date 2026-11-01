package logistics

// BUG-1142 destructive round r1 (opus-round-r1-bug1142): startingStocks data
// hostility. Every malformed row must be a fail-closed registry refusal at
// Load, never a silent clamp or a silently-unused shelf.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func b1142r1DirWithStocks(t *testing.T, stocks []map[string]any) string {
	t.Helper()
	cid := errs.NewCorrelationID()
	src, err := data.ResolveDataDir(cid)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if e.Name() == "logistics.json" {
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatalf("unmarshal logistics.json: %v", err)
			}
			doc["startingStocks"] = stocks
			if b, err = json.Marshal(doc); err != nil {
				t.Fatalf("marshal: %v", err)
			}
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dst
}

func b1142r1Row(district, commodity string, capacity, initial int64) map[string]any {
	return map[string]any{"district": district, "commodity": commodity, "capacity": capacity, "initialLevel": initial}
}

func TestBUG1142R1_StartingStocks_HostileRowsRefused(t *testing.T) {
	cases := map[string][]map[string]any{
		"negative initialLevel":   {b1142r1Row(b1142r1BuildDistrict, "constructionMaterials", 100, -1)},
		"negative capacity":       {b1142r1Row(b1142r1BuildDistrict, "constructionMaterials", -1, 0)},
		"initialLevel > capacity": {b1142r1Row(b1142r1BuildDistrict, "constructionMaterials", 100, 101)},
		"unknown commodity":       {b1142r1Row(b1142r1BuildDistrict, "unobtainium", 100, 100)},
		"empty district":          {b1142r1Row("", "constructionMaterials", 100, 100)},
		"duplicate district row": {
			b1142r1Row(b1142r1BuildDistrict, "constructionMaterials", 100000, 100000),
			b1142r1Row(b1142r1BuildDistrict, "constructionMaterials", 10, 0),
		},
	}
	for name, rows := range cases {
		rows := rows
		t.Run(name, func(t *testing.T) {
			dir := b1142r1DirWithStocks(t, rows)
			l, err := Load(dir, errs.NewCorrelationID())
			if err == nil {
				lvl := int64(-1)
				if perr := l.ProvisionStartingStocks(); perr == nil {
					if s, serr := l.Stock(b1142r1BuildDistrict, market.ConstructionMaterials); serr == nil {
						lvl = s.Level
					}
				}
				t.Errorf("Load accepted hostile startingStocks (%s); provisioned city shelf level = %d", name, lvl)
			}
		})
	}
}

// The shipped data must provision the shelf engine.build actually draws from.
func TestBUG1142R1_StartingStocks_ShippedDataFeedsBuildDistrict(t *testing.T) {
	l, err := LoadDefault(errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if err := l.ProvisionStartingStocks(); err != nil {
		t.Fatalf("ProvisionStartingStocks: %v", err)
	}
	s, err := l.Stock(b1142r1BuildDistrict, market.ConstructionMaterials)
	if err != nil {
		t.Fatalf("Stock(%s): %v", b1142r1BuildDistrict, err)
	}
	if s.Level <= 0 {
		t.Fatalf("shipped starting stock for %s is empty", b1142r1BuildDistrict)
	}
}

// b1142r1BuildDistrict mirrors engine.build.DefaultDistrict (build imports
// logistics, so the constant cannot be imported here without a cycle).
const b1142r1BuildDistrict = "city"
