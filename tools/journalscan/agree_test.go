package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385620 round r1 (FIX 3): the tool and the real restore must reach
// the same verdict, with the same registered code, on the same directory.

// craftCity builds a real city (three advance frames, one snapshot taken after
// the third, and optionally one more frame after the snapshot) as a legacy
// city (rule version 0); craftLiveCity builds it under the live rules.
func craftCity(t *testing.T, dir string, tail bool) { buildCraftCity(t, dir, tail, true) }

func craftLiveCity(t *testing.T, dir string, tail bool) { buildCraftCity(t, dir, tail, false) }

func buildCraftCity(t *testing.T, dir string, tail, legacy bool) {
	t.Helper()
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: scanKey})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if legacy {
		if err := c.PinRuleVersion(0); err != nil {
			t.Fatalf("PinRuleVersion(0): %v", err)
		}
	}
	for i, n := range []int64{5, 7, 9} {
		if res := e.HandleCommand(cmd("agree-"+string(rune('a'+i)), n)); !res.Accepted {
			t.Fatalf("command rejected: %+v", res.Error)
		}
	}
	if _, ok, err := c.MaybeSnapshotEvery(context.Background(), store, scanKey, 1); err != nil || !ok {
		t.Fatalf("snapshot: ok=%v err=%v", ok, err)
	}
	if tail {
		if res := e.HandleCommand(cmd("agree-d", 3)); !res.Accepted {
			t.Fatalf("command rejected: %+v", res.Error)
		}
	}
}

// restampSnapshot rewrites the rule version in the city's only snapshot.
func restampSnapshot(t *testing.T, dir string, v int) {
	t.Helper()
	ctx := context.Background()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := st.ListSnapshots(ctx, scanKey)
	if err != nil || len(ids) != 1 {
		t.Fatalf("snapshots: %v %v", ids, err)
	}
	data, err := st.GetSnapshot(ctx, scanKey, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	edited := 0
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.HasSuffix(filepath.ToSlash(f.Name), "save-meta.json") {
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			m["ruleVersion"] = v
			b, _ = json.Marshal(m)
			edited++
		}
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	_ = zw.Close()
	if edited == 0 {
		t.Fatal("no save-meta.json in the snapshot")
	}
	if _, err := st.PutSnapshot(ctx, scanKey, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSnapshot(ctx, scanKey, ids[0]); err != nil {
		t.Fatal(err)
	}
}

// restoreVerdict runs the REAL restore against dir (a copy is NOT taken: the
// restore with a store that has no paging writes only the world-seed stamp,
// which is why the caller hands it a throwaway directory) and returns the
// registered code it refused with, or "" when it restored.
func restoreVerdict(t *testing.T, dir string) string {
	t.Helper()
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: scanKey})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := compose.RestoreLatestSnapshotOrGenesisReport(context.Background(), e, c, store, scanKey); err != nil {
		var ee *errs.E
		if errors.As(err, &ee) {
			return ee.Code
		}
		t.Fatalf("restore failed without a registered code: %v", err)
	}
	return ""
}

func journalFile(t *testing.T, dir string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "journal.dat" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no journal.dat under %s", dir)
	}
	return found
}

// protocolPlain is a version-0 (unstamped) frame.
func protocolPlain(t *testing.T) ([]byte, error) {
	t.Helper()
	return protocol.EncodeCommand(cmd("agree-plain", 2))
}

var findingRe = regexp.MustCompile(`finding: (MET-[A-Z0-9]+|-) `)

// putGarbageSnapshot stores a payload that is not a snapshot (unreadable).
func putGarbageSnapshot(t *testing.T, dir string) {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSnapshot(context.Background(), scanKey, []byte("not a zip archive")); err != nil {
		t.Fatal(err)
	}
}

func toolVerdict(t *testing.T, dir string) string {
	t.Helper()
	code, out, errb := scan(t, dir)
	// A registered rule or restore code wins over the "-" finding the tool
	// adds for an unreadable snapshot; "-" alone means "reported, no code".
	var m []string
	for _, f := range findingRe.FindAllStringSubmatch(out, -1) {
		if m == nil || (m[1] == "-" && f[1] != "-") {
			m = f
		}
	}
	switch {
	case code == exitClean && m == nil:
		return ""
	case code == exitFindings && m != nil:
		return m[1]
	}
	t.Fatalf("tool exit %d without a coded finding:\n%s\n%s", code, out, errb)
	return ""
}

