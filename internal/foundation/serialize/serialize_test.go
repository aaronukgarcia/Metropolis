package serialize

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// recordSourceFromSlice builds a RecordSource that yields recs in order,
// then reports exhaustion. Used throughout as the simplest possible
// streaming producer.
func recordSourceFromSlice(recs []Record) RecordSource {
	i := 0
	return func() (Record, bool, error) {
		if i >= len(recs) {
			return Record{}, false, nil
		}
		r := recs[i]
		i++
		return r, true, nil
	}
}

func sampleRecords(n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		data, _ := json.Marshal(map[string]any{"id": i, "name": "citizen"})
		recs[i] = Record{Kind: "citizen", Data: data}
	}
	return recs
}

func TestNDJSONRoundTrip(t *testing.T) {
	recs := sampleRecords(50)
	var buf bytes.Buffer

	ser := NDJSONSerializer{}
	meta, err := ser.WriteShard(&buf, ShardMeta{Name: "citizens.0", Kind: "citizen"}, recordSourceFromSlice(recs))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if meta.RecordCount != int64(len(recs)) {
		t.Fatalf("RecordCount = %d, want %d", meta.RecordCount, len(recs))
	}
	if meta.ByteSize != int64(buf.Len()) {
		t.Fatalf("ByteSize = %d, want %d (actual buffer length)", meta.ByteSize, buf.Len())
	}
	if meta.SHA256 == "" {
		t.Fatal("SHA256 not set")
	}
	if meta.Encoding != "ndjson+gzip" {
		t.Fatalf("Encoding = %q, want ndjson+gzip", meta.Encoding)
	}

	var got []Record
	err = ser.ReadShard(bytes.NewReader(buf.Bytes()), func(r Record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if len(got) != len(recs) {
		t.Fatalf("read back %d records, want %d", len(got), len(recs))
	}
	for i := range recs {
		if got[i].Kind != recs[i].Kind {
			t.Errorf("record %d: Kind = %q, want %q", i, got[i].Kind, recs[i].Kind)
		}
		if !bytes.Equal(got[i].Data, recs[i].Data) {
			t.Errorf("record %d: Data = %s, want %s", i, got[i].Data, recs[i].Data)
		}
	}
}

func TestNDJSONRoundTripEmptyShard(t *testing.T) {
	var buf bytes.Buffer
	ser := NDJSONSerializer{}
	meta, err := ser.WriteShard(&buf, ShardMeta{Name: "empty"}, recordSourceFromSlice(nil))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if meta.RecordCount != 0 {
		t.Fatalf("RecordCount = %d, want 0", meta.RecordCount)
	}

	var count int
	err = ser.ReadShard(bytes.NewReader(buf.Bytes()), func(Record) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if count != 0 {
		t.Fatalf("handled %d records from empty shard, want 0", count)
	}
}

func TestNDJSONByteDeterminism(t *testing.T) {
	recs := sampleRecords(200)
	ser := NDJSONSerializer{}

	var buf1, buf2 bytes.Buffer
	if _, err := ser.WriteShard(&buf1, ShardMeta{Name: "s"}, recordSourceFromSlice(recs)); err != nil {
		t.Fatalf("WriteShard #1: %v", err)
	}
	if _, err := ser.WriteShard(&buf2, ShardMeta{Name: "s"}, recordSourceFromSlice(recs)); err != nil {
		t.Fatalf("WriteShard #2: %v", err)
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("two writes of the same records produced different bytes; NDJSONSerializer must be byte-deterministic")
	}
}

func TestNDJSONWriteShardPropagatesSourceError(t *testing.T) {
	sentinel := errTest("boom")
	src := func() (Record, bool, error) { return Record{}, false, sentinel }

	var buf bytes.Buffer
	_, err := (NDJSONSerializer{}).WriteShard(&buf, ShardMeta{Name: "s"}, src)
	if err == nil {
		t.Fatal("expected error from failing RecordSource, got nil")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestBundleRoundTripAndValidate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save1")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	recs := sampleRecords(10)
	meta := ShardMeta{Name: "citizens.0", Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	meta, err = (NDJSONSerializer{}).WriteShard(f, meta, recordSourceFromSlice(recs))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}

	h := NewHeader(42, 100, 3, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	got, err := ValidateBundle(dir)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if got.WorldSeed != 42 || got.GameMonth != 3 || got.CreatedAtTick != 100 {
		t.Fatalf("header round-trip mismatch: %+v", got)
	}

	r, err := OpenShardReader(dir, meta)
	if err != nil {
		t.Fatalf("OpenShardReader: %v", err)
	}
	defer r.Close()
	var readBack int
	if err := (NDJSONSerializer{}).ReadShard(r, func(Record) error { readBack++; return nil }); err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if readBack != len(recs) {
		t.Fatalf("read back %d records, want %d", readBack, len(recs))
	}
}

func TestValidateBundleCatchesCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save2")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	meta := ShardMeta{Name: "citizens.0", Encoding: "ndjson+gzip"}
	f, err := CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	meta, err = (NDJSONSerializer{}).WriteShard(f, meta, recordSourceFromSlice(sampleRecords(5)))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	f.Close()

	h := NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	// Flip a byte in the shard file to simulate corruption.
	shardPath, err := ShardPath(dir, meta)
	if err != nil {
		t.Fatalf("ShardPath: %v", err)
	}
	raw, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("shard file unexpectedly empty")
	}
	raw[0] ^= 0xFF
	if err := os.WriteFile(shardPath, raw, 0o644); err != nil {
		t.Fatalf("writing corrupted shard: %v", err)
	}

	if _, err := ValidateBundle(dir); err == nil {
		t.Fatal("expected ValidateBundle to detect corruption, got nil error")
	}
}

func TestCreateBundleDirRefusesExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "save3")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("first CreateBundleDir: %v", err)
	}
	if err := CreateBundleDir(dir); err == nil {
		t.Fatal("expected error creating an already-existing bundle directory")
	}
}

