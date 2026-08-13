package save

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestWriteBundle_ForcedFailure_NeverVisibleToListOrLoad is AC-9: force
// a failure partway through a save (a participant errors after N
// records) and assert, against the save root as a whole, that (a) List
// returns exactly the same result as before the attempt, and (b) no
// directory reachable by Load/List carries the failed save's intended
// name — the "does the naive final-return-value check even matter"
// distinction this AC's own text calls out.
func TestWriteBundle_ForcedFailure_NeverVisibleToListOrLoad(t *testing.T) {
	root := t.TempDir()
	good := newWidgetParticipant(widget{ID: 1, Name: "already-here", Score: 1})
	mgr := NewManager(root, []Participant{good}, "test-corr")
	if err := mgr.SaveManual(fixtureContext(1, 0), "existing"); err != nil {
		t.Fatalf("setup SaveManual: %v", err)
	}

	before, readErrsBefore, err := List(root)
	if err != nil || len(readErrsBefore) != 0 {
		t.Fatalf("List before: err=%v readErrs=%v", err, readErrsBefore)
	}

	failMgr := NewManager(root, []Participant{&erroringParticipant{kind: "widget", failAfter: 0}}, "test-corr")
	if err := failMgr.SaveManual(fixtureContext(2, 0), "will-fail"); err == nil {
		t.Fatalf("SaveManual with a forced participant failure returned nil, want an error")
	}

	after, readErrsAfter, err := List(root)
	if err != nil || len(readErrsAfter) != 0 {
		t.Fatalf("List after: err=%v readErrs=%v", err, readErrsAfter)
	}
	if len(after) != len(before) {
		t.Fatalf("List after a failed save returned %d entries, want unchanged %d", len(after), len(before))
	}

	failedDir := manualDir(root, "will-fail")
	if _, err := os.Stat(failedDir); !os.IsNotExist(err) {
		t.Fatalf("failed save's intended directory %q exists (stat err=%v), want it absent from the discoverable tree", failedDir, err)
	}
	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	if _, _, err := loadMgr.Load(failedDir); err == nil {
		t.Fatalf("Load(failedDir) succeeded, want an error — the failed save must never become loadable")
	}
}

// TestLoadLatest_SkipsCorruptedNewest is AC-10 (BUG-054's pattern
// applied here): 3 valid autosaves plus a hand-corrupted 4th (newest)
// must make LoadLatest return the 3rd (newest-still-valid) bundle's
// state with a non-nil warning identifying the skipped one.
func TestLoadLatest_SkipsCorruptedNewest(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant()
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	for y := 0; y < 4; y++ {
		widgets.items = []widget{{ID: y, Name: "year", Score: float64(y)}}
		if err := mgr.Autosave(fixtureContext(int64(y), int64(y))); err != nil {
			t.Fatalf("Autosave %d: %v", y, err)
		}
	}

	// Corrupt the newest (seq 3): byte-flip a shard file, matching
	// int.serializer's own corruption test pattern.
	newestDir := autosaveDir(root, 3)
	shardsDir := serialize.ShardsDir(newestDir)
	entries, err := os.ReadDir(shardsDir)
	if err != nil {
		t.Fatalf("reading shards dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("test setup: expected at least one shard file")
	}
	shardPath := shardsDir + string(os.PathSeparator) + entries[0].Name()
	data, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("shard file unexpectedly empty")
	}
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(shardPath, data, 0o644); err != nil {
		t.Fatalf("writing corrupted shard: %v", err)
	}

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets}, "test-corr")
	header, _, skipped, err := loadMgr.LoadLatest()
	if err != nil {
		t.Fatalf("LoadLatest: %v, want it to skip the corrupted newest and succeed on seq 2", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("LoadLatest skipped %d entries, want exactly 1 (the corrupted newest)", len(skipped))
	}
	if skipped[0].Path != newestDir {
		t.Fatalf("LoadLatest skipped %q, want the corrupted newest %q", skipped[0].Path, newestDir)
	}
	if skipped[0].Reason == nil {
		t.Fatalf("LoadLatest's skip record has a nil Reason, want a non-nil explanation")
	}
	if header.CreatedAtTick != 2 {
		t.Fatalf("LoadLatest returned header for tick %d, want tick 2 (seq 2, the newest-still-valid)", header.CreatedAtTick)
	}
	want := []widget{{ID: 2, Name: "year", Score: 2}}
	if len(loadWidgets.State()) != 1 || loadWidgets.State()[0] != want[0] {
		t.Fatalf("LoadLatest reconstructed state = %+v, want %+v", loadWidgets.State(), want)
	}
}

