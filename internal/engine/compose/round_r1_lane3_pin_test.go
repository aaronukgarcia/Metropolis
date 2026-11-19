package compose

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
)

// Round r1 lane 3 (sonnet-round-r1-rules-lane3): FEAT-9995385612 C22 against
// the REAL composition. Live rules are stood at version 1 by the test seam;
// the fixture is version 0.

func lane3Fixture(t *testing.T, v rules.Version) replay.Fixture {
	t.Helper()
	source := core.NewEngine(core.WithWorldSeed(42), core.WithPoolSize(1))
	sc, err := Wire(source, nil)
	if err != nil {
		t.Fatalf("Wire(source): %v", err)
	}
	rec := sc.Journaler().(*replay.Recorder)
	for _, cmd := range journalTestSeq(t) {
		res := source.HandleCommand(cmd)
		if !res.Accepted {
			t.Fatalf("source rejected %s", cmd.Kind)
		}
		if err := rec.ObserveResult(res); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := replay.Save(dir, "fx", rec, replay.FixtureMeta{WorldSeed: 42, AppVersion: "r1", RuleVersion: replay.RecordedAt(v)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := replay.Load(dir, "fx")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fx
}

func lane3Run(t *testing.T, pin bool, v rules.Version) (*ruleCity, error) {
	t.Helper()
	fx := lane3Fixture(t, v)
	player, err := replay.NewEnginePlayer(fx)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRuleCity(t, nil, -1)
	if pin {
		if err := player.PinWith(rc.c.PinRuleVersion); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- rc.e.RunCommandLoop(ctx, player) }()
	cmp, rerr := player.Replay(ctx)
	if rerr == nil && !cmp.Matched {
		t.Fatalf("mismatch: %v", cmp.Diffs)
	}
	cancel()
	<-done
	return rc, rerr
}

func TestRoundR1Lane3_PinnedReplayRunsUnderFixtureVersion(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	rc, err := lane3Run(t, true, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(rc.probe.decisions) == 0 {
		t.Fatal("probe saw no commands")
	}
	for _, d := range rc.probe.decisions {
		if d.Version != 0 {
			t.Fatalf("command %s judged under version %d, want 0 (fixture)", d.Kind, d.Version)
		}
	}
	if got := rc.c.RuleVersion(); got != 1 {
		t.Fatalf("after replay live version = %d, want 1", got)
	}
}

// An unpinned player replaying a fixture whose version differs from the live
// one must fail loudly (MET-H008), before any command reaches the engine, not
// replay under the wrong rules.
func TestRoundR1Lane3_UnpinnedMismatchedReplayMustRefuse(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	rc, err := lane3Run(t, false, 0)
	if err == nil {
		vs := map[rules.Version]int{}
		for _, d := range rc.probe.decisions {
			vs[d.Version]++
		}
		t.Fatalf("BLOCKER: unpinned player replayed a version-0 fixture with no error; commands judged under versions %v", vs)
	}
	if !errors.Is(err, &errs.E{Code: "MET-H008"}) {
		t.Fatalf("want registered code MET-H008, got %v", err)
	}
	if n := len(rc.probe.decisions); n != 0 {
		t.Fatalf("%d commands reached the engine before the refusal, want 0", n)
	}
}
