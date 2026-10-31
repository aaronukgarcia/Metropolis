package compose

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/defence"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/fiscal"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609836: tests for engine.defence wired into the composition root
// (pack docs/planning/acceptance/wire-engine.defence.md). Every scenario is
// driven through compose.Wire and the real protocol command path
// (e.HandleCommand -> handleGameplay -> handleDefenceCommand); no test calls
// DefenceAPI.BidForGrant / RespondToMandate directly except the labelled
// AC-8 post-load comparison, which uses the same composed command path too.
//
// POPULATION FIXTURE: mandates fire at 100k+ population and a real city never
// reaches that here, so the mandate scenarios script the population through
// defenceWiring.popFn. That seam is nil in production and is assigned ONLY by
// these tests (a labelled test fixture, not a game feature).

const defenceTestSeed = uint64(20261101)

const (
	potWin  = "win"  // base 1.0: always wins (Float64() < 1)
	potLose = "lose" // base 0.0, zero weights: always loses (Float64() < 0 is false)
	potCoin = "coin" // base 0.5, zero weights: a genuinely informative draw (AC-8)

	awardMicro = int64(500_000_000)
)

// defenceTestConfig is defence/helpers_test.go validConfig() with deterministic
// pots and small facility sizes.
func defenceTestConfig() defence.Config {
	pot := func(id string, base float64) defence.GrantPotConfig {
		return defence.GrantPotConfig{ID: id, Name: id, BaseWinProbability: base, MatchFundingWeight: 0, PlanningQualityWeight: 0, MaxMatchMicropounds: 1_000_000_000, AwardMicropounds: awardMicro}
	}
	return defence.Config{
		Version:        1,
		GrantPots:      []defence.GrantPotConfig{pot(potWin, 1.0), pot(potLose, 0.0), pot(potCoin, 0.5)},
		FormulaSupport: defence.FormulaSupportConfig{TaxCapacityThresholdMicropounds: 10_000_000_000, FormulaAmountMicropounds: 200_000_000},
		Mandates: []defence.MandateConfig{
			{ID: "naval-100k", PopulationThreshold: 100_000, FacilityType: "naval", CompensationMicropounds: 4_000_000_000,
				Choices: []defence.MandateChoiceConfig{
					{ID: "naval-base", FacilityType: "naval", Description: "full naval base"},
					{ID: "naval-patrol-berth", FacilityType: "naval-patrol", Description: "patrol berth"},
				}},
			{ID: "army-500k", PopulationThreshold: 500_000, FacilityType: "army", CompensationMicropounds: 6_000_000_000,
				Choices: []defence.MandateChoiceConfig{
					{ID: "infantry-garrison", FacilityType: "army", Description: "garrison"},
					{ID: "armoured-regiment", FacilityType: "army-armoured", Description: "regiment"},
				}},
		},
		Facilities: map[string]defence.FacilityConfig{
			"naval":         {BuildZone: "heavy_industry", PayrollMicropounds: 1_000_000, PayrollFloorMicropounds: 1_000_000, PersonnelCount: 6, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 100_000},
			"naval-patrol":  {BuildZone: "heavy_industry", PayrollMicropounds: 500_000, PayrollFloorMicropounds: 500_000, PersonnelCount: 4, MarriedQuarters: 0, ChildrenPerQuarter: 0, ProcurementMicropounds: 50_000},
			"army":          {BuildZone: "heavy_industry", PayrollMicropounds: 2_000_000, PayrollFloorMicropounds: 2_000_000, PersonnelCount: 8, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 80_000},
			"army-armoured": {BuildZone: "heavy_industry", PayrollMicropounds: 2_500_000, PayrollFloorMicropounds: 2_500_000, PersonnelCount: 8, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 90_000},
		},
		Reputation: defence.ReputationConfig{RefusalPenaltyPoints: 50},
	}
}

// defenceMonth is one month's observation taken at PhaseLandValueDecay (after
// PhaseFinance, before the next BeginMonth clears the tick log).
type defenceMonth struct {
	month             int64
	wagesDebits       []int64 // treasury-debit CatWages line amounts
	defPayroll        int64   // defence.payroll treasury-debit total
	defProcure        int64   // defence.procurement treasury-debit total
	publicEmployed    int64   // live residents Employed+SectorPublic (independent count)
	personnelPub      int64   // facility personnel ids that are Employed+SectorPublic
	personnelEmployed int64   // facility personnel ids that are Employed (any sector)
}

