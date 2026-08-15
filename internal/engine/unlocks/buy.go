package unlocks

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// OffMapKind names one of the five off-map capacity kinds §22's Buy path
// covers: grid tranches, gas pipeline tranches, external rail access,
// port permits, and water bulk-supply contracts (US-5, AC-9).
type OffMapKind string

const (
	OffMapGrid  OffMapKind = "grid"
	OffMapGas   OffMapKind = "gas"
	OffMapRail  OffMapKind = "rail"
	OffMapPort  OffMapKind = "port"
	OffMapWater OffMapKind = "water"
)

// offMapBuyPrices is the per-tranche money price for each off-map kind.
// PLACEHOLDER v1 shape — no §22 figure exists (see placeholders.go and
// the logged ASM). Map lookup only (never iterated on a path whose
// result matters), so map order is irrelevant to determinism.
var offMapBuyPrices = map[OffMapKind]finance.Money{
	OffMapGrid:  50_000 * finance.MicropoundsPerPound,
	OffMapGas:   50_000 * finance.MicropoundsPerPound,
	OffMapRail:  100_000 * finance.MicropoundsPerPound,
	OffMapPort:  200_000 * finance.MicropoundsPerPound,
	OffMapWater: 50_000 * finance.MicropoundsPerPound,
}

// BuyOffMapCapacity purchases one tranche of off-map capacity with money
// (AC-9, US-5). It is the money-only path §22 names: it debits money
// through engine.finance and grants capacity directly, and it does NOT
// check or consume Development Points and does NOT check the DP-tree gate
// for the equivalent infrastructure — the Buy path is genuinely
// independent of the points economy.
func (u *UnlocksAPI) BuyOffMapCapacity(kind OffMapKind, correlationID string) error {
	if err := u.checkNotCopied("BuyOffMapCapacity"); err != nil {
		return err
	}
	price, ok := offMapBuyPrices[kind]
	if !ok {
		return errs.New(ErrUnknownOffMapKind, u.correlationID, map[string]any{
			"kind": string(kind),
		})
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if u.finance == nil {
		return errs.New(ErrFinanceNotWired, u.correlationID, map[string]any{
			"operation": "BuyOffMapCapacity",
		})
	}

	// Debit the treasury, credit the outside world — money out for the
	// purchased capacity, always balanced. A treasury overdraft is
	// rejected by finance.Post (its ErrInsufficientFunds), surfaced here
	// unchanged.
	if _, err := u.finance.Post(finance.Transaction{
		Description: "off-map capacity purchase (" + string(kind) + ")",
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: price, Category: categoryCapacityBuy},
			{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: price, Category: categoryCapacityBuy},
		},
	}); err != nil {
		return err
	}

	u.capacity[kind]++
	return nil
}

// OffMapCapacity returns the number of tranches of the given off-map kind
// purchased via BuyOffMapCapacity. Returns ErrUnknownOffMapKind for a
// kind outside the five §22 names.
func (u *UnlocksAPI) OffMapCapacity(kind OffMapKind) (int64, error) {
	if err := u.checkNotCopied("OffMapCapacity"); err != nil {
		return 0, err
	}
	if _, ok := offMapBuyPrices[kind]; !ok {
		return 0, errs.New(ErrUnknownOffMapKind, u.correlationID, map[string]any{
			"kind": string(kind),
		})
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.capacity[kind], nil
}
