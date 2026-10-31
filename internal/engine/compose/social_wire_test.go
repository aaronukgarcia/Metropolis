package compose

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/social"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609834: tests for engine.social wired into the composition root
// (pack docs/planning/acceptance/wire-engine.social.md). Scenarios are driven
// ONLY through compose.Wire ticks; no test calls AdvanceMonth.

const socialTestSeed = uint64(20261101)

// socialMonthRecord is one month's observation taken at the start of
// PhaseLandValueDecay (BUG-1141: lines posted on PhasePopulation are cleared
// from the per-tick log by the next BeginMonth, so they are sampled before it).
type socialMonthRecord struct {
	month        int64
	open         [5]int64 // per social category, at this month
	totalOpen    int64
	supportDebit int64 // social.support treasury-debit total this month
	supportCred  int64 // social.support households-credit total this month
	supportOther int   // social.support lines that are neither leg
	benefitsDebt int64 // benefits.unemployment treasury-debit total this month
	unemployed   int64
	violations   int
}

type socialRun struct {
	e          *core.Engine
	comp       *Composition
	records    []socialMonthRecord
	invariantV int64
}

// runSocialScenario wires a composition (unlimited mode: social support and
// fiscal benefits exceed the real-mode starting treasury), runs months with an
// optional per-month callback BEFORE each month is advanced (job shock,
// funding change), and records per-month observations.
func runSocialScenario(t *testing.T, seed uint64, months int64, before func(month int64, comp *Composition)) *socialRun {
	t.Helper()
	run := &socialRun{}
	var violations int64
	var comp *Composition
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1), core.WithPhaseObserver(func(kind core.PhaseKind, _ int64, month int64) {
		if kind != core.PhaseLandValueDecay || comp == nil || comp.state == nil || comp.state.socialW == nil {
			return
		}
		fin := comp.state.finance
		rec := socialMonthRecord{month: month}
		for i, cat := range socialCategories {
			rec.open[i], _ = comp.state.socialW.api.Caseload(cat, month)
			rec.totalOpen += rec.open[i]
		}
		for _, l := range fin.LinesByCategory(finCatSocialSupport) {
			switch {
			case l.Account == finance.AcctTreasury && l.Side == finance.SideDebit:
				rec.supportDebit += int64(l.Amount)
			case l.Account == finance.AcctHouseholds && l.Side == finance.SideCredit:
				rec.supportCred += int64(l.Amount)
			default:
				rec.supportOther++
			}
		}
		for _, l := range fin.LinesByCategory("benefits.unemployment") {
			if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
				rec.benefitsDebt += int64(l.Amount)
			}
		}
		rec.unemployed = comp.state.unemployedResidentCount()
		rec.violations = len(fin.FindConservationViolations())
		run.records = append(run.records, rec)
	}))
	c, err := Wire(e, &Deps{
		GameMode: "unlimited", Coastal: quietCoastal(t),
		InvariantOpts: []invariant.HookOption{invariant.WithLogSink(func(*errs.E) { violations++ })},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	comp = c
	for m := int64(0); m < months; m++ {
		if before != nil {
			before(m, comp)
		}
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	run.e, run.comp, run.invariantV = e, c, violations
	return run
}

// --- AC-1: reachability --------------------------------------------------

func TestSocialWire_AC1_Reachability(t *testing.T) {
	idx := func(n string) int {
		for i, v := range RegistrationOrder() {
			if v == n {
				return i
			}
		}
		return -1
	}
	if idx("social") < 0 {
		t.Fatalf("RegistrationOrder() = %v lacks the social monthly hook", RegistrationOrder())
	}
	if idx("social") != idx("coastal")+1 {
		t.Fatalf("social must sit directly after coastal: %v", RegistrationOrder())
	}
	found := false
	for _, n := range RegisteredViewNames() {
		if n == socialViewSubscriptionName {
			found = true
		}
	}
	if !found {
		t.Fatalf("RegisteredViewNames() = %v lacks %q", RegisteredViewNames(), socialViewSubscriptionName)
	}

	r := runSocialScenario(t, socialTestSeed, 3, nil)
	if got := r.e.HookCount(); got != BaselineOneHookCount() {
		t.Fatalf("HookCount() = %d, want %d", got, BaselineOneHookCount())
	}
	st := r.comp.state
	for _, cat := range socialCategories {
		if _, err := st.services.FundingLevel(services.ServiceID("social." + cat.String())); err != nil {
			t.Fatalf("social category %s not registered with engine.services: %v", cat, err)
		}
	}
	raw, err := st.buildSocialPatch()
	if err != nil {
		t.Fatalf("buildSocialPatch: %v", err)
	}
	var patch socialWirePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if len(patch.Categories) != 5 {
		t.Fatalf("f4.social carries %d categories, want 5: %s", len(patch.Categories), raw)
	}
	var total int64
	for _, c := range patch.Categories {
		total += c.Open
	}
	if total == 0 {
		t.Fatalf("f4.social shows zero open cases after 3 months: %s", raw)
	}
	// projections is the ONE instance buildPolicies constructed: social's
	// curve provider is registered on it, so a second registration collides.
	err = st.pol.proj.RegisterCurveProvider("social.caseload", projections.CurveProviderFunc(func(int64) (float64, error) { return 0, nil }))
	if err == nil {
		t.Fatal("social.caseload is NOT registered on the shared projections instance (social built its own or none)")
	}
}

// --- AC-2: steady state is quiet, deterministic, conserved ------------------

func TestSocialWire_AC2_SteadyStateDeterministic(t *testing.T) {
	a := runSocialScenario(t, socialTestSeed, 12, nil)
	b := runSocialScenario(t, socialTestSeed, 12, nil)
	if len(a.records) != 12 || len(b.records) != 12 {
		t.Fatalf("records = %d / %d, want 12", len(a.records), len(b.records))
	}
	if !reflect.DeepEqual(a.records, b.records) {
		t.Fatalf("same-seed runs diverged:\nA %+v\nB %+v", a.records, b.records)
	}
	if a.comp.StateDigest() != b.comp.StateDigest() {
		t.Fatal("same-seed StateDigest differs")
	}
	last := a.records[11]
	if last.totalOpen <= 0 {
		t.Fatalf("steady-state caseload is zero")
	}
	if last.totalOpen > 200 {
		t.Fatalf("steady-state caseload %d is not bounded (closure by duration should cap it)", last.totalOpen)
	}
	if a.invariantV != 0 {
		t.Fatalf("%d invariant (people/money conservation) violations", a.invariantV)
	}
	for _, rec := range a.records {
		if rec.violations != 0 {
			t.Fatalf("month %d: %d ledger conservation violations", rec.month, rec.violations)
		}
	}
}

// --- Scenario D --------------------------------------------------------------

const (
	socialShockMonth = 12
	socialEndMonths  = 36
)

func runScenarioD(t *testing.T, funding float64) *socialRun {
	t.Helper()
	return runSocialScenario(t, socialTestSeed, socialEndMonths, func(m int64, comp *Composition) {
		if m == 0 {
			for _, cat := range socialCategories {
				if err := comp.state.services.SetFunding(services.ServiceID("social."+cat.String()), funding); err != nil {
					t.Fatalf("fixture SetFunding: %v", err)
				}
			}
		}
		if m == socialShockMonth {
			if n := injectJobShock(t, comp, 0.9); n == 0 {
				t.Fatal("job shock hit nobody; fixture invalid")
			}
		}
	})
}

// --- AC-3: the downturn raises caseload ---------------------------------------

// peak returns the maximum of f over records with from <= month <= to.
func (r *socialRun) peak(from, to int64, f func(socialMonthRecord) int64) int64 {
	var m int64
	for _, rec := range r.records {
		if rec.month >= from && rec.month <= to && f(rec) > m {
			m = f(rec)
		}
	}
	return m
}

// The labour market re-employs the shocked citizens within a few months (the
// engine's own dynamics), so the downturn shows as a PEAK in the post-shock
// window over the steady pre-shock level, not as a persistent month-36 plateau.
func TestSocialWire_AC3_DownturnRaisesCaseload(t *testing.T) {
	a := runScenarioD(t, 1.0)
	homeless := func(r socialMonthRecord) int64 { return r.open[1] }
	total := func(r socialMonthRecord) int64 { return r.totalOpen }
	unemployed := func(r socialMonthRecord) int64 { return r.unemployed }
	preH, postH := a.peak(4, socialShockMonth, homeless), a.peak(socialShockMonth+1, socialEndMonths, homeless)
	if postH <= preH {
		t.Fatalf("homelessness peak %d after the shock must exceed the pre-shock peak %d", postH, preH)
	}
	preT, postT := a.peak(4, socialShockMonth, total), a.peak(socialShockMonth+1, socialEndMonths, total)
	if postT <= preT {
		t.Fatalf("total open peak %d after the shock must exceed pre-shock %d", postT, preT)
	}
	if pu, qu := a.peak(1, socialShockMonth, unemployed), a.peak(socialShockMonth+1, socialEndMonths, unemployed); qu <= pu {
		t.Fatalf("fixture: unemployed peak %d did not rise over %d", qu, pu)
	}
}

// --- AC-4: a funded net makes the decline shallower ---------------------------

func TestSocialWire_AC4_FundedNetIsShallower(t *testing.T) {
	a := runScenarioD(t, 1.0)
	b := runScenarioD(t, 0.0)
	var peakA, peakB int64
	for _, r := range a.records {
		if r.totalOpen > peakA {
			peakA = r.totalOpen
		}
	}
	for _, r := range b.records {
		if r.totalOpen > peakB {
			peakB = r.totalOpen
		}
	}
	if peakA >= peakB {
		t.Fatalf("funded peak open caseload %d must be lower than minimum-funding peak %d", peakA, peakB)
	}
}

// --- AC-5: observable transfer in the one-money ledger ------------------------

func TestSocialWire_AC5_BalancedSupportTransfer(t *testing.T) {
	a := runScenarioD(t, 1.0)
	for _, r := range a.records {
		if r.supportOther != 0 {
			t.Fatalf("month %d: %d social.support lines are neither treasury-debit nor households-credit", r.month, r.supportOther)
		}
		if r.supportDebit != r.supportCred {
			t.Fatalf("month %d: social.support unbalanced debit %d credit %d", r.month, r.supportDebit, r.supportCred)
		}
		if r.violations != 0 {
			t.Fatalf("month %d: %d ledger conservation violations", r.month, r.violations)
		}
	}
	support := func(r socialMonthRecord) int64 { return r.supportDebit }
	pre := a.peak(4, socialShockMonth, support)
	if pre == 0 {
		t.Fatal("no social.support lines were posted pre-shock")
	}
	if shock := a.peak(socialShockMonth+1, socialEndMonths, support); shock <= pre {
		t.Fatalf("shock-window support peak %d must exceed the pre-shock peak %d", shock, pre)
	}
	if a.invariantV != 0 {
		t.Fatalf("%d invariant violations", a.invariantV)
	}
	if got := a.comp.SocialPostedMicropounds(); got <= 0 {
		t.Fatalf("SocialPostedMicropounds = %d", got)
	}

	// The posting itself is money-neutral (internal transfer).
	st := a.comp.state
	fin := st.finance
	before := fin.TotalMoneyInCirculation()
	if !st.postSocialSupport(a.records[len(a.records)-1].month, 123456789) {
		t.Fatal("direct postSocialSupport failed")
	}
	if after := fin.TotalMoneyInCirculation(); after != before {
		t.Fatalf("TotalMoneyInCirculation %d -> %d across a social support posting", before, after)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("conservation violations after direct posting: %+v", v)
	}
}

// --- AC-6: no double count with fiscal benefits --------------------------------

func TestSocialWire_AC6_NoDoubleCountWithFiscalBenefits(t *testing.T) {
	a := runScenarioD(t, 1.0)
	st := a.comp.state
	per := int64(st.fiscal.UnemploymentSupportPerPerson())
	cost := st.socialW.data.SupportPerOpenCaseMicropounds
	checked := 0
	for _, r := range a.records {
		// fiscal owns unemployment support: headcount x per-person, nothing else.
		// The line is a whole multiple of the per-person amount (sampled before
		// the next fiscal hook, so the headcount at posting time is not the
		// sampled one, hence no exact headcount equality).
		if per > 0 && r.benefitsDebt%per != 0 {
			t.Fatalf("month %d: benefits.unemployment %d is not a multiple of the fiscal per-person amount %d (social posted into it?)", r.month, r.benefitsDebt, per)
		}
		// social owns caseload support: open cases x cost (funding 1.0), nothing else.
		if want := r.totalOpen * cost; r.supportDebit != want {
			t.Fatalf("month %d: social.support %d != open %d x %d", r.month, r.supportDebit, r.totalOpen, cost)
		}
		if r.supportDebit > 0 && r.benefitsDebt > 0 {
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no month carried both owners' postings; the scenario does not exercise the split")
	}
	// social never keys a case to a citizen, so it cannot pay per-citizen unemployment support.
	for _, c := range st.socialW.api.ExportState().Cases {
		if c.CitizenID != 0 {
			t.Fatalf("social case %d is keyed to citizen %d: per-citizen support would double count fiscal", c.ID, c.CitizenID)
		}
	}
	if d, _, _ := benefitLineTotal(st.finance, "benefits.housing"); d != 0 {
		t.Fatalf("benefits.housing has %d posted; nobody owns it yet", d)
	}
}

// --- AC-7: a funding cut cannot be laundered through the command seam ---------

func TestSocialWire_AC7_CutRefusedOverCommandSeam(t *testing.T) {
	// Real social (NOT newTestEngine's quiet social): a cut needs a non-zero
	// funding level to cut from.
	e := core.NewEngine(core.WithWorldSeed(31), core.WithPoolSize(1))
	comp, werr := Wire(e, &Deps{Coastal: quietCoastal(t)})
	if werr != nil {
		t.Fatalf("Wire: %v", werr)
	}
	const id = "social.homelessness"
	before, err := comp.state.services.FundingLevel(id)
	if err != nil {
		t.Fatalf("FundingLevel: %v", err)
	}
	cut := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "social-cut", Kind: protocol.KindSetFunding,
		Payload: protocol.SetFundingPayload{ServiceID: id, Level: before / 2},
	})
	if cut.Accepted {
		t.Fatal("an unprojected social funding cut was accepted over the raw command seam")
	}
	if cut.Error == nil || cut.Error.Code != ErrSocialFundingCutRefused {
		t.Fatalf("cut error = %+v, want %s", cut.Error, ErrSocialFundingCutRefused)
	}
	if after, _ := comp.state.services.FundingLevel(id); after != before {
		t.Fatalf("funding changed %v -> %v despite the refusal", before, after)
	}
	// A non-cut (raise / no change) goes through social's own validated path.
	same := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "social-same", Kind: protocol.KindSetFunding,
		Payload: protocol.SetFundingPayload{ServiceID: id, Level: before},
	})
	if !same.Accepted {
		t.Fatalf("non-cut funding command rejected: %+v", same.Error)
	}
}