func TestScan_AgreesWithRestore(t *testing.T) {
	cases := []struct {
		name     string
		want     string // the code both must report; "" means both succeed
		toolWant string // when the tool reports no registered code for a non-rule restore error: "-"
		setup    func(t *testing.T, dir string)
	}{
		{"clean city", "", "", func(t *testing.T, dir string) { craftCity(t, dir, true) }},
		{"journal frame newer than this build", "MET-G5610", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			appendRaw(t, dir, envelope(t, "2"))
		}},
		{"torn tail after a newer frame", "MET-G5610", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			appendRaw(t, dir, envelope(t, "2"))
			f, err := os.OpenFile(journalFile(t, dir), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write([]byte{0x00, 0x00})
			_ = f.Close()
		}},
		{"torn tail on a clean journal is not malformed", "", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			f, err := os.OpenFile(journalFile(t, dir), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.Write([]byte{0x00, 0x00})
			_ = f.Close()
		}},
		{"snapshot newer than this build", "MET-G5610", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			restampSnapshot(t, dir, 3)
		}},
		{"malformed stamp", "MET-G5613", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			appendRaw(t, dir, envelope(t, "0"))
		}},
		{"versions go backwards", "MET-G5612", "", func(t *testing.T, dir string) {
			craftLiveCity(t, dir, true) // live frames (version 1), then a version-0 frame
			plain, _ := protocolPlain(t)
			appendRaw(t, dir, plain)
		}},
		{"snapshot tail older than its snapshot", "MET-G5612", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true) // v0 frames, v0 snapshot, one v0 frame after it
			restampSnapshot(t, dir, 1)
		}},
		{"retired snapshot", "MET-G5611", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			rules.SetOldestSupportedForTest(t, 1)
		}},
		{"retired frames before the snapshot are not replayed", "", "", func(t *testing.T, dir string) {
			craftCity(t, dir, false) // v0 frames, v0 snapshot, no tail
			restampSnapshot(t, dir, 1)
			rules.SetOldestSupportedForTest(t, 1)
		}},
		// Round r3: an unreadable snapshot never hides a rule-version refusal the
		// restore makes first (the whole-journal gate runs before any snapshot
		// is read), and never turns a clean journal into a rule finding.
		{"future frame plus an unreadable old snapshot", "MET-G5610", "", func(t *testing.T, dir string) {
			putGarbageSnapshot(t, dir) // oldest
			craftCity(t, dir, true)
			appendRaw(t, dir, envelope(t, "2"))
		}},
		{"future frame plus an unreadable newest snapshot", "MET-G5610", "", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			putGarbageSnapshot(t, dir) // newest
			appendRaw(t, dir, envelope(t, "2"))
		}},
		// The restore fails on an unreadable newest snapshot and does not walk
		// back to the usable older one: not a rule question, so the tool
		// reports the snapshot (no registered code) and no rule finding.
		{"unreadable newest snapshot, clean journal, usable older snapshot", compose.ErrSnapshotUnpackFailed, "-", func(t *testing.T, dir string) {
			craftCity(t, dir, true)
			putGarbageSnapshot(t, dir) // newest
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			tool := toolVerdict(t, dir)
			got := restoreVerdict(t, dir)
			t.Logf("tool=%q restore=%q want=%q toolWant=%q", tool, got, tc.want, tc.toolWant)
			if got != tc.want {
				t.Fatalf("restore verdict %q, want %q", got, tc.want)
			}
			if tc.toolWant != "" {
				if tool != tc.toolWant {
					t.Fatalf("tool verdict %q, want %q", tool, tc.toolWant)
				}
				return
			}
			if tool != got {
				t.Fatalf("tool verdict %q disagrees with the restore's %q", tool, got)
			}
		})
	}
}

func TestScan_StoppedAtMalformedFrame_SaysSo(t *testing.T) {
	dir := t.TempDir()
	craftCity(t, dir, true)
	appendRaw(t, dir, envelope(t, "0"))
	appendRaw(t, dir, envelope(t, "1"))
	_, out, _ := scan(t, dir)
	if !strings.Contains(out, "frames after this point were not checked") {
		t.Fatalf("the report does not say it stopped:\n%s", out)
	}
}

// The scan unpacks snapshots into the OS temp folder and must remove every
// byte again: nothing is left behind there, and the scanned directory, the
// output and the exit code are identical on a second run.
func TestScan_LeavesNothingInOSTemp(t *testing.T) {
	dir := t.TempDir()
	craftCity(t, dir, true)
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	before := dirDigest(t, dir)
	c1, o1, _ := scan(t, dir)
	c2, o2, _ := scan(t, dir)
	if c1 != c2 || o1 != o2 {
		t.Fatalf("two runs differ: exit %d/%d\n%s\n---\n%s", c1, c2, o1, o2)
	}
	left, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d entries left in the OS temp folder, first %s", len(left), left[0].Name())
	}
	if dirDigest(t, dir) != before {
		t.Fatal("the scan changed the scanned directory")
	}
}

