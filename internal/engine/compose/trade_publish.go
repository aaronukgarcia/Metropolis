package compose

import (
	"encoding/json"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-017 (docs/planning/acceptance/ui.screen.trade.md): the seventh real
// UI delta-publishing vertical slice, "f5.trade" — the Trade & Logistics
// screen (F5). It publishes the three sub-surfaces engine.freight can
// actually back — the balance-of-trade breakdown (imports/exports by
// commodity), the port panel (berths/crane-rate/hours/customs/smuggling
// risk), and the warehouse stock view — all derived live from the composed
// engine.freight module (constructed in compose.go's Wire).
//
// This file mirrors census_publish.go / districts_publish.go /
// projections_publish.go / finance_publish.go / services_publish.go /
// viewport_publish.go's one-file-per-integration convention exactly and,
// per the FEAT-208 design's §3.3, builds compose's OWN copy of the wire
// schema — the same JSON tags as ui.screen.trade's wire.go, duplicated
// independently, NEVER importing internal/ui/screens/trade (GR#20's
// engine-never-imports-ui half of the seam).

// tradeWireSchemaVersion mirrors ui.screen.trade/wire.go's wireSchemaVersion
// constant VALUE (1), kept as a separate, independently maintained value per
// the same GR#20/SF-1 discipline the other six publish files' identical
// constants follow.
const tradeWireSchemaVersion = 1

// The wire structs below mirror ui.screen.trade/wire.go field for field
// (same JSON tags) for the THREE sub-surfaces this build actually emits:
// balance (imports/exports), port, and warehouse. The other three wire
// sub-surfaces — contracts, junctions, safety — are deliberately NOT
// declared here because engine.freight exposes no query surface backing
// them (see buildTradePatch's honest-scope note): the patch simply omits
// those fields, and ui.screen.trade's ApplyDelta treats an absent
// sub-surface as "unavailable" (SF-7/TRD-8), never blank and never stale.

// tradeWireTradeFlow mirrors ui.screen.trade/wire.go's wireTradeFlow.
type tradeWireTradeFlow struct {
	Key                    string `json:"key"`
	TonnesPerDay           int64  `json:"tonnesPerDay"`
	ValuePerDayMicropounds int64  `json:"valuePerDayMicropounds"`
}

// tradeWireLedger mirrors ui.screen.trade/wire.go's wireLedger.
type tradeWireLedger struct {
	ByCommodity []tradeWireTradeFlow `json:"byCommodity,omitempty"`
	ByArtery    []tradeWireTradeFlow `json:"byArtery,omitempty"`
}

// tradeWireBalance mirrors ui.screen.trade/wire.go's wireBalance.
type tradeWireBalance struct {
	Imports *tradeWireLedger `json:"imports,omitempty"`
	Exports *tradeWireLedger `json:"exports,omitempty"`
}

// tradeWireWarehouse mirrors ui.screen.trade/wire.go's wireWarehouse.
type tradeWireWarehouse struct {
	Commodity          string `json:"commodity"`
	StockTonnes        int64  `json:"stockTonnes"`
	CapacityTonnes     int64  `json:"capacityTonnes"`
	BufferTonnesPerDay int64  `json:"bufferTonnesPerDay"`
	FlowTonnesPerDay   int64  `json:"flowTonnesPerDay"`
}

// tradeWirePort mirrors ui.screen.trade/wire.go's wirePort.
type tradeWirePort struct {
	Unlocked                      bool    `json:"unlocked"`
	Berths                        int64   `json:"berths"`
	CraneRateTonnesPerHour        int64   `json:"craneRateTonnesPerHour"`
	OperatingHoursPerDay          int64   `json:"operatingHoursPerDay"`
	CustomsThroughputTonnesPerDay int64   `json:"customsThroughputTonnesPerDay"`
	SmugglingRisk                 float64 `json:"smugglingRisk"`
}

// tradeWirePatch is compose's own copy of ui.screen.trade/wire.go's
// wirePatch, restricted to the three sub-surfaces this build emits.
type tradeWirePatch struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Warehouse     *[]tradeWireWarehouse `json:"warehouse,omitempty"`
	Port          *tradeWirePort        `json:"port,omitempty"`
	Balance       *tradeWireBalance     `json:"balance,omitempty"`
}