type defenceRig struct {
	e          *core.Engine
	comp       *Composition
	months     []defenceMonth
	violations *atomic.Int64
	prepared   bool
}

func newDefenceRig(t *testing.T, cfg defence.Config, pop int64, observe bool) *defenceRig {
	t.Helper()
	r := &defenceRig{violations: &atomic.Int64{}}
	var comp *Composition
	opts := []core.Option{core.WithWorldSeed(defenceTestSeed), core.WithPoolSize(1)}
	if observe {
		opts = append(opts, core.WithPhaseObserver(func(kind core.PhaseKind, _ int64, month int64) {
			if kind != core.PhaseLandValueDecay || comp == nil || comp.state == nil {
				return
			}
			r.months = append(r.months, observeDefenceMonth(comp, month))
		}))
	}
	e := core.NewEngine(opts...)
	api, err := defence.New(cfg, defenceTestSeed, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("defence.New: %v", err)
	}
	v := r.violations
	c, err := Wire(e, &Deps{Defence: api, InvariantOpts: []invariant.HookOption{invariant.WithLogSink(func(*errs.E) { v.Add(1) })}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	comp = c
	if pop >= 0 {
		c.state.defenceW.popFn = func() int64 { return pop } // LABELLED TEST FIXTURE
	}
	r.e, r.comp = e, c
	return r
}

func observeDefenceMonth(c *Composition, month int64) defenceMonth {
	st := c.state
	m := defenceMonth{month: month}
	for _, l := range st.finance.LinesByCategory(finance.CatWages) {
		if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
			m.wagesDebits = append(m.wagesDebits, int64(l.Amount))
		}
	}
	for _, l := range st.finance.LinesByCategory(finance.Category("defence.payroll")) {
		if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
			m.defPayroll += int64(l.Amount)
		}
	}
	for _, l := range st.finance.LinesByCategory(finCatDefenceProcurement) {
		if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
			m.defProcure += int64(l.Amount)
		}
	}
	st.citizens.GatherInShardOrder(st.liveResidentIDs(), st.cid, func(_ uint64, cit citizens.Citizen, ok bool) {
		if ok && cit.Employment.State == citizens.EmploymentEmployed && cit.Employment.Sector == citizens.SectorPublic {
			m.publicEmployed++
		}
	})
	m.personnelPub = int64(len(personnelPublic(st)))
	m.personnelEmployed = int64(personnelEmployedCount(st))
	return m
}

// personnelFacilityIDs returns every id the open defence facilities hold.
func personnelFacilityIDs(c *Composition) []uint64 {
	var out []uint64
	for _, f := range c.state.defenceW.api.Snapshot().Facilities {
		out = append(out, f.IDs...)
	}
	return out
}

// personnelPublic returns the facility ids whose citizen is Employed+Public.
func personnelPublic(st *simState) []uint64 {
	var out []uint64
	snap := st.defenceW.api.Snapshot()
	for _, f := range snap.Facilities {
		for _, id := range f.IDs[:f.Personnel] {
			cit, ok := st.citizens.CitizenAt(id, st.cid)
			if ok && cit.Employment.State == citizens.EmploymentEmployed && cit.Employment.Sector == citizens.SectorPublic {
				out = append(out, id)
			}
		}
	}
	return out
}

// personnelEmployedCount counts facility personnel (adults) whose citizen is
// Employed in any sector.
func personnelEmployedCount(st *simState) int {
	n := 0
	for _, f := range st.defenceW.api.Snapshot().Facilities {
		for _, id := range f.IDs[:f.Personnel] {
			cit, ok := st.citizens.CitizenAt(id, st.cid)
			if ok && cit.Employment.State == citizens.EmploymentEmployed {
				n++
			}
		}
	}
	return n
}

func (r *defenceRig) cmd(t *testing.T, name string, kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
	t.Helper()
	return r.e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(name),
		Kind: kind, Payload: payload,
	})
}

func (r *defenceRig) bid(t *testing.T, name, pot string) protocol.CommandResult {
	return r.cmd(t, name, protocol.KindBidForGrant, protocol.BidForGrantPayload{Pot: pot})
}