// A scan that runs while another process appends valid frames neither crashes
// nor reports a false malformed frame: a record being written is either
// absent or a torn tail the restore's reader drops, never a finding. The
// writer signals when it is done; there is no sleep and no fixed count of
// scans (at least one scan runs, then scans continue until the writer ends).
func TestScan_WhileFramesAreAppended_NoFalseFindings(t *testing.T) {
	dir := t.TempDir()
	craftCity(t, dir, true)
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := protocolPlain(t)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 60; i++ {
			if err := store.AppendJournal(context.Background(), scanKey, rec); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	scans := 0
	for finished := false; !finished || scans == 0; {
		select {
		case werr := <-done:
			if werr != nil {
				t.Fatalf("append: %v", werr)
			}
			finished = true
		default:
		}
		code, out, errb := scan(t, dir)
		scans++
		if code != exitClean {
			t.Fatalf("scan %d during appends: exit %d\n%s\n%s", scans, code, out, errb)
		}
	}
	if code, out, _ := scan(t, dir); code != exitClean || !strings.Contains(out, "journal frames: 64,") {
		t.Fatalf("final scan: exit %d\n%s", code, out)
	}
	t.Logf("%d scans ran while the writer appended", scans)
}

// dropAppendStore drops AppendJournal while lost is set: a failed durable
// append, the documented cause of a snapshot tail that cannot be replayed.
type dropAppendStore struct {
	persist.Store
	lost atomic.Bool
}

func (s *dropAppendStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if s.lost.Load() {
		return nil
	}
	return s.Store.AppendJournal(ctx, city, rec)
}

// craftLostAdvanceCity builds, with no tampering: snapshot S1 at rule version
// 0; one AdvanceTicks append lost; the rule version raised to 1; snapshot S2
// at version 1 and a tail after it. The lost advance shifts S2's journal
// split, so S2's tail starts with a command whose building is not in S2's
// state: replay rejects it and the restore walks back to S1.
func craftLostAdvanceCity(t *testing.T, dir string) {
	t.Helper()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := &dropAppendStore{Store: disk}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: scanKey})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := c.PinRuleVersion(0); err != nil {
		t.Fatal(err)
	}
	n := 0
	do := func(kind protocol.Kind, payload protocol.CommandPayload) {
		t.Helper()
		n++
		res := e.HandleCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID("lost-" + strconv.Itoa(n)),
			Kind:            kind,
			Payload:         payload,
		})
		if !res.Accepted {
			t.Fatalf("%s rejected: %+v", kind, res.Error)
		}
	}
	adv := func(k int64) { do(protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: k}) }
	snap := func() {
		t.Helper()
		if _, ok, err := c.MaybeSnapshotEvery(context.Background(), disk, scanKey, 5); err != nil || !ok {
			t.Fatalf("snapshot: ok=%v err=%v", ok, err)
		}
	}
	c3, c4, c5 := protocol.CellRef{X: 3, Y: 3}, protocol.CellRef{X: 4, Y: 3}, protocol.CellRef{X: 5, Y: 3}
	adv(5)
	do(protocol.KindBuy, protocol.BuyPayload{Cell: c3})
	do(protocol.KindZone, protocol.ZonePayload{Cell: c3, ZoneType: "dwelling"})
	do(protocol.KindBuild, protocol.BuildPayload{Cell: c3, BuildingType: "dwelling"})
	adv(60)
	snap() // S1: version 0, tick 65
	if err := c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	store.lost.Store(true)
	adv(10) // tick 75, the append is lost
	store.lost.Store(false)
	do(protocol.KindZone, protocol.ZonePayload{Cell: c4, ZoneType: "dwelling"})
	do(protocol.KindBuild, protocol.BuildPayload{Cell: c4, BuildingType: "dwelling"})
	adv(30)
	snap() // S2: version 1, tick 105
	do(protocol.KindZone, protocol.ZonePayload{Cell: c5, ZoneType: "dwelling"})
	do(protocol.KindBuild, protocol.BuildPayload{Cell: c5, BuildingType: "dwelling"})
	adv(60)
	do(protocol.KindDemolish, protocol.DemolishPayload{Cell: c5})
	adv(10)
}

// THE DOCUMENTED, ASSERTED DISAGREEMENT. The tool is a static check; it
// cannot run an engine. For a city whose newest snapshot's tail is rejected on
// replay (a lost append) and whose older snapshot's rule version has since
// been retired, the real restore refuses with MET-G5611 and the tool reports
// clean, with the static-check note on its report. The server is not affected:
// its own pre-check is a dry-run restore (compose.PrecheckRuleVersions, whose
// test is TestRoundR3Lane5_LostAdvanceThenRetirement_PrecheckMustAgreeWithRestore).
// Nobody should read TestScan_AgreesWithRestore as full agreement: this is the
// case it does not hold for. If the tool ever learns to run an engine, this
// test must be flipped to agreement.
func TestScan_KnownDisagreement_SnapshotTailRejectedThenOlderSnapshotRetired(t *testing.T) {
	dir := t.TempDir()
	craftLostAdvanceCity(t, dir)

	// Control: without the retirement the city wakes (from the older snapshot).
	if got := restoreVerdict(t, dir); got != "" {
		t.Fatalf("control: restore refused with %q before any retirement", got)
	}

	rules.SetOldestSupportedForTest(t, 1)
	if got := restoreVerdict(t, dir); got != rules.ErrSavedUnderRetiredRules {
		t.Fatalf("restore verdict %q, want MET-G5611", got)
	}
	code, out, errb := scan(t, dir)
	if code != exitClean {
		t.Fatalf("the static check is expected to report clean here (exit %d):\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "static (no engine is run)") || !strings.Contains(out, "dry-run restore") {
		t.Fatalf("the report does not carry the static-check note:\n%s", out)
	}
}