// buildTradePatch reads the composed engine.freight module's live query
// surface and returns the "f5.trade" patch. It runs on the subscription
// pump goroutine (subscribe.go's ViewPatchFunc contract), CONCURRENTLY with
// the phase pipeline — safe only because every read goes through
// FreightAPI's own synchronization (each accessor takes f.mu.RLock
// internally) and because freight is composed here as a pure QUERY surface:
// no phase hook ticks it, so nothing mutates it concurrently with this
// read (see the simState.freight field doc comment in compose.go).
//
// # Honest scope (what engine.freight backs, and what it does not)
//
//   - balance (imports/exports): ALWAYS emitted, non-nil, from
//     FreightAPI.BalanceOfTrade() — the independently-sourced per-commodity
//     import/export ledgers. At a fresh seed BOTH are empty (nothing
//     imports/exports before gameplay), so byCommodity carries zero rows;
//     the surface is present-but-empty, which the screen renders as its
//     "imports"/"exports" sub-labels with no rows rather than "unavailable".
//   - port: ALWAYS emitted, non-nil, from FreightAPI.PortCapacity() +
//     CustomsCapacity() + SmugglingRisk(). data/freight.json seeds 2 berths,
//     so this is the ONE sub-surface with real, non-empty figures at a fresh
//     boot (berths 2, crane 60t/hr, 16 operating hours, 1500t/day customs,
//     smuggling risk 0 at zero customs demand). The wire `unlocked` flag is
//     mapped to freight's own "is the port built" signal (berths > 0) —
//     engine.unlocks is NOT composed, and freight has no tier-unlock state,
//     so this is the closest honest signal; it renders "not yet unlocked"
//     only if data/freight.json ever ships berths: 0.
//   - warehouse: ALWAYS emitted, non-nil, from FreightAPI.StorageSites() —
//     one row per commodity currently holding stock, carrying that
//     commodity's storage-class site capacity. At a fresh seed it is empty
//     (no production has run, no import has landed), so the screen renders
//     "no commodities" rather than "unavailable". BufferTonnesPerDay and
//     FlowTonnesPerDay are ZERO because engine.freight has no buffer-policy
//     state and no per-commodity warehouse-flow ledger — genuinely unbacked,
//     not fabricated (TRD-3's buffer control is ASM-251-flagged and the
//     set-buffer command rides the Debug seam; both are out of this
//     publish-side scope).
//   - contracts / junctions / safety: OMITTED entirely (nil) — engine.freight
//     exposes no import-contract roster, no junction-queue view (that is
//     engine.logistics/engine.traffic's full scheduler, not built at
//     baseline-one depth), and no §50 pipeline-vs-truck corridor (BLOCKED on
//     the BUG-058 registry edge, per ui.screen.trade/doc.go). The screen
//     renders each omitted surface as "unavailable", which is the honest
//     no-data state — never fabricated rows.
func (st *simState) buildTradePatch() (json.RawMessage, error) {
	bot := st.freight.BalanceOfTrade()
	imports := ledgerToWire(bot.Imports)
	exports := ledgerToWire(bot.Exports)

	port, err := buildTradePort(st.freight, st.cid)
	if err != nil {
		return nil, err
	}

	warehouse := buildTradeWarehouse(st.freight)

	patch := tradeWirePatch{
		SchemaVersion: tradeWireSchemaVersion,
		Warehouse:     warehouse,
		Port:          port,
		Balance: &tradeWireBalance{
			Imports: &tradeWireLedger{ByCommodity: imports},
			Exports: &tradeWireLedger{ByCommodity: exports},
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of ints/strings/bools/floats cannot
		// fail; unreachable in practice — mirrored on the other six publish
		// files' identical "cannot fail" branches. Per GR#1, degrade loudly
		// rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "freight", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// ledgerToWire folds freight's TradeLedger (per-commodity map) into the
// wire's deterministic, commodity-sorted flow slice. It drops
// TradeLedger.TotalTonnes/TotalValueMicropounds (the screen does not carry
// totals) and leaves ByArtery empty: engine.freight's trade ledger has NO
// per-artery (road/rail/sea) rollup — the movement Mode is tracked per
// movement, never aggregated into the import/export ledger — so byArtery is
// genuinely empty, not omitted-by-accident.
func ledgerToWire(l freight.TradeLedger) []tradeWireTradeFlow {
	keys := make([]string, 0, len(l.ByCommodity))
	for c := range l.ByCommodity {
		keys = append(keys, string(c))
	}
	sort.Strings(keys)

	out := make([]tradeWireTradeFlow, 0, len(keys))
	for _, k := range keys {
		ct := l.ByCommodity[freight.Commodity(k)]
		out = append(out, tradeWireTradeFlow{
			Key:                    k,
			TonnesPerDay:           ct.Tonnes,
			ValuePerDayMicropounds: ct.ValueMicropounds,
		})
	}
	return out
}

// buildTradePort resolves the port sub-surface from FreightAPI's own
// capacity/customs/smuggling model (GR#3 — never a reimplementation of the
// §33 formulas). When the port is built (berths > 0) it emits the full
// figures; when berths == 0 (PortCapacity's ErrNoBerthsConfigured, the only
// error that accessor returns) it emits the surface present-but-locked so
// the screen renders "not yet unlocked" rather than "unavailable".
func buildTradePort(f *freight.FreightAPI, cid string) (*tradeWirePort, error) {
	cap, err := f.PortCapacity()
	if err != nil {
		// The only failure mode PortCapacity has is ErrNoBerthsConfigured
		// (berths == 0, "port not yet built"). Emit present-but-locked.
		return &tradeWirePort{Unlocked: false}, nil
	}
	customs, err := f.CustomsCapacity()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "freight", "accessor": "CustomsCapacity"})
	}
	risk, err := f.SmugglingRisk()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "freight", "accessor": "SmugglingRisk"})
	}
	return &tradeWirePort{
		Unlocked:                      true,
		Berths:                        cap.Berths,
		CraneRateTonnesPerHour:        cap.CraneRateTonnesPerHour,
		OperatingHoursPerDay:          cap.OperatingHoursPerDay,
		CustomsThroughputTonnesPerDay: customs.TonnesPerDay,
		SmugglingRisk:                 risk,
	}, nil
}

