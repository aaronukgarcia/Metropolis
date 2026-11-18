package replay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Round r2 (sonnet-round-r2-rules-lane3) attacks on FEAT-9995385612 lane 3 r2.

// A Fixture literal whose header (not the player field) carries a version
// differing from live, unpinned: refused before any send, older and newer.
func TestRoundR2_UnpinnedFixtureLiteralHeaderMismatch(t *testing.T) {
	for _, tc := range []struct{ live, fx int }{{1, 0}, {1, 5}, {2, 1}} {
		rules.SetCurrentForTest(t, rules.Version(tc.live))
		f := fixtureWithCommands(t, 2)
		f.Header.RuleVersion = tc.fx
		p, err := NewEnginePlayer(f)
		if err != nil {
			t.Fatal(err)
		}
		cmp, err := p.Replay(boundedCtx(t))
		if !isCode(err, codeReplayPinRequired) || cmp != nil || len(p.cmdCh) != 0 {
			t.Fatalf("live %d fx %d: cmp=%v err=%v queued=%d", tc.live, tc.fx, cmp, err, len(p.cmdCh))
		}
	}
}

// PinWith(nil) after a real pin must fall back to the unpinned refusal.
func TestRoundR2_PinWithNilReverts(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	p, _ := NewEnginePlayer(fixtureWithCommandsAtVersion(t, 1, 0))
	called := 0
	_ = p.PinWith(func(rules.Version) error { called++; return nil })
	_ = p.PinWith(nil)
	_, err := p.Replay(boundedCtx(t))
	if !isCode(err, codeReplayPinRequired) || called != 0 {
		t.Fatalf("err=%v called=%d", err, called)
	}
}

// Pinned, fixture version equal to live: pin is still invoked (fixture, then
// live), and the replay matches.
func TestRoundR2_PinnedEqualVersionStillPinsTwice(t *testing.T) {
	p, _ := NewEnginePlayer(fixtureWithCommands(t, 1))
	var seen []rules.Version
	_ = p.PinWith(func(v rules.Version) error { seen = append(seen, v); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runLoop(ctx, p, func(c protocol.Command) protocol.CommandResult {
		return protocol.CommandResult{CorrelationID: c.CorrelationID, Accepted: true}
	})
	cmp, err := p.Replay(ctx)
	if err != nil || !cmp.Matched || len(seen) != 2 {
		t.Fatalf("cmp=%v err=%v seen=%v", cmp, err, seen)
	}
}

// Fix 3: replay fails AND restore fails; both codes visible via errors.Is,
// the original replay code is findable, result nil.
func TestRoundR2_ReplayFailAndRestoreFailBothVisible(t *testing.T) {
	p, _ := NewEnginePlayer(fixtureWithCommands(t, 2))
	n := 0
	_ = p.PinWith(func(rules.Version) error {
		n++
		if n == 2 {
			return errors.New("no unpin")
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cmp, err := p.Replay(ctx) // nobody answers: target-closed-early
	if cmp != nil || !isCode(err, codeReplayTargetClosedEarly) || !isCode(err, codeReplayPinRestoreFailed) {
		t.Fatalf("cmp=%v err=%v", cmp, err)
	}
	if n != 2 {
		t.Fatalf("pin calls %d", n)
	}
}

// Fix 3: a panic inside Replay (second Replay closes cmdCh twice) still runs
// the restore, and the panic is not swallowed.
func TestRoundR2_PanicStillRestoresAndPropagates(t *testing.T) {
	p, _ := NewEnginePlayer(fixtureWithCommands(t, 1))
	n := 0
	_ = p.PinWith(func(rules.Version) error { n++; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runLoop(ctx, p, func(c protocol.Command) protocol.CommandResult {
		return protocol.CommandResult{CorrelationID: c.CorrelationID, Accepted: true}
	})
	if _, err := p.Replay(ctx); err != nil || n != 2 {
		t.Fatalf("first replay err=%v n=%d", err, n)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("second Replay did not panic; expected the pre-existing double-close panic")
			}
		}()
		_, _ = p.Replay(ctx)
	}()
	if n != 4 {
		t.Fatalf("restore not attempted around the panic: pin calls %d, want 4", n)
	}
}

// Pin error on the way in: no command sent, no restore needed.
func TestRoundR2_PinErrorSendsNothing(t *testing.T) {
	p, _ := NewEnginePlayer(fixtureWithCommands(t, 2))
	boom := errors.New("pin refused")
	_ = p.PinWith(func(rules.Version) error { return boom })
	_, err := p.Replay(boundedCtx(t))
	if !errors.Is(err, boom) || len(p.cmdCh) != 0 {
		t.Fatalf("err=%v queued=%d", err, len(p.cmdCh))
	}
}

// Fix 4 header variants.
func TestRoundR2_RuleVersionHeaderVariants(t *testing.T) {
	cases := []struct {
		name, repl string
		wantErr    bool
		wantCode   string
	}{
		{"null", `"ruleVersion": null, "protocolVersion"`, true, codeFixtureCorrupt},
		{"null then zero (last wins)", `"ruleVersion": null, "ruleVersion": 0, "protocolVersion"`, false, ""},
		{"one then null", `"ruleVersion": 0, "ruleVersion": null, "protocolVersion"`, true, codeFixtureCorrupt},
		{"nested null unknown top key", `"extra": {"ruleVersion": null}, "protocolVersion"`, false, ""},
		{"upper-case null", `"RULEVERSION": null, "protocolVersion"`, true, codeFixtureCorrupt},
		{"string", `"ruleVersion": "1", "protocolVersion"`, true, codeFixtureCorrupt},
		{"float", `"ruleVersion": 0.5, "protocolVersion"`, true, codeFixtureCorrupt},
		{"huge", `"ruleVersion": 99999999999999999999, "protocolVersion"`, true, codeFixtureCorrupt},
		{"explicit zero", `"ruleVersion": 0, "protocolVersion"`, false, ""},
		{"1e0 form", `"ruleVersion": 1e0, "protocolVersion"`, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := headerWithEdit(t, `"protocolVersion"`, tc.repl)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantCode != "" && !isCode(err, tc.wantCode) {
				t.Fatalf("want %s got %v", tc.wantCode, err)
			}
			t.Logf("%s -> %v", tc.name, err)
		})
	}
}

func boundedCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}
