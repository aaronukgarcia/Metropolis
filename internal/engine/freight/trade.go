package freight

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Price returns a freight commodity's per-unit value in micropounds (M0-ENG
// §1.2), derived from the commodity's mapped engine.market commodity through
// the registered MarketAPI.Price edge (AC-8) — never a freight-owned
// hardcoded price. For a tonne commodity the result is £/tonne
// (marketPrice × unitsPerTonne); for a kWh/L commodity it is £/market-unit
// (the freight unit is the market unit). Errors with ErrUnknownCommodity for
// an unregistered freight commodity.
func (f *FreightAPI) Price(commodity Commodity) (int64, error) {
	if err := f.checkNotCopied("Price"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return 0, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	unitPrice, err := f.market.Price(cc.Market)
	if err != nil {
		return 0, err
	}
	return safeMulTonnes(int64(unitPrice), cc.UnitsPerTonne), nil
}

// Availability bounds a requested quantity of a freight commodity by its
// mapped market commodity's configured import-capacity ceiling (AC-8), read
// through the registered MarketAPI.Availability edge. The result is returned
// in the freight commodity's own unit (tonnes for tonne commodities, market
// units otherwise). Errors with ErrUnknownCommodity for an unregistered
// freight commodity.
func (f *FreightAPI) Availability(commodity Commodity, quantity int64) (int64, error) {
	if err := f.checkNotCopied("Availability"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return 0, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	requestedUnits := safeMulTonnes(quantity, cc.UnitsPerTonne)
	avail, err := f.market.Availability(cc.Market, requestedUnits)
	if err != nil {
		return 0, err
	}
	return avail.Available / cc.UnitsPerTonne, nil
}

// CommodityTrade is one commodity's per-day trade figure (AC-9): its tonnage
// and its value in micropounds, both sourced from the same ledger entry.
type CommodityTrade struct {
	Commodity        Commodity
	Tonnes           int64
	ValueMicropounds int64
}

// TradeLedger is a per-commodity import or export rollup (AC-9): each
// commodity's independently-tracked tonnage and value, plus deterministic
// (sorted-commodity) totals.
type TradeLedger struct {
	ByCommodity           map[Commodity]CommodityTrade
	TotalTonnes           int64
	TotalValueMicropounds int64
}

// BalanceOfTrade is the import/export breakdown the F5 trade screen reads
// (AC-9): two INDEPENDENTLY-sourced ledgers — Imports from import movements
// (MarketAPI-priced), Exports from the port departure ledger's own tracked
// departures — never one computed as the other's signed complement.
type BalanceOfTrade struct {
	Imports TradeLedger
	Exports TradeLedger
}

// Imports returns the current tick's import ledger (AC-9): tonnage and
// value per commodity, sourced from import movements alone.
func (f *FreightAPI) Imports() TradeLedger {
	if err := f.checkNotCopied("Imports"); err != nil {
		return TradeLedger{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ledgerLocked(f.imported)
}

// Exports returns the current tick's export/departure ledger (AC-9):
// tonnage and value per commodity, sourced from the departure ledger's own
// tracked departures alone — never derived from imports.
func (f *FreightAPI) Exports() TradeLedger {
	if err := f.checkNotCopied("Exports"); err != nil {
		return TradeLedger{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ledgerLocked(f.exported)
}

// BalanceOfTrade returns both independently-sourced ledgers in one call.
func (f *FreightAPI) BalanceOfTrade() BalanceOfTrade {
	if err := f.checkNotCopied("BalanceOfTrade"); err != nil {
		return BalanceOfTrade{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return BalanceOfTrade{
		Imports: f.ledgerLocked(f.imported),
		Exports: f.ledgerLocked(f.exported),
	}
}

// ledgerLocked rolls a per-commodity tonnage map into a TradeLedger with
// market-priced values. The caller holds f.mu (at least RLock).
func (f *FreightAPI) ledgerLocked(tonnage map[Commodity]int64) TradeLedger {
	ledger := TradeLedger{ByCommodity: make(map[Commodity]CommodityTrade, len(tonnage))}
	keys := make([]Commodity, 0, len(tonnage))
	for c := range tonnage {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, c := range keys {
		t := tonnage[c]
		value := f.valueLocked(c, t)
		ledger.ByCommodity[c] = CommodityTrade{Commodity: c, Tonnes: t, ValueMicropounds: value}
		ledger.TotalTonnes = num.SatAdd(ledger.TotalTonnes, t)
		ledger.TotalValueMicropounds = num.SatAdd(ledger.TotalValueMicropounds, value)
	}
	return ledger
}

// valueLocked computes a tonnage's value in micropounds using the mapped
// market commodity's static price (the registered edge, AC-8). The caller
// holds f.mu. MarketAPI has no separate non-waste export price, so the
// static market price is the value basis for both import and export —
// documented in doc.go.
func (f *FreightAPI) valueLocked(c Commodity, tonnes int64) int64 {
	cc := f.cfg.commodities[c]
	unitPrice, err := f.market.Price(cc.Market)
	if err != nil {
		return 0
	}
	perUnit := safeMulTonnes(int64(unitPrice), cc.UnitsPerTonne)
	return safeMulTonnes(perUnit, tonnes)
}
