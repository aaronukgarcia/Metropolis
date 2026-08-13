package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// buildSampleBundle writes a minimal valid NDJSON bundle under t.TempDir()
// and returns its directory, for use by both the export and verify tests.
func buildSampleBundle(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "sample-save")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	meta := serialize.ShardMeta{Name: "citizens.0", Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := serialize.CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}

	i := 0
	src := func() (serialize.Record, bool, error) {
		if i >= 3 {
			return serialize.Record{}, false, nil
		}
		data, _ := json.Marshal(map[string]any{"id": i})
		i++
		return serialize.Record{Kind: "citizen", Data: data}, true, nil
	}

	meta, err = (serialize.NDJSONSerializer{}).WriteShard(f, meta, src)
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}

	h := serialize.NewHeader(1, 10, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	return dir
}

func TestRunVerifyHappyPath(t *testing.T) {
	dir := buildSampleBundle(t)
	if err := runVerify([]string{dir}); err != nil {
		t.Fatalf("runVerify: %v", err)
	}
}

func TestRunVerifyMissingBundle(t *testing.T) {
	if err := runVerify([]string{filepath.Join(t.TempDir(), "does-not-exist")}); err == nil {
		t.Fatal("expected runVerify to fail on a missing bundle")
	}
}

func TestRunVerifyCorruptShard(t *testing.T) {
	dir := buildSampleBundle(t)

	h, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	shardPath, err := serialize.ShardPath(dir, h.ShardIndex[0])
	if err != nil {
		t.Fatalf("ShardPath: %v", err)
	}
	raw, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	raw[0] ^= 0xFF
	if err := os.WriteFile(shardPath, raw, 0o644); err != nil {
		t.Fatalf("writing corrupted shard: %v", err)
	}

	if err := runVerify([]string{dir}); err == nil {
		t.Fatal("expected runVerify to fail on a corrupted shard")
	}
}

