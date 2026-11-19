package compose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385620 round r1 (FIX 3): the exported, pure rule-version check is
// built from the restore's own functions; these cases pin its verdicts.

func advFrame(n int64, v rules.Version) JournalFrame {
	return JournalFrame{
		Command: protocol.Command{
			Kind:    protocol.KindAdvanceTicks,
			Payload: protocol.AdvanceTicksPayload{N: n},
		},
		Version: v,
	}
}

func codeOfErr(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("not a registered error: %v", err)
	}
	return e.Code
}

func TestCheckRuleVersionHistory_Verdicts(t *testing.T) {
	// A build whose newest rule is RefuseOccupiedCell: in the table below 0 is
	// history from before rule versions, 1 is that rule, 2 is a newer build's.
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	frames := func(vs ...rules.Version) []JournalFrame {
		out := make([]JournalFrame, 0, len(vs))
		for _, v := range vs {
			out = append(out, advFrame(5, v))
		}
		return out
	}
	cases := []struct {
		name   string
		frames []JournalFrame
		snaps  []SnapshotRuleInfo
		oldest rules.Version
		want   string
	}{
		{"clean genesis", frames(0, 0, 1), nil, 0, ""},
		{"frame newer than build", frames(0, 2), nil, 0, rules.ErrSavedUnderNewerRules},
		{"backwards", frames(1, 0), nil, 0, rules.ErrRuleVersionWentBackwards},
		{"negative", frames(-1), nil, 0, rules.ErrMalformedRuleStamp},
		{"retired frame replayed from genesis", frames(0, 1), nil, 1, rules.ErrSavedUnderRetiredRules},
		{"retired frame before the snapshot is not replayed", frames(0, 0, 1), []SnapshotRuleInfo{{Version: 1, Tick: 10}}, 1, ""},
		{"tail older than its snapshot", frames(0, 0, 0), []SnapshotRuleInfo{{Version: 1, Tick: 10}}, 0, rules.ErrRuleVersionWentBackwards},
		{"snapshot newer than build", frames(0), []SnapshotRuleInfo{{Version: 2, Tick: 5}}, 0, rules.ErrSavedUnderNewerRules},
		{"snapshot retired", frames(0), []SnapshotRuleInfo{{Version: 0, Tick: 5}}, 1, rules.ErrSavedUnderRetiredRules},
		// A snapshot taken past the end of the journal is walked past exactly as
		// the restore walks past it, down to the older usable one.
		{"short tail walks back", frames(1, 1), []SnapshotRuleInfo{{Version: 1, Tick: 5}, {Version: 1, Tick: 999}}, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules.SetOldestSupportedForTest(t, tc.oldest)
			if got := codeOfErr(t, CheckRuleVersionHistory("cid", tc.frames, tc.snaps)); got != tc.want {
				t.Fatalf("code %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsRuleVersionRefusal(t *testing.T) {
	if IsRuleVersionRefusal(nil) {
		t.Fatal("nil is not a refusal")
	}
	if !IsRuleVersionRefusal(errs.New(rules.ErrSavedUnderNewerRules, "c", map[string]any{"savedVersion": 9, "currentVersion": 0})) {
		t.Fatal("MET-G5610 must count")
	}
	if IsRuleVersionRefusal(errs.New(ErrSnapshotTailShort, "c", map[string]any{"city": "x", "tick": 1})) {
		t.Fatal("a tail refusal is not a rule-version refusal")
	}
}

// FEAT-9995385620 round r3: ONE decision procedure (DecideRuleVersions), run
// in the restore's own order, reading snapshots lazily newest first.

// recordingReader serves snapshots from a slice (oldest first) and records the
// indexes DecideRuleVersions asked for, in order.
type recordingReader struct {
	infos []SnapshotRuleInfo
	errs  map[int]error
	asked []int
}

func (r *recordingReader) read(i int) (SnapshotRuleInfo, error) {
	r.asked = append(r.asked, i)
	if err := r.errs[i]; err != nil {
		return SnapshotRuleInfo{}, err
	}
	return r.infos[i], nil
}

func TestDecideRuleVersions_UnreadableSnapshotsFollowTheRestoresOrder(t *testing.T) {
	// A build whose newest rule is RefuseOccupiedCell (table: 0 before
	// versions, 1 that rule, 2 a newer build's).
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	bad := errors.New("unreadable")
	adv := func(vs ...rules.Version) []JournalFrame {
		out := make([]JournalFrame, 0, len(vs))
		for _, v := range vs {
			out = append(out, advFrame(5, v))
		}
		return out
	}
	cases := []struct {
		name   string
		frames []JournalFrame
		infos  []SnapshotRuleInfo
		errs   map[int]error
		want   string
		asked  []int
	}{
		// The whole-journal gate runs before any snapshot is read.
		{"future frame, every snapshot unreadable", adv(0, 2), make([]SnapshotRuleInfo, 2), map[int]error{0: bad, 1: bad}, rules.ErrSavedUnderNewerRules, nil},
		{"future frame, newest unreadable", adv(0, 2), []SnapshotRuleInfo{{Version: 0, Tick: 5}, {}}, map[int]error{1: bad}, rules.ErrSavedUnderNewerRules, nil},
		{"backwards frames, old snapshot unreadable", adv(1, 0), []SnapshotRuleInfo{{}}, map[int]error{0: bad}, rules.ErrRuleVersionWentBackwards, nil},
		// The restore fails on an unreadable newest snapshot; it never walks back.
		{"clean journal, newest unreadable, older usable", adv(0, 0), []SnapshotRuleInfo{{Version: 0, Tick: 5}, {}}, map[int]error{1: bad}, "", []int{1}},
		// Walked past (tail short) then unreadable: the restore stops there.
		{"newest tail short, next unreadable", adv(0, 0), []SnapshotRuleInfo{{}, {Version: 0, Tick: 999}}, map[int]error{0: bad}, "", []int{1, 0}},
		// A newer snapshot that is refused ends the walk before an older unreadable one is read.
		{"newest from a newer build, older unreadable", adv(0, 0), []SnapshotRuleInfo{{}, {Version: 2, Tick: 5}}, map[int]error{0: bad}, rules.ErrSavedUnderNewerRules, []int{1}},
		// The first usable snapshot ends the walk: an older one is never read.
		{"usable newest stops the walk", adv(0, 0), []SnapshotRuleInfo{{}, {Version: 0, Tick: 5}}, map[int]error{0: bad}, "", []int{1}},
		{"walked past newest, older tail older than it", adv(0, 0, 0), []SnapshotRuleInfo{{Version: 1, Tick: 10}, {Version: 0, Tick: 999}}, nil, rules.ErrRuleVersionWentBackwards, []int{1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := &recordingReader{infos: tc.infos, errs: tc.errs}
			if got := codeOfErr(t, DecideRuleVersions("cid", tc.frames, len(tc.infos), rd.read)); got != tc.want {
				t.Fatalf("code %q, want %q", got, tc.want)
			}
			if len(rd.asked) != len(tc.asked) {
				t.Fatalf("snapshots read %v, want %v", rd.asked, tc.asked)
			}
			for i := range rd.asked {
				if rd.asked[i] != tc.asked[i] {
					t.Fatalf("snapshots read %v, want %v", rd.asked, tc.asked)
				}
			}
		})
	}
}

// precheckT runs the dry-run pre-check the way the host does, except that the
// throwaway composition carries the same quiet coastal module the test cities
// were built with (the server builds both with the default one).
func precheckT(t *testing.T, store persist.Store, city persist.CityKey, seed uint64) error {
	t.Helper()
	return precheckRuleVersionsDeps(context.Background(), NewDryRunStore(store), city, seed, &Deps{Coastal: quietCoastal(t)})
}

// countingStore counts GetSnapshot calls and can swap a snapshot's payload.
type countingStore struct {
	persist.Store
	gets atomic.Int64
}

func (s *countingStore) GetSnapshot(ctx context.Context, city persist.CityKey, id persist.SnapshotID) ([]byte, error) {
	s.gets.Add(1)
	return s.Store.GetSnapshot(ctx, city, id)
}

// precheckCity builds a city with one real snapshot at the cadence tick and a
// short tail, and returns its store, key and the snapshot payload.
func precheckCity(t *testing.T) (persist.Store, persist.CityKey, []byte) {
	t.Helper()
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "r3-precheck"}
	e, comp := buildPersistedComposition(t, store, city)
	driveGameplayOnly(t, e)
	advanceViaCommand(t, e, SnapshotCadenceTicks)
	id, ok, err := comp.MaybeSnapshot(ctx, store, city)
	if err != nil || !ok {
		t.Fatalf("MaybeSnapshot: ok=%v err=%v", ok, err)
	}
	advanceViaCommand(t, e, 10)
	data, err := store.GetSnapshot(ctx, city, id)
	if err != nil {
		t.Fatal(err)
	}
	return store, city, data
}

// restoreTarget is a fresh, never-ticked engine and composition for a restore.
func restoreTarget(t *testing.T) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	c, err := Wire(e, &Deps{Coastal: quietCoastal(t)})
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	return e, c
}

// The normal case reads ONE snapshot however many the store holds.
func TestPrecheckRuleVersions_NormalCaseReadsOneSnapshot(t *testing.T) {
	for _, n := range []int{1, 5, 20} {
		store, city, data := precheckCity(t)
		for i := 1; i < n; i++ { // PutSnapshot directly: no pruning, so 20 are held
			if _, err := store.PutSnapshot(context.Background(), city, data); err != nil {
				t.Fatal(err)
			}
		}
		ids, _ := store.ListSnapshots(context.Background(), city)
		if len(ids) != n {
			t.Fatalf("store holds %d snapshots, want %d", len(ids), n)
		}
		cs := &countingStore{Store: store}
		if err := precheckT(t, cs, city, roundTripSeed); err != nil {
			t.Fatalf("%d snapshots: clean city refused: %v", n, err)
		}
		if got := cs.gets.Load(); got != 1 {
			t.Fatalf("%d snapshots: pre-check read %d snapshots, want exactly 1", n, got)
		}
	}
}

// A future frame plus an unreadable snapshot: the pre-check and the restore
// both refuse with MET-G5610, and the pre-check reads no snapshot at all.
func TestPrecheckRuleVersions_FutureFrameAndUnreadableSnapshot_AgreesWithRestore(t *testing.T) {
	ctx := context.Background()
	store, city, _ := precheckCity(t)
	if _, err := store.PutSnapshot(ctx, city, []byte("not a zip archive")); err != nil {
		t.Fatal(err)
	}
	fut := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(), Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 1}}
	rec, err := encodeJournalFrame(fut, rules.Current+1) // a frame from a newer build
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendJournal(ctx, city, rec); err != nil {
		t.Fatal(err)
	}
	cs := &countingStore{Store: store}
	pre := precheckT(t, cs, city, roundTripSeed)
	if codeOfErr(t, pre) != rules.ErrSavedUnderNewerRules {
		t.Fatalf("pre-check = %v, want MET-G5610", pre)
	}
	if cs.gets.Load() != 0 {
		t.Fatalf("pre-check read %d snapshots before the journal gate refused", cs.gets.Load())
	}
	eR, compR := restoreTarget(t)
	_, err = RestoreLatestSnapshotOrGenesisReport(ctx, eR, compR, store, city)
	if codeOfErr(t, err) != rules.ErrSavedUnderNewerRules {
		t.Fatalf("restore = %v, want MET-G5610", err)
	}
}

