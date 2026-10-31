package compose

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/fiscal"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609835 (engine.fiscal wiring) tests: AC-2..AC-10 of
// docs/planning/acceptance/wire-engine.fiscal.md under the lead amendments.

const fiscalTestMonthTicks = int64(core.DailyTicksPerMonth)

// fiscalAdvanceMonths advances the composed engine by whole months.
func fiscalAdvanceMonths(t *testing.T, e *core.Engine, months int64) {
	t.Helper()
	if err := e.AdvanceTicks(errs.NewCorrelationID(), months*fiscalTestMonthTicks); err != nil {
		t.Fatalf("AdvanceTicks(%d months): %v", months, err)
	}
}

// fiscalPatch is the test-side decode of the published f2.finance patch.
type fiscalPatch struct {
	BalanceSheet  json.RawMessage           `json:"balanceSheet"`
	Sankey        *financeSankeyView        `json:"sankey"`
	PublicPayroll *financePublicPayrollView `json:"publicPayroll"`
}

func fiscalReadPatch(t *testing.T, comp *Composition) fiscalPatch {
	t.Helper()
	raw, err := comp.state.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}
	var p fiscalPatch
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	return p
}

// ledgerRecompute independently recomputes the Sankey inputs from raw ledger
// lines (no finance aggregate helper for the legs it can derive from lines).
type ledgerRecompute struct {
	taxIn, imports, interest int64
	byTax                    map[string]int64
}

func recomputeFromLines(fin *finance.FinanceAPI) ledgerRecompute {
	r := ledgerRecompute{byTax: map[string]int64{}}
	for _, e := range fin.Lines(finance.AcctTreasury) {
		cat := string(e.Category)
		switch {
		case e.Side == finance.SideCredit && (cat == "tax.income" || cat == "tax.sales" || cat == "tax.corp" || cat == "tax.council"):
			r.taxIn += int64(e.Amount)
			r.byTax[cat] += int64(e.Amount)
		case e.Side == finance.SideDebit && cat == string(finance.CatImports):
			r.imports += int64(e.Amount)
		case e.Side == finance.SideDebit && cat == string(finance.CatDebtService):
			r.interest += int64(e.Amount)
		}
	}
	return r
}

func nodeByID(v *financeSankeyView, id string) (financeSankeyNode, bool) {
	for _, n := range v.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return financeSankeyNode{}, false
}

// --- built-but-not-wired gate ---

func TestFiscal_WiredIntoRegistrationAndState(t *testing.T) {
	_, comp := newTestEngine(t, 11)
	found := false
	for _, n := range RegistrationOrder() {
		if n == "fiscal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RegistrationOrder() lacks fiscal: %v", RegistrationOrder())
	}
	if comp.state.fiscal == nil {
		t.Fatal("simState.fiscal is nil after Wire")
	}
	if got := comp.PlanningFunding(); got != fiscalDefaultPlanningFunding {
		t.Fatalf("default planning funding = %v, want %v", got, fiscalDefaultPlanningFunding)
	}
}

// --- AC-2: sankey reachable and ledger-true ---