// TestLoadLatest_AllCorrupted is AC-10's exhausted-history edge: every
// candidate failing must surface ErrNoValidSaveFound, not a silent
// success.
func TestLoadLatest_AllCorrupted(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 1})}, "test-corr")
	if err := mgr.Autosave(fixtureContext(0, 0)); err != nil {
		t.Fatalf("Autosave: %v", err)
	}
	dir := autosaveDir(root, 0)
	if err := os.RemoveAll(serialize.ShardsDir(dir)); err != nil {
		t.Fatalf("removing shards dir: %v", err)
	}

	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	_, _, skipped, err := loadMgr.LoadLatest()
	if err == nil {
		t.Fatalf("LoadLatest with every candidate broken returned nil error")
	}
	if !errors.Is(err, &errs.E{Code: ErrNoValidSaveFound}) {
		t.Fatalf("LoadLatest error = %v, want MET-E810 (ErrNoValidSaveFound)", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %v, want exactly 1 entry", skipped)
	}
}

// TestLoad_FormatVersionMismatch is AC-12: a bundle with a bumped-major
// FormatVersion must produce a MET- coded error naming both versions.
func TestLoad_FormatVersionMismatch(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 1})}, "test-corr")
	if err := mgr.SaveManual(fixtureContext(1, 0), "s1"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	dir := manualDir(root, "s1")
	header, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	header.FormatVersion = "99.0.0"
	if err := serialize.WriteHeader(dir, header); err != nil {
		t.Fatalf("WriteHeader with bumped major: %v", err)
	}

	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	_, _, err = loadMgr.Load(dir)
	if err == nil {
		t.Fatalf("Load with a newer-major FormatVersion returned nil error")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("Load error is not a registry-sourced *errs.E: %v", err)
	}
	if e.Code != ErrFormatVersionMismatch {
		t.Fatalf("Load error code = %q, want %q (ErrFormatVersionMismatch)", e.Code, ErrFormatVersionMismatch)
	}
	if !containsBoth(e.Error(), "99.0.0", serialize.CurrentFormatVersion) {
		t.Fatalf("Load error message %q does not name both the saved (99.0.0) and current (%s) format versions", e.Error(), serialize.CurrentFormatVersion)
	}
}

func containsBoth(s, a, b string) bool {
	return strings.Contains(s, a) && strings.Contains(s, b)
}

