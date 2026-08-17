package fiscal

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FiscalAPI is code.json's "engine.fiscal" inbound contract (FiscalAPI,
// "Sankey topology provider; every band drill-through to ledger"): the §54
// whole-economy Sankey view, the tax breakdown, the civil-service gross/net
// pair, the municipality-quality functions, the childcare net line, and the
// benefits/social-housing postings. It consumes engine.finance and
// engine.tax through their registered inbound contracts alone (GR#20).
//
// The zero value is not usable; construct via [New] or [Load]. A *FiscalAPI
// is safe for concurrent use (AC-13): every mutable field is guarded by mu,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class, mirroring engine.finance/engine.tax).
type FiscalAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           Config

	// Dependencies, wired via SetFinance/SetTax and read under mu. Both are
	// concrete types (matching engine.tax → engine.finance and
	// engine.services → engine.finance), consumed only through their
	// registered inbound contracts (GR#20).
	finance *finance.FinanceAPI
	tax     *tax.TaxAPI

	// Documented state fields (inputs pushed by the composition root), not
	// running money totals. The gross civil-service wage bill is summed from
	// engine.services at the composition root (this package holds no running
	// accumulator — GR#3/AC-9).
	civilServiceWageBill finance.Money
	planningFunding      float64 // 0..1 fraction of the data target level
	childcarePlaces      int64   // number of subsidised places

	// self is the SEC-020 copy guard (atomic.Pointer), stored exactly once in
	// New before the value is returned to any caller.
	self atomic.Pointer[FiscalAPI]
}

// New constructs a ready-to-wire FiscalAPI from a validated Config.
// correlationID is attached to every error the returned API constructs
// (GR#1); an empty one mints a fresh ID. Dependencies are wired later via
// SetFinance/SetTax.
func New(cfg Config, correlationID string) (*FiscalAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrFiscalDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	f := &FiscalAPI{
		correlationID: correlationID,
		cfg:           cfg,
	}
	// Stored exactly once, before f is returned to any caller.
	f.self.Store(f)
	return f, nil
}

// checkNotCopied rejects a method call on a struct-copied *FiscalAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (f *FiscalAPI) checkNotCopied(method string) error {
	if f.self.Load() != f {
		return errs.New(ErrCopiedValue, f.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetFinance wires the engine.finance dependency used by the Sankey topology
// money-in/money-out aggregation, the drill-through and the welfare postings
// (the registered engine.fiscal → engine.finance edge, GR#20).
func (f *FiscalAPI) SetFinance(fin *finance.FinanceAPI) error {
	if err := f.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finance = fin
	return nil
}

// SetTax wires the engine.tax dependency used by the tax breakdown, the
// civil-service clawback and the childcare tax yield (the registered
// engine.fiscal → engine.tax edge, GR#20).
func (f *FiscalAPI) SetTax(t *tax.TaxAPI) error {
	if err := f.checkNotCopied("SetTax"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tax = t
	return nil
}

// financeLocked returns the wired *finance.FinanceAPI and whether it is
// present (the caller holds f.mu at least read-locked).
func (f *FiscalAPI) financeLocked() (*finance.FinanceAPI, bool) {
	if err := f.checkNotCopied("financeLocked"); err != nil {
		return nil, false
	}
	return f.finance, f.finance != nil
}

// taxLocked returns the wired *tax.TaxAPI and whether it is present (the
// caller holds f.mu at least read-locked).
func (f *FiscalAPI) taxLocked() (*tax.TaxAPI, bool) {
	if err := f.checkNotCopied("taxLocked"); err != nil {
		return nil, false
	}
	return f.tax, f.tax != nil
}

// requireFinance returns the wired finance dependency or a registry-sourced
// ErrDependencyMissing naming the operation (GR#17 — fail closed, never
// fabricate a figure on an unwired dependency).
func (f *FiscalAPI) requireFinance(op string) (*finance.FinanceAPI, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	fin, ok := f.financeLocked()
	if !ok {
		return nil, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{"operation": op, "dependency": "engine.finance"})
	}
	return fin, nil
}

// requireTax returns the wired tax dependency or a registry-sourced
// ErrDependencyMissing naming the operation.
func (f *FiscalAPI) requireTax(op string) (*tax.TaxAPI, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.taxLocked()
	if !ok {
		return nil, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{"operation": op, "dependency": "engine.tax"})
	}
	return t, nil
}

// satAdd adds two money values with int64 saturation (GR#16) so a
// cross-node sum can never wrap. Thin adapter over foundation/num's
// canonical SatAddChecked (reuse-first — the overflow predicate lives in num).
func satAdd(a, b finance.Money) (finance.Money, bool) {
	v, ok := num.SatAddChecked(int64(a), int64(b))
	return finance.Money(v), ok
}

// satSub subtracts two money values with int64 saturation (GR#16). Thin
// adapter over foundation/num's canonical SatSub.
func satSub(a, b finance.Money) finance.Money {
	return finance.Money(num.SatSub(int64(a), int64(b)))
}

// moneyTimesRate applies a percentage rate (e.g. 20.0 = 20%) to a money
// amount in exact int64 fixed-point: amount × round(rate×100) / 10000, the
// same basis-point shape engine.services' NetFiscalCost uses. The rate is
// first rounded to basis points so a float percentage never bleeds float
// rounding into an int64 money figure, and the intermediate product is
// overflow-checked (ErrFiscalOverflow) rather than allowed to wrap (GR#16,
// SEC-094 class).
func (f *FiscalAPI) moneyTimesRate(amount finance.Money, ratePercent float64) (finance.Money, error) {
	if err := f.checkNotCopied("moneyTimesRate"); err != nil {
		return 0, err
	}
	if !num.IsFinite(ratePercent) || ratePercent < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "rate", "value": ratePercent})
	}
	bp := int64(math.Round(ratePercent * 100))
	if bp == 0 {
		return 0, nil
	}
	p, overflow := num.SafeMul(int64(amount), bp)
	if overflow {
		return 0, errs.New(ErrFiscalOverflow, f.correlationID, map[string]any{
			"amount": int64(amount), "ratePercent": ratePercent,
		})
	}
	return finance.Money(p / basisPointScale), nil
}

// basisPointScale is the fixed-point denominator for moneyTimesRate (10000
// basis points = 100%). It is a deliberate duplication of engine.finance's
// unexported basisPointScale / engine.services' incomeTaxBasisPointScale:
// the value exists on both sides of the module boundary because finance does
// not export its scale, and the drift risk is held by a _test.go import of
// finance (weakness pattern #2 — see determinism_test.go).
const basisPointScale int64 = 10000