func TestFiscal_AC2_SankeyEqualsIndependentLedgerRecompute(t *testing.T) {
	e, comp := newTestEngine(t, 21)
	var prevIn int64 = -1
	moved := false
	for m := 0; m < 6; m++ {
		fiscalAdvanceMonths(t, e, 1)
		p := fiscalReadPatch(t, comp)
		if p.Sankey == nil {
			t.Fatal("f2.finance patch carries no sankey sub-view")
		}
		if len(p.BalanceSheet) == 0 {
			t.Fatal("balanceSheet sub-view disappeared (must stay byte-compatible)")
		}
		r := recomputeFromLines(comp.state.finance)
		s := p.Sankey
		if s.MoneyIn != r.taxIn {
			t.Fatalf("month %d MoneyIn = %d, independent recompute = %d", m+1, s.MoneyIn, r.taxIn)
		}
		if s.MoneyOut != r.imports+r.interest {
			t.Fatalf("month %d MoneyOut = %d, recompute = %d", m+1, s.MoneyOut, r.imports+r.interest)
		}
		if s.BudgetBalance != int64(comp.state.finance.BudgetBalance()) {
			t.Fatalf("month %d BudgetBalance = %d, finance.BudgetBalance = %d", m+1, s.BudgetBalance, comp.state.finance.BudgetBalance())
		}
		if s.MoneyIn-s.MoneyOut-s.OtherOutflows != s.BudgetBalance {
			t.Fatalf("month %d figures do not add up: in %d out %d other %d bal %d", m+1, s.MoneyIn, s.MoneyOut, s.OtherOutflows, s.BudgetBalance)
		}
		// node by node
		for _, tc := range []struct {
			id   string
			want int64
		}{{"taxRevenue", r.taxIn}, {"imports", r.imports}, {"interest", r.interest}, {"budget", s.BudgetBalance}} {
			n, ok := nodeByID(s, tc.id)
			if !ok || n.Amount != tc.want {
				t.Fatalf("month %d node %s = %+v (found %v), want %d", m+1, tc.id, n, ok, tc.want)
			}
		}
		for _, id := range []string{"exports", "tourism", "outCommuterWages", "fdi", "grants", "leakage"} {
			n, ok := nodeByID(s, id)
			if !ok || !n.Provisional || n.Amount != 0 {
				t.Fatalf("provisional node %s = %+v (found %v), want provisional with amount 0", id, n, ok)
			}
		}
		// per-type tax equals the independent per-category recompute (A1)
		if s.Tax.Income != r.byTax["tax.income"] || s.Tax.Sales != r.byTax["tax.sales"] ||
			s.Tax.Corporation != r.byTax["tax.corp"] || s.Tax.Rates != r.byTax["tax.council"] {
			t.Fatalf("month %d tax split %+v != recompute %+v", m+1, s.Tax, r.byTax)
		}
		if prevIn >= 0 && s.MoneyIn != prevIn {
			moved = true
		}
		prevIn = s.MoneyIn
		if v := comp.state.finance.FindConservationViolations(); len(v) != 0 {
			t.Fatalf("month %d conservation violations: %+v", m+1, v)
		}
	}
	if prevIn <= 0 {
		t.Fatalf("MoneyIn after 6 months = %d, want > 0 (tax collected via finance.CollectTax)", prevIn)
	}
	if !moved {
		t.Log("note: MoneyIn identical across all 6 months (flat steady-state); equality to recompute is the gate")
	}
}

// The sankey sub-view is returned by a REAL Subscribe on a composed city.
func TestFiscal_AC2_SankeyViaRealSubscribe(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(22), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: errs.NewCorrelationID()})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	fiscalAdvanceMonths(t, e, 6)
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { _ = transport.Close() }()
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()
	_, delta := subscribeAndAwaitFirstDelta(t, transport, financeViewSubscriptionName)
	var p fiscalPatch
	if err := json.Unmarshal(delta.Patch, &p); err != nil {
		t.Fatalf("decode delta: %v", err)
	}
	if p.Sankey == nil || p.Sankey.MoneyIn != int64(comp.state.finance.TaxRevenue()) || p.Sankey.MoneyIn <= 0 {
		t.Fatalf("subscribed sankey = %+v, want MoneyIn == finance.TaxRevenue() > 0", p.Sankey)
	}
}

// --- AC-3: drill-through reaches ledger lines (A3: includes tax.council) ---

func TestFiscal_AC3_TaxDrillThroughIsExactlyTheLedgerLines(t *testing.T) {
	e, comp := newTestEngine(t, 23)
	fiscalAdvanceMonths(t, e, 3)
	got, err := comp.state.fiscal.LedgerDrillThrough(fiscal.NodeTaxRevenue)
	if err != nil {
		t.Fatalf("LedgerDrillThrough: %v", err)
	}
	want := 0
	var sum int64
	sawCouncil := false
	for _, c := range []string{"tax.income", "tax.sales", "tax.corp", "tax.council"} {
		for _, en := range comp.state.finance.LinesByCategory(finance.Category(c)) {
			want++
			if en.Account == finance.AcctTreasury && en.Side == finance.SideCredit {
				sum += int64(en.Amount)
			}
			if c == "tax.council" {
				sawCouncil = true
			}
		}
	}
	if want == 0 || len(got) != want {
		t.Fatalf("drill-through returned %d entries, ledger has %d (must be non-empty and equal)", len(got), want)
	}
	if !sawCouncil {
		t.Fatal("no tax.council lines in the ledger this month; A3 not exercised")
	}
	var gotSum int64
	for _, en := range got {
		if en.Account == finance.AcctTreasury && en.Side == finance.SideCredit {
			gotSum += int64(en.Amount)
		}
	}
	if gotSum != sum || sum != int64(comp.state.finance.TaxRevenue()) {
		t.Fatalf("drill sum %d / ledger sum %d / TaxRevenue %d must all agree", gotSum, sum, comp.state.finance.TaxRevenue())
	}
}

// --- Scenario D fixture (reusable by the social pack) ---

