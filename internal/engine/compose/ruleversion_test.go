package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 2 tests: rule versions in snapshots and journal
// frames. Today rules.Current is 0 (rule 1 is pending wiring), so versions
// above 0 are exercised with rules.SetCurrentForTest (standing in for a
// future build that has wired rule N) and the test-only probe module in
// ruleversion_helpers_test.go. Criteria: C5..C27 (lane 2 per the lane split).

func isCode(err error, code string) bool {
	return err != nil && errors.Is(err, &errs.E{Code: code})
}

func advCmd(n int64) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(fmt.Sprintf("rvc-%d", n)),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
}

// ---------------------------------------------------------------------------
// Version 0 is byte-identical to before this feature (C5, C6, C7).
// ---------------------------------------------------------------------------

// bundleFiles returns the header.json and save-meta.json bytes of the
// composition bundle inside a snapshot payload.
func bundleFiles(t *testing.T, data []byte) (header, meta []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := unzipDir(data, dir); err != nil {
		t.Fatal(err)
	}
	summaries, _, err := save.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			header, err = os.ReadFile(filepath.Join(s.Path, "header.json"))
			if err != nil {
				t.Fatal(err)
			}
			meta, err = os.ReadFile(filepath.Join(s.Path, "save-meta.json"))
			if err != nil {
				t.Fatal(err)
			}
			return header, meta
		}
	}
	t.Fatal("no composition bundle in snapshot")
	return nil, nil
}

func TestRuleVersion_V0_ByteIdenticalToGoldens(t *testing.T) {
	run := func(t *testing.T) {
		ctx := context.Background()
		store := persist.NewMemStore()
		e, c := newPersistComposition(t, store)
		submitAll(t, e, rtCommands())
		if _, ok, err := c.MaybeSnapshotEvery(ctx, store, defaultPersistCity, 65); err != nil || !ok {
			t.Fatalf("snapshot ok=%v err=%v", ok, err)
		}
		frames, err := store.ReadJournal(ctx, defaultPersistCity)
		if err != nil {
			t.Fatal(err)
		}
		gotJournal := bytes.Join(frames, []byte("\n"))
		ids, err := store.ListSnapshots(ctx, defaultPersistCity)
		if err != nil || len(ids) != 1 {
			t.Fatalf("snapshots: %v %v", ids, err)
		}
		data, err := store.GetSnapshot(ctx, defaultPersistCity, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		header, meta := bundleFiles(t, data)
		for name, got := range map[string][]byte{
			"journal.golden":        gotJournal,
			"header.json.golden":    header,
			"save-meta.json.golden": meta,
		} {
			want, err := os.ReadFile(filepath.Join("testdata", "rules", "golden-v0", name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
				t.Errorf("%s differs from the pre-change golden:\n--- want\n%s\n--- got\n%s", name, want, got)
			}
		}
	}
	t.Run("plain", run)
	// A build whose Current is higher, but whose city is still at version 0
	// (an old city before its first new command), must write the same bytes.
	t.Run("pinned_zero_under_higher_current", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		ctx := context.Background()
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 0)
		// rtCommands under the ruleCity engine would add a probe; compare
		// only the journal bytes of a version-0 frame.
		rc.sessionA(t)
		frames, err := store.ReadJournal(ctx, ruleTestCity)
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range frames {
			if bytes.Contains(f, []byte(`"rv"`)) {
				t.Errorf("frame %d written at version 0 carries an envelope: %s", i, f)
			}
		}
	})
	t.Run("current_is_zero_today", func(t *testing.T) {
		if rules.Current != 0 || rules.Effective() != 0 {
			t.Fatalf("Current/Effective = %d/%d, want 0/0 while every registered rule is pending wiring", rules.Current, rules.Effective())
		}
		c := newRuleCity(t, nil, -1).c
		if c.RuleVersion() != 0 {
			t.Fatalf("live version %d, want 0", c.RuleVersion())
		}
	})
}

