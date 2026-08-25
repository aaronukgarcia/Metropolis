package compose

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/policies"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uidistricts "github.com/aaronukgarcia/Metropolis/internal/ui/screens/districts"
)

// FEAT-022's engine-side proof set for the "f8.districts" view
// (districts_publish.go): it is REGISTERED, its Subscribe is ACCEPTED, and
// the delta it publishes carries the real (district, instrument) join —
// engine.policies' district roster cross engine.tax's instrument table with
// EffectiveRate = Rate × Multiplier. The load-bearing assertion is the join
// CONTENT (a real district name, a real instrument label, and the
// engine-computed effective rate), never merely "the subscription
// succeeded".

// TestDistrictsViewSubscriptionName_MatchesUIScreenConstant guards the two
// independently-maintained copies of "f8.districts" (this package's
// districtsViewSubscriptionName and ui.screen.districts' ViewSubscriptionName)
// against drifting apart in VALUE — the same GR#20/SF-1 discipline the
// census/finance/services/viewport name-match tests apply. This test file is
// the only place in this package that imports internal/ui/screens/districts;
// production code never does.
func TestDistrictsViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if districtsViewSubscriptionName != uidistricts.ViewSubscriptionName {
		t.Fatalf("districtsViewSubscriptionName = %q, want %q (ui.screen.districts' own ViewSubscriptionName)", districtsViewSubscriptionName, uidistricts.ViewSubscriptionName)
	}
}

// TestDistrictsView_IsRegistered pins the registration itself, by name, in
// compose's fixed view-registration slice — the same shape as
// TestCensusView_IsRegistered, so deleting the entry names the symptom.
func TestDistrictsView_IsRegistered(t *testing.T) {
	for _, n := range RegisteredViewNames() {
		if n == districtsViewSubscriptionName {
			return
		}
	}
	t.Fatalf("%q is not in compose's registered view set %v — with it absent, engine.core rejects the districts screen's Subscribe and F8 renders blank (FEAT-022)", districtsViewSubscriptionName, RegisteredViewNames())
}

// wireDistrictsTestEngine builds a real compose.Wire'd engine (default deps
// — engine.policies/engine.tax are always constructed internally by Wire, no
// test-injection seam exists for them) and a live subscription pump, and
// returns the *Composition too so a test can seed a district + multiplier
// through the composed modules' own public APIs before subscribing.
func wireDistrictsTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, *Composition, context.CancelFunc) {
	t.Helper()
	cid := errs.NewCorrelationID()

	e := core.NewEngine()
	comp, err := Wire(e, &Deps{CorrelationID: cid})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		cancel()
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	return e, transport, comp, cancel
}

// seedDistrict is the shared test helper: creates one named district and
// sets a per-district multiplier on one instrument, through the composed
// modules' own public APIs (the real data path, not a fabricated patch).
func seedDistrict(t *testing.T, comp *Composition, name, instrument string, multiplier float64) policies.DistrictID {
	t.Helper()
	did, err := comp.Policies().CreateDistrict(name, []policies.CellRef{{
		Tile:  world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY},
		Local: world.CellLocal{Row: 0, Col: 0},
	}})
	if err != nil {
		t.Fatalf("CreateDistrict(%q): %v", name, err)
	}
	if err := comp.Tax().SetDistrictMultiplier(tax.DistrictID(did), instrument, multiplier); err != nil {
		t.Fatalf("SetDistrictMultiplier(%q, %q, %v): %v", did, instrument, multiplier, err)
	}
	return did
}