// --- AC-8: crisis injection through the composed path: N/A -----------------------
// No protocol command exposes InjectCrisis; the 64-char/empty id bound and the
// ring cap are covered at package level (social/destructive_regression_test.go).

// --- AC-9: save/load round-trips social state ------------------------------------

func TestSocialWire_AC9_SaveLoadRoundTrip(t *testing.T) {
	a := runScenarioD(t, 1.0)
	// month 36 state includes closed + open cases and posted counters.
	dir := t.TempDir()
	if err := a.comp.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	digestA := a.comp.StateDigest()
	stateA := a.comp.state.socialW.api.ExportState()
	if len(stateA.Cases) == 0 {
		t.Fatal("precondition: no social cases to round-trip")
	}

	eB := core.NewEngine(core.WithWorldSeed(socialTestSeed), core.WithPoolSize(1))
	compB, err := Wire(eB, &Deps{GameMode: "unlimited", Coastal: quietCoastal(t)})
	if err != nil {
		t.Fatalf("Wire B: %v", err)
	}
	clockA, err := a.e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if !reflect.DeepEqual(compB.state.socialW.api.ExportState(), stateA) {
		t.Fatal("social ledger/state did not round-trip")
	}
	if compB.SocialPostedMicropounds() != a.comp.SocialPostedMicropounds() {
		t.Fatalf("posted counter %d != %d", compB.SocialPostedMicropounds(), a.comp.SocialPostedMicropounds())
	}
	if compB.StateDigest() != digestA {
		t.Fatalf("StateDigest did not round-trip: A=%x B=%x", digestA, compB.StateDigest())
	}
}