// prepareMandateCell buys and zones the mandate site (the compose_test.go
// Buy/Zone cell preparation) so the engine.build siting accepts it.
func (r *defenceRig) prepareMandateCell(t *testing.T) {
	t.Helper()
	if r.prepared {
		return
	}
	r.prepared = true
	cell := protocol.CellRef{X: 4, Y: 4}
	requireAccepted(t, r.cmd(t, "prep-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}))
	requireAccepted(t, r.cmd(t, "prep-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "heavy_industry"}))
}

func (r *defenceRig) respond(t *testing.T, name, mandate, choice string, refuse bool) protocol.CommandResult {
	if !refuse {
		r.prepareMandateCell(t)
	}
	return r.cmd(t, name, protocol.KindRespondToMandate, protocol.RespondToMandatePayload{
		MandateID: mandate, Refuse: refuse, Choice: choice, Cell: protocol.CellRef{X: 4, Y: 4},
	})
}

func (r *defenceRig) advanceMonths(t *testing.T, n int64) {
	t.Helper()
	advanceInChunks(t, r.e, n*int64(core.DailyTicksPerMonth))
}

func requireAccepted(t *testing.T, res protocol.CommandResult) {
	t.Helper()
	if !res.Accepted {
		t.Fatalf("command rejected: %+v", res.Error)
	}
}

func requireRejected(t *testing.T, res protocol.CommandResult, code string) {
	t.Helper()
	if res.Accepted || res.Error == nil || res.Error.Code != code {
		t.Fatalf("want rejection %s, got accepted=%v err=%+v", code, res.Accepted, res.Error)
	}
}

// grantsLines returns (treasury-credit total, external-debit total, line count).
func grantsLines(f *finance.FinanceAPI) (cred, deb int64, n int) {
	for _, l := range f.LinesByCategory(finance.CatGrants) {
		n++
		switch {
		case l.Account == finance.AcctTreasury && l.Side == finance.SideCredit:
			cred += int64(l.Amount)
		case l.Account == finance.AcctExternal && l.Side == finance.SideDebit:
			deb += int64(l.Amount)
		}
	}
	return
}

func ledgerGrantsNode(t *testing.T, f *fiscal.FiscalAPI) fiscal.SankeyNode {
	t.Helper()
	topo, err := f.LedgerTopology()
	if err != nil {
		t.Fatalf("LedgerTopology: %v", err)
	}
	for _, n := range topo.Nodes {
		if n.ID == fiscal.NodeGrants {
			return n
		}
	}
	t.Fatal("grants node missing")
	return fiscal.SankeyNode{}
}

// --- AC-1 reachability -------------------------------------------------------

func TestDefenceWire_AC1_Reachability(t *testing.T) {
	order := RegistrationOrder()
	idx := func(n string) int {
		for i, v := range order {
			if v == n {
				return i
			}
		}
		return -1
	}
	if idx("defence") < 0 {
		t.Fatalf("RegistrationOrder() = %v lacks the defence monthly hook", order)
	}
	if idx("defence") != idx("fiscal")+1 {
		t.Fatalf("defence must sit directly after fiscal: %v", order)
	}
	found := false
	for _, n := range RegisteredViewNames() {
		if n == defenceViewSubscriptionName {
			found = true
		}
	}
	if !found {
		t.Fatalf("RegisteredViewNames() = %v lacks %q", RegisteredViewNames(), defenceViewSubscriptionName)
	}
	// Default Wire (real data/defence.json, REAL population): the module is
	// constructed by compose and the view reports pots + formula support with
	// no pending mandate -- dormancy at default population is stated, not
	// claimed as the pass condition (the mandate ACs below script population).
	e := core.NewEngine(core.WithWorldSeed(defenceTestSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp.Defence() == nil {
		t.Fatal("compose did not construct engine.defence")
	}
	raw, err := comp.state.buildDefencePatch()
	if err != nil {
		t.Fatalf("buildDefencePatch: %v", err)
	}
	var p defenceWirePatch
	mustUnmarshal(t, raw, &p)
	if len(p.Pots) == 0 || len(p.PendingMandates) != 0 {
		t.Fatalf("default view pots=%d pending=%d, want pots>0 pending=0", len(p.Pots), len(p.PendingMandates))
	}
}

// --- AC-2 a won bid is a balanced credit; the Sankey stops lying -------------

func TestDefenceWire_AC2_WonBidBalancedCreditAndSankey(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), -1, false)
	fin := r.comp.state.finance

	// Before any grant line the node is a Provisional zero (honest-when-empty).
	if n := ledgerGrantsNode(t, r.comp.state.fiscal); !n.Provisional || n.Amount != 0 {
		t.Fatalf("grants node before any bid = %+v, want Provisional zero", n)
	}

	// Bid MID-RUN (after a few ticks) so the money invariant has an opening
	// stock to compare against: an untracked award would be a violation.
	advanceInChunks(t, r.e, 3)
	before := int64(fin.TotalMoneyInCirculation())
	treasuryBefore := int64(r.comp.Treasury())
	requireAccepted(t, r.bid(t, "bid-win", potWin))
	after := int64(fin.TotalMoneyInCirculation())

	if after-before != awardMicro {
		t.Fatalf("money stock rose by %d, want exactly the award %d", after-before, awardMicro)
	}
	// The composition's treasury mirror folds the award in the SAME call
	// (withLedgerDelta), not at some later hook.
	if got := int64(r.comp.Treasury()) - treasuryBefore; got != awardMicro {
		t.Fatalf("composition treasury mirror rose by %d, want the award %d", got, awardMicro)
	}
	cred, deb, n := grantsLines(fin)
	if cred != awardMicro || deb != awardMicro || n != 2 {
		t.Fatalf("grants lines credit=%d debit=%d n=%d, want %d/%d/2", cred, deb, n, awardMicro, awardMicro)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation violations after a won bid: %v", v)
	}
	// Sankey: node equals the INDEPENDENTLY summed ledger lines, not Provisional.
	node := ledgerGrantsNode(t, r.comp.state.fiscal)
	if node.Provisional || int64(node.Amount) != cred {
		t.Fatalf("grants node = %+v, want real %d", node, cred)
	}
	in, err := r.comp.state.fiscal.LedgerMoneyIn()
	if err != nil || int64(in) != int64(fin.TaxRevenue())+cred {
		t.Fatalf("LedgerMoneyIn = %d,%v want tax(%d)+grants(%d)", int64(in), err, int64(fin.TaxRevenue()), cred)
	}
	// The money invariant sees a tracked flow across the next ticks.
	r.advanceMonths(t, 2)
	if v := r.violations.Load(); v != 0 {
		t.Fatalf("invariant suite reported %d violations after a won bid", v)
	}
	// The view carries the same ledger sum.
	var p defenceWirePatch
	raw, _ := r.comp.state.buildDefencePatch()
	mustUnmarshal(t, raw, &p)
	if p.LastBid == nil || !p.LastBid.Won || p.LastBid.Award != awardMicro || p.BidSeq != 1 {
		t.Fatalf("view lastBid=%+v bidSeq=%d", p.LastBid, p.BidSeq)
	}
}

// --- AC-3 a lost bid posts nothing -------------------------------------------

func TestDefenceWire_AC3_LostBidPostsNothing(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), -1, false)
	fin := r.comp.state.finance
	before := int64(fin.TotalMoneyInCirculation())
	requireAccepted(t, r.bid(t, "bid-lose", potLose))
	if got := int64(fin.TotalMoneyInCirculation()); got != before {
		t.Fatalf("money changed on a lost bid: %d -> %d", before, got)
	}
	if _, _, n := grantsLines(fin); n != 0 {
		t.Fatalf("lost bid left %d grants lines", n)
	}
	if n := ledgerGrantsNode(t, r.comp.state.fiscal); !n.Provisional || n.Amount != 0 {
		t.Fatalf("grants node after a lost bid = %+v, want Provisional zero", n)
	}
	if r.comp.state.defenceW.api.BidSeq() != 1 {
		t.Fatalf("a lost bid must still consume a draw (bidSeq=%d)", r.comp.state.defenceW.api.BidSeq())
	}
}

// --- AC-4 threshold crossing, data-driven ------------------------------------

func TestDefenceWire_AC4_ThresholdLadderIsDataDriven(t *testing.T) {
	pending := func(r *defenceRig) []string {
		var p defenceWirePatch
		raw, err := r.comp.state.buildDefencePatch()
		if err != nil {
			t.Fatalf("buildDefencePatch: %v", err)
		}
		mustUnmarshal(t, raw, &p)
		var ids []string
		for _, m := range p.PendingMandates {
			ids = append(ids, m.ID)
		}
		return ids
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("pending = %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("pending = %v, want %v", got, want)
			}
		}
	}
	var pop int64
	cfg := defenceTestConfig()
	r := newDefenceRig(t, cfg, 0, false)
	r.comp.state.defenceW.popFn = func() int64 { return pop }

	pop = 99_999
	eq(pending(r))
	pop = 100_000
	eq(pending(r), "naval-100k")
	pop = 500_000
	eq(pending(r), "naval-100k", "army-500k") // data-file order; 100k still pending

	// Moving the DATA threshold moves the result (a hardcoded ladder would not).
	cfg2 := defenceTestConfig()
	cfg2.Mandates[0].PopulationThreshold = 7
	r2 := newDefenceRig(t, cfg2, 7, false)
	eq(pending(r2), "naval-100k")
	r3 := newDefenceRig(t, cfg2, 6, false)
	eq(pending(r3))
}

// --- AC-5 mandate response atomic + one-shot ---------------------------------

func TestDefenceWire_AC5_AcceptAtomicOneShotPeopleConserved(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	st := r.comp.state
	fin := st.finance
	api := st.defenceW.api

	popBefore := int64(st.citizens.TotalPopulation(st.cid))
	moneyBefore := int64(fin.TotalMoneyInCirculation())

	// Not pending (threshold not reached) is refused by the composition.
	requireRejected(t, r.respond(t, "resp-notpending", "army-500k", "infantry-garrison", false), ErrDefenceMandateNotPending)

	requireAccepted(t, r.respond(t, "resp-accept", "naval-100k", "naval-base", false))
	if len(api.FacilityIDs()) != 1 {
		t.Fatalf("facilities = %d, want 1", len(api.FacilityIDs()))
	}
	// naval: 6 personnel + 2 quarters x 1 child = 8 citizens.
	const wantNew = int64(8)
	if got := int64(st.citizens.TotalPopulation(st.cid)) - popBefore; got != wantNew {
		t.Fatalf("population rose by %d, want %d", got, wantNew)
	}
	// Compensation is a balanced external -> treasury credit, money rises by it.
	cred, deb, _ := grantsLines(fin)
	const comp = int64(4_000_000_000)
	if cred != comp || deb != comp {
		t.Fatalf("compensation grants credit=%d debit=%d, want %d", cred, deb, comp)
	}
	if got := int64(fin.TotalMoneyInCirculation()) - moneyBefore; got != comp {
		t.Fatalf("money rose by %d, want compensation %d", got, comp)
	}
	// IDs: unique across the whole population and inside the allocator range.
	ids := personnelFacilityIDs(r.comp)
	if int64(len(ids)) != wantNew {
		t.Fatalf("facility holds %d ids, want %d", len(ids), wantNew)
	}
	seen := map[uint64]bool{}
	live := map[uint64]bool{}
	for _, id := range st.liveResidentIDs() {
		live[id] = true
	}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate personnel id %d", id)
		}
		seen[id] = true
		if _, ok := st.citizens.CitizenAt(id, st.cid); !ok {
			t.Fatalf("personnel id %d has no citizen record", id)
		}
		if !live[id] {
			t.Fatalf("personnel id %d is not in the composition's live resident set", id)
		}
	}

	// A second response is refused with the registry code and changes nothing.
	snap := api.Snapshot()
	popMid := int64(st.citizens.TotalPopulation(st.cid))
	moneyMid := int64(fin.TotalMoneyInCirculation())
	nextID := st.nextCitizenID
	requireRejected(t, r.respond(t, "resp-again", "naval-100k", "naval-patrol-berth", false), defence.ErrMandateAlreadyResponded)
	requireRejected(t, r.respond(t, "resp-refuse-after", "naval-100k", "", true), defence.ErrMandateAlreadyResponded)
	if int64(st.citizens.TotalPopulation(st.cid)) != popMid || int64(fin.TotalMoneyInCirculation()) != moneyMid ||
		st.nextCitizenID != nextID || len(api.Snapshot().Facilities) != len(snap.Facilities) {
		t.Fatal("a refused second response changed state")
	}

	// People + money conservation hold across ticks with the new citizens.
	r.advanceMonths(t, 2)
	if v := r.violations.Load(); v != 0 {
		t.Fatalf("invariant suite reported %d violations after mandate acceptance", v)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("finance conservation violations: %v", v)
	}
}