// buildTradeWarehouse folds FreightAPI.StorageSites() into the wire's
// per-commodity warehouse rows. Each storage site carries a per-commodity
// stock map and one class-scoped capacity; because each freight commodity's
// storage class matches exactly one site type, a commodity appears in at
// most one site, so the flattening is one row per commodity. Rows are sorted
// by commodity for determinism (GR#21). BufferTonnesPerDay/FlowTonnesPerDay
// stay at their zero value: engine.freight tracks neither (see
// buildTradePatch's honest-scope note).
func buildTradeWarehouse(f *freight.FreightAPI) *[]tradeWireWarehouse {
	sites := f.StorageSites()
	rows := make([]tradeWireWarehouse, 0, len(sites))
	for _, site := range sites {
		for c, stock := range site.Stock {
			rows = append(rows, tradeWireWarehouse{
				Commodity:      string(c),
				StockTonnes:    stock,
				CapacityTonnes: site.CapacityTonnes,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Commodity < rows[j].Commodity })
	return &rows
}

// tradeViewSubscriptionName mirrors internal/ui/screens/trade/wire.go's
// ViewSubscriptionName constant VALUE ("f5.trade") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/trade (GR#20's engine-never-imports-ui half of the
// seam; this file's own doc comment). Kept as its own named constant for the
// same reason censusViewSubscriptionName / districtsViewSubscriptionName /
// projectionsViewSubscriptionName / financeViewSubscriptionName /
// servicesViewSubscriptionName / viewportViewSubscriptionName are.
const tradeViewSubscriptionName = "f5.trade"
