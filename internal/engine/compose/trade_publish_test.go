package compose

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uitrade "github.com/aaronukgarcia/Metropolis/internal/ui/screens/trade"
)

// FEAT-017's engine-side proof set for the "f5.trade" view
// (trade_publish.go): it is REGISTERED, its Subscribe is ACCEPTED, and the
// delta it publishes carries real, NON-EMPTY data on the one surface
// engine.freight can genuinely back at a fresh boot — the port panel
// (data/freight.json seeds 2 berths) — while the balance/warehouse surfaces
// are present-but-empty (nothing imports/exports before gameplay) and the
// contracts/junctions/safety surfaces are honestly absent (freight exposes
// no query surface backing them). The load-bearing assertion is the PORT
// CONTENT, never merely "the subscription succeeded".

// TestTradeViewSubscriptionName_MatchesUIScreenConstant guards the two
// independently-maintained copies of "f5.trade" (this package's
// tradeViewSubscriptionName and ui.screen.trade's ViewSubscriptionName)
// against drifting apart in VALUE — the same GR#20/SF-1 discipline the
// census/districts/finance/services/viewport name-match tests apply. This
// test file is the only place in this package that imports
// internal/ui/screens/trade; production code never does.
func TestTradeViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if tradeViewSubscriptionName != uitrade.ViewSubscriptionName {
		t.Fatalf("tradeViewSubscriptionName = %q, want %q (ui.screen.trade's own ViewSubscriptionName)", tradeViewSubscriptionName, uitrade.ViewSubscriptionName)
	}
}

// TestTradeView_IsRegistered pins the registration itself, by name, in
// compose's fixed view-registration slice — the same shape as
// TestDistrictsView_IsRegistered, so deleting the entry names the symptom.
func TestTradeView_IsRegistered(t *testing.T) {
	for _, n := range RegisteredViewNames() {
		if n == tradeViewSubscriptionName {
			return
		}
	}
	t.Fatalf("%q is not in compose's registered view set %v — with it absent, engine.core rejects the trade screen's Subscribe and F5 renders as unavailable panes (FEAT-017)", tradeViewSubscriptionName, RegisteredViewNames())
}

// wireTradeTestEngine builds a real compose.Wire'd engine (default deps —
// engine.freight is constructed internally by Wire) and a live subscription
// pump, mirroring wireCensusTestEngine's shape exactly.
func wireTradeTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, *Composition, context.CancelFunc) {
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

// TestTradeView_EndToEnd_DeltaCarriesRealPortFigures is the core
// engine-side CONTENT proof: Subscribe("f5.trade") against a REAL
// compose.Wire'd engine is accepted, and the delivered patch's port
// sub-surface carries the real, data-sourced figures (berths 2, crane 60t/hr,
// 16 operating hours, 1500t/day customs) rather than zeros — the ONE surface
// freight genuinely backs non-empty at a fresh boot. An absent port field
// here (the exact failure a registered-but-empty view would produce) fails
// the first assertion below, on purpose.
func TestTradeView_EndToEnd_DeltaCarriesRealPortFigures(t *testing.T) {
	_, transport, _, cancel := wireTradeTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uitrade.ViewSubscriptionName)

	var patch tradeWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f5.trade patch: %v", err)
	}
	if patch.SchemaVersion != tradeWireSchemaVersion {
		t.Fatalf("patch schemaVersion = %d, want %d (ui.screen.trade's decodeWirePatch drops any other version outright)", patch.SchemaVersion, tradeWireSchemaVersion)
	}
	if patch.Port == nil {
		t.Fatal("port is absent from the wire patch — buildTradePatch always emits it; an absent port renders as 'unavailable'")
	}
	if !patch.Port.Unlocked {
		t.Fatal("port.unlocked = false, want true (data/freight.json seeds berths 2, so the port is built)")
	}
	if patch.Port.Berths != 2 {
		t.Errorf("port.berths = %d, want 2 (data/freight.json)", patch.Port.Berths)
	}
	if patch.Port.CraneRateTonnesPerHour != 60 {
		t.Errorf("port.craneRateTonnesPerHour = %d, want 60 (data/freight.json)", patch.Port.CraneRateTonnesPerHour)
	}
	if patch.Port.OperatingHoursPerDay != 16 {
		t.Errorf("port.operatingHoursPerDay = %d, want 16 (data/freight.json)", patch.Port.OperatingHoursPerDay)
	}
	if patch.Port.CustomsThroughputTonnesPerDay != 1500 {
		t.Errorf("port.customsThroughputTonnesPerDay = %d, want 1500 (data/freight.json)", patch.Port.CustomsThroughputTonnesPerDay)
	}
	if patch.Port.SmugglingRisk != 0 {
		t.Errorf("port.smugglingRisk = %v, want 0 (zero customs demand at seed)", patch.Port.SmugglingRisk)
	}

	// The balance surface must also be present (non-nil), even though empty.
	if patch.Balance == nil {
		t.Fatal("balance is absent from the wire patch — buildTradePatch always emits it so the screen distinguishes 'served, empty' from 'not served'")
	}
	if patch.Balance.Imports == nil || patch.Balance.Exports == nil {
		t.Fatal("balance.imports/exports absent — both ledgers must always be emitted (empty at seed)")
	}
}