// --- AC-6 refusal gates bids -------------------------------------------------

func TestDefenceWire_AC6_RefusalGatesBids(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	fin := r.comp.state.finance
	requireAccepted(t, r.respond(t, "refuse", "naval-100k", "", true))
	before := int64(fin.TotalMoneyInCirculation())
	requireRejected(t, r.bid(t, "bid-after-refusal", potWin), defence.ErrGrantRefused)
	if _, _, n := grantsLines(fin); n != 0 {
		t.Fatalf("refused bid posted %d grants lines", n)
	}
	if int64(fin.TotalMoneyInCirculation()) != before {
		t.Fatal("money changed on a refused bid")
	}
	var p defenceWirePatch
	raw, _ := r.comp.state.buildDefencePatch()
	mustUnmarshal(t, raw, &p)
	if !p.GrantAccessRevoked || p.ReputationPenalty != 50 {
		t.Fatalf("view revoked=%v penalty=%d, want true/50", p.GrantAccessRevoked, p.ReputationPenalty)
	}
}

// --- AC-7 / double-pay: facility personnel are paid EXACTLY ONCE -------------

// The generic financeHook already pays every Employed live resident
// monthlyWageGrossPerEmployedMicropounds (public sector from the treasury via
// PostWages, private from firms), and mandate personnel ARE such residents
// (allocator ids inside baseResidentIDs, Employed). So the ONE payment path
// for personnel is the generic wage path; defence posts NO separate payroll
// (an earlier defence.payroll posting measured a 1,000,000 line ON TOP of the
// generic wage line for the same citizen-months: double pay). Procurement is
// not a citizen wage and is the facility's only defence-posted money flow.
//
// Ledger observation note: the observer runs at PhaseLandValueDecay, BEFORE
// the month's BeginMonth, so the tick log it reads holds the PREVIOUS month's
// postings; the public wage line seen in record m+1 is paid on the headcount
// recorded in record m.
func TestDefenceWire_AC7_PersonnelPaidExactlyOnce(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, true)
	st := r.comp.state
	requireAccepted(t, r.respond(t, "pay-accept", "naval-100k", "naval-base", false))
	const personnel = int64(6)
	if got := int64(len(personnelPublic(st))); got != personnel {
		t.Fatalf("personnel Employed+Public at accept = %d, want %d", got, personnel)
	}
	r.advanceMonths(t, 4)
	if len(r.months) < 4 {
		t.Fatalf("observed %d months", len(r.months))
	}
	checked := 0
	for i := 0; i+1 < len(r.months); i++ {
		m, next := r.months[i], r.months[i+1]
		// Every personnel citizen is Employed (so inside the generic headcount).
		if m.personnelEmployed != personnel {
			t.Fatalf("month %d: personnel Employed = %d, want %d", m.month, m.personnelEmployed, personnel)
		}
		// The public wage line paid for month m is exactly publicEmployed*wage
		// (personnel who are SectorPublic are INSIDE that count) ...
		withP := m.publicEmployed * monthlyWageGrossPerEmployedMicropounds
		withoutP := (m.publicEmployed - m.personnelPub) * monthlyWageGrossPerEmployedMicropounds
		var nWith, nWithout int
		for _, a := range next.wagesDebits {
			if a == withP {
				nWith++
			}
			if m.personnelPub > 0 && a == withoutP {
				nWithout++
			}
		}
		if m.publicEmployed > 0 && (nWith != 1 || nWithout != 0) {
			t.Fatalf("month %d: wage lines %v: want exactly one line of %d (public headcount %d incl. %d public personnel), with=%d without=%d",
				m.month, next.wagesDebits, withP, m.publicEmployed, m.personnelPub, nWith, nWithout)
		}
		checked++
		// ... and NO second (defence) payroll path pays the same people.
		if m.defPayroll != 0 || next.defPayroll != 0 {
			t.Fatalf("month %d: defence.payroll posted (%d/%d) on top of the generic wage path: DOUBLE PAY", m.month, m.defPayroll, next.defPayroll)
		}
	}
	if checked < 3 {
		t.Fatalf("only %d month pairs checked", checked)
	}
}