// TestLoad_CorruptedShard_ReturnsRegistrySourcedError is the Destructive
// round's REJECT finding (Vex, on this item): Load's ValidateBundle
// fallback used to return the BARE, unwrapped error for every corruption
// shape except a FormatVersion mismatch, so errors.As(err, &errs.E{})
// was false — a direct GR#7 violation on the corrupted-save read path
// this item exists to harden. This table reproduces every corruption
// shape Vex tried and asserts each one now surfaces as a registry-sourced
// *errs.E carrying ErrBundleValidationFailed, wrapping the original cause
// (checked via errors.Unwrap/strings.Contains so no diagnostic detail was
// lost in translation).
func TestLoad_CorruptedShard_ReturnsRegistrySourcedError(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(t *testing.T, dir string)
	}{
		{
			name: "sha256_mismatch",
			corrupt: func(t *testing.T, dir string) {
				shardPath := onlyShardPath(t, dir)
				data, err := os.ReadFile(shardPath)
				if err != nil {
					t.Fatalf("reading shard: %v", err)
				}
				data[len(data)/2] ^= 0xFF
				if err := os.WriteFile(shardPath, data, 0o644); err != nil {
					t.Fatalf("writing corrupted shard: %v", err)
				}
			},
		},
		{
			name: "size_mismatch",
			corrupt: func(t *testing.T, dir string) {
				shardPath := onlyShardPath(t, dir)
				data, err := os.ReadFile(shardPath)
				if err != nil {
					t.Fatalf("reading shard: %v", err)
				}
				if err := os.WriteFile(shardPath, append(data, 0x00, 0x01, 0x02), 0o644); err != nil {
					t.Fatalf("writing truncated/extended shard: %v", err)
				}
			},
		},
		{
			name: "missing_header",
			corrupt: func(t *testing.T, dir string) {
				if err := os.Remove(serialize.HeaderPath(dir)); err != nil {
					t.Fatalf("removing header.json: %v", err)
				}
			},
		},
		{
			name: "shard_path_is_a_directory",
			corrupt: func(t *testing.T, dir string) {
				shardPath := onlyShardPath(t, dir)
				if err := os.Remove(shardPath); err != nil {
					t.Fatalf("removing shard file: %v", err)
				}
				if err := os.Mkdir(shardPath, 0o755); err != nil {
					t.Fatalf("replacing shard file with a directory: %v", err)
				}
			},
		},
		{
			name: "semantically_bogus_header_field",
			corrupt: func(t *testing.T, dir string) {
				header, err := serialize.ReadHeader(dir)
				if err != nil {
					t.Fatalf("ReadHeader: %v", err)
				}
				if len(header.ShardIndex) == 0 {
					t.Fatalf("test setup: expected at least one shard in ShardIndex")
				}
				header.ShardIndex[0].ByteSize = -1
				if err := serialize.WriteHeader(dir, header); err != nil {
					t.Fatalf("WriteHeader with a negative ByteSize: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mgr := NewManager(root, []Participant{newWidgetParticipant(widget{ID: 1, Name: "corruptme", Score: 1})}, "test-corr")
			if err := mgr.SaveManual(fixtureContext(1, 0), "s1"); err != nil {
				t.Fatalf("SaveManual: %v", err)
			}
			dir := manualDir(root, "s1")

			tc.corrupt(t, dir)

			loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
			_, _, err := loadMgr.Load(dir)
			if err == nil {
				t.Fatalf("Load against a %s-corrupted bundle returned nil error", tc.name)
			}

			var e *errs.E
			if !errors.As(err, &e) {
				t.Fatalf("Load error for %s is not a registry-sourced *errs.E (the pre-fix REJECT finding): %v", tc.name, err)
			}
			if e.Code != ErrBundleValidationFailed {
				t.Fatalf("Load error code for %s = %q, want %q (ErrBundleValidationFailed)", tc.name, e.Code, ErrBundleValidationFailed)
			}
			if e.Wrapped == nil {
				t.Fatalf("Load error for %s has no wrapped cause -- diagnostic detail was lost", tc.name)
			}
		})
	}
}

// TestLoadLatest_SkipInfoReasonIsRegistrySourced confirms AC-10's
// skip-and-continue behavior still works after the fix AND that
// SkipInfo.Reason -- which LoadLatest populates straight from Load's
// returned error -- now carries the registry-sourced error instead of a
// bare one, closing the second half of Vex's finding (LoadLatest
// inherited the bare error via this exact field).
func TestLoadLatest_SkipInfoReasonIsRegistrySourced(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant()
	mgr := NewManager(root, []Participant{widgets}, "test-corr")

	for y := 0; y < 2; y++ {
		widgets.items = []widget{{ID: y, Name: "year", Score: float64(y)}}
		if err := mgr.Autosave(fixtureContext(int64(y), int64(y))); err != nil {
			t.Fatalf("Autosave %d: %v", y, err)
		}
	}

	newestDir := autosaveDir(root, 1)
	shardPath := onlyShardPath(t, newestDir)
	data, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	data[len(data)/2] ^= 0xFF
	if err := os.WriteFile(shardPath, data, 0o644); err != nil {
		t.Fatalf("writing corrupted shard: %v", err)
	}

	loadMgr := NewManager(root, []Participant{newWidgetParticipant()}, "test-corr")
	_, _, skipped, err := loadMgr.LoadLatest()
	if err != nil {
		t.Fatalf("LoadLatest: %v, want it to skip the corrupted newest and succeed on seq 0", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("LoadLatest skipped %d entries, want exactly 1", len(skipped))
	}

	var e *errs.E
	if !errors.As(skipped[0].Reason, &e) {
		t.Fatalf("SkipInfo.Reason is not a registry-sourced *errs.E: %v", skipped[0].Reason)
	}
	if e.Code != ErrBundleValidationFailed {
		t.Fatalf("SkipInfo.Reason code = %q, want %q (ErrBundleValidationFailed)", e.Code, ErrBundleValidationFailed)
	}
}

// onlyShardPath resolves the path to the (fixture-guaranteed) single
// shard file under dir, for corruption tests that need to mutate it
// directly.
func onlyShardPath(t *testing.T, dir string) string {
	t.Helper()
	shardsDir := serialize.ShardsDir(dir)
	entries, err := os.ReadDir(shardsDir)
	if err != nil {
		t.Fatalf("reading shards dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("shards dir has %d entries, want exactly 1 for this fixture", len(entries))
	}
	return shardsDir + string(os.PathSeparator) + entries[0].Name()
}
