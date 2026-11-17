package serialize

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// TestBUG1205_ShardGzipLevelPinned pins the shard compression level: a silent
// return to gzip.BestCompression cost ~70 percent of a 1M-citizen Save
// (BUG-1205, Q100156). The gzip header's XFL byte (offset 8) records the
// level class: 4 = fastest, 2 = best compression.
func TestBUG1205_ShardGzipLevelPinned(t *testing.T) {
	if ShardGzipLevel != gzip.BestSpeed {
		t.Fatalf("ShardGzipLevel = %d, want gzip.BestSpeed (%d)", ShardGzipLevel, gzip.BestSpeed)
	}
	var buf bytes.Buffer
	recs := []Record{{Kind: "k", Data: []byte(`{"a":1}`)}}
	if _, err := (NDJSONSerializer{}).WriteShard(&buf, ShardMeta{Name: "s", Kind: "k"}, recordSourceFromSlice(recs)); err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	b := buf.Bytes()
	if len(b) < 10 {
		t.Fatalf("short gzip output: %d bytes", len(b))
	}
	if b[8] != 4 {
		t.Fatalf("gzip XFL byte = %d, want 4 (BestSpeed); the writer is not using the fast level", b[8])
	}
}