// TestDistrictsView_EndToEnd_DeltaCarriesRosterAndJoin is the core
// engine-side CONTENT proof: after seeding one district + one multiplier
// through the composed APIs, Subscribe("f8.districts") against a REAL
// compose.Wire'd engine is accepted, and the delivered patch carries the
// district's name, one tax-setting row per (loaded) instrument, and the
// engine-computed EffectiveRate = Rate × Multiplier on the instrument that
// was given a non-neutral multiplier.
func TestDistrictsView_EndToEnd_DeltaCarriesRosterAndJoin(t *testing.T) {
	_, transport, comp, cancel := wireDistrictsTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	did := seedDistrict(t, comp, "Folkestone", "council-tax", 1.5)

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uidistricts.ViewSubscriptionName)

	var patch districtsWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f8.districts patch: %v", err)
	}
	if patch.SchemaVersion != districtsWireSchemaVersion {
		t.Fatalf("patch schemaVersion = %d, want %d (ui.screen.districts' decodeWirePatch drops any other version outright)", patch.SchemaVersion, districtsWireSchemaVersion)
	}
	if patch.Districts == nil {
		t.Fatal("districts is absent from the wire patch — buildDistrictsPatch always emits it; an absent roster renders as unavailable")
	}
	if len(*patch.Districts) != 1 {
		t.Fatalf("districts carries %d entries, want 1 (the single seeded district)", len(*patch.Districts))
	}
	if (*patch.Districts)[0].Name != "Folkestone" || (*patch.Districts)[0].DistrictID != string(did) {
		t.Fatalf("districts[0] = %+v, want {districtId %q, name \"Folkestone\"}", (*patch.Districts)[0], string(did))
	}

	if patch.TaxSettings == nil {
		t.Fatal("taxSettings is absent from the wire patch — buildDistrictsPatch always emits it")
	}
	// Six loaded instruments (data/tax_instruments.json) × one district.
	if len(*patch.TaxSettings) != 6 {
		t.Fatalf("taxSettings carries %d rows, want 6 (one per loaded instrument × the single seeded district)", len(*patch.TaxSettings))
	}

	var council *districtsWireTaxSetting
	for i := range *patch.TaxSettings {
		row := &(*patch.TaxSettings)[i]
		if row.DistrictID != string(did) {
			t.Errorf("taxSettings row for instrument %q has districtId %q, want %q", row.InstrumentID, row.DistrictID, string(did))
		}
		if row.InstrumentID == "council-tax" {
			council = row
		}
	}
	if council == nil {
		t.Fatal("taxSettings has no council-tax row — the join did not iterate the loaded instruments")
	}
	if council.InstrumentLabel != "Council tax" {
		t.Errorf("council-tax row label = %q, want %q (the instrument's data-loaded name)", council.InstrumentLabel, "Council tax")
	}
	if council.Multiplier != 1.5 {
		t.Errorf("council-tax row multiplier = %v, want 1.5", council.Multiplier)
	}
	// council-tax's reference rate is 100 (data/tax_instruments.json's
	// lowest bearer-weight rate point); the engine computes
	// EffectiveRate = Rate × Multiplier.
	if council.Rate != 100 {
		t.Errorf("council-tax row rate = %v, want 100", council.Rate)
	}
	if council.EffectiveRate != 150 {
		t.Errorf("council-tax row effectiveRate = %v, want 150 (100 × 1.5)", council.EffectiveRate)
	}
}

// TestDistrictsView_EmptyAtSeed pins the honest data-source gap: baseline
// one seeds NO districts (engine.policies is constructed empty via
// NewPoliciesAPI), so a fresh boot's patch carries non-nil-but-empty
// Districts and TaxSettings lists. This is NOT a placeholder to be silently
// deleted — when a gameplay path starts creating districts at Wire time (or
// a future seed) this assertion is expected to fail, and that failure is
// the SIGNAL that the F8 roster surface just turned on (update it
// deliberately then, never by loosening it now).
func TestDistrictsView_EmptyAtSeed(t *testing.T) {
	_, transport, _, cancel := wireDistrictsTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uidistricts.ViewSubscriptionName)

	var patch districtsWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f8.districts patch: %v", err)
	}
	if patch.Districts == nil {
		t.Fatal("districts is absent from the wire patch — buildDistrictsPatch must always emit the surface (even empty) so the screen distinguishes 'served, empty' from 'not served'")
	}
	if len(*patch.Districts) != 0 {
		t.Fatalf("districts carries %d entries at a fresh boot, want 0 (no district is seeded by Wire)", len(*patch.Districts))
	}
	if patch.TaxSettings == nil {
		t.Fatal("taxSettings is absent from the wire patch — same always-emitted contract as Districts")
	}
	if len(*patch.TaxSettings) != 0 {
		t.Fatalf("taxSettings carries %d rows at a fresh boot, want 0 (the join is per-district, and no district exists)", len(*patch.TaxSettings))
	}
}

// TestDistrictsView_EndToEnd_RoundTripsThroughUIScreenDecoder proves
// compose's independently-maintained schema copy (districts_publish.go)
// actually round-trips through ui.screen.districts' own ApplyDelta/accessor
// path — the UI half of the seam the engine can never import (GR#20). The
// screen's Districts()/TaxSettings() must report have=true with the seeded
// content, which is the state the render layer turns into visible glyphs.
func TestDistrictsView_EndToEnd_RoundTripsThroughUIScreenDecoder(t *testing.T) {
	_, transport, comp, cancel := wireDistrictsTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	did := seedDistrict(t, comp, "Folkestone", "council-tax", 1.5)

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uidistricts.ViewSubscriptionName)

	scr := uidistricts.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-districts")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: delta.Patch})

	dists, have := scr.Districts()
	if !have {
		t.Fatal("ui.screen.districts Districts() reported have=false after ApplyDelta — the published patch did not carry a districts field the UI decoder recognised")
	}
	if len(dists) != 1 || dists[0].Name != "Folkestone" || dists[0].DistrictID != string(did) {
		t.Fatalf("Districts() = %+v, want one \"Folkestone\" entry with id %q", dists, string(did))
	}

	settings, have := scr.TaxSettings()
	if !have {
		t.Fatal("ui.screen.districts TaxSettings() reported have=false after ApplyDelta — the published patch did not carry a taxSettings field")
	}
	if len(settings) != 6 {
		t.Fatalf("TaxSettings() = %d rows, want 6", len(settings))
	}
}
