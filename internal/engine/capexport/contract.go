package capexport

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// ContractID is the stable identity of one issued export contract, assigned
// monotonically by CapExportAPI starting at 1 (0 is the "no contract"
// sentinel) — mirroring engine.finance's TxID.
type ContractID uint64

// Contract is a signed capacity-export commitment (US-2, AC-4): a term
// (duration in months), a per-unit monthly rate in micro-pounds, and a
// documented cancellation-penalty FUNCTION — never a boolean "exporting"
// toggle. It is the durable, queryable record [CapExportAPI.IssueContract]
// produces, independently retrievable via [CapExportAPI.Contract] /
// [CapExportAPI.Contracts].
type Contract struct {
	ID              ContractID
	Line            ExportableService
	ServiceID       services.ServiceID // the bound engine.services instance backing the line
	Quantity        float64            // units sold (e.g. tonnes, bed-days)
	TermMonths      int64              // contract duration in simulation months
	RateMicropounds int64              // £/unit/month, micro-pounds (1 pound sterling = 1,000,000)
	IssuedMonth     int64              // simulation month the contract was signed
	AccruedMonths   int64              // months of revenue already accrued (SEC-193 idempotency cursor)
	Cancelled       bool
}

// RemainingMonths returns the contract's unexpired term at currentMonth,
// clamped to [0, TermMonths]. A currentMonth before IssuedMonth (defensive —
// should not occur) reads as the full term.
func (c Contract) RemainingMonths(currentMonth int64) int64 {
	remaining := c.TermMonths - (currentMonth - c.IssuedMonth)
	if remaining < 0 {
		return 0
	}
	if remaining > c.TermMonths {
		return c.TermMonths
	}
	return remaining
}

// CancellationPenalty is the documented cancellation-penalty FUNCTION
// (AC-4): penalty = remainingTermMonths × RateMicropounds × Quantity — the
// full remaining contract revenue forfeited on early cancellation. It is a
// real function of remaining term, not a flat toggle: cancelling before
// term-end (remaining > 0) yields a strictly positive penalty, cancelling
// at/after term-end (remaining == 0) yields exactly zero. The intermediate
// product is overflow-checked via foundation/num and REJECTED, never silently
// saturated (SEC-192, GR#16): a remaining × rate product that overflows int64,
// or a ×Quantity product outside the int64 range, returns
// ErrInvalidContractInput rather than a fabricated MaxInt64 penalty. The
// placeholder rate magnitudes are ASM-309's (Aaron's balance call, not
// spec-fixed).
func (c Contract) CancellationPenalty(currentMonth int64) (finance.Money, error) {
	remaining := c.RemainingMonths(currentMonth)
	perUnit, overflow := num.SafeMul(c.RateMicropounds, remaining)
	if overflow {
		return 0, errs.New(ErrInvalidContractInput, errs.NewCorrelationID(), map[string]any{
			"field":  "penalty",
			"reason": "rate × remaining-months overflow",
		})
	}
	amountV, err := num.SafeInt64(float64(perUnit) * c.Quantity)
	if err != nil {
		return 0, errs.Wrap(ErrInvalidContractInput, errs.NewCorrelationID(), err, map[string]any{"field": "penalty"})
	}
	return finance.Money(amountV), nil
}

// IssueRequest is the input to [CapExportAPI.IssueContract] (AC-4's "issuing
// a contract is a command"): the line to sell, the quantity, the term, and the
// per-unit rate. A zero RateMicropounds means "use the catalogue's default
// rate" (the player sells at the data-driven rate); a negative rate is
// rejected, never silently clamped.
type IssueRequest struct {
	Line            ExportableService
	Quantity        float64 // units sold; must be positive and finite
	TermMonths      int64   // duration; must be positive
	RateMicropounds int64   // £/unit/month; 0 => catalogue default
}

// SurplusBook is the per-service surplus book US-1/AC-1 names (capacity −
// internal demand), plus the exportable slack actually available once
// committed contracts are honoured. Capacity and Demand are sourced live from
// engine.services' ServicesAPI through the bound ServiceID (GR#20) — never
// re-derived locally.
type SurplusBook struct {
	Line      ExportableService
	Capacity  float64 // ServicesAPI.Capacity of the bound instance
	Demand    float64 // ServicesAPI.Demand of the bound instance (internal demand)
	Committed float64 // sold quantity already under active contracts
	Surplus   float64 // Capacity − Demand (US-1's formula)
	Available float64 // Surplus − Committed, the exportable slack (clamped ≥ 0)
}

// CrossingState is the crossing query's answer (AC-2/AC-3): whether internal
// demand has grown past the capacity left for citizens after honouring
// committed contracts, and by how much (the shortfall).
type CrossingState struct {
	Line      ExportableService
	Crossing  bool
	Shortfall float64 // Demand − (Capacity − Committed), > 0 exactly when Crossing
	Capacity  float64
	Committed float64
	Demand    float64
	Headroom  float64 // Capacity − Committed, the capacity left for internal demand
}

// ServiceCut is the durable record CutInternalService produces (AC-3's
// "cut the service" path): the line, the shortfall the citizens lost, and the
// month the decision was taken. It is queryable via [CapExportAPI.Cut].
type ServiceCut struct {
	Line      ExportableService
	Shortfall float64
	Month     int64
}

// Cancellation is the result of PayCancellationPenalty (AC-3/AC-7): the
// cancelled contract, the penalty posted (zero at/after term-end), and the
// finance transaction id (zero when no penalty was posted).
type Cancellation struct {
	Contract Contract
	Penalty  finance.Money
	TxID     finance.TxID
}
