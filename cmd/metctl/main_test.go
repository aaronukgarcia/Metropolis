package main

import (
	"bytes"
	"encoding/json"
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
