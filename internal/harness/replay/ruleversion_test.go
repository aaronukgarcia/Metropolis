package replay

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 3 (C22): fixtures carry and replay at their own rule
// version.

func isCode(err error, code string) bool {
	return err != nil && errors.Is(err, &errs.E{Code: code})
}

func saveVersioned(t *testing.T, dir, name string, v rules.Version) {
	t.Helper()
	if err := Save(dir, name, sampleRecorder(t), FixtureMeta{WorldSeed: 7, AppVersion: "rv", RuleVersion: RecordedAt(v)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// A fixture with no ruleVersion field is version 0 and its header bytes
// carry no such field (existing goldens stay byte-identical).
func TestFixtureWithoutRuleVersionIsZero(t *testing.T) {
	dir := t.TempDir()
	saveVersioned(t, dir, "old", 0)
	raw, err := os.ReadFile(filepath.Join(dir, "old.header.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ruleVersion") {
		t.Fatalf("version 0 header must omit ruleVersion, got %s", raw)
	}
	fx, err := Load(dir, "old")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fx.RuleVersion() != 0 {
		t.Fatalf("RuleVersion = %d, want 0", fx.RuleVersion())
	}
}

// A fixture with ruleVersion 1 loads as 1.
func TestFixtureRuleVersionRoundTrips(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	dir := t.TempDir()
	saveVersioned(t, dir, "v1", 1)
	raw, _ := os.ReadFile(filepath.Join(dir, "v1.header.json"))
	if !strings.Contains(string(raw), `"ruleVersion": 1`) {
		t.Fatalf("header lacks ruleVersion: %s", raw)
	}
	fx, err := Load(dir, "v1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fx.RuleVersion() != 1 {
		t.Fatalf("RuleVersion = %d, want 1", fx.RuleVersion())
	}
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatal(err)
	}
	if p.RuleVersion() != 1 {
		t.Fatalf("player RuleVersion = %d, want 1", p.RuleVersion())
	}
}

// A future value is refused by Load (and Save) with the registry error.
func TestFixtureFutureRuleVersionRefused(t *testing.T) {
	dir := t.TempDir()
	t.Run("written by a newer build", func(t *testing.T) {
		rules.SetCurrentForTest(t, 3)
		saveVersioned(t, dir, "future", 3)
	})
	if _, err := Load(dir, "future"); !isCode(err, rules.ErrSavedUnderNewerRules) {
		t.Fatalf("Load future fixture: want %s, got %v", rules.ErrSavedUnderNewerRules, err)
	}
	if err := Save(t.TempDir(), "x", sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(5)}); !isCode(err, rules.ErrSavedUnderNewerRules) {
		t.Fatalf("Save future: want %s, got %v", rules.ErrSavedUnderNewerRules, err)
	}
}

// A negative stamp is a malformed stamp, not clamped.
func TestFixtureNegativeRuleVersionRefused(t *testing.T) {
	dir := t.TempDir()
	saveVersioned(t, dir, "neg", 0)
	hp := filepath.Join(dir, "neg.header.json")
	raw, _ := os.ReadFile(hp)
	edited := strings.Replace(string(raw), `"protocolVersion"`, `"ruleVersion": -1, "protocolVersion"`, 1)
	if edited == string(raw) {
		t.Fatal("header edit did not apply")
	}
	if err := os.WriteFile(hp, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "neg"); !isCode(err, rules.ErrMalformedRuleStamp) {
		t.Fatalf("want %s, got %v", rules.ErrMalformedRuleStamp, err)
	}
}

// replayWithVersionedTarget runs p against a fake engine that, like the real
// build module, accepts a duplicate command at version 0 and refuses it from
// version 1 on. The pinner moves the fake engine's live version.
func replayWithVersionedTarget(t *testing.T, p *EnginePlayer, live *rules.Version) *CompareResult {
	t.Helper()
	if err := p.PinWith(func(v rules.Version) error { *live = v; return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		seen := 0
		runLoop(ctx, p, func(cmd protocol.Command) protocol.CommandResult {
			seen++
			res := protocol.CommandResult{CorrelationID: cmd.CorrelationID, Tick: protocol.Tick(seen - 1), Accepted: true}
			if seen > 1 && rules.Has(*live, rules.RefuseOccupiedCell) {
				res.Accepted = false
			}
			return res
		})
	}()
	cmp, err := p.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	cancel()
	<-done
	return cmp
}

// C22 can-fail: a version-0 fixture holding a duplicate build (second command
// accepted) must replay under version 0 even when the live build is at
// version 1, and the live version is put back afterwards.
func TestEnginePlayerReplaysAtFixtureVersionNotCurrent(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	p, err := NewEnginePlayer(fixtureWithCommandsAtVersion(t, 2, 0))
	if err != nil {
		t.Fatal(err)
	}
	live := rules.Effective()
	cmp := replayWithVersionedTarget(t, p, &live)
	if !cmp.Matched {
		t.Fatalf("v0 fixture must match when replayed at its own version: %v", cmp.Diffs)
	}
	if live != rules.Effective() {
		t.Fatalf("live version after replay = %d, want %d", live, rules.Effective())
	}
}

// A fixture recorded at version 1 (second command refused) replays at 1.
func TestEnginePlayerReplaysVersionOneFixtureAtOne(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	r := NewRecorder()
	for i := 0; i < 2; i++ {
		corr := protocol.NewCorrelationID()
		cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corr, Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
		if err := r.ObserveCommand(cmd); err != nil {
			t.Fatal(err)
		}
		if err := r.ObserveResult(protocol.CommandResult{CorrelationID: corr, Tick: protocol.Tick(i), Accepted: i == 0}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := Save(dir, "v1dup", r, r.Meta(0, "")); err != nil {
		t.Fatal(err)
	}
	fx, err := Load(dir, "v1dup")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := NewEnginePlayer(fx)
	var live rules.Version
	cmp := replayWithVersionedTarget(t, p, &live)
	if !cmp.Matched {
		t.Fatalf("v1 fixture must match at version 1: %v", cmp.Diffs)
	}
}

// Round r1 fix 1: an unpinned player must refuse a fixture whose rule
// version differs from the live one, before any command is sent.
func TestEnginePlayerUnpinnedMismatchRefusesBeforeSending(t *testing.T) {
	for _, tc := range []struct {
		name    string
		live    rules.Version
		fixture rules.Version
	}{
		{"older fixture", 1, 0},
		{"newer fixture", 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules.SetCurrentForTest(t, tc.live)
			p, err := NewEnginePlayer(fixtureWithCommands(t, 2))
			if err != nil {
				t.Fatal(err)
			}
			p.ruleVersion = tc.fixture // stand in for a header at that version
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmp, err := p.Replay(ctx)
			if !isCode(err, codeReplayPinRequired) {
				t.Fatalf("want %s, got cmp=%v err=%v", codeReplayPinRequired, cmp, err)
			}
			if got := len(p.cmdCh); got != 0 {
				t.Fatalf("%d commands were sent before the refusal", got)
			}
		})
	}
}

// Equal versions with no pin stay allowed (every existing caller).
func TestEnginePlayerUnpinnedEqualVersionStillReplays(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	r := NewRecorder()
	corr := protocol.NewCorrelationID()
	cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corr, Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	if err := r.ObserveCommand(cmd); err != nil {
		t.Fatal(err)
	}
	if err := r.ObserveResult(protocol.CommandResult{CorrelationID: corr, Accepted: true}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Save(dir, "eq", r, r.Meta(0, "")); err != nil {
		t.Fatal(err)
	}
	fx, err := Load(dir, "eq")
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewEnginePlayer(fx)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runLoop(ctx, p, func(c protocol.Command) protocol.CommandResult {
		return protocol.CommandResult{CorrelationID: c.CorrelationID, Accepted: true}
	})
	cmp, err := p.Replay(ctx)
	if err != nil || !cmp.Matched {
		t.Fatalf("equal-version unpinned replay: cmp=%v err=%v", cmp, err)
	}
}

// Fix 3: a failed restore after a matching replay is never silent.
func TestEnginePlayerRestoreFailureIsReturned(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	p, err := NewEnginePlayer(fixtureWithCommands(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := p.PinWith(func(rules.Version) error {
		calls++
		if calls == 2 {
			return errors.New("target would not unpin")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runLoop(ctx, p, func(c protocol.Command) protocol.CommandResult {
		return protocol.CommandResult{CorrelationID: c.CorrelationID, Tick: 0, Accepted: true}
	})
	cmp, err := p.Replay(ctx)
	if !isCode(err, codeReplayPinRestoreFailed) {
		t.Fatalf("want %s, got cmp=%v err=%v", codeReplayPinRestoreFailed, cmp, err)
	}
	if cmp != nil {
		t.Fatal("a failed restore must not return a result as if all were well")
	}
	if calls != 2 {
		t.Fatalf("pin called %d times, want 2", calls)
	}
}

func headerWithEdit(t *testing.T, old, repl string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	saveVersioned(t, dir, "h", 0)
	hp := filepath.Join(dir, "h.header.json")
	raw, err := os.ReadFile(hp)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), old, repl, 1)
	if edited == string(raw) {
		t.Fatal("header edit did not apply")
	}
	if err := os.WriteFile(hp, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	_, lerr := Load(dir, "h")
	return dir, lerr
}

// Fix 4: "ruleVersion": null is corrupt; an absent field is version 0.
func TestFixtureNullRuleVersionIsCorrupt(t *testing.T) {
	_, err := headerWithEdit(t, `"protocolVersion"`, `"ruleVersion": null, "protocolVersion"`)
	if !isCode(err, codeFixtureCorrupt) {
		t.Fatalf("null ruleVersion: want %s, got %v", codeFixtureCorrupt, err)
	}
}

func TestFixtureAbsentRuleVersionStillReadsZero(t *testing.T) {
	dir := t.TempDir()
	saveVersioned(t, dir, "abs", 0)
	fx, err := Load(dir, "abs")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fx.RuleVersion() != 0 {
		t.Fatalf("absent field read as %d, want 0", fx.RuleVersion())
	}
}

// Evidence: a committed golden fixture loads and saves again with its
// decoded stream and header unchanged. The compressed shard bytes (and so
// the header's ByteSize and SHA256 of them) belong to the toolchain that
// wrote the golden: this build's compress/flate emits a different but
// equivalent gzip stream (measured: 13132 vs 20382 bytes, identical decoded
// bytes), so those two fields are compared as "re-saved twice, identical"
// rather than against the committed values.
func TestCommittedFixtureResavesUnchanged(t *testing.T) {
	const name = "folkestone64-sample"
	src := filepath.Join("..", "..", "..", "fixtures")
	fx, err := Load(src, name)
	if err != nil {
		t.Fatalf("Load committed fixture: %v", err)
	}
	if fx.RuleVersion() != 0 {
		t.Fatalf("committed fixture RuleVersion = %d, want 0", fx.RuleVersion())
	}
	meta := fx.Meta()
	resave := func() (string, Fixture) {
		rec := NewRecorder()
		rec.records = fx.Records
		dir := t.TempDir()
		if err := Save(dir, name, rec, meta); err != nil {
			t.Fatalf("Save: %v", err)
		}
		again, err := Load(dir, name)
		if err != nil {
			t.Fatalf("Load re-saved: %v", err)
		}
		return dir, again
	}
	dirA, a := resave()
	dirB, _ := resave()

	// Decoded records identical to the committed ones, byte for byte.
	if len(a.Records) != len(fx.Records) {
		t.Fatalf("record count %d, want %d", len(a.Records), len(fx.Records))
	}
	for i := range fx.Records {
		if a.Records[i].Kind != fx.Records[i].Kind || !bytes.Equal(a.Records[i].Data, fx.Records[i].Data) {
			t.Fatalf("record %d changed on re-save", i)
		}
	}
	// Header identical to the committed one apart from the two fields that
	// describe the compressed bytes.
	a.Header.Header.ShardIndex[0].ByteSize, a.Header.Header.ShardIndex[0].SHA256 = 0, ""
	want := fx.Header
	want.Header.ShardIndex = append([]serialize.ShardMeta(nil), fx.Header.Header.ShardIndex...)
	want.Header.ShardIndex[0].ByteSize, want.Header.ShardIndex[0].SHA256 = 0, ""
	if !reflect.DeepEqual(a.Header, want) {
		t.Fatalf("re-saved header differs: got %+v want %+v", a.Header, want)
	}
	// Saving is deterministic: two re-saves are byte-identical on disk.
	for _, suffix := range []string{".header.json", ".ndjson.gz"} {
		x, _ := os.ReadFile(filepath.Join(dirA, name+suffix))
		y, _ := os.ReadFile(filepath.Join(dirB, name+suffix))
		if len(x) == 0 || !bytes.Equal(x, y) {
			t.Fatalf("%s is not byte-identical across two saves", suffix)
		}
	}
}
