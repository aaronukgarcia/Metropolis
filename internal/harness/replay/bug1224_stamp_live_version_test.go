package replay

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1224 port (FEAT-9995385612 lane 4): a fixture recorded from a live
// engine is stamped with the rule version in force when it is saved, so the
// replay of a fixture a build has just recorded does not refuse itself.

// replayAnswering runs an unpinned replay of fx against a target that
// answers every command as recorded.
func replayAnswering(t *testing.T, fx Fixture) error {
	t.Helper()
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatalf("NewEnginePlayer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		i := 0
		runLoop(ctx, p, func(cmd protocol.Command) protocol.CommandResult {
			r := protocol.CommandResult{CorrelationID: cmd.CorrelationID, Tick: protocol.Tick(i), Accepted: true}
			i++
			return r
		})
	}()
	_, rerr := p.Replay(context.Background())
	cancel()
	<-done
	return rerr
}

func TestBug1224_RecordedFixtureIsStampedWithLiveVersion(t *testing.T) {
	if rules.Current < 1 {
		t.Skip("needs a build whose live rule version is at least 1")
	}
	fx := fixtureWithCommands(t, 3) // saved with the Recorder's own meta
	if got := fx.RuleVersion(); got != rules.Effective() {
		t.Fatalf("unstated fixture stamped %d, want the live version %d", got, rules.Effective())
	}
	if err := replayAnswering(t, fx); err != nil {
		t.Fatalf("unpinned replay of a fixture recorded under the same live version: %v", err)
	}

	// The live version moves on: the fixture keeps the version it was
	// recorded under and an unpinned replay refuses at once.
	rules.SetCurrentForTest(t, rules.Current+1)
	if got := fx.RuleVersion(); got != rules.Current {
		t.Fatalf("stamp moved to %d, want it to stay %d", got, rules.Current)
	}
	if err := replayAnswering(t, fx); !isCode(err, codeReplayPinRequired) {
		t.Fatalf("unpinned replay under a newer live version = %v, want %s", err, codeReplayPinRequired)
	}

	// A fixture recorded while the live version is Current+1 says so.
	fx2 := fixtureWithCommands(t, 1)
	if got := fx2.RuleVersion(); got != rules.Current+1 {
		t.Fatalf("recorded under live %d, header says %d", rules.Current+1, got)
	}
}

// A meta whose version was never stated is refused, never defaulted.
func TestBug1224_UnsetRuleVersionIsRefused(t *testing.T) {
	dir := t.TempDir()
	for name, meta := range map[string]FixtureMeta{
		"zero":   {},
		"seeded": {WorldSeed: 5, AppVersion: "x"},
	} {
		err := Save(dir, "unset-"+name, sampleRecorder(t), meta)
		if !isCode(err, codeFixtureRuleVersionUnset) {
			t.Fatalf("%s: Save with an unstated rule version = %v, want %s", name, err, codeFixtureRuleVersionUnset)
		}
		if _, statErr := os.Stat(filepath.Join(dir, "unset-"+name+".header.json")); statErr == nil {
			t.Fatalf("%s: a refused Save left a header on disk", name)
		}
	}
}

func TestBug1224_ExplicitZeroIsKeptAndNonZeroIsStated(t *testing.T) {
	rules.SetCurrentForTest(t, rules.Current+1)
	dir := t.TempDir()
	if err := Save(dir, "explicit0", sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(0)}); err != nil {
		t.Fatal(err)
	}
	fx, err := Load(dir, "explicit0")
	if err != nil {
		t.Fatal(err)
	}
	if fx.RuleVersion() != 0 {
		t.Fatalf("explicit 0 stamped %d", fx.RuleVersion())
	}
	if err := Save(dir, "stated1", sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(1)}); err != nil {
		t.Fatal(err)
	}
	fx, err = Load(dir, "stated1")
	if err != nil {
		t.Fatal(err)
	}
	if fx.RuleVersion() != 1 {
		t.Fatalf("stated 1 stamped %d", fx.RuleVersion())
	}
}
