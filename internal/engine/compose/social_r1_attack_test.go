package compose

// Destructive-F834-social-r1 attack tests (throwaway; NOT the author's).

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/social"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func r1Wire(t *testing.T, mode string) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(77), core.WithPoolSize(1))
	c, err := Wire(e, &Deps{GameMode: mode, Coastal: quietCoastal(t)})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, c
}

func r1SupportDebit(fin *finance.FinanceAPI) (d, c int64) {
	for _, l := range fin.LinesByCategory(finCatSocialSupport) {
		if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit {
			d += int64(l.Amount)
		}
		if l.Account == finance.AcctHouseholds && l.Side == finance.SideCredit {
			c += int64(l.Amount)
		}
	}
	return
}

// Attack 1: real-mode skip leaves zero partial state, no catch-up afterwards.
func TestR1Attack_RealModeSkipNoPartialNoCatchUp(t *testing.T) {
	e, c := r1Wire(t, "")
	advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	st := c.state
	fin := st.finance
	tre := ledgerBalance(fin, finance.AcctTreasury)
	if tre <= 0 {
		t.Skipf("treasury %d not positive", tre)
	}
	money0 := fin.TotalMoneyInCirculation()
	posted0 := c.SocialPostedMicropounds()
	d0, c0 := r1SupportDebit(fin)
	if st.postSocialSupport(99, tre+1) {
		t.Fatalf("an unaffordable support of %d (treasury %d) was posted in real mode", tre+1, tre)
	}
	d1, c1 := r1SupportDebit(fin)
	if d1 != d0 || c1 != c0 || c.SocialPostedMicropounds() != posted0 || st.socialW.lastSupport != 0 {
		t.Fatalf("partial state after a skipped posting: lines %d/%d->%d/%d posted %d->%d last %d", d0, c0, d1, c1, posted0, c.SocialPostedMicropounds(), st.socialW.lastSupport)
	}
	if fin.TotalMoneyInCirculation() != money0 || ledgerBalance(fin, finance.AcctTreasury) != tre {
		t.Fatal("money moved on a skipped posting")
	}
	if !st.postSocialSupport(100, 1000) {
		t.Fatal("affordable posting failed")
	}
	if got := c.SocialPostedMicropounds() - posted0; got != 1000 {
		t.Fatalf("next funded month posted %d, want exactly 1000 (no catch-up)", got)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("violations %+v", v)
	}
}

// Attack 1b: overflow-scale per-case constant must not print money / break conservation.
func TestR1Attack_OverflowSupportConstant(t *testing.T) {
	e, c := r1Wire(t, "unlimited")
	advanceInChunks(t, e, 3*int64(core.DailyTicksPerMonth))
	st := c.state
	st.socialW.data.SupportPerOpenCaseMicropounds = math.MaxInt64
	money0 := st.finance.TotalMoneyInCirculation()
	clock, _ := e.Clock()
	st.runSocial(socialEffect{month: clock.Month() + 1})
	if st.finance.TotalMoneyInCirculation() != money0 {
		t.Fatalf("money stock changed %d -> %d", money0, st.finance.TotalMoneyInCirculation())
	}
	if v := st.finance.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("violations %+v", v)
	}
	t.Logf("posted=%d last=%d failed=%d", c.SocialPostedMicropounds(), st.socialW.lastSupport, st.socialW.failedMonths)
}

