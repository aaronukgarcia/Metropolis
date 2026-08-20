package policies

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ---------------------------------------------------------------------------
// Enact atomicity (GR#1/GR#12) — a recoverable failure must never leave the
// treasury debited or a projection decision enqueued, and a retry must never
// double-debit.
// ---------------------------------------------------------------------------

// TestEnactNoDebitWhenProjectionsUnwired proves the blocking defect is fixed:
// with finance wired but projections unwired, Enact fails with
// ErrProjectionsNotWired BEFORE debiting the treasury (the old ordering
// posted the enactment cost first), and a retry after wiring projections
// debits exactly once.
func TestEnactNoDebitWhenProjectionsUnwired(t *testing.T) {
	a := testAPI(t)
	fin := &recordingFinance{}
	a.finance = fin
	// projections intentionally left nil.

	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Cost = CostDef{EnactmentMicroPounds: 5_000_000}
	addPolicy(t, a, def)

	_, err := a.Enact("freeport", Scope{Kind: ScopeCitywide})
	if err == nil {
		t.Fatal("Enact with unwired projections must fail")
	}
	if !errors.Is(err, &errs.E{Code: ErrProjectionsNotWired}) {
		t.Fatalf("want ErrProjectionsNotWired, got %v", err)
	}
	if got := fin.debitTotal(); got != 0 {
		t.Fatalf("failed enact must NOT debit the treasury, got %d", got)
	}
	if got := fin.postCount(); got != 0 {
		t.Fatalf("failed enact must NOT post any transaction, got %d", got)
	}

	// Wire projections and retry: exactly one debit, never a double-debit.
	a.projections = &recordingProjections{horizon: 72}
	if _, err := a.Enact("freeport", Scope{Kind: ScopeCitywide}); err != nil {
		t.Fatalf("retry after wiring projections: %v", err)
	}
	if got := fin.debitTotal(); got != finance.Money(5_000_000) {
		t.Fatalf("retry must debit exactly once, got %d", got)
	}
}

// TestEnactNoDebitOrDecisionWhenTaxUnwired proves the orphaned-decision half
// of the defect is fixed: with finance and projections wired but tax unwired,
// a tax-carrying policy fails with ErrTaxNotWired BEFORE debiting the
// treasury OR enqueueing any projection decision (the old ordering debited
// and then orphaned the decision when the tax route failed).
func TestEnactNoDebitOrDecisionWhenTaxUnwired(t *testing.T) {
	a := testAPI(t)
	fin := &recordingFinance{}
	rec := &recordingProjections{horizon: 72}
	a.finance = fin
	a.projections = rec
	// tax intentionally left nil.

	def := &policyDef{
		ID:        "freeport",
		Name:      "Tax-Free Harbour",
		Category:  "economy",
		Scope:     ScopeDistrict,
		Mechanism: []CoefficientDelta{{Key: "tax.businessRates.districtMultiplier", Delta: -1.0, Tax: &TaxMove{Instrument: "business-rates", Mode: taxMoveDistrictMultiplier}}},
		Cost:      CostDef{EnactmentMicroPounds: 5_000_000},
	}
	addPolicy(t, a, def)

	districtID, err := a.CreateDistrict("Harbour", cells(1))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}

	_, err = a.Enact("freeport", Scope{Kind: ScopeDistrict, District: districtID})
	if err == nil {
		t.Fatal("Enact with unwired tax must fail")
	}
	if !errors.Is(err, &errs.E{Code: ErrTaxNotWired}) {
		t.Fatalf("want ErrTaxNotWired, got %v", err)
	}
	if got := fin.debitTotal(); got != 0 {
		t.Fatalf("failed enact must NOT debit the treasury, got %d", got)
	}
	if got := rec.deltasWithPrefix("enactment-"); len(got) != 0 {
		t.Fatalf("failed enact must NOT enqueue a projection decision, got %+v", got)
	}
	if got := len(a.CoefficientState()); got != 0 {
		t.Fatalf("failed enact must leave no active coefficients, got %d", got)
	}

	// Retry after wiring tax: exactly one debit and one permanent decision.
	a.tax = &recordingTax{}
	if _, err := a.Enact("freeport", Scope{Kind: ScopeDistrict, District: districtID}); err != nil {
		t.Fatalf("retry after wiring tax: %v", err)
	}
	if got := fin.debitTotal(); got != finance.Money(5_000_000) {
		t.Fatalf("retry must debit exactly once, got %d", got)
	}
	if got := rec.deltasWithPrefix("enactment-"); len(got) != 1 {
		t.Fatalf("retry must enqueue exactly one permanent decision, got %d", len(got))
	}
}

