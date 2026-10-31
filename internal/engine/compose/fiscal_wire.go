package compose

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/fiscal"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-2326609835 (mkey feat.compositionroot): engine.fiscal composition-root
// wiring, kept in its own file so compose.go carries only the simState field,
// the construction call and one registrationOrder row.
//
// Scope (docs/planning/acceptance/wire-engine.fiscal.md + verdict file + lead
// amendments A1-A6):
//   - the Sankey and civil-service figures published on f2.finance are read
//     from the FINANCE LEDGER (A1/A2/A3), never from engine.tax (unwired);
//   - the monthly fiscalHook drives PostUnemploymentSupport from the live
//     unemployed headcount and supplies the civil-service wage bill from
//     ServicesAPI.GrossWageCost summed over the registered services;
//   - planning funding has ONE consumer: the construction posting's cost is
//     scaled by BuildCostErrorRate at compose level (applyBuildCostError);
//   - the non-ledger-derivable inputs (planning funding, childcare places,
//     civil-service bill) round-trip through fiscalParticipant (A5).
//
// Not wired (verdict file): PostSocialHousingBuild (A4, finance.
// SettleConstruction covers it), SetTax / the five-way TaxBreakdown, childcare
// (no places source in the composed city).

// fiscalDefaultPlanningFunding is the planning-funding level a fresh
// composition starts at: FULL funding, at which BuildCostErrorRate is the
// data's full-funding anchor (0) so the consumer is a no-op until a player
// or test lowers it (an unmodified city is byte-identical to before this
// wiring). Saved with the game (fiscalParticipant).
const fiscalDefaultPlanningFunding = 1.0

// wireFiscal constructs engine.fiscal against the composed finance ledger and
// sets the neutral default funding.
func wireFiscal(fin *finance.FinanceAPI, cid string) (*fiscal.FiscalAPI, error) {
	f, err := fiscal.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "fiscal"})
	}
	if err := f.SetFinance(fin); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "fiscal", "step": "SetFinance"})
	}
	if err := f.SetPlanningFunding(fiscalDefaultPlanningFunding); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "fiscal", "step": "SetPlanningFunding"})
	}
	return f, nil
}

// ---- monthly hook -------------------------------------------------------

// fiscalEffect is the monthly fiscal hook's tick marker.
type fiscalEffect struct{}

// fiscalHook runs on PhaseFinance AFTER financeHook. RunShard only emits the
// marker (shard 0); every read-then-post happens in ApplyEffect on the
// serialized barrier (GR#21: no clock/state work in parallel shards).
type fiscalHook struct {
	st *simState
}

func (h *fiscalHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: fiscalEffect{}}}, nil
}

func (h *fiscalHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(fiscalEffect); !ok {
		return
	}
	h.st.applyFiscalMonth()
}

// SingleShard implements core.SingleShardHook.
func (h *fiscalHook) SingleShard() bool { return true }

// civilServiceWageBill sums ServicesAPI.GrossWageCost over every registered
// service in ascending id order (ServiceIDs is sorted; GR#21). A per-service
// error is surfaced, never swallowed into a partial bill.
func (st *simState) civilServiceWageBill() (finance.Money, error) {
	if st.services == nil {
		return 0, nil
	}
	ids, err := st.services.ServiceIDs()
	if err != nil {
		return 0, err
	}
	var total finance.Money
	for _, id := range ids {
		g, gerr := st.services.GrossWageCost(id)
		if gerr != nil {
			return 0, gerr
		}
		total = finance.Money(num.SatAdd(int64(total), int64(g)))
	}
	return total, nil
}

// unemployedResidentCount counts live residents in EmploymentUnemployed (one
// shard-order pass, like employedResidentCount).
func (st *simState) unemployedResidentCount() int64 {
	var n int64
	st.citizens.GatherInShardOrder(st.liveResidentIDs(), st.cid, func(_ uint64, cit citizens.Citizen, ok bool) {
		if ok && cit.Employment.State == citizens.EmploymentUnemployed {
			n++
		}
	})
	return n
}