// injectJobShock forces `fraction` (0..1) of the currently Employed live
// residents to Unemployed through the registered LifeEventEmployment command,
// in ascending id order (deterministic). Returns how many were shocked.
func injectJobShock(t *testing.T, comp *Composition, fraction float64) int {
	t.Helper()
	st := comp.state
	var victims []uint64
	st.citizens.GatherInShardOrder(st.liveResidentIDs(), st.cid, func(id uint64, cit citizens.Citizen, ok bool) {
		if ok && cit.Employment.State == citizens.EmploymentEmployed {
			victims = append(victims, id)
		}
	})
	n := int(math.Floor(float64(len(victims)) * fraction))
	for i := 0; i < n; i++ {
		if err := st.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: st.cid, Kind: citizens.LifeEventEmployment, CitizenID: victims[i],
			Employment: citizens.EmploymentUnemployed, Sector: citizens.SectorNone,
		}); err != nil {
			t.Fatalf("job shock on %d: %v", victims[i], err)
		}
	}
	return n
}

func benefitLineTotal(fin *finance.FinanceAPI, cat finance.Category) (debit, credit int64, balancedPairs bool) {
	balancedPairs = true
	for _, en := range fin.LinesByCategory(cat) {
		switch {
		case en.Account == finance.AcctTreasury && en.Side == finance.SideDebit:
			debit += int64(en.Amount)
		case en.Account == finance.AcctHouseholds && en.Side == finance.SideCredit:
			credit += int64(en.Amount)
		default:
			balancedPairs = false
		}
	}
	return
}

// st0UnemployedBenefits is the unemployment support the fiscal hook pays in a
// month: live unemployed headcount x the data-sourced per-person amount.
// Used by older literal-arithmetic tests (TestBUG355...) that must now account
// for the treasury -> households transfer.
func st0UnemployedBenefits(comp *Composition) int64 {
	return comp.state.unemployedResidentCount() * int64(comp.state.fiscal.UnemploymentSupportPerPerson())
}

// --- AC-4 / AC-5: benefits driven, conserved, grows in a downturn ---

func TestFiscal_AC4_UnemploymentBenefitsDrivenConservedAndGrowInDownturn(t *testing.T) {
	e, comp := newTestEngine(t, 41)
	fin := comp.state.finance
	// The fixture city's treasury only collects ~GBP 45/month, far below the
	// GBP 500/head placeholder, so benefits would (correctly) fail with a logged
	// insufficient-funds error. Endow the treasury exactly as
	// attack_bug720_round_test.go does so the benefits path is exercised.
	if _, err := fin.Post(finance.Transaction{
		Description: "fiscal AC-4 fixture endowment",
		Entries: []finance.Entry{
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: finance.Money(500_000_000_000), Category: "test.topup"},
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: finance.Money(500_000_000_000), Category: "test.topup"},
		},
	}); err != nil {
		t.Fatalf("treasury endowment: %v", err)
	}
	comp.state.syncMoneyFromLedger()
	fiscalAdvanceMonths(t, e, 6)
	preDebit, preCredit, ok := benefitLineTotal(fin, "benefits.unemployment")
	if !ok || preDebit == 0 || preDebit != preCredit {
		t.Fatalf("pre-shock benefits: debit %d credit %d balancedPairs %v (want >0, equal, pure treasury->households)", preDebit, preCredit, ok)
	}
	moneyBefore := fin.TotalMoneyInCirculation()

	shocked := injectJobShock(t, comp, 0.9)
	if shocked == 0 {
		t.Fatal("job shock hit nobody; fixture invalid")
	}
	fiscalAdvanceMonths(t, e, 1)
	postDebit, postCredit, ok := benefitLineTotal(fin, "benefits.unemployment")
	if !ok || postDebit != postCredit {
		t.Fatalf("shock-month benefits unbalanced: debit %d credit %d ok %v", postDebit, postCredit, ok)
	}
	if postDebit <= preDebit {
		t.Fatalf("shock-month benefits %d must exceed pre-shock month %d", postDebit, preDebit)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation violations after shock month: %+v", v)
	}
	_ = moneyBefore

	// The posting itself is money-neutral.
	before := fin.TotalMoneyInCirculation()
	if _, err := comp.state.fiscal.PostUnemploymentSupportFor(100); err != nil {
		t.Fatalf("PostUnemploymentSupportFor: %v", err)
	}
	if after := fin.TotalMoneyInCirculation(); after != before {
		t.Fatalf("TotalMoneyInCirculation %d -> %d across a benefits posting", before, after)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation violations after direct posting: %+v", v)
	}

	// AC-5: the fiscal category is the only unemployment-support category in
	// the ledger this month (social pack not built; no other poster exists).
	if d, _, _ := benefitLineTotal(fin, "benefits.housing"); d != 0 {
		t.Fatalf("benefits.housing has %d posted; nothing drives it in this wiring", d)
	}
}