// TestTradeView_EmptyAtSeed pins the honest data-source gap: baseline one
// issues no import/export before gameplay (and no phase hook ticks freight),
// so a fresh boot's balance-of-trade ledgers are empty, the warehouse holds
// no stock, and the contracts/junctions/safety sub-surfaces are ABSENT from
// the wire patch entirely (freight exposes no query surface backing them).
// This is NOT a placeholder to be silently deleted — when a gameplay path
// starts importing/exporting, the balance/warehouse assertions below are
// expected to fail, and that failure is the SIGNAL that the F5 surfaces just
// turned on (update them deliberately then, never by loosening them now).
func TestTradeView_EmptyAtSeed(t *testing.T) {
	_, transport, _, cancel := wireTradeTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uitrade.ViewSubscriptionName)

	var patch tradeWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f5.trade patch: %v", err)
	}

	if patch.Balance == nil || patch.Balance.Imports == nil || patch.Balance.Exports == nil {
		t.Fatal("balance/imports/exports absent — buildTradePatch always emits them")
	}
	if len(patch.Balance.Imports.ByCommodity) != 0 {
		t.Fatalf("balance.imports.byCommodity carries %d rows at a fresh boot, want 0 (nothing imports before gameplay)", len(patch.Balance.Imports.ByCommodity))
	}
	if len(patch.Balance.Exports.ByCommodity) != 0 {
		t.Fatalf("balance.exports.byCommodity carries %d rows at a fresh boot, want 0 (nothing exports before gameplay)", len(patch.Balance.Exports.ByCommodity))
	}
	if len(patch.Balance.Imports.ByArtery) != 0 || len(patch.Balance.Exports.ByArtery) != 0 {
		t.Fatalf("balance byArtery is non-empty at seed — engine.freight's trade ledger has no per-artery rollup, so byArtery must stay empty")
	}
	if patch.Warehouse == nil {
		t.Fatal("warehouse is absent from the wire patch — buildTradePatch always emits the surface (even empty) so the screen distinguishes 'served, empty' from 'not served'")
	}
	if len(*patch.Warehouse) != 0 {
		t.Fatalf("warehouse carries %d rows at a fresh boot, want 0 (no production has run, no import has landed)", len(*patch.Warehouse))
	}

	// The three surfaces freight cannot back must be ABSENT from the raw JSON
	// (not merely empty): an absent field is what ui.screen.trade's ApplyDelta
	// decodes as "unavailable" (SF-7/TRD-8), which is the honest no-data
	// state for a surface the engine has no producer for.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(delta.Patch, &raw); err != nil {
		t.Fatalf("unmarshalling f5.trade patch into raw keys: %v", err)
	}
	for _, absent := range []string{"contracts", "junctions", "safety"} {
		if _, ok := raw[absent]; ok {
			t.Fatalf("raw f5.trade patch carries %q, want it ABSENT — engine.freight exposes no query surface backing it, and an omitted field is what the screen renders as 'unavailable'", absent)
		}
	}
}

// TestTradeView_EndToEnd_RoundTripsThroughUIScreenDecoder proves compose's
// independently-maintained schema copy (trade_publish.go) actually
// round-trips through ui.screen.trade's own ApplyDelta/accessor path — the
// UI half of the seam the engine can never import (GR#20). The screen's
// Port() must report have=true with the real berths figure (the state the
// render layer turns into the "berths: 2" glyph), Balance()/Warehouse()
// have=true (present-but-empty), and Contracts()/Junctions()/Safety()
// have=false (absent → "unavailable").
func TestTradeView_EndToEnd_RoundTripsThroughUIScreenDecoder(t *testing.T) {
	_, transport, _, cancel := wireTradeTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uitrade.ViewSubscriptionName)

	scr := uitrade.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-trade")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: delta.Patch})

	port, have := scr.Port()
	if !have {
		t.Fatal("ui.screen.trade Port() reported have=false after ApplyDelta — the published patch did not carry a port field the UI decoder recognised")
	}
	if !port.Unlocked || port.Berths != 2 {
		t.Fatalf("Port() = %+v, want unlocked=true berths=2 — the wire schema did not round-trip through ui.screen.trade's decoder", port)
	}

	if _, have := scr.Balance(); !have {
		t.Fatal("ui.screen.trade Balance() reported have=false after ApplyDelta — the published patch did not carry a balance field")
	}
	if _, have := scr.Warehouse(); !have {
		t.Fatal("ui.screen.trade Warehouse() reported have=false after ApplyDelta — the published patch did not carry a warehouse field")
	}

	if _, have := scr.Contracts(); have {
		t.Fatal("ui.screen.trade Contracts() reported have=true, want false — the patch must omit contracts (freight has no contract surface), which the screen decodes as unavailable")
	}
	if _, have := scr.Junctions(); have {
		t.Fatal("ui.screen.trade Junctions() reported have=true, want false — the patch must omit junctions")
	}
	if _, have := scr.Safety(); have {
		t.Fatal("ui.screen.trade Safety() reported have=true, want false — the patch must omit safety (BLOCKED on the BUG-058 registry edge)")
	}
}