// Procurement is the facility's defence-posted flow: balanced, per month, from
// the facility table, conservation clean.
func TestDefenceWire_AC7_ProcurementReachesLedger(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, true)
	requireAccepted(t, r.respond(t, "proc-accept", "naval-100k", "naval-base", false))
	r.advanceMonths(t, 3)
	const perMonth = int64(100_000)
	seen := 0
	for _, m := range r.months {
		if m.defProcure == perMonth {
			seen++
		} else if m.defProcure != 0 {
			t.Fatalf("month %d: procurement %d, want %d or 0", m.month, m.defProcure, perMonth)
		}
	}
	if seen < 2 {
		t.Fatalf("procurement observed in %d of %d months, want >= 2", seen, len(r.months))
	}
	// The monthly hook ran for months 0..2: three postings, none lost or doubled.
	if got := r.comp.state.defenceW.postedProcurement; got != 3*perMonth {
		t.Fatalf("postedProcurement counter %d, want %d", got, 3*perMonth)
	}
	// Balanced: the counterparty leg (firms credit) equals the treasury debit.
	var cred int64
	for _, l := range r.comp.state.finance.LinesByCategory(finCatDefenceProcurement) {
		if l.Account == finance.AcctFirms && l.Side == finance.SideCredit {
			cred += int64(l.Amount)
		}
	}
	if cred != perMonth { // the tick log holds one month
		t.Fatalf("firms credit leg %d, want %d", cred, perMonth)
	}
	if v := r.comp.state.finance.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation violations: %v", v)
	}
	if v := r.violations.Load(); v != 0 {
		t.Fatalf("invariant suite reported %d violations", v)
	}
}