// --- AC-6: municipality consumer, A/B by planning funding ---

func fiscalConstructionSettled(t *testing.T, funding float64) int64 {
	t.Helper()
	buyCell := protocol.CellRef{X: 3, Y: 3}
	cid := "fiscal-ac6"
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(502), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := comp.SetPlanningFunding(funding); err != nil {
		t.Fatalf("SetPlanningFunding: %v", err)
	}
	if res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "fiscal-buy",
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: buyCell}}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	zoneCell(t, e, "fiscal-zone", buyCell, "dwelling")
	if res := e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "fiscal-build",
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: buyCell, BuildingType: "dwelling"}}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}
	if err := e.AdvanceTicks("fiscal-ac6-90", 90); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	return comp.state.constructionSettledExternal + comp.state.constructionSettledLocal
}

func TestFiscal_AC6_PlanningFundingChangesConstructionCost(t *testing.T) {
	full := fiscalConstructionSettled(t, 1.0)
	zero := fiscalConstructionSettled(t, 0.0)
	if full != expectedConstructionCost(dwellingMaterialsTonnesInc2) {
		t.Fatalf("full-funding settled %d, want the unmodified cost %d (neutral default)", full, expectedConstructionCost(dwellingMaterialsTonnesInc2))
	}
	if zero <= full {
		t.Fatalf("zero-funding settled %d must exceed full-funding %d (build-cost error)", zero, full)
	}
	// 15% data anchor (data/fiscal.json buildCostErrorAtZeroFunding)
	if want := full + full*15/100; zero != want {
		t.Fatalf("zero-funding settled %d, want %d (full + 15%% error from data)", zero, want)
	}
}

// --- AC-7: civil-service gross vs net is real data ---

