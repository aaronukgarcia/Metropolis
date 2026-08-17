package comms

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
)

// TestSectorMirrorsCitizens is the weakness-pattern-#2 drift test: engine.comms
// deliberately mirrors engine.citizens' five Sector buckets as a local type so
// it need not import engine.citizens (not a registered outbound edge, GR#20).
// The duplication is acceptable; silent divergence is not. If this test fails,
// one side's sector order/buckets changed and the mirror must be updated to
// match — do NOT "fix" the test by loosening it.
func TestSectorMirrorsCitizens(t *testing.T) {
	pairs := []struct {
		name string
		mine Sector
		ref  citizens.Sector
	}{
		{"none", SectorNone, citizens.SectorNone},
		{"primary", SectorPrimary, citizens.SectorPrimary},
		{"secondary", SectorSecondary, citizens.SectorSecondary},
		{"tertiary", SectorTertiary, citizens.SectorTertiary},
		{"public", SectorPublic, citizens.SectorPublic},
	}
	for _, p := range pairs {
		if int(p.mine) != int(p.ref) {
			t.Errorf("%s sector diverged: comms=%d citizens=%d — the two enums must stay in lockstep (weakness pattern #2)", p.name, p.mine, p.ref)
		}
	}
	if numSectors != 5 {
		t.Errorf("numSectors=%d, want 5 (the five citizens buckets)", numSectors)
	}
}

// TestParcelCommodityMatchesMarket is the weakness-pattern-#2 drift test:
// engine.comms duplicates engine.market's ConsumerGoods slug as an untyped
// string constant (parcelCommodity) so it can pass the commodity to
// engine.logistics without importing engine.market (not a registered outbound
// edge, GR#20). If this fails, one side's commodity slug changed and the
// other must be updated in lockstep.
func TestParcelCommodityMatchesMarket(t *testing.T) {
	if parcelCommodity != string(market.ConsumerGoods) {
		t.Errorf("parcelCommodity=%q but market.ConsumerGoods=%q — the two must stay in lockstep (weakness pattern #2)", parcelCommodity, string(market.ConsumerGoods))
	}
}