// applyFiscalMonth is the monthly fiscal step: refresh the civil-service wage
// bill from the services roster, then post unemployment support for the live
// unemployed headcount (a balanced treasury -> households transfer; money
// neutral). Failures are logged loudly (GR#1/GR#17) and never block the tick.
func (st *simState) applyFiscalMonth() {
	if st.fiscal == nil || st.finance == nil {
		return
	}
	bill, err := st.civilServiceWageBill()
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "fiscal", "op": "civilServiceWageBill", "cause": err.Error()})
	} else if err := st.fiscal.SetCivilServiceWageBill(bill); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "fiscal", "op": "SetCivilServiceWageBill", "cause": err.Error()})
	}

	if st.citizens != nil {
		if _, err := st.fiscal.PostUnemploymentSupportFor(st.unemployedResidentCount()); err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "fiscal", "op": "PostUnemploymentSupportFor", "cause": err.Error()})
		}
	}
	st.syncMoneyFromLedger()
}

// ---- municipality consumer (AC-6) ---------------------------------------

// buildCostErrorDenominatorBp is the basis-point scale applyBuildCostError
// converts the data-sourced error rate into (1 bp = 0.01%).
const buildCostErrorDenominatorBp = 10000

// applyBuildCostError scales a construction cost by the municipality's
// planning-funding-driven build-cost error rate: cost + cost x rate. At full
// funding the rate is 0 and the cost is returned unchanged. Pure function of
// fiscal state (deterministic); saturating (GR#16).
func (st *simState) applyBuildCostError(cost int64) int64 {
	if st.fiscal == nil || cost <= 0 {
		return cost
	}
	rate := st.fiscal.BuildCostErrorRate()
	if !num.IsFinite(rate) || rate <= 0 {
		return cost
	}
	bp := num.ClampInt64FromFloat(math.Round(rate * buildCostErrorDenominatorBp))
	p, overflow := num.SafeMul(cost, bp)
	if overflow {
		return math.MaxInt64
	}
	return num.SatAdd(cost, p/buildCostErrorDenominatorBp)
}

// SetPlanningFunding sets the municipality planning-funding level (0..1).
// Non-finite or out-of-range input is refused with fiscal's registry error
// (MET-G3104) and state is unchanged (AC-10). NOTE: this is a direct setter,
// NOT a journaled protocol command; a protocol Kind is deferred (needs
// internal/protocol registration), so it must not be used to mutate a
// replayed game mid-run.
func (c *Composition) SetPlanningFunding(level float64) error {
	return c.state.fiscal.SetPlanningFunding(level)
}

// PlanningFunding returns the current planning-funding level.
func (c *Composition) PlanningFunding() float64 { return c.state.fiscal.PlanningFunding() }

// ---- f2.finance sankey / publicPayroll sub-views ------------------------

// financeSankeyBand mirrors ui.screen.finance/wire.go's wireSankeyBand.
type financeSankeyBand struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Amount int64  `json:"amount"`
}

// financeSankeyNode is one fiscal node (additive detail beyond the UI's
// band list): Provisional nodes carry amount 0 and the flag.
type financeSankeyNode struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Amount      int64  `json:"amount"`
	Provisional bool   `json:"provisional"`
}

// financeSankeyView is the sankey sub-view: `bands` is the UI's existing
// wireFiscalCircuitView shape; the remaining fields are additive.
type financeSankeyView struct {
	Bands         []financeSankeyBand `json:"bands"`
	Nodes         []financeSankeyNode `json:"nodes"`
	MoneyIn       int64               `json:"moneyIn"`
	MoneyOut      int64               `json:"moneyOut"`
	OtherOutflows int64               `json:"otherOutflows"`
	BudgetBalance int64               `json:"budgetBalance"`
	BenefitsPaid  int64               `json:"benefitsPaid"`
	Tax           financeTaxView      `json:"tax"`
}

// financeTaxView is the ledger-sourced per-type tax split (A1).
type financeTaxView struct {
	Income      int64 `json:"income"`
	Sales       int64 `json:"sales"`
	Corporation int64 `json:"corporation"`
	Rates       int64 `json:"rates"`
	Duties      int64 `json:"duties"`
}