func TestCheckFormatVersionMatrix(t *testing.T) {
	cases := []struct {
		name    string
		saved   string
		wantErr bool
	}{
		{"same version", CurrentFormatVersion, false},
		{"same major, higher minor", "1.9.0", false},
		{"same major, higher patch", "1.0.99", false},
		{"same major, lower minor", "1.0.0", false},
		{"older major", "0.9.0", true},
		{"newer major", "2.0.0", true},
		{"malformed", "not-a-version", true},
		{"two components", "1.0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckFormatVersion(c.saved)
			if c.wantErr && err == nil {
				t.Fatalf("CheckFormatVersion(%q): expected error, got nil", c.saved)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("CheckFormatVersion(%q): unexpected error: %v", c.saved, err)
			}
		})
	}
}

func TestParseSemVer(t *testing.T) {
	got, err := ParseSemVer("1.2.3")
	if err != nil {
		t.Fatalf("ParseSemVer: %v", err)
	}
	want := Semver{Major: 1, Minor: 2, Patch: 3}
	if got != want {
		t.Fatalf("ParseSemVer(1.2.3) = %+v, want %+v", got, want)
	}

	if _, err := ParseSemVer("1.2"); err == nil {
		t.Fatal("expected error for two-component version")
	}
	if _, err := ParseSemVer("1.2.x"); err == nil {
		t.Fatal("expected error for non-numeric component")
	}
	if _, err := ParseSemVer("-1.2.3"); err == nil {
		t.Fatal("expected error for negative component")
	}
}

func TestDebugTouchedIsSticky(t *testing.T) {
	h := NewHeader(1, 1, 1, "test-build")
	if h.DebugTouched {
		t.Fatal("new header must start with DebugTouched = false")
	}

	h.TouchDebug()
	if !h.DebugTouched {
		t.Fatal("TouchDebug must set DebugTouched = true")
	}

	// Sticky: merging false must not clear it.
	h.MergeDebugTouched(false)
	if !h.DebugTouched {
		t.Fatal("DebugTouched must remain true after merging false (sticky invariant)")
	}

	h.MergeDebugTouched(true)
	if !h.DebugTouched {
		t.Fatal("DebugTouched must remain true after merging true")
	}

	// A fresh header carrying forward a previously debug-touched flag via
	// MergeDebugTouched (e.g. metctl export / save-over) must also end up
	// touched.
	h2 := NewHeader(1, 1, 1, "test-build")
	h2.MergeDebugTouched(true)
	if !h2.DebugTouched {
		t.Fatal("MergeDebugTouched(true) on a clean header must set DebugTouched")
	}
}

func TestHeaderJSONRoundTripPreservesDebugTouched(t *testing.T) {
	h := NewHeader(7, 8, 9, "test-build")
	h.TouchDebug()

	raw, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Header
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !got.DebugTouched {
		t.Fatal("DebugTouched did not survive a JSON round-trip")
	}
}

func TestBinarySerializerNotImplemented(t *testing.T) {
	bs := BinarySerializer{}
	if _, err := bs.WriteShard(&bytes.Buffer{}, ShardMeta{Name: "x"}, recordSourceFromSlice(nil)); err == nil {
		t.Fatal("expected BinarySerializer.WriteShard to return an error")
	}
	if err := bs.ReadShard(bytes.NewReader(nil), func(Record) error { return nil }); err == nil {
		t.Fatal("expected BinarySerializer.ReadShard to return an error")
	}
}