func TestFiscal_AC7_CivilServiceBillFromServicesRoster(t *testing.T) {
	cid := errs.NewCorrelationID()
	svc, err := services.LoadDefault(cid)
	if err != nil {
		t.Fatalf("services.LoadDefault: %v", err)
	}
	if err := svc.RegisterService(services.ServiceSpec{
		ID: "clinic-1", Kind: services.ServiceHealthcare, CapacityRaw: "100 visits/d",
		UpgradePath: []services.UpgradeStep{{BuildingID: "clinic", Name: "Clinic", CapacityCeiling: 100}},
	}); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if err := svc.UpdateStaffing("clinic-1", 10); err != nil {
		t.Fatalf("UpdateStaffing: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(71), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Services: svc})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	fiscalAdvanceMonths(t, e, 2)
	want, err := svc.GrossWageCost("clinic-1")
	if err != nil || want <= 0 {
		t.Fatalf("GrossWageCost = %d, %v; fixture needs a positive bill", want, err)
	}
	p := fiscalReadPatch(t, comp)
	if p.PublicPayroll == nil || p.PublicPayroll.WageCostMicropounds != int64(want) {
		t.Fatalf("publicPayroll = %+v, want gross == GrossWageCost %d", p.PublicPayroll, want)
	}
	wantClaw := int64(want) * incomeNITaxRateBp / 10000
	if p.PublicPayroll.TaxClawbackMicropounds != wantClaw || wantClaw <= 0 || wantClaw >= int64(want) {
		t.Fatalf("clawback = %d, want %d (gross x rate, 0 < clawback < gross)", p.PublicPayroll.TaxClawbackMicropounds, wantClaw)
	}
	// same clawback as finance's own income-tax rounding
	if net := int64(want) - p.PublicPayroll.TaxClawbackMicropounds; net <= 0 || net >= int64(want) {
		t.Fatalf("net %d must lie strictly between 0 and gross %d", net, want)
	}

	// Change the real roster (staffing -> 0): both figures follow after the next month.
	if err := svc.UpdateStaffing("clinic-1", 0); err != nil {
		t.Fatalf("UpdateStaffing(0): %v", err)
	}
	fiscalAdvanceMonths(t, e, 1)
	p = fiscalReadPatch(t, comp)
	if p.PublicPayroll == nil || p.PublicPayroll.WageCostMicropounds != 0 || p.PublicPayroll.TaxClawbackMicropounds != 0 {
		t.Fatalf("after de-staffing publicPayroll = %+v, want both 0", p.PublicPayroll)
	}
}

// No services at all: both are 0.
func TestFiscal_AC7_NoServicesBothZero(t *testing.T) {
	e, comp := newTestEngine(t, 72)
	fiscalAdvanceMonths(t, e, 2)
	p := fiscalReadPatch(t, comp)
	if p.PublicPayroll == nil || p.PublicPayroll.WageCostMicropounds != 0 || p.PublicPayroll.TaxClawbackMicropounds != 0 {
		t.Fatalf("publicPayroll = %+v, want zeros with no services", p.PublicPayroll)
	}
}

// --- AC-9: save participant round-trips non-derivable state ---

func TestFiscal_AC9_FiscalStateRoundTripsThroughSaveLoad(t *testing.T) {
	e, comp := newTestEngine(t, 91)
	fiscalAdvanceMonths(t, e, 2)
	if err := comp.SetPlanningFunding(0.37); err != nil {
		t.Fatalf("SetPlanningFunding: %v", err)
	}
	if err := comp.state.fiscal.SetCivilServiceWageBill(123456789); err != nil {
		t.Fatalf("SetCivilServiceWageBill: %v", err)
	}
	dir := t.TempDir()
	if err := comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	e2, comp2 := newTestEngine(t, 91)
	_ = e2
	if got := comp2.PlanningFunding(); got != fiscalDefaultPlanningFunding {
		t.Fatalf("fresh composition funding = %v", got)
	}
	if err := comp2.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := comp2.PlanningFunding(); got != 0.37 {
		t.Fatalf("planning funding after load = %v, want 0.37", got)
	}
	if got := comp2.state.fiscal.CivilServiceGross(); got != 123456789 {
		t.Fatalf("civil-service bill after load = %d, want 123456789", got)
	}
}

// Determinism: same seed, byte-identical sankey.
func TestFiscal_AC9_SameSeedSankeyIdentical(t *testing.T) {
	run := func() []byte {
		e, comp := newTestEngine(t, 92)
		fiscalAdvanceMonths(t, e, 4)
		raw, err := comp.state.buildFinanceBalanceSheetPatch()
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		return raw
	}
	a, b := run(), run()
	if string(a) != string(b) {
		t.Fatalf("same-seed f2.finance patches differ:\n%s\n%s", a, b)
	}
}

// --- AC-10: inputs fail closed ---

func TestFiscal_AC10_InvalidPlanningFundingRefusedStateUnchanged(t *testing.T) {
	_, comp := newTestEngine(t, 101)
	if err := comp.SetPlanningFunding(0.5); err != nil {
		t.Fatalf("SetPlanningFunding(0.5): %v", err)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1, 1.0001} {
		err := comp.SetPlanningFunding(bad)
		if err == nil {
			t.Fatalf("SetPlanningFunding(%v) accepted", bad)
		}
		var ee *errs.E
		if !errors.As(err, &ee) || ee.Code != fiscal.ErrInvalidFundingLevel {
			t.Fatalf("SetPlanningFunding(%v) error = %v, want registry code %s", bad, err, fiscal.ErrInvalidFundingLevel)
		}
		if got := comp.PlanningFunding(); got != 0.5 {
			t.Fatalf("state changed to %v after refused %v", got, bad)
		}
	}
	if err := comp.state.fiscal.SetCivilServiceWageBill(-1); err == nil {
		t.Fatal("negative wage bill accepted")
	}
	if err := comp.state.fiscal.ImportState(fiscal.State{PlanningFunding: math.NaN()}); err == nil {
		t.Fatal("ImportState accepted NaN")
	}
	if got := comp.PlanningFunding(); got != 0.5 {
		t.Fatalf("ImportState(NaN) mutated funding to %v", got)
	}
}

// applyBuildCostError is a no-op at full funding and saturates, never wraps.
func TestFiscal_ApplyBuildCostError_NeutralAndSaturating(t *testing.T) {
	_, comp := newTestEngine(t, 111)
	if got := comp.state.applyBuildCostError(1000); got != 1000 {
		t.Fatalf("full funding altered the cost: %d", got)
	}
	if err := comp.SetPlanningFunding(0); err != nil {
		t.Fatal(err)
	}
	if got := comp.state.applyBuildCostError(1_000_000); got != 1_150_000 {
		t.Fatalf("zero funding cost = %d, want 1150000", got)
	}
	if got := comp.state.applyBuildCostError(math.MaxInt64); got != math.MaxInt64 {
		t.Fatalf("overflow case = %d, want saturation at MaxInt64", got)
	}
	if got := comp.state.applyBuildCostError(-5); got != -5 {
		t.Fatalf("negative cost altered: %d", got)
	}
}