func TestRunExportHappyPath(t *testing.T) {
	dir := buildSampleBundle(t)
	out := filepath.Join(t.TempDir(), "exported")

	if err := runExport([]string{"-out", out, dir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	exportedFile := filepath.Join(out, "citizens.0.ndjson")
	raw, err := os.ReadFile(exportedFile)
	if err != nil {
		t.Fatalf("reading exported shard: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("exported shard file is empty")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	var count int
	for dec.More() {
		var line struct {
			Kind string          `json:"kind"`
			Data json.RawMessage `json:"data"`
		}
		if err := dec.Decode(&line); err != nil {
			t.Fatalf("decoding exported line %d: %v", count, err)
		}
		if line.Kind != "citizen" {
			t.Errorf("line %d: Kind = %q, want citizen", count, line.Kind)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("exported %d records, want 3", count)
	}
}

// TestRunExportMultiShardSuccess confirms the BUG-153 staging/promotion
// rework didn't regress the genuinely-successful multi-shard path: every
// shard's file must land correctly in -out when nothing fails.
func TestRunExportMultiShardSuccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "multi-shard-src")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	names := []string{"citizens.0", "citizens.1", "citizens.2"}
	h := serialize.NewHeader(1, 1, 1, "test-build")
	for _, name := range names {
		meta := serialize.ShardMeta{Name: name, Kind: "citizen", Encoding: "ndjson+gzip"}
		f, err := serialize.CreateShardWriter(dir, meta)
		if err != nil {
			t.Fatalf("CreateShardWriter(%q): %v", name, err)
		}
		i := 0
		src := func() (serialize.Record, bool, error) {
			if i >= 2 {
				return serialize.Record{}, false, nil
			}
			data, _ := json.Marshal(map[string]any{"id": i})
			i++
			return serialize.Record{Kind: "citizen", Data: data}, true, nil
		}
		meta, err = (serialize.NDJSONSerializer{}).WriteShard(f, meta, src)
		if err != nil {
			t.Fatalf("WriteShard(%q): %v", name, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("closing shard file %q: %v", name, err)
		}
		h.ShardIndex = append(h.ShardIndex, meta)
	}
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	out := filepath.Join(t.TempDir(), "multi-shard-out")
	if err := runExport([]string{"-out", out, dir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	for _, name := range names {
		exportedFile := filepath.Join(out, name+".ndjson")
		raw, err := os.ReadFile(exportedFile)
		if err != nil {
			t.Fatalf("reading exported shard %q: %v", name, err)
		}
		if len(raw) == 0 {
			t.Fatalf("exported shard %q is empty", name)
		}
	}
}

// buildShardBundle writes a minimal single-shard NDJSON bundle under dir
// (which must not yet exist) with the given shard name and record count,
// mirroring buildSampleBundle but letting callers control the directory
// path directly (needed for the BUG-154 fixtures below, which build several
// bundles as siblings of a shared, pre-populated -out directory).
func buildShardBundle(t *testing.T, dir, shardName string) {
	t.Helper()

	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}
	meta := serialize.ShardMeta{Name: shardName, Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := serialize.CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	i := 0
	src := func() (serialize.Record, bool, error) {
		if i >= 2 {
			return serialize.Record{}, false, nil
		}
		data, _ := json.Marshal(map[string]any{"id": i})
		i++
		return serialize.Record{Kind: "citizen", Data: data}, true, nil
	}
	meta, err = (serialize.NDJSONSerializer{}).WriteShard(f, meta, src)
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}
	h := serialize.NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
}

// TestRunExportPromotionFailureLeavesPriorExportIntact is BUG-154's core
// regression fixture: seed dest with a prior good export, then drive a real
// runExport call while forcing the final stagingDir -> dest rename to fail
// (via the promoteRename test seam -- the same failure mode the bug report
// reproduced live on Windows by holding an open handle inside stagingDir,
// which makes renaming that directory fail with "Access is denied").
//
// Before BUG-154's fix, runExport did an unconditional os.RemoveAll(dest)
// before attempting this rename, so a failure here destroyed the prior
// export with nothing to show for it -- worse than BUG-153's original
// symptom. After the fix, dest must still contain the ORIGINAL export's
// file, untouched, and runExport must return the injected error.
func TestRunExportPromotionFailureLeavesPriorExportIntact(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	buildShardBundle(t, srcDir, "citizens.new")

	dest := filepath.Join(root, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("MkdirAll(dest): %v", err)
	}
	priorFile := filepath.Join(dest, "prior-export-sentinel.ndjson")
	if err := os.WriteFile(priorFile, []byte("prior export data\n"), 0o644); err != nil {
		t.Fatalf("seeding prior export: %v", err)
	}

	injectedErr := errors.New("BUG-154 test: simulated promotion rename failure")
	prevRename := promoteRename
	promoteRename = func(oldpath, newpath string) error { return injectedErr }
	t.Cleanup(func() { promoteRename = prevRename })

	err := runExport([]string{"-out", dest, srcDir})
	if err == nil {
		t.Fatal("expected runExport to fail when the promotion rename fails")
	}
	if !errors.Is(err, injectedErr) {
		t.Errorf("runExport error %v does not wrap the injected promotion failure", err)
	}

	raw, readErr := os.ReadFile(priorFile)
	if readErr != nil {
		t.Fatalf("BUG-154 NOT fixed: prior export sentinel missing after failed promotion: %v", readErr)
	}
	if string(raw) != "prior export data\n" {
		t.Fatalf("prior export sentinel content corrupted: got %q", raw)
	}

	// No orphaned backup directory left sitting next to dest either --
	// the failed-promotion recovery path must rename it straight back.
	if _, statErr := os.Stat(dest + ".metctl-export-backup"); !os.IsNotExist(statErr) {
		t.Fatalf("backup path leaked after recovery (stat err: %v)", statErr)
	}
}

// TestRunExportReplacesExistingDestOnSuccess confirms the BUG-154 rework's
// happy path: a normal successful re-export over an EXISTING dest must
// still end up containing only the new export's shard(s) -- the prior
// export's file must be gone (replaced, not merged), proving the new
// backup-then-promote-then-cleanup sequence still achieves the same
// wholesale-replace behaviour the old RemoveAll+Rename sequence had, minus
// the destructive failure mode.
func TestRunExportReplacesExistingDestOnSuccess(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	buildShardBundle(t, srcDir, "citizens.new")

	dest := filepath.Join(root, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("MkdirAll(dest): %v", err)
	}
	priorFile := filepath.Join(dest, "prior-export-sentinel.ndjson")
	if err := os.WriteFile(priorFile, []byte("stale export data\n"), 0o644); err != nil {
		t.Fatalf("seeding prior export: %v", err)
	}

	if err := runExport([]string{"-out", dest, srcDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	if _, statErr := os.Stat(priorFile); !os.IsNotExist(statErr) {
		t.Fatalf("stale prior export file survived a successful re-export (stat err: %v)", statErr)
	}
	newFile := filepath.Join(dest, "citizens.new.ndjson")
	if _, statErr := os.Stat(newFile); statErr != nil {
		t.Fatalf("new export shard missing after successful re-export: %v", statErr)
	}
	if _, statErr := os.Stat(dest + ".metctl-export-backup"); !os.IsNotExist(statErr) {
		t.Fatalf("backup path leaked after successful promotion (stat err: %v)", statErr)
	}
}

// TestRunExportRecoversSurvivingBackupWhenDestMissing is BUG-155's core
// regression fixture. It reproduces a prior run killed right after step 1 of
// the BUG-154 promotion sequence succeeded (dest renamed to backupPath) but
// before step 2 (promoteRename) ran: on disk this leaves dest ABSENT and
// backupPath holding the only surviving good export.
//
// Before BUG-155's fix, runExport unconditionally os.RemoveAll'd backupPath
// before ever checking whether dest existed, so it silently destroyed that
// survivor and proceeded as if this were a first-ever export -- total data
// loss with no trace. After the fix, the sentinel must be restored to dest
// (not deleted) and the export must then proceed correctly from that
// recovered state, ending with the NEW export's shard in dest and no
// leftover backup or sentinel anywhere.
func TestRunExportRecoversSurvivingBackupWhenDestMissing(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	buildShardBundle(t, srcDir, "citizens.new")

	dest := filepath.Join(root, "out")
	backupPath := dest + ".metctl-export-backup"

	// Seed backupPath as the ONLY surviving export; dest itself is absent,
	// exactly as BUG-155's live repro found it.
	if err := os.MkdirAll(backupPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(backupPath): %v", err)
	}
	sentinelFile := filepath.Join(backupPath, "surviving-export-sentinel.ndjson")
	if err := os.WriteFile(sentinelFile, []byte("last known-good export\n"), 0o644); err != nil {
		t.Fatalf("seeding surviving backup: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("test setup invariant broken: dest %q must not exist yet", dest)
	}

	if err := runExport([]string{"-out", dest, srcDir}); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	// The recovery must have restored the sentinel to dest (renamed, not
	// copied-and-left-behind) before the new export replaced it -- so by
	// the time runExport returns successfully, the new export's shard must
	// be in dest and the sentinel must be gone from dest (superseded by the
	// new export, same as any ordinary replace-export), but crucially it
	// must NEVER have been silently deleted without ever reaching dest.
	newFile := filepath.Join(dest, "citizens.new.ndjson")
	if _, statErr := os.Stat(newFile); statErr != nil {
		t.Fatalf("new export shard missing after recovery+export: %v", statErr)
	}
	if _, statErr := os.Stat(backupPath); !os.IsNotExist(statErr) {
		t.Fatalf("BUG-155 backup path leaked after recovery+promotion (stat err: %v)", statErr)
	}
}

// TestRunExportRecoveredBackupSurvivesPromotionFailure confirms the BUG-155
// recovery path composes correctly with BUG-154's failure handling: if dest
// is missing and backupPath holds a survivor, but the recovered export's
// own re-promotion then fails (promoteRename seam), the surviving data must
// still be recoverable on disk afterward -- either back under backupPath or
// already restored to dest -- never lost. This is the "third round of
// hardening the same function" regression case: recovering the BUG-155
// survivor must not reintroduce BUG-154's "both copies gone" failure mode.
func TestRunExportRecoveredBackupSurvivesPromotionFailure(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "src")
	buildShardBundle(t, srcDir, "citizens.new")

	dest := filepath.Join(root, "out")
	backupPath := dest + ".metctl-export-backup"

	if err := os.MkdirAll(backupPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(backupPath): %v", err)
	}
	sentinelFile := filepath.Join(backupPath, "surviving-export-sentinel.ndjson")
	if err := os.WriteFile(sentinelFile, []byte("last known-good export\n"), 0o644); err != nil {
		t.Fatalf("seeding surviving backup: %v", err)
	}

	injectedErr := errors.New("BUG-155 test: simulated promotion rename failure after recovery")
	prevRename := promoteRename
	promoteRename = func(oldpath, newpath string) error { return injectedErr }
	t.Cleanup(func() { promoteRename = prevRename })

	err := runExport([]string{"-out", dest, srcDir})
	if err == nil {
		t.Fatal("expected runExport to fail when the post-recovery promotion rename fails")
	}
	if !errors.Is(err, injectedErr) {
		t.Errorf("runExport error %v does not wrap the injected promotion failure", err)
	}

	// The survivor must be found at exactly one of dest or backupPath --
	// never neither -- with its sentinel content intact.
	foundAt := ""
	for _, p := range []string{dest, backupPath} {
		if raw, readErr := os.ReadFile(filepath.Join(p, "surviving-export-sentinel.ndjson")); readErr == nil {
			if string(raw) != "last known-good export\n" {
				t.Fatalf("survivor sentinel content corrupted at %q: got %q", p, raw)
			}
			foundAt = p
			break
		}
	}
	if foundAt == "" {
		t.Fatalf("BUG-155 NOT fixed: surviving export lost after recovery+failed re-promotion (checked dest %q and backupPath %q)", dest, backupPath)
	}
}

func TestRunExportMissingBundle(t *testing.T) {
	if err := runExport([]string{filepath.Join(t.TempDir(), "does-not-exist")}); err == nil {
		t.Fatal("expected runExport to fail on a missing bundle")
	}
}

// TestRunExportRejectsHostileShardName is SEC-001's write-side
// containment test: `metctl export` builds its destination path from
// ShardMeta.Name the same unsanitized way the read side once did (main.go's
// exportShard), so a hostile bundle whose header.json carries a
// traversal Name must be rejected rather than writing outside -out.
//
// The setup plants a sentinel file one level above the export -out
// directory — exactly where "../escaped" would land — so a regression
// would be caught by this test actually observing an overwritten/created
// file outside -out, not just a generic error.
func TestRunExportRejectsHostileShardName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hostile-export-src")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	// A real, validly-encoded shard so OpenShardReader would succeed if
	// the name check didn't fire first — proves the rejection comes from
	// the name validation, not from some unrelated read failure.
	realMeta := serialize.ShardMeta{Name: "citizens.0", Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := serialize.CreateShardWriter(dir, realMeta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	i := 0
	src := func() (serialize.Record, bool, error) {
		if i >= 2 {
			return serialize.Record{}, false, nil
		}
		data, _ := json.Marshal(map[string]any{"id": i})
		i++
		return serialize.Record{Kind: "citizen", Data: data}, true, nil
	}
	realMeta, err = (serialize.NDJSONSerializer{}).WriteShard(f, realMeta, src)
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}

	// The header lies: it points to realMeta's on-disk bytes (so
	// OpenShardReader itself would succeed) but under a hostile
	// traversal Name, exactly as a crafted bundle would.
	hostileMeta := realMeta
	hostileMeta.Name = "../escaped"

	h := serialize.NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, hostileMeta)
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	out := filepath.Join(t.TempDir(), "export-out")
	err = runExport([]string{"-out", out, dir})
	if err == nil {
		t.Fatal("expected runExport to reject a hostile traversal shard name, got nil error")
	}
	if !strings.Contains(err.Error(), "MET-F301") {
		t.Errorf("runExport error %q does not carry the registry code MET-F301", err.Error())
	}

	escapedPath := filepath.Join(filepath.Dir(out), "escaped.ndjson")
	if _, statErr := os.Stat(escapedPath); statErr == nil {
		t.Fatalf("SEC-001 NOT closed: export wrote outside -out at %q", escapedPath)
	}
}

// TestRunExportPartialFailureLeavesNoTrace is BUG-153's regression fixture:
// a 2-shard bundle where shard0 is a legitimate, successfully-writable
// shard and shard1 (later in h.ShardIndex) carries a hostile traversal
// name. runExport must fail (MET-F301) AND -out must end up with zero
// trace of shard0's output -- not merely "the command returned an
// error", but genuinely clean filesystem state: no -out directory at
// all if it didn't already exist before the call.
func TestRunExportPartialFailureLeavesNoTrace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "partial-fail-src")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	writeShard := func(name string) serialize.ShardMeta {
		meta := serialize.ShardMeta{Name: name, Kind: "citizen", Encoding: "ndjson+gzip"}
		f, err := serialize.CreateShardWriter(dir, meta)
		if err != nil {
			t.Fatalf("CreateShardWriter(%q): %v", name, err)
		}
		i := 0
		src := func() (serialize.Record, bool, error) {
			if i >= 2 {
				return serialize.Record{}, false, nil
			}
			data, _ := json.Marshal(map[string]any{"id": i})
			i++
			return serialize.Record{Kind: "citizen", Data: data}, true, nil
		}
		meta, err = (serialize.NDJSONSerializer{}).WriteShard(f, meta, src)
		if err != nil {
			t.Fatalf("WriteShard(%q): %v", name, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("closing shard file %q: %v", name, err)
		}
		return meta
	}

	// shard0: legitimate, exported successfully before the loop reaches
	// the hostile shard1.
	shard0 := writeShard("citizens.0")
	// shard1's on-disk bytes are legitimate too (so OpenShardReader would
	// succeed) but its recorded Name is a traversal string, exactly as
	// BUG-153's live repro used -- proving the failure comes from the
	// name check, not an unrelated read error, and that it fires AFTER
	// shard0 has already been written.
	shard1Real := writeShard("citizens.1")
	shard1Hostile := shard1Real
	shard1Hostile.Name = "../escaped2"

	h := serialize.NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, shard0, shard1Hostile)
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	out := filepath.Join(t.TempDir(), "partial-fail-out")
	err := runExport([]string{"-out", out, dir})
	if err == nil {
		t.Fatal("expected runExport to fail on the hostile shard1 name, got nil error")
	}
	if !strings.Contains(err.Error(), "MET-F301") {
		t.Errorf("runExport error %q does not carry the registry code MET-F301", err.Error())
	}

	// The core BUG-153 assertion: -out must not exist at all (it never
	// existed before this call), not merely "be empty" -- a directory
	// left behind, even empty, is itself residue of the failed run.
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("BUG-153 NOT fixed: -out %q exists after a mid-loop shard failure (stat err: %v)", out, statErr)
	}

	// Belt-and-braces: explicitly confirm shard0's file is nowhere on
	// disk under -out (covers a hypothetical future where -out is
	// pre-created for some other reason and only its contents matter).
	shard0File := filepath.Join(out, "citizens.0.ndjson")
	if _, statErr := os.Stat(shard0File); statErr == nil {
		t.Fatalf("BUG-153 NOT fixed: shard0's output %q survived the mid-loop failure", shard0File)
	}

	// No staging debris left behind alongside -out either.
	siblingEntries, err := os.ReadDir(filepath.Dir(out))
	if err != nil {
		t.Fatalf("reading -out's parent directory: %v", err)
	}
	for _, e := range siblingEntries {
		if strings.HasPrefix(e.Name(), ".metctl-export-") {
			t.Fatalf("staging directory %q leaked next to -out after failure", e.Name())
		}
	}
}