// A snapshot payload holding TWO bundles, the composition bundle second: the
// pre-check's reader and the restore both pick the bundle by its display name
// (not the first one), so they agree on its version, and a decoy first bundle
// stamped from a newer build refuses neither.
func TestSnapshotWithDecoyBundleFirst_PrecheckAndRestoreAgree(t *testing.T) {
	ctx := context.Background()
	store, city, data := precheckCity(t)
	root := t.TempDir()
	if err := unzipDir(data, root); err != nil {
		t.Fatal(err)
	}
	sums, _, err := save.List(root)
	if err != nil || len(sums) != 1 || sums[0].DisplayName != compositionSaveName {
		t.Fatalf("snapshot layout: %v %v", sums, err)
	}
	orig := sums[0].Path
	decoy := filepath.Join(root, "manual", "0-decoy")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(orig, decoy); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(decoy, "save-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatal(err)
	}
	m["displayName"] = "decoy"
	m["ruleVersion"] = 9
	if meta, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "save-meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	sums, _, err = save.List(root)
	if err != nil || len(sums) != 2 {
		t.Fatalf("want two bundles, got %v %v", sums, err)
	}
	if sums[0].DisplayName != "decoy" || sums[1].DisplayName != compositionSaveName {
		t.Fatalf("bundle order %q then %q: the composition bundle must be second", sums[0].DisplayName, sums[1].DisplayName)
	}
	packed, err := zipDir(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := ReadSnapshotRuleInfo(packed)
	if err != nil || info.Version != int(rules.Current) || info.Tick != sums[1].CreatedAtTick {
		t.Fatalf("reader picked %+v (%v), want the composition bundle: version %d tick %d", info, err, rules.Current, sums[1].CreatedAtTick)
	}
	ids, _ := store.ListSnapshots(ctx, city)
	for _, id := range ids {
		if err := store.DeleteSnapshot(ctx, city, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutSnapshot(ctx, city, packed); err != nil {
		t.Fatal(err)
	}
	if err := precheckT(t, store, city, roundTripSeed); err != nil {
		t.Fatalf("pre-check refused: %v", err)
	}
	eR, compR := restoreTarget(t)
	rep, err := RestoreLatestSnapshotOrGenesisReport(ctx, eR, compR, store, city)
	if err != nil {
		t.Fatalf("restore refused: %v", err)
	}
	if !rep.UsedSnapshot {
		t.Fatal("restore did not use the snapshot")
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