// ---------------------------------------------------------------------------
// Frame codec (C8, C9).
// ---------------------------------------------------------------------------

func TestRuleVersion_FrameCodec(t *testing.T) {
	cmd := advCmd(5)
	plain, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("plain at 0", func(t *testing.T) {
		got, err := encodeJournalFrame(cmd, 0)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("version 0 frame is not the plain bytes: %s (%v)", got, err)
		}
		f, err := decodeJournalFrame(got, 0, 1)
		if err != nil || f.Version != 0 {
			t.Fatalf("decode: %+v %v", f, err)
		}
	})
	t.Run("envelope at N>=1 round trips", func(t *testing.T) {
		for _, v := range []rules.Version{1, 2, 7, 1000} {
			got, err := encodeJournalFrame(cmd, v)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(got, []byte(fmt.Sprintf(`{"rv":%d,"cmd":`, v))) || !bytes.HasSuffix(got, []byte("}")) {
				t.Fatalf("v%d envelope shape: %s", v, got)
			}
			f, err := decodeJournalFrame(got, 0, 1)
			if err != nil || f.Version != v {
				t.Fatalf("v%d decode: %+v %v", v, f, err)
			}
			back, err := protocol.EncodeCommand(f.Command)
			if err != nil || !bytes.Equal(back, plain) {
				t.Fatalf("v%d command did not round trip: %s", v, back)
			}
		}
	})
	t.Run("negative version is never written", func(t *testing.T) {
		if _, err := encodeJournalFrame(cmd, -1); !isCode(err, rules.ErrMalformedRuleStamp) {
			t.Fatalf("want MET-G5613, got %v", err)
		}
	})
	p := string(plain)
	malformed := map[string]string{
		"extra key":               `{"rv":1,"cmd":` + p + `,"x":1}`,
		"rv zero":                 `{"rv":0,"cmd":` + p + `}`,
		"rv negative":             `{"rv":-1,"cmd":` + p + `}`,
		"rv string":               `{"rv":"1","cmd":` + p + `}`,
		"rv float":                `{"rv":1.5,"cmd":` + p + `}`,
		"rv exponent":             `{"rv":1e2,"cmd":` + p + `}`,
		"rv beyond range":         `{"rv":99999999999,"cmd":` + p + `}`,
		"rv null":                 `{"rv":null,"cmd":` + p + `}`,
		"cmd null":                `{"rv":1,"cmd":null}`,
		"cmd missing":             `{"rv":1}`,
		"rv missing":              `{"cmd":` + p + `}`,
		"plain and envelope keys": `{"rv":1,"cmd":` + p + `,"kind":"AdvanceTicks","protocolVersion":"1.0"}`,
		"plain keys plus rv":      strings.Replace(p, `{`, `{"rv":1,`, 1),
	}
	for name, raw := range malformed {
		raw := raw
		t.Run("malformed/"+name, func(t *testing.T) {
			_, err := decodeJournalFrame([]byte(raw), 3, 9)
			if !isCode(err, rules.ErrMalformedRuleStamp) {
				t.Fatalf("want MET-G5613, got %v", err)
			}
			if !strings.Contains(err.Error(), "frame 3 of 9") {
				t.Errorf("error does not name the frame: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// A journal that crosses from version 0 to a later version restores exactly.
// ---------------------------------------------------------------------------

type crossing struct {
	store     persist.Store
	ctrl      outcome
	decisions []probeDecision
}

// buildCrossing runs sessionA at version 0, takes a snapshot (before), moves
// to the test rule's version, takes a snapshot at the same tick (at), runs
// sessionB, and takes a snapshot (after). It also runs the uninterrupted
// control with no persistence.
func buildCrossing(t *testing.T) crossing {
	t.Helper()
	rules.SetCurrentForTest(t, 1)
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)
	rc.snapshot(t, store, 5) // before the boundary: version 0
	if err := rc.c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	rc.snapshot(t, store, 5) // at the boundary: version 1, same tick
	rc.sessionB(t)
	rc.snapshot(t, store, 5) // after: version 1

	ctrl := newRuleCity(t, nil, 0)
	ctrl.sessionA(t)
	if err := ctrl.c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	ctrl.sessionB(t)
	if !reflect.DeepEqual(ctrl.probe.decisions, rc.probe.decisions) {
		t.Fatalf("live run and control disagree:\n%+v\n%+v", rc.probe.decisions, ctrl.probe.decisions)
	}
	if got, want := rc.outcome(t), ctrl.outcome(t); got != want {
		t.Fatalf("live run and control state differ: %+v vs %+v", got, want)
	}
	ids, err := store.ListSnapshots(context.Background(), ruleTestCity)
	if err != nil || len(ids) != 3 {
		t.Fatalf("want three snapshots (before, at, after), got %v (%v)", ids, err)
	}
	return crossing{store: store, ctrl: ctrl.outcome(t), decisions: ctrl.probe.decisions}
}

func TestRuleVersion_CrossingRestoresExactly(t *testing.T) {
	cr := buildCrossing(t)
	if len(cr.decisions) != 8 {
		t.Fatalf("control made %d decisions, want 8", len(cr.decisions))
	}
	for i, d := range cr.decisions {
		want := rules.Version(0)
		if i >= 5 {
			want = 1
		}
		if d.Version != want || d.NewRule != (want == 1) {
			t.Fatalf("control decision %d = %+v, want version %d", i, d, want)
		}
	}
	cases := []struct {
		name     string
		keep     []int
		wantTail int // commands replayed after the snapshot (all for genesis)
	}{
		{"genesis", nil, 8},
		{"snapshot before the boundary", []int{0}, 3},
		{"snapshot at the boundary", []int{1}, 3},
		{"snapshot after the boundary", []int{2}, 0},
		{"all three, newest wins", []int{0, 1, 2}, 0},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rules.SetCurrentForTest(t, 1)
			src := cloneStore(t, cr.store, c.keep...)
			rc, rep := wake(t, src)
			if got := rc.outcome(t); got != cr.ctrl {
				t.Fatalf("restored state differs from the uninterrupted control:\n got %+v\nwant %+v", got, cr.ctrl)
			}
			n := len(rc.probe.decisions)
			if n != c.wantTail {
				t.Fatalf("replayed %d commands, want %d", n, c.wantTail)
			}
			wantSuffix := cr.decisions[len(cr.decisions)-n:]
			if len(wantSuffix) > 0 && !reflect.DeepEqual(rc.probe.decisions, wantSuffix) {
				t.Fatalf("per-command decisions differ from the control:\n got %+v\nwant %+v", rc.probe.decisions, wantSuffix)
			}
			if rep.UsedSnapshot != (len(c.keep) > 0) {
				t.Errorf("UsedSnapshot = %v", rep.UsedSnapshot)
			}
			if rc.c.RuleVersion() != 1 || rep.LiveVersion != 1 {
				t.Errorf("live version after restore = %d (report %d), want Effective 1", rc.c.RuleVersion(), rep.LiveVersion)
			}
			if rep.OlderRules {
				t.Errorf("the journal reaches the current version; no older-rules report expected: %+v", rep)
			}
		})
	}
}

// TestRuleVersion_SingleVersionReplayIsWrong is the C11 negative control: a
// replay that applies ONE version to the whole journal makes different
// per-command decisions than the control, so the crossing test above can
// tell the two apart.
func TestRuleVersion_SingleVersionReplayIsWrong(t *testing.T) {
	cr := buildCrossing(t)
	frames, err := RestoreCommands(context.Background(), cr.store, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, single := range []rules.Version{0, 1} {
		rc := newRuleCity(t, nil, single)
		for _, f := range frames {
			if f.Kind == protocol.KindSubscribe || f.Kind == protocol.KindUnsubscribe {
				continue
			}
			if res := rc.e.HandleCommand(f.Command); !res.Accepted {
				t.Fatalf("rejected: %+v", res.Error)
			}
		}
		if reflect.DeepEqual(rc.probe.decisions, cr.decisions) {
			t.Fatalf("replay under the single version %d made the same decisions as the recorded versions; the crossing test would not notice", single)
		}
	}
	// The frames really carry both versions.
	seen := map[rules.Version]int{}
	for _, f := range frames {
		seen[f.Version]++
	}
	if seen[0] != 5 || seen[1] != 3 {
		t.Fatalf("journal versions = %v, want 5 at 0 and 3 at 1", seen)
	}
}

// ---------------------------------------------------------------------------
// Load and live version; the older-rules warning (C13..C16).
// ---------------------------------------------------------------------------

func warnCount() int {
	n := 0
	for _, e := range errs.Recent() {
		if e.Code == rules.WarnRestoredUnderOlderRules {
			n += 1 + e.Repeat
		}
	}
	return n
}

func TestRuleVersion_OlderRulesWarningOnceAndNothingWritten(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	ctx := context.Background()
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)
	rc.snapshot(t, store, 5)
	rc.advance(t, 5) // a tail command at version 0

	before := fingerprint(t, store)
	w0 := warnCount()
	w, rep := wake(t, store)
	if got := warnCount() - w0; got != 1 {
		t.Fatalf("older-rules warning raised %d times on the first wake, want 1", got)
	}
	if !rep.OlderRules || rep.ReplayedVersion != 0 || rep.LiveVersion != 1 {
		t.Fatalf("report = %+v", rep)
	}
	want := rules.Describe(0, 1)
	if want == "" || !strings.Contains(rep.OperatorLine, want) {
		t.Fatalf("operator line %q lacks the registry text %q", rep.OperatorLine, want)
	}
	last := errs.Recent()
	found := false
	for i := len(last) - 1; i >= 0; i-- {
		if last[i].Code == rules.WarnRestoredUnderOlderRules {
			found = true
			if ch, _ := last[i].Ctx["changes"].(string); ch != want {
				t.Errorf("event changes = %q, want %q", ch, want)
			}
			break
		}
	}
	if !found {
		t.Fatal("no MET-G5614 event in the ring")
	}
	if w.c.RuleVersion() != 1 || w.c.LoadedRuleVersion() != 0 {
		t.Errorf("live/loaded = %d/%d, want 1/0", w.c.RuleVersion(), w.c.LoadedRuleVersion())
	}
	// A wake followed by an immediate shutdown writes nothing; replay did
	// try to append (so the guard, not absence of attempts, is what held).
	if after := fingerprint(t, store); after != before {
		t.Fatal("the switch from old rules to new wrote to the store")
	}
	if w.guard.attempts.Load() == 0 {
		t.Error("replay made no append attempts; the no-write assertion proves nothing")
	}

	// The next accepted command is stamped with the new version...
	w.advance(t, 5)
	frames, err := RestoreCommands(ctx, store, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	if got := frames[len(frames)-1].Version; got != 1 {
		t.Fatalf("first new frame stamped %d, want 1", got)
	}
	// ...so the second wake is silent.
	w1 := warnCount()
	_, rep2 := wake(t, store)
	if warnCount() != w1 || rep2.OlderRules || rep2.OperatorLine != "" {
		t.Fatalf("second wake warned: %+v", rep2)
	}
}

// ---------------------------------------------------------------------------
// Fail closed (C17..C22): nothing is touched.
// ---------------------------------------------------------------------------

// failClosed restores store into a fresh wired city and asserts the restore
// returns want and that neither the store bytes nor the engine's state
// digest moved.
func failClosed(t *testing.T, store persist.Store, want string, mutate func(*ruleCity)) {
	t.Helper()
	rc := newRuleCity(t, store, -1)
	if mutate != nil {
		mutate(rc)
	}
	d0, fp0 := rc.c.StateDigest(), fingerprint(t, store)
	rc.guard.replaying.Store(true)
	_, err := RestoreLatestSnapshotOrGenesisReport(context.Background(), rc.e, rc.c, store, ruleTestCity)
	rc.guard.replaying.Store(false)
	if !isCode(err, want) {
		t.Fatalf("want %s, got %v", want, err)
	}
	if rc.c.StateDigest() != d0 {
		t.Error("a refused restore changed the engine's state")
	}
	if fingerprint(t, store) != fp0 {
		t.Error("a refused restore changed the store")
	}
	if rc.guard.attempts.Load() != 0 {
		t.Error("a refused restore replayed a command")
	}
	if rc.c.RuleVersion() != rules.Effective() {
		t.Errorf("live version left at %d", rc.c.RuleVersion())
	}
}

func appendRaw(t *testing.T, store persist.Store, raw string) {
	t.Helper()
	if err := store.AppendJournal(context.Background(), ruleTestCity, []byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestRuleVersion_FailsClosed(t *testing.T) {
	t.Run("future snapshot, no walk-back to an older snapshot", func(t *testing.T) {
		rules.SetCurrentForTest(t, 2)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 1)
		rc.sessionA(t)
		rc.snapshot(t, store, 5) // old snapshot, version 1: restorable on its own
		rc.sessionB(t)
		if err := rc.c.PinRuleVersion(2); err != nil {
			t.Fatal(err)
		}
		rc.snapshot(t, store, 5) // newest snapshot at version 2, journal all version 1
		rules.SetCurrentForTest(t, 1)
		// The same store restores fine from the old snapshot alone, so the
		// refusal below is proof that no walk-back happened.
		ok := cloneStore(t, store, 0)
		w, _ := wake(t, ok)
		if w.c.RuleVersion() != 1 {
			t.Fatal("control restore from the old snapshot failed")
		}
		failClosed(t, store, rules.ErrSavedUnderNewerRules, nil)
	})
	t.Run("future frame", func(t *testing.T) {
		rules.SetCurrentForTest(t, 2)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 1)
		rc.sessionA(t)
		rc.snapshot(t, store, 5)
		if err := rc.c.PinRuleVersion(2); err != nil {
			t.Fatal(err)
		}
		rc.advance(t, 5)
		rules.SetCurrentForTest(t, 1)
		failClosed(t, store, rules.ErrSavedUnderNewerRules, nil)
	})
	t.Run("future frame, genesis", func(t *testing.T) {
		rules.SetCurrentForTest(t, 2)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 2)
		rc.sessionA(t)
		rules.SetCurrentForTest(t, 1)
		failClosed(t, store, rules.ErrSavedUnderNewerRules, nil)
	})
	t.Run("frame version lower than the previous one", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 1)
		rc.advance(t, 5)
		if err := rc.c.PinRuleVersion(0); err != nil {
			t.Fatal(err)
		}
		rc.advance(t, 5)
		failClosed(t, store, rules.ErrRuleVersionWentBackwards, nil)
	})
	t.Run("a skipped Subscribe frame still takes part in the ordering check", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 1)
		rc.advance(t, 5)
		if err := rc.c.PinRuleVersion(0); err != nil {
			t.Fatal(err)
		}
		rc.do(t, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"})
		failClosed(t, store, rules.ErrRuleVersionWentBackwards, nil)
	})
	malformed := map[string]string{
		"extra key":       `{"rv":1,"cmd":{"x":1},"extra":true}`,
		"rv zero":         `{"rv":0,"cmd":{}}`,
		"rv string":       `{"rv":"1","cmd":{}}`,
		"cmd missing":     `{"rv":1}`,
		"rv beyond range": `{"rv":99999999999,"cmd":{}}`,
	}
	for name, raw := range malformed {
		name, raw := name, raw
		t.Run("malformed stamp/"+name, func(t *testing.T) {
			rules.SetCurrentForTest(t, 1)
			store := persist.NewMemStore()
			rc := newRuleCity(t, store, 0)
			rc.sessionA(t)
			rc.snapshot(t, store, 5)
			appendRaw(t, store, raw)
			failClosed(t, store, rules.ErrMalformedRuleStamp, nil)
		})
	}
	t.Run("below OldestSupported, genesis", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 0)
		rc.sessionA(t)
		rules.SetOldestSupportedForTest(t, 1)
		failClosed(t, store, rules.ErrSavedUnderRetiredRules, nil)
	})
	t.Run("below OldestSupported, snapshot", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 0)
		rc.sessionA(t)
		rc.snapshot(t, store, 5)
		rules.SetOldestSupportedForTest(t, 1)
		failClosed(t, store, rules.ErrSavedUnderRetiredRules, nil)
	})
	t.Run("a snapshot at a supported version restores although older frames precede it", func(t *testing.T) {
		// Retirement is judged on what is replayed, not on frames the
		// snapshot already covers.
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 0)
		rc.sessionA(t)
		if err := rc.c.PinRuleVersion(1); err != nil {
			t.Fatal(err)
		}
		rc.snapshot(t, store, 5)
		rules.SetOldestSupportedForTest(t, 1)
		w, rep := wake(t, store)
		if rep.OlderRules || w.c.RuleVersion() != 1 {
			t.Fatalf("report = %+v", rep)
		}
	})
	t.Run("negative or newer Meta stamp", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		if err := checkSnapshotVersion("c", -1); !isCode(err, rules.ErrMalformedRuleStamp) {
			t.Errorf("negative: %v", err)
		}
		if err := checkSnapshotVersion("c", 2); !isCode(err, rules.ErrSavedUnderNewerRules) {
			t.Errorf("newer: %v", err)
		}
		if err := checkSnapshotVersion("c", 0); err != nil {
			t.Errorf("zero: %v", err)
		}
	})
	t.Run("a Meta stamp that is not an integer is refused on read", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "save-meta.json"), []byte(`{"saveKind":"manual","ruleVersion":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found := readBundleRuleVersion(dir); found {
			t.Fatal("a malformed Meta stamp was read as a version")
		}
	})
}

// ---------------------------------------------------------------------------
// Versioned modules must hold a getter (C23).
// ---------------------------------------------------------------------------

func TestRuleVersion_ModuleWithoutGetterRefused(t *testing.T) {
	t.Run("at registration (Wire)", func(t *testing.T) {
		rc := newRuleCity(t, nil, -1)
		if err := rc.c.state.rv.wire(rc.c.state.cid, "test.dead", deadModule{}); !isCode(err, ErrModuleFailed) {
			t.Fatalf("want MET-G801, got %v", err)
		}
		if err := rc.c.state.rv.wire(rc.c.state.cid, "test.nil", nil); !isCode(err, ErrModuleFailed) {
			t.Fatalf("nil module: want MET-G801, got %v", err)
		}
	})
	t.Run("at replay", func(t *testing.T) {
		rules.SetCurrentForTest(t, 1)
		store := persist.NewMemStore()
		rc := newRuleCity(t, store, 0)
		rc.sessionA(t)
		rc.snapshot(t, store, 5)
		rc.advance(t, 5)
		// genesis and snapshot paths both refuse, and touch nothing.
		failClosed(t, cloneStore(t, store), ErrModuleFailed, func(w *ruleCity) { w.probe.get = nil })
		failClosed(t, cloneStore(t, store, 0), ErrModuleFailed, func(w *ruleCity) { w.probe.get = nil })
	})
}

// ---------------------------------------------------------------------------
// Subscribe / Unsubscribe frames (BUG-1225 with versions).
// ---------------------------------------------------------------------------

func TestRuleVersion_SubscribeFramesStampedAndSkipped(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	ctx := context.Background()
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.advance(t, 5)
	rc.do(t, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"}) // version 0
	if err := rc.c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	rc.do(t, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"}) // version 1
	rc.do(t, protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: "sub-1"})
	rc.advance(t, 5)

	frames, err := RestoreCommands(ctx, store, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range frames {
		got = append(got, fmt.Sprintf("%s@%d", f.Kind, f.Version))
	}
	want := []string{"AdvanceTicks@0", "Subscribe@0", "Subscribe@1", "Unsubscribe@1", "AdvanceTicks@1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %v, want %v", got, want)
	}
	// Restore skips the session frames (only the two advances are applied)
	// and the ordering check, which counted all five frames, passes.
	ref := newRuleCity(t, nil, 0)
	ref.advance(t, 5)
	if err := ref.c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	ref.advance(t, 5)
	w, _ := wake(t, store)
	if len(w.probe.decisions) != 2 {
		t.Fatalf("replay applied %d commands, want 2 (session frames skipped)", len(w.probe.decisions))
	}
	if w.outcome(t) != ref.outcome(t) {
		t.Fatal("restored state differs from the control without session frames")
	}
}

// ---------------------------------------------------------------------------
// The rule fixture check (C25..C27).
// ---------------------------------------------------------------------------

func recordFixture(t *testing.T, pinFrames rules.Version, snapAt rules.Version, withSnap bool) string {
	t.Helper()
	dir := t.TempDir()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRuleCity(t, disk, pinFrames)
	rc.sessionA(t)
	if withSnap {
		if err := rc.c.PinRuleVersion(snapAt); err != nil {
			t.Fatal(err)
		}
		rc.snapshot(t, disk, 5)
	}
	return dir
}

func TestRuleVersion_CheckRuleFixture(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	t.Run("good at 0", func(t *testing.T) {
		if err := CheckRuleFixture(recordFixture(t, 0, 0, true), 0); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("good at 1", func(t *testing.T) {
		if err := CheckRuleFixture(recordFixture(t, 1, 1, true), 1); err != nil {
			t.Fatal(err)
		}
	})
	bad := []struct {
		name string
		dir  func(t *testing.T) string
		want rules.Version
		sub  string
	}{
		{"missing directory", func(t *testing.T) string { return filepath.Join(t.TempDir(), "nope") }, 0, "does not exist"},
		{"empty journal", func(t *testing.T) string { return t.TempDir() }, 0, "no frames"},
		{"no snapshot", func(t *testing.T) string { return recordFixture(t, 0, 0, false) }, 0, "no snapshot"},
		{"frame at the wrong version", func(t *testing.T) string { return recordFixture(t, 0, 0, true) }, 1, "journal frame"},
		{"snapshot at the wrong version", func(t *testing.T) string { return recordFixture(t, 0, 1, true) }, 0, "snapshot"},
	}
	for _, b := range bad {
		b := b
		t.Run("rejects "+b.name, func(t *testing.T) {
			err := CheckRuleFixture(b.dir(t), b.want)
			if err == nil || !strings.Contains(err.Error(), b.sub) {
				t.Fatalf("want an error containing %q, got %v", b.sub, err)
			}
		})
	}
}

// TestRuleVersion_PinRefusesUnknownVersion: PinRuleVersion refuses a version
// the build does not know (C10).
func TestRuleVersion_PinRefusesUnknownVersion(t *testing.T) {
	rc := newRuleCity(t, nil, -1)
	if err := rc.c.PinRuleVersion(1); !isCode(err, rules.ErrSavedUnderNewerRules) {
		t.Fatalf("pin 1 under Current 0: %v", err)
	}
	if err := rc.c.PinRuleVersion(-1); !isCode(err, rules.ErrMalformedRuleStamp) {
		t.Fatalf("pin -1: %v", err)
	}
	if rc.c.RuleVersion() != 0 {
		t.Fatalf("live version moved to %d", rc.c.RuleVersion())
	}
}
