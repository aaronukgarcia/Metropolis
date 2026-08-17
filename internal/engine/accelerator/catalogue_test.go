package accelerator

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestAcceleratorEntryReconcilesCatalogue is AC-1 (shape (a)): the
// accelerator reconciles against the existing hadron_research_ring
// mega-project entry in place — exactly one catalogue anchor exists, its
// pre-existing fields stay byte-equivalent (M10, 2B), and the only change is
// its consumptionRef now resolving to the "accelerator" class.
func TestAcceleratorEntryReconcilesCatalogue(t *testing.T) {
	dir, err := data.ResolveDataDir(testCorrelationID())
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	b, err := data.LoadBuildings(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadBuildings: %v", err)
	}

	var anchors []data.BuildingEntry
	for _, e := range b.Entries {
		if e.ID == CatalogueKey {
			anchors = append(anchors, e)
		}
	}
	if len(anchors) != 1 {
		t.Fatalf("expected exactly one accelerator catalogue anchor (%s), got %d", CatalogueKey, len(anchors))
	}
	entry := anchors[0]

	// Pre-existing fields remain byte-equivalent (AC-1 shape (a) keeps them).
	if entry.Unlock.Milestone != "M10" {
		t.Errorf("hadron_research_ring unlock.milestone = %q, want M10 (byte-equivalent)", entry.Unlock.Milestone)
	}
	if entry.CostRaw != "2B" {
		t.Errorf("hadron_research_ring costRaw = %q, want 2B (byte-equivalent)", entry.CostRaw)
	}
	if entry.CatalogueSection != "MP" || entry.SupplementCategory != "mega-projects" {
		t.Errorf("hadron_research_ring catalogue tags changed: section=%q category=%q", entry.CatalogueSection, entry.SupplementCategory)
	}

	// The one enrichment: its consumptionRef resolves to the accelerator class.
	if entry.ConsumptionRef != "accelerator" {
		t.Errorf("hadron_research_ring consumptionRef = %q, want %q (the accelerator class)", entry.ConsumptionRef, "accelerator")
	}

	// And that class actually exists in data/consumption.json (the draw's
	// coefficient source — AC-4).
	c, err := data.LoadConsumption(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadConsumption: %v", err)
	}
	if _, ok := c.Classes["accelerator"]; !ok {
		t.Errorf("data/consumption.json has no %q class for the accelerator's consumptionRef", "accelerator")
	}
}