// TestEnactNoDecisionWhenFinanceUnwired proves the finance half of the
// preflight: with projections wired but finance unwired, a cost-carrying
// policy fails with ErrFinanceNotWired BEFORE enqueueing any projection
// decision (so no orphaned decision outlives the failed enact).
func TestEnactNoDecisionWhenFinanceUnwired(t *testing.T) {
	a := testAPI(t)
	rec := &recordingProjections{horizon: 72}
	a.projections = rec
	// finance intentionally left nil.

	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Cost = CostDef{EnactmentMicroPounds: 5_000_000}
	addPolicy(t, a, def)

	_, err := a.Enact("freeport", Scope{Kind: ScopeCitywide})
	if err == nil {
		t.Fatal("Enact with unwired finance must fail")
	}
	if !errors.Is(err, &errs.E{Code: ErrFinanceNotWired}) {
		t.Fatalf("want ErrFinanceNotWired, got %v", err)
	}
	if got := rec.deltasWithPrefix("enactment-"); len(got) != 0 {
		t.Fatalf("failed enact must NOT enqueue a projection decision, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// AdvanceMonth atomicity — recurring opex must not post before a due
// checkpoint's dependency is known to be satisfiable.
// ---------------------------------------------------------------------------

// TestAdvanceMonthNoOpexWhenCheckpointProjectionsUnwired proves the
// AdvanceMonth recurrence of the defect is fixed: at a checkpoint month with
// projections unwired, the recurring opex must NOT post (the old ordering
// posted opex first and then failed the checkpoint with
// ErrProjectionsNotWired).
func TestAdvanceMonthNoOpexWhenCheckpointProjectionsUnwired(t *testing.T) {
	a := testAPI(t)
	fin := &recordingFinance{}
	a.finance = fin
	a.projections = &recordingProjections{horizon: 72}

	def := simplePolicy("freeport", ScopeCitywide, "tax.businessRates", -1.0)
	def.Cost = CostDef{OpexMonthlyMicroPounds: 50_000}
	addPolicy(t, a, def)
	mustEnact(t, a, "freeport", Scope{Kind: ScopeCitywide})

	// Unwire projections, then advance to a checkpoint month (3 % 3 == 0).
	a.projections = nil
	before := fin.postCount()

	_, err := a.AdvanceMonth(3)
	if err == nil {
		t.Fatal("AdvanceMonth at a checkpoint month with unwired projections must fail")
	}
	if !errors.Is(err, &errs.E{Code: ErrProjectionsNotWired}) {
		t.Fatalf("want ErrProjectionsNotWired, got %v", err)
	}
	if got := fin.postCount(); got != before {
		t.Fatalf("no opex must post when the checkpoint preflight fails: before=%d after=%d", before, got)
	}
}

// ---------------------------------------------------------------------------
// Input-validation error codes (secondary) — every defensive input rejection
// carries a code from the module's registered range (G4000-G4012), matched by
// meaning: G4003 (unknown/malformed scope) for malformed or empty-resolving
// inputs, G4004 (unknown district) for invalid district identity, G4005
// (unknown road) for invalid road identity.
// ---------------------------------------------------------------------------

func roadEdges() []EdgeRef {
	return []EdgeRef{{
		From: CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 1, Col: 0}},
		To:   CellRef{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 2, Col: 0}},
	}}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", code)
	}
	if !errors.Is(err, &errs.E{Code: code}) {
		t.Fatalf("want error %s, got %v", code, err)
	}
}

func TestInputValidationErrorCodes(t *testing.T) {
	t.Run("month regression", func(t *testing.T) {
		a := testAPI(t)
		a.currentMonth = 5
		_, err := a.AdvanceMonth(3)
		assertCode(t, err, ErrMonthRegression)
	})

	t.Run("checkpoint precedes current month", func(t *testing.T) {
		a := testAPI(t)
		a.currentMonth = 5
		_, err := a.Checkpoint(3)
		assertCode(t, err, ErrCheckpointPrecedes)
	})

	t.Run("inverted preview range", func(t *testing.T) {
		a := testAPI(t)
		a.projections = &recordingProjections{horizon: 72}
		a.currentMonth = 10
		addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))
		_, err := a.PreviewImpactRange("cycling", Scope{Kind: ScopeCitywide}, 5)
		assertCode(t, err, ErrUnknownScope)
	})

	t.Run("empty district name", func(t *testing.T) {
		a := testAPI(t)
		_, err := a.CreateDistrict("", cells(1))
		assertCode(t, err, ErrUnknownDistrict)
	})

	t.Run("no district cells", func(t *testing.T) {
		a := testAPI(t)
		_, err := a.CreateDistrict("CBD", nil)
		assertCode(t, err, ErrUnknownScope)
	})

	t.Run("empty road id", func(t *testing.T) {
		a := testAPI(t)
		err := a.RegisterRoad("", roadEdges())
		assertCode(t, err, ErrUnknownRoad)
	})

	t.Run("no road edges", func(t *testing.T) {
		a := testAPI(t)
		err := a.RegisterRoad("high.street", nil)
		assertCode(t, err, ErrUnknownScope)
	})

	t.Run("road already registered", func(t *testing.T) {
		a := testAPI(t)
		if err := a.RegisterRoad("high.street", roadEdges()); err != nil {
			t.Fatalf("first RegisterRoad: %v", err)
		}
		err := a.RegisterRoad("high.street", roadEdges())
		assertCode(t, err, ErrUnknownRoad)
	})

	t.Run("empty rename name", func(t *testing.T) {
		a := testAPI(t)
		id, err := a.CreateDistrict("CBD", cells(1))
		if err != nil {
			t.Fatalf("CreateDistrict: %v", err)
		}
		err = a.RenameDistrict(id, "")
		assertCode(t, err, ErrUnknownDistrict)
	})
}