// financePublicPayrollView mirrors ui.screen.finance/wire.go's
// wirePublicPayrollView.
type financePublicPayrollView struct {
	WageCostMicropounds    int64 `json:"wageCostMicropounds"`
	TaxClawbackMicropounds int64 `json:"taxClawbackMicropounds"`
}

// buildFiscalSections builds the sankey + publicPayroll sub-views from the
// finance ledger (called from buildFinanceBalanceSheetPatch on the pump
// goroutine: every read goes through finance/fiscal's own locked accessors).
// Returns nils (omitted from the patch) when fiscal is unwired or a read
// fails - never a fabricated figure.
func (st *simState) buildFiscalSections() (*financeSankeyView, *financePublicPayrollView) {
	if st.fiscal == nil {
		return nil, nil
	}
	topo, err := st.fiscal.LedgerTopology()
	if err != nil {
		return nil, nil
	}
	view := &financeSankeyView{Bands: []financeSankeyBand{}, Nodes: []financeSankeyNode{}}
	for _, n := range topo.Nodes {
		view.Nodes = append(view.Nodes, financeSankeyNode{ID: string(n.ID), Label: n.Label, Amount: int64(n.Amount), Provisional: n.Provisional})
	}
	for _, e := range topo.Edges {
		view.Bands = append(view.Bands, financeSankeyBand{Source: string(e.From), Target: string(e.To), Amount: int64(e.Amount)})
	}
	in, e1 := st.fiscal.LedgerMoneyIn()
	out, e2 := st.fiscal.LedgerMoneyOut()
	other, e3 := st.fiscal.LedgerOtherOutflows()
	bal, e4 := st.fiscal.LedgerBudgetBalance()
	tb, e5 := st.fiscal.LedgerTaxBreakdown()
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return nil, nil
	}
	view.MoneyIn, view.MoneyOut, view.OtherOutflows, view.BudgetBalance = int64(in), int64(out), int64(other), int64(bal)
	view.Tax = financeTaxView{Income: int64(tb.Income), Sales: int64(tb.Sales), Corporation: int64(tb.Corporation), Rates: int64(tb.Rates), Duties: int64(tb.Duties)}
	view.BenefitsPaid = st.benefitsPaid()

	var payroll *financePublicPayrollView
	if claw, cerr := st.fiscal.LedgerCivilServiceClawbackBp(incomeNITaxRateBp); cerr == nil {
		payroll = &financePublicPayrollView{
			WageCostMicropounds:    int64(st.fiscal.CivilServiceGross()),
			TaxClawbackMicropounds: int64(claw),
		}
	}
	return view, payroll
}

// benefitsPaid sums the treasury-debit legs of benefits.unemployment and
// benefits.housing over the current month's ledger lines.
func (st *simState) benefitsPaid() int64 {
	var total int64
	for _, cat := range []finance.Category{"benefits.unemployment", "benefits.housing"} {
		for _, e := range st.finance.LinesByCategory(cat) {
			if e.Account == finance.AcctTreasury && e.Side == finance.SideDebit {
				total = num.SatAdd(total, int64(e.Amount))
			}
		}
	}
	return total
}

// ---- save participant (A5) ----------------------------------------------

const (
	kindFiscal = "fiscal"
	recFiscal  = "fiscal.state"
)

// fiscalParticipant round-trips the fiscal inputs that cannot be derived from
// the ledger: planning funding, childcare places, civil-service bill.
type fiscalParticipant struct {
	st *simState
}

func newFiscalParticipant(st *simState) *fiscalParticipant { return &fiscalParticipant{st: st} }

func (p *fiscalParticipant) Kind() string { return kindFiscal }

func (p *fiscalParticipant) Source() serialize.RecordSource {
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted || p.st.fiscal == nil {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(p.st.fiscal.ExportState())
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recFiscal, err)
		}
		emitted = true
		return serialize.Record{Kind: recFiscal, Data: data}, true, nil
	}
}

func (p *fiscalParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recFiscal {
			return fmt.Errorf("compose: unknown fiscal save record kind %q", rec.Kind)
		}
		var s fiscal.State
		if err := json.Unmarshal(rec.Data, &s); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		return p.st.fiscal.ImportState(s)
	}
}
