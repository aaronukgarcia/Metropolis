package compose

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079943 destructive-round regression (GR#23). The headline
// save_roundtrip tests drive a gameplay sequence and assert the compose ledger
// round-trips, but that sequence leaves SOME durable digest fields at zero
// (vitalBirths / vitalDeaths never move off default in the short driven window
// — verified: driveMultiDomain yields births=0 deaths=0), so their round-trip
// proof there is vacuous (0 -> 0) and would NOT catch a dropped restore line.
//
// This test closes that hole directly at the participant boundary: it sets
// EVERY durable field to a distinct non-zero sentinel, streams the participant
// Source into its own Handler on a fresh simState, and asserts each field
// restored to exactly its sentinel. Mutation-proof: deleting any single line in
// composeLedgerFromWire (or omitting any field from composeLedgerToWire)
// reddens that field's assertion, with no dependence on gameplay driving the
// field off zero.
func TestComposeLedgerParticipant_EveryDurableFieldRoundTrips(t *testing.T) {
	// Distinct sentinels so a cross-wired field (A copied into B's slot) is
	// caught as well as a dropped one.
	src := &simState{
		moneyFlows:                       1_000_001,
		netMigration:                     -222,
		consumptionDelivered:             333.5,
		vitalBirths:                      444,
		vitalDeaths:                      555,
		peopleOpening:                    666,
		peopleDelta:                      -777,
		moneyOpening:                     8_000_008,
		moneyDelta:                       -99_099,
		buildRegistryCursor:              build.BuildOrderID(11_011),
		buildDemolitionCursor:            build.BuildOrderID(22_022),
		deathServiceBridgeCemeteryIDs:    []string{"build-order-1-cemetery", "build-order-3-cemetery"},
		deathServiceBridgeCrematoriumIDs: []string{"build-order-2-crematorium"},
	}

	// Drain the participant Source (one record) and feed it straight into a
	// fresh participant's Handler — the exact save->load path save.Manager runs.
	pSrc := newComposeLedgerParticipant(src)
	dst := &simState{}
	pDst := newComposeLedgerParticipant(dst)

	source := pSrc.Source()
	handler := pDst.Handler()
	got := 0
	for {
		rec, ok, err := source()
		if err != nil {
			t.Fatalf("Source error: %v", err)
		}
		if !ok {
			break
		}
		if err := handler(rec); err != nil {
			t.Fatalf("Handler error: %v", err)
		}
		got++
	}
	if got != 1 {
		t.Fatalf("expected exactly 1 compose-ledger record, got %d", got)
	}

	checks := []struct {
		name string
		a, b int64
	}{
		{"moneyFlows", src.moneyFlows, dst.moneyFlows},
		{"netMigration", src.netMigration, dst.netMigration},
		{"vitalBirths", src.vitalBirths, dst.vitalBirths},
		{"vitalDeaths", src.vitalDeaths, dst.vitalDeaths},
		{"peopleOpening", src.peopleOpening, dst.peopleOpening},
		{"peopleDelta", src.peopleDelta, dst.peopleDelta},
		{"moneyOpening", src.moneyOpening, dst.moneyOpening},
		{"moneyDelta", src.moneyDelta, dst.moneyDelta},
	}
	for _, c := range checks {
		if c.a != c.b {
			t.Errorf("durable field %q did NOT round-trip through the participant: src=%d dst=%d", c.name, c.a, c.b)
		}
	}
	if src.consumptionDelivered != dst.consumptionDelivered {
		t.Errorf("durable field consumptionDelivered did NOT round-trip: src=%f dst=%f", src.consumptionDelivered, dst.consumptionDelivered)
	}

	// BUG-743: the two build->deathservices bridge cursors + roster.
	if src.buildRegistryCursor != dst.buildRegistryCursor {
		t.Errorf("durable field buildRegistryCursor did NOT round-trip: src=%d dst=%d", src.buildRegistryCursor, dst.buildRegistryCursor)
	}
	if src.buildDemolitionCursor != dst.buildDemolitionCursor {
		t.Errorf("durable field buildDemolitionCursor did NOT round-trip: src=%d dst=%d", src.buildDemolitionCursor, dst.buildDemolitionCursor)
	}
	if !reflect.DeepEqual(src.deathServiceBridgeCemeteryIDs, dst.deathServiceBridgeCemeteryIDs) {
		t.Errorf("durable field deathServiceBridgeCemeteryIDs did NOT round-trip: src=%v dst=%v", src.deathServiceBridgeCemeteryIDs, dst.deathServiceBridgeCemeteryIDs)
	}
	if !reflect.DeepEqual(src.deathServiceBridgeCrematoriumIDs, dst.deathServiceBridgeCrematoriumIDs) {
		t.Errorf("durable field deathServiceBridgeCrematoriumIDs did NOT round-trip: src=%v dst=%v", src.deathServiceBridgeCrematoriumIDs, dst.deathServiceBridgeCrematoriumIDs)
	}
	// The Wire-time stopgap roster is DELIBERATELY untouched by this
	// participant (compose.go's own field doc) — prove a fresh dst keeps
	// its zero value, unaffected by whatever src carried.
	if dst.deathServiceCemeteryIDs != nil || dst.deathServiceCrematoriumIDs != nil {
		t.Errorf("participant unexpectedly wrote the Wire-time stopgap roster: cemeteryIDs=%v crematoriumIDs=%v (must stay untouched by Load)", dst.deathServiceCemeteryIDs, dst.deathServiceCrematoriumIDs)
	}

	// treasury/citizenWealth are DERIVED (recomputed from finance in Load), so
	// the participant deliberately does NOT carry them: prove that contract
	// holds (a fresh dst keeps its zero, the participant did not touch them).
	if dst.treasury != 0 || dst.citizenWealth != 0 {
		t.Errorf("participant unexpectedly wrote a derived mirror: treasury=%d citizenWealth=%d (must be recomputed in Load, not serialized)", dst.treasury, dst.citizenWealth)
	}

	// Unknown-kind must fail loud/closed rather than silently loading a partial
	// ledger.
	if err := pDst.Handler()(serialize.Record{Kind: "compose.WRONG", Data: []byte(`{}`)}); err == nil {
		t.Error("Handler accepted an unknown record kind; must fail closed")
	}
}
