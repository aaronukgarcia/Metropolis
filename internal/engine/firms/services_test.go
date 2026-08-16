package firms

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// TestServicesDemandIsSuperlinear (AC-11): doubling the served firm count
// more than doubles the professional-services demand figure.
func TestServicesDemandIsSuperlinear(t *testing.T) {
	api := newTestAPI(t, 1) // real data/firms.json, exponent 1.3
	const n = int64(100)
	dn := api.ServicesDemand(n)
	d2n := api.ServicesDemand(2 * n)
	if d2n <= 2*dn {
		t.Fatalf("services demand is not superlinear: Demand(2n)=%d <= 2*Demand(n)=%d", d2n, 2*dn)
	}
	// Sanity: a positive-but-small count yields a positive demand.
	if api.ServicesDemand(1) <= 0 {
		t.Fatal("expected positive services demand for a positive firm count")
	}
	// A non-positive count is the empty case (0), never negative.
	if api.ServicesDemand(0) != 0 || api.ServicesDemand(-1) != 0 {
		t.Fatal("expected zero services demand for a non-positive firm count")
	}
}

// TestNonServicesFirmCountFeedsDemand (AC-11): the superlinear demand figure
// is parameterised by the count of NON-services firms (services firms are
// excluded from the served count).
func TestNonServicesFirmCountFeedsDemand(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	// Register firms directly (same-package) with known sectors.
	api.firms[1] = &firmState{firm: Firm{ID: 1, Sector: citizens.SectorSecondary}}
	api.firms[2] = &firmState{firm: Firm{ID: 2, Sector: citizens.SectorSecondary}}
	api.firms[3] = &firmState{firm: Firm{ID: 3, Sector: citizens.SectorTertiary}}
	if got := api.NonServicesFirmCount(); got != 2 {
		t.Fatalf("NonServicesFirmCount = %d, want 2", got)
	}
}
