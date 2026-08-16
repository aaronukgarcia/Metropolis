package firms

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestFreightFirmRegistrarIntegration proves the freight FirmRegistrar seam
// is a REAL, callable surface: freight's RegisterFirms wires firms in as
// the registrar and every chain stage registers as a firm in this registry
// (freight AC-4 "stages register as firms").
func TestFreightFirmRegistrarIntegration(t *testing.T) {
	dir, err := data.ResolveDataDir("firms-freight-integration")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	fa, err := freight.Load(dir, "firms-freight-integration")
	if err != nil {
		t.Fatalf("freight.Load: %v", err)
	}
	api, err := Load(dir, 42, "firms-freight-integration")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := fa.RegisterFirms(api); err != nil {
		t.Fatalf("RegisterFirms: %v", err)
	}

	stages := fa.Stages()
	if len(stages) == 0 {
		t.Fatal("freight returned no chain stages")
	}
	seen := map[uint64]bool{}
	for _, st := range stages {
		if st.Firm.ID == 0 {
			t.Fatalf("stage %s did not register a firm through the seam", st.ID)
		}
		if seen[st.Firm.ID] {
			t.Fatalf("firm ID %d assigned to more than one stage", st.Firm.ID)
		}
		seen[st.Firm.ID] = true
	}

	// The firms registry now holds the registered stage firms.
	firms := api.Firms()
	if len(firms) == 0 {
		t.Fatal("no firms registered in the firms registry")
	}
	// Every registered firm has a non-zero ID (matches the stage firm IDs).
	for _, f := range firms {
		if f.ID == 0 {
			t.Fatal("registered firm has a zero ID")
		}
	}
}

// TestMarketInputShortfallConstrainsGrowth (AC-8): constraining a firm's
// input commodity below its requested input reduces its output/growth rate
// rather than being silently absorbed.
func TestMarketInputShortfallConstrainsGrowth(t *testing.T) {
	mkt, err := market.LoadDefault("firms-market")
	if err != nil {
		t.Fatalf("market.LoadDefault: %v", err)
	}
	api := newAPIWithConfig(t, controlledConfig(), 1)
	_ = api.SetMarket(mkt)
	_ = api.SetCitizens(seedCitizens(t, 5))

	id, err := api.Found(1) // tertiary founder → ConsumerGoods input
	if err != nil {
		t.Fatalf("Found: %v", err)
	}

	// Request double the commodity's capacity so availability binds.
	avail, err := mkt.Availability(market.ConsumerGoods, 1<<40)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if avail.Available <= 0 {
		t.Fatal("expected a positive ConsumerGoods capacity ceiling")
	}
	api.firms[id].firm.InputCommodity = market.ConsumerGoods
	api.firms[id].firm.InputRequired = avail.Available * 2 // request 2× capacity

	if err := api.ResolveMonth(0); err != nil {
		t.Fatalf("ResolveMonth: %v", err)
	}

	firm, err := api.Firm(id)
	if err != nil {
		t.Fatalf("Firm: %v", err)
	}
	if firm.Financial.OutputScale >= 1000 {
		t.Fatalf("output scale not reduced under input shortfall: %d", firm.Financial.OutputScale)
	}
	// The reduction tracks the shortfall (≈ capacity/request = 1/2).
	want := avail.Available * 1000 / (avail.Available * 2)
	if firm.Financial.OutputScale != want {
		t.Fatalf("output scale = %d, want %d (capacity/request)", firm.Financial.OutputScale, want)
	}
}
