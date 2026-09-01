package converge

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// attack_phase3_inc2_orientation_test.go — FEAT-1972079936 Phase-3 inc2,
// independent destructive round r1 (Opus). Kept as a permanent regression
// for two findings the round raised against the increment's own tests and
// against docs/planning/phase3-finance-ab-report-2026-09-01.md.
//
// FINDING A (report §2 columns inverted). finance_ab_test.go calls
// Compare("finance", goTraj, tsTraj, ...) and compare.go's signature is
// Compare(domain string, ref, candidate Trajectory, ...) — so in every
// emitted FieldDiff, Ref is the GO composed-engine value and Got is the
// TS webconsole fixture value. The r1 report's per-month table labelled
// the Ref column "TS reference" and the Got column "Go candidate", i.e.
// exactly backwards, and its surrounding prose ("the TS side barely
// moves … while the Go side swings") inverted the story with it. Nothing
// in the increment's own tests pinned the orientation, so the mistake was
// invisible to the gates. This test pins it mechanically: whatever the
// numbers become, Ref must track the Go trajectory and Got the TS fixture.
//
// FINDING B (the Go treasury is insensitive to the journal's gameplay).
// A mutation that deleted the bridge's entire "zone" case — no KindBuy,
// no KindZone ever issued — produced a byte-identical Go trajectory and
// was caught by NO test. compose.go's KindBuy path calls
// world.PurchaseTile, which never touches simState.treasury, so report
// §3.3's "Buy-before-Zone spend the Go side pays" is not a real
// contributor to any delta in the table. TestAttack_..._GameplayIsInert
// records that as a measured fact with an explicit failure message, so
// the day financeHook (or the build/world seam) starts charging for a
// tile or a zone, this test goes RED and forces §3.3 to be rewritten
// from evidence rather than from plausibility.

// TestAttack_Phase3Inc2_CompareOrientation_RefIsGo proves which side of
// every FieldDiff is which, so a future reader of the A/B report can
// check its table against a mechanical assertion instead of prose.
func TestAttack_Phase3Inc2_CompareOrientation_RefIsGo(t *testing.T) {
	goTraj, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	_, tsTraj, err := LoadFixture(webconsoleFixture)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	report := Compare("finance", goTraj, tsTraj, financeABContract)
	if len(report.Diffs) == 0 {
		t.Fatal("expected a non-empty report to inspect orientation on")
	}

	goByTick := map[int64]int64{}
	for _, s := range goTraj {
		goByTick[s.Tick] = s.Values["treasury"]
	}
	tsByTick := map[int64]int64{}
	for _, s := range tsTraj {
		tsByTick[s.Tick] = s.Values["treasury"]
	}

	for _, d := range report.Diffs {
		if d.Field != "treasury" {
			continue
		}
		if want, ok := goByTick[d.Tick]; !ok || d.Ref != want {
			t.Fatalf("tick %d: diff.Ref=%d but the GO composed trajectory says %d — "+
				"Compare(domain, ref, candidate) is called with goTraj as ref, so Ref is ALWAYS the Go value. "+
				"Any report table labelling the ref column \"TS\" is inverted.", d.Tick, d.Ref, want)
		}
		if want, ok := tsByTick[d.Tick]; !ok || d.Got != want {
			t.Fatalf("tick %d: diff.Got=%d but the TS webconsole fixture says %d — "+
				"Got is ALWAYS the TS value under this call.", d.Tick, d.Got, want)
		}
		if d.Delta != d.Got-d.Ref {
			t.Fatalf("tick %d: delta=%d but got-ref=%d", d.Tick, d.Delta, d.Got-d.Ref)
		}
	}
}

// TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury measures, rather than
// assumes, how much of the Go treasury trajectory the journal's gameplay
// commands actually move. It re-derives the zero-gameplay Go trajectory by
// replaying only the advance segments through the same composed engine and
// asserts it equals the full journal's trajectory — the state of the world
// r1 measured. See FINDING B above: this is deliberately a tripwire, not an
// endorsement. When it fails, report §3.3 must be re-derived.
func TestAttack_Phase3Inc2_GameplayIsInert_ForTreasury(t *testing.T) {
	full, _, err := RunFinanceActionsComposed(actionsListPath)
	if err != nil {
		t.Fatalf("RunFinanceActionsComposed: %v", err)
	}
	list, err := loadActionList(actionsListPath)
	if err != nil {
		t.Fatalf("loadActionList: %v", err)
	}
	gameplayOps := 0
	for _, a := range list.Actions {
		if a.Op == "zone" {
			gameplayOps++
		}
	}
	if gameplayOps == 0 {
		t.Fatal("the canonical journal has no gameplay ops at all — this test is vacuous; fix the fixture")
	}

	bare, err := runAdvanceOnlyForAttack(list)
	if err != nil {
		t.Fatalf("advance-only replay: %v", err)
	}
	if len(bare) != len(full) {
		t.Fatalf("sample-count mismatch: bare=%d full=%d", len(bare), len(full))
	}
	for i := range full {
		if full[i].Tick != bare[i].Tick {
			t.Fatalf("sample %d tick mismatch: full=%d bare=%d", i, full[i].Tick, bare[i].Tick)
		}
		gotFull := full[i].Values["treasury"]
		gotBare := bare[i].Values["treasury"]
		if gotFull != gotBare {
			t.Fatalf("tick %d: treasury WITH the %d gameplay commands = %d, WITHOUT them = %d (delta %d). "+
				"r1 measured these as identical (world.PurchaseTile and build.SubmitZoneCommand never debit "+
				"simState.treasury). This test going red is GOOD NEWS — it means the Go gameplay economy is "+
				"now real — but docs/planning/phase3-finance-ab-report-2026-09-01.md §3.3 must be re-derived "+
				"from the new numbers rather than left asserting an unmeasured Buy cost.",
				full[i].Tick, gameplayOps, gotFull, gotBare, gotFull-gotBare)
		}
	}
	t.Logf("measured: %d gameplay commands move the sampled Go treasury by exactly 0 milli-pounds at every checkpoint", gameplayOps)
}

// runAdvanceOnlyForAttack replays ONLY the journal's "advance" ops against
// a freshly composed engine constructed exactly as RunFinanceActionsComposed
// constructs one (same seed, same compose.Wire, same protocol.Command entry
// point), sampling Composition.Treasury() at the same logical ticks. It
// deliberately mirrors the bridge rather than calling it, so it stays a
// genuinely independent control: if the bridge's own construction drifts,
// this control drifts visibly with it (the sample-count/tick assertions
// above fail) instead of silently agreeing.
func runAdvanceOnlyForAttack(list actionListFile) (Trajectory, error) {
	cid := errs.NewCorrelationID()
	e := core.NewEngine(core.WithWorldSeed(1972079936))
	comp, err := compose.Wire(e, &compose.Deps{CorrelationID: cid})
	if err != nil {
		return nil, err
	}
	var traj Trajectory
	var logicalTick int64
	for idx, a := range list.Actions {
		if a.Op != "advance" {
			continue
		}
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(fmt.Sprintf("%s-attack-%d", cid, idx)),
			IssuedAtTick:    protocol.Tick(e.TicksCompleted()),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: a.N},
		}
		if res := e.HandleCommand(cmd); !res.Accepted {
			return nil, fmt.Errorf("advance %d rejected", idx)
		}
		logicalTick += a.N
		traj = append(traj, Sample{Tick: logicalTick, Values: map[string]int64{"treasury": comp.Treasury()}})
	}
	return traj, nil
}
