package power

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// writeTemp writes content to a temp .json file and returns its path —
// the catalogue loader's test seam (mirrors how data-file loaders are
// exercised elsewhere without touching the repo's real data/ tree).
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pylons.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

const validCatalogueJSON = `{
  "version": 1,
  "tiers": {
    "localPole":       { "capacityMW": 0.5, "costMicropounds": 2000000000,   "footprintCells": 1 },
    "standardLattice": { "capacityMW": 40,  "costMicropounds": 25000000000,  "footprintCells": 4 },
    "superGrid":       { "capacityMW": 400, "costMicropounds": 800000000000, "footprintCells": 9 }
  }
}`

func TestLoadPylonCatalogue_Valid(t *testing.T) {
	cat, err := LoadPylonCatalogue(writeTemp(t, validCatalogueJSON), "test-cid")
	if err != nil {
		t.Fatalf("LoadPylonCatalogue(valid): %v", err)
	}
	if cat.Version != 1 {
		t.Errorf("Version = %d, want 1", cat.Version)
	}
	if len(cat.Tiers) != 3 {
		t.Fatalf("len(Tiers) = %d, want 3", len(cat.Tiers))
	}
	// Canonical enum order, not JSON key order.
	wantOrder := []PylonClass{ClassLocalPole, ClassStandardLattice, ClassSuperGrid}
	for i, want := range wantOrder {
		if got := cat.Tiers[i].Class; got != want {
			t.Errorf("Tiers[%d].Class = %v, want %v (canonical enum order)", i, got, want)
		}
	}
	super, ok := cat.Tier(ClassSuperGrid)
	if !ok {
		t.Fatal("Tier(superGrid): not found")
	}
	if super.CapacityMW != 400 || super.CostMicropounds != 800000000000 || super.FootprintCells != 9 {
		t.Errorf("superGrid tier = %+v, want capacity 400 / cost 8e11 / footprint 9", super)
	}
}

func TestLoadPylonCatalogue_DeterministicAcrossLoads(t *testing.T) {
	a, err := LoadPylonCatalogue(writeTemp(t, validCatalogueJSON), "test-cid")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	b, err := LoadPylonCatalogue(writeTemp(t, validCatalogueJSON), "test-cid")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(a.Tiers) != len(b.Tiers) {
		t.Fatalf("tier count drifted across identical loads: %d vs %d", len(a.Tiers), len(b.Tiers))
	}
	for i := range a.Tiers {
		if a.Tiers[i] != b.Tiers[i] {
			t.Errorf("Tiers[%d] differs across identical loads: %+v vs %+v", i, a.Tiers[i], b.Tiers[i])
		}
	}
}

func TestLoadPylonCatalogue_Rejections(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		field string
	}{
		{"missing version", `{"tiers":{"localPole":{"capacityMW":1,"costMicropounds":1,"footprintCells":1}}}`, "version"},
		{"empty tiers", `{"version":1,"tiers":{}}`, "tiers"},
		{"unknown tier key", `{"version":1,"tiers":{"hvdcFrance":{"capacityMW":1,"costMicropounds":1,"footprintCells":1}}}`, "tiers.hvdcFrance"},
		{"negative capacity", `{"version":1,"tiers":{"localPole":{"capacityMW":-1,"costMicropounds":1,"footprintCells":1}}}`, "tiers.localPole.capacityMW"},
		{"zero cost", `{"version":1,"tiers":{"localPole":{"capacityMW":1,"costMicropounds":0,"footprintCells":1}}}`, "tiers.localPole.costMicropounds"},
		{"zero footprint", `{"version":1,"tiers":{"localPole":{"capacityMW":1,"costMicropounds":1,"footprintCells":0}}}`, "tiers.localPole.footprintCells"},
		{"overflow magnitude", `{"version":1,"tiers":{"localPole":{"capacityMW":1e308,"costMicropounds":1,"footprintCells":1}}}`, "tiers.localPole.capacityMW"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadPylonCatalogue(writeTemp(t, tc.json), "test-cid")
			if err == nil {
				t.Fatal("expected ErrCatalogueDataInvalid, got nil")
			}
			var e *errs.E
			if !errors.As(err, &e) || e.Code != ErrCatalogueDataInvalid {
				t.Fatalf("error = %v, want registry code %s", err, ErrCatalogueDataInvalid)
			}
		})
	}
}

func TestLoadPylonCatalogue_MissingFile(t *testing.T) {
	_, err := LoadPylonCatalogue(filepath.Join(t.TempDir(), "absent.json"), "test-cid")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrCatalogueDataInvalid {
		t.Fatalf("error = %v, want registry code %s", err, ErrCatalogueDataInvalid)
	}
}

func TestLoadPylonCatalogue_MalformedJSON(t *testing.T) {
	_, err := LoadPylonCatalogue(writeTemp(t, "{not json"), "test-cid")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrCatalogueDataInvalid {
		t.Fatalf("error = %v, want registry code %s", err, ErrCatalogueDataInvalid)
	}
}
