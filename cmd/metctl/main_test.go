package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
