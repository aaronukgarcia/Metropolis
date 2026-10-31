package compose

// Destructive round r2 (FEAT-2326609836) attacker probes — throwaway, not for landing.

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/defence"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func f836r2Clock(t *testing.T, r *defenceRig) (tick, month int64) {
	t.Helper()
	c, err := r.e.Clock()
	if err != nil {
		t.Fatal(err)
	}
	return c.Tick(), c.Month()
}

func f836r2Seq(r *defenceRig) uint64 { return r.comp.state.defenceW.api.BidSeq() }

func f836r2Bid(t *testing.T, r *defenceRig, name, pot string, match int64) protocol.CommandResult {
	return r.cmd(t, name, protocol.KindBidForGrant, protocol.BidForGrantPayload{Pot: pot, MatchFundingMicropounds: match})
}

// A1: cap survives a mandate response; different pots each once; refusals don't advance bidSeq.
func TestR2Attack_CapAcrossMandateAndPots(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	advanceInChunks(t, r.e, 3)
	requireAccepted(t, r.bid(t, "a1", potWin))
	requireAccepted(t, r.respond(t, "a1-m", "naval-100k", "naval-base", false))
	s := f836r2Seq(r)
	requireRejected(t, r.bid(t, "a1-again", potWin), defence.ErrBidCapReached)
	if f836r2Seq(r) != s {
		t.Fatalf("cap refusal advanced bidSeq")
	}
	requireAccepted(t, r.bid(t, "a1-lose", potLose))
	requireAccepted(t, r.bid(t, "a1-coin", potCoin))
	requireRejected(t, r.bid(t, "a1-lose2", potLose), defence.ErrBidCapReached)
	requireRejected(t, r.bid(t, "a1-coin2", potCoin), defence.ErrBidCapReached)
	if f836r2Seq(r) != s+2 {
		t.Fatalf("bidSeq %d want %d", f836r2Seq(r), s+2)
	}
}

// A2: bid, save, LoadAt an EARLIER tick in the same month: still refused.
func TestR2Attack_LoadAtEarlierTickSameMonth(t *testing.T) {
	a := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	advanceInChunks(t, a.e, 5)
	tick0, m0 := f836r2Clock(t, a)
	requireAccepted(t, a.bid(t, "a2", potWin))
	advanceInChunks(t, a.e, 3)
	tick1, m1 := f836r2Clock(t, a)
	if m1 != m0 {
		t.Fatalf("setup crossed a month")
	}
	dir := t.TempDir()
	if err := a.comp.Save(dir); err != nil {
		t.Fatal(err)
	}
	b := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	if err := b.comp.LoadAt(dir, tick0); err != nil {
		t.Fatalf("LoadAt earlier (%d < %d): %v", tick0, tick1, err)
	}
	if _, mb := f836r2Clock(t, b); mb != m0 {
		t.Fatalf("month after LoadAt %d want %d", mb, m0)
	}
	tre := b.comp.state.finance
	before, _ := tre.AccountBalance(finance.AcctTreasury)
	requireRejected(t, b.bid(t, "a2-b", potWin), defence.ErrBidCapReached)
	after, _ := tre.AccountBalance(finance.AcctTreasury)
	if before != after || f836r2Seq(b) != 1 {
		t.Fatalf("loaded world minted: %d->%d seq %d", before, after, f836r2Seq(b))
	}
}

// A3: strip bidMonths from the save record: does restore fail closed?
func TestR2Attack_StrippedBidMonthsRecord(t *testing.T) {
	a := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	advanceInChunks(t, a.e, 3)
	requireAccepted(t, a.bid(t, "a3", potWin))
	p := newDefenceParticipant(a.comp.state)
	rec, ok, err := p.Source()()
	if err != nil || !ok {
		t.Fatalf("source: %v %v", ok, err)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Data, &m); err != nil {
		t.Fatal(err)
	}
	snap := m["snapshot"].(map[string]any)
	t.Logf("saved bidMonths=%v bidSeq=%v", snap["bidMonths"], snap["bidSeq"])
	for _, variant := range []any{"__delete__", nil, []any{}} {
		if variant == "__delete__" {
			delete(snap, "bidMonths")
		} else {
			snap["bidMonths"] = variant
		}
		rec.Data, _ = json.Marshal(m)
		herr := p.Handler()(rec)
		t.Logf("variant %v: handler err=%v", variant, herr)
		if herr == nil {
			res := a.bid(t, "a3-after", potWin)
			t.Logf("variant %v: re-bid same month accepted=%v err=%+v", variant, res.Accepted, res.Error)
			if res.Accepted {
				t.Errorf("STRIPPED bidMonths (%v) restored default-open: second same-month bid accepted", variant)
			}
		}
	}
}

// A4: month boundary tick-by-tick: exactly one bid allowed on the first tick of M+1.
func TestR2Attack_MonthBoundaryTickByTick(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	advanceInChunks(t, r.e, 2)
	_, m0 := f836r2Clock(t, r)
	requireAccepted(t, r.bid(t, "a4", potCoin))
	accepted := 0
	for i := 0; i < int(core.DailyTicksPerMonth)+3; i++ {
		_, m := f836r2Clock(t, r)
		res := r.bid(t, "a4-spam", potCoin)
		if res.Accepted {
			accepted++
			if m != m0+1 {
				t.Fatalf("accepted in month %d (start %d)", m, m0)
			}
		} else if res.Error == nil || res.Error.Code != defence.ErrBidCapReached {
			t.Fatalf("unexpected refusal %+v", res.Error)
		}
		advanceInChunks(t, r.e, 1)
	}
	if accepted != 1 {
		t.Fatalf("accepted %d bids across one boundary, want 1", accepted)
	}
}

// A5: affordability edges.
func TestR2Attack_Affordability(t *testing.T) {
	r := newDefenceRig(t, defenceTestConfig(), 100_000, false)
	advanceInChunks(t, r.e, 3)
	fin := r.comp.state.finance
	tre, _ := fin.AccountBalance(finance.AcctTreasury)
	s := f836r2Seq(r)
	requireRejected(t, f836r2Bid(t, r, "a5-over", potWin, int64(tre)+1), defence.ErrBidUnaffordable)
	if f836r2Seq(r) != s {
		t.Fatalf("unaffordable advanced bidSeq")
	}
	// unaffordable must not consume the month's cap
	requireAccepted(t, f836r2Bid(t, r, "a5-exact", potWin, int64(tre)))
	requireRejected(t, f836r2Bid(t, r, "a5-zero-capped", potWin, 0), defence.ErrBidCapReached)
	requireAccepted(t, f836r2Bid(t, r, "a5-zero", potLose, 0))
	requireRejected(t, f836r2Bid(t, r, "a5-zero2", potLose, 0), defence.ErrBidCapReached)
	if res := f836r2Bid(t, r, "a5-neg", potCoin, -1); res.Accepted {
		t.Fatalf("negative match accepted")
	} else {
		t.Logf("negative match refused: %+v", res.Error)
	}
}