// --- AC-8 save/load ----------------------------------------------------------

func TestDefenceWire_AC8_SaveLoadPreservesBidStreamAndDigest(t *testing.T) {
	build := func() *defenceRig { return newDefenceRig(t, defenceTestConfig(), 100_000, false) }
	prefix := func(r *defenceRig) {
		requireAccepted(t, r.respond(t, "s-accept", "naval-100k", "naval-base", false))
		// r2: one bid per pot per month, so the 5 prefix bids sit in 5
		// distinct months; the world is saved inside the 5th bid's month.
		for i := 0; i < 5; i++ {
			requireAccepted(t, r.bid(t, "s-pre", potCoin))
			if i < 4 {
				r.advanceMonths(t, 1)
			}
		}
	}
	// outcomes records (won, bidSeq) after each further bid.
	suffix := func(r *defenceRig, n int) []bool {
		var out []bool
		// The cap state survives the save: a second bid in the saved month is
		// refused in both worlds before the clock moves on.
		requireRejected(t, r.bid(t, "s-capped", potCoin), defence.ErrBidCapReached)
		r.advanceMonths(t, 1)
		for i := 0; i < n; i++ {
			requireAccepted(t, r.bid(t, "s-post", potCoin))
			out = append(out, r.comp.state.defenceW.lastBid.Won)
			r.advanceMonths(t, 1)
		}
		return out
	}

	a := build()
	prefix(a)
	digestA := a.comp.StateDigest()
	dir := t.TempDir()
	if err := a.comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, cerr := a.e.Clock()
	if cerr != nil {
		t.Fatalf("Clock: %v", cerr)
	}
	saveTick := clockA.Tick()
	want := suffix(a, 24) // uninterrupted continuation

	b := build()
	// r2: bids are month-keyed (draw stream + cap), so the loaded world must
	// resume at the saved clock: LoadAt, not the tick-0 Load.
	if err := b.comp.LoadAt(dir, saveTick); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	b.prepared = true // the loaded world already owns the mandate cell
	if digestB := b.comp.StateDigest(); digestB != digestA {
		t.Fatalf("StateDigest did not round-trip: A=%x B=%x", digestA, digestB)
	}
	if b.comp.state.defenceW.api.BidSeq() != 5 {
		t.Fatalf("loaded bidSeq = %d, want 5", b.comp.state.defenceW.api.BidSeq())
	}
	got := suffix(b, 24)
	wins := 0
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("post-load bid draw %d diverged: uninterrupted=%v loaded=%v (want %v got %v)", i, want[i], got[i], want, got)
		}
		if want[i] {
			wins++
		}
	}
	if wins == 0 || wins == len(want) {
		t.Fatalf("degenerate draw sequence (wins=%d/%d): the comparison cannot detect a stream reset", wins, len(want))
	}
	// The one-shot answer survives the load: re-answering is still refused.
	requireRejected(t, b.respond(t, "s-again", "naval-100k", "naval-base", false), defence.ErrMandateAlreadyResponded)
}

func mustUnmarshal(t *testing.T, raw []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