// Attack 1c: month replay (same month twice, and after a save/load) never double-posts.
func TestR1Attack_ReplayNoDoublePost(t *testing.T) {
	e, c := r1Wire(t, "unlimited")
	advanceInChunks(t, e, 3*int64(core.DailyTicksPerMonth))
	st := c.state
	last := st.socialW.lastMonth
	p0 := c.SocialPostedMicropounds()
	st.runSocial(socialEffect{month: last})
	if c.SocialPostedMicropounds() != p0 {
		t.Fatal("replayed month double-posted")
	}
	dir := t.TempDir()
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, _ := e.Clock()
	e2 := core.NewEngine(core.WithWorldSeed(77), core.WithPoolSize(1))
	c2, err := Wire(e2, &Deps{GameMode: "unlimited", Coastal: quietCoastal(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if c2.state.socialW.lastMonth != last || c2.SocialPostedMicropounds() != p0 {
		t.Fatalf("load: last %d posted %d, want %d %d", c2.state.socialW.lastMonth, c2.SocialPostedMicropounds(), last, p0)
	}
	c2.state.runSocial(socialEffect{month: last})
	if c2.SocialPostedMicropounds() != p0 {
		t.Fatal("replay after load double-posted")
	}
}

// Attack 3: gate bypass routes over the command seam.
func TestR1Attack_GateBypassRoutes(t *testing.T) {
	e, c := r1Wire(t, "")
	const id = "social.homelessness"
	cur, _ := c.state.services.FundingLevel(id)
	send := func(sid string, lvl float64) protocol.CommandResult {
		return e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1", Kind: protocol.KindSetFunding,
			Payload: protocol.SetFundingPayload{ServiceID: sid, Level: lvl}})
	}
	for _, sid := range []string{"Social.homelessness", "SOCIAL.HOMELESSNESS", " social.homelessness", "social.homelessness ", "social.Homelessness", "social.homelessness\x00", "social.", "social"} {
		if r := send(sid, 0); r.Accepted {
			t.Errorf("variant %q accepted a cut", sid)
		}
	}
	// math.Copysign, not the literal -0.0: Go constant-folds -0.0 to +0
	// (staticcheck SA4026), so the literal never probed negative zero.
	for _, lvl := range []float64{math.NaN(), math.Inf(-1), math.Copysign(0, -1), -1, math.SmallestNonzeroFloat64} {
		_ = send(id, lvl)
	}
	if after, _ := c.state.services.FundingLevel(id); after != cur {
		t.Fatalf("funding moved %v -> %v via variant/degenerate commands", cur, after)
	}
	// no-change then cut; raise then cut back.
	if r := send(id, cur); !r.Accepted {
		t.Fatalf("no-change refused: %+v", r.Error)
	}
	if r := send(id, cur-0.01); r.Accepted {
		t.Fatal("cut after no-change accepted")
	}
	if r := send(id, cur+0.2); !r.Accepted {
		t.Fatalf("raise refused: %+v", r.Error)
	}
	if r := send(id, cur); r.Accepted {
		t.Fatal("cut back after a raise accepted")
	}
	for _, cat := range socialCategories {
		sid := "social." + cat.String()
		lv, _ := c.state.services.FundingLevel(services.ServiceID(sid))
		if r := send(sid, lv/2); r.Accepted && lv > 0 {
			t.Errorf("%s cut accepted", sid)
		}
	}
}

// Attack 4/6: ImportState fail-closed on real social.
func TestR1Attack_ImportStateCorruptions(t *testing.T) {
	_, c := r1Wire(t, "unlimited")
	api := c.state.socialW.api
	open := func(id social.CaseID) social.Case {
		return social.Case{ID: id, Category: social.CategoryHomelessness, ClosedMonth: -1, Status: social.StatusOpen}
	}
	cases := map[string]social.State{
		"badCategory":    {Cases: []social.Case{{ID: 1, Category: 99, ClosedMonth: -1}}, NextCaseID: 2},
		"duplicateID":    {Cases: []social.Case{open(1), open(1)}, NextCaseID: 2},
		"descendingID":   {Cases: []social.Case{open(2), open(1)}, NextCaseID: 3},
		"openWithClosed": {Cases: []social.Case{{ID: 1, Category: 1, ClosedMonth: 4, Status: social.StatusOpen}}, NextCaseID: 2},
		"closedNoMonth":  {Cases: []social.Case{{ID: 1, Category: 1, ClosedMonth: -1, Status: social.StatusResolved}}, NextCaseID: 2},
		"idAtNext":       {Cases: []social.Case{open(2)}, NextCaseID: 2},
	}
	for name, s := range cases {
		if err := api.ImportState(s); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Probes (reported, not asserted): fields ImportState does not validate.
	probes := map[string]social.State{
		"unknownStatus":     {Cases: []social.Case{{ID: 1, Category: 1, ClosedMonth: 3, Status: 9}}, NextCaseID: 2},
		"closedBeforeOpen":  {Cases: []social.Case{{ID: 1, Category: 1, OpenedMonth: 10, ClosedMonth: 3, Status: social.StatusResolved}}, NextCaseID: 2},
		"citizenKeyed":      {Cases: []social.Case{{ID: 1, Category: 1, ClosedMonth: -1, CitizenID: 42}}, NextCaseID: 2},
		"negativeOccupancy": {HostelOccupancy: -5, NextCaseID: 1},
		"caseIDZero":        {Cases: []social.Case{open(0)}, NextCaseID: 1},
	}
	for name, s := range probes {
		t.Logf("PROBE %s accepted=%v", name, api.ImportState(s) == nil)
	}
}