// --- state validation -----------------------------------------------------------

func TestSocialWire_ImportStateRejectsCorruptLedger(t *testing.T) {
	_, comp := newTestEngine(t, 41)
	bad := social.State{Cases: []social.Case{{ID: 5, Category: social.CategoryHomelessness, ClosedMonth: -1, Status: social.StatusOpen}}, NextCaseID: 3}
	if err := comp.state.socialW.api.ImportState(bad); err == nil {
		t.Fatal("ImportState accepted a ledger with a case id >= NextCaseID")
	}
}

// --- r2 rework: real-mode (default GameMode) support posting ------------------

// TestSocialWire_RealMode_SupportPostsEveryMonth runs the DEFAULT game mode (a
// finite treasury) for 12 months: every month with open cases posts a social
// support line (no G5491 skip at the rescaled per-case constant), the treasury
// is never driven to zero, the ledger conserves, and the monthly posting is
// exactly open-cases x per-case constant x funding (derived from the data
// constant, not a hard-coded sum).
func TestSocialWire_RealMode_SupportPostsEveryMonth(t *testing.T) {
	type rec struct {
		month, open, debit, cred, treasury int64
		viol                               int
	}
	var recs []rec
	var comp *Composition
	var violations int64
	e := core.NewEngine(core.WithWorldSeed(socialTestSeed), core.WithPoolSize(1), core.WithPhaseObserver(func(kind core.PhaseKind, _ int64, month int64) {
		if kind != core.PhaseLandValueDecay || comp == nil || comp.state == nil || comp.state.socialW == nil {
			return
		}
		fin := comp.state.finance
		r := rec{month: month}
		for _, cat := range socialCategories {
			n, _ := comp.state.socialW.api.Caseload(cat, month)
			r.open += n
		}
		for _, l := range fin.LinesByCategory(finCatSocialSupport) {
			if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
				r.debit += int64(l.Amount)
			}
			if l.Account == finance.AcctHouseholds && l.Side == finance.SideCredit {
				r.cred += int64(l.Amount)
			}
		}
		r.treasury = int64(ledgerBalance(fin, finance.AcctTreasury))
		r.viol = len(fin.FindConservationViolations())
		recs = append(recs, r)
	}))
	c, err := Wire(e, &Deps{
		Coastal:       quietCoastal(t), // default GameMode: the real finite treasury
		InvariantOpts: []invariant.HookOption{invariant.WithLogSink(func(*errs.E) { violations++ })},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	comp = c
	for m := 0; m < 12; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	if len(recs) < 12 {
		t.Fatalf("observed %d months, want >= 12", len(recs))
	}
	perCase := comp.state.socialW.data.SupportPerOpenCaseMicropounds
	posted, openMonths := 0, 0
	for _, r := range recs {
		t.Logf("month %2d open=%3d supportDebit=%10d credit=%10d treasury=%d", r.month, r.open, r.debit, r.cred, r.treasury)
		if r.viol != 0 {
			t.Fatalf("month %d: %d conservation violations", r.month, r.viol)
		}
		if r.debit != r.cred {
			t.Fatalf("month %d: unbalanced support debit %d credit %d", r.month, r.debit, r.cred)
		}
		if r.treasury <= 0 {
			t.Fatalf("month %d: treasury %d drained to/below zero by social support", r.month, r.treasury)
		}
		if r.open > 0 {
			openMonths++
			if r.debit > 0 {
				posted++
			}
			// Funding scales the amount into [0, open*perCase]; never above it.
			if r.debit > r.open*perCase {
				t.Fatalf("month %d: support %d exceeds open(%d) x perCase(%d)", r.month, r.debit, r.open, perCase)
			}
		}
	}
	if openMonths == 0 {
		t.Fatal("no month had open cases in the real-mode run (scenario proves nothing)")
	}
	if posted != openMonths {
		t.Fatalf("support posted in %d of %d months with open cases (a G5491 skip or zero posting happened)", posted, openMonths)
	}
	if got := c.state.socialW.failedMonths; got != 0 {
		t.Fatalf("failedMonths = %d, want 0 (no G5491 skips at the rescaled amount)", got)
	}
	if violations != 0 {
		t.Fatalf("%d invariant violations", violations)
	}
}

// writeSocialWireData writes a social_wire.json (shipped values with the
// per-case constant replaced) into a temp data dir and points the loader at it.
func writeSocialWireData(t *testing.T, perCase int64) {
	t.Helper()
	cid := errs.NewCorrelationID()
	realDir, err := data.ResolveDataDir(cid)
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(realDir, socialWireFile))
	if err != nil {
		t.Fatalf("read shipped %s: %v", socialWireFile, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m["supportPerOpenCaseMicropounds"] = perCase
	out, _ := json.Marshal(m)
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, socialWireFile), out, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("METROPOLIS_DATA_DIR", dst)
}

func TestSocialWire_SupportPerCaseUpperBound(t *testing.T) {
	// The shipped value loads and sits inside the cap.
	shipped, err := loadSocialWireData("test-shipped")
	if err != nil {
		t.Fatalf("shipped social_wire.json rejected: %v", err)
	}
	if shipped.SupportPerOpenCaseMicropounds <= 0 || shipped.SupportPerOpenCaseMicropounds > socialSupportPerCaseCapMicropounds {
		t.Fatalf("shipped per-case %d outside (0, %d]", shipped.SupportPerOpenCaseMicropounds, socialSupportPerCaseCapMicropounds)
	}
	for _, bad := range []int64{socialSupportPerCaseCapMicropounds + 1, 200000000, math.MaxInt64, -1} {
		writeSocialWireData(t, bad)
		if _, err := loadSocialWireData("test-bad"); err == nil {
			t.Fatalf("per-case %d accepted, want refusal", bad)
		}
	}
	writeSocialWireData(t, socialSupportPerCaseCapMicropounds) // the cap itself is allowed
	if _, err := loadSocialWireData("test-cap"); err != nil {
		t.Fatalf("per-case == cap refused: %v", err)
	}
}
