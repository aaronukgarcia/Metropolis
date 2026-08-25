package core

import (
	"bytes"
	"math"
	"testing"
)

// TestRegression_Snapshot_SeedAtInt64Max_Succeeds proves the boundary is
// exactly math.MaxInt64: a seed AT the int64 ceiling is representable and
// must round-trip cleanly, not be rejected off-by-one.
func TestRegression_Snapshot_SeedAtInt64Max_Succeeds(t *testing.T) {
	e := NewEngine(WithWorldSeed(uint64(math.MaxInt64)))
	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, "corr-bug310-seed-max")
	if err != nil {
		t.Fatalf("Snapshot with seed == MaxInt64: unexpected error: %v", err)
	}
	if header.WorldSeed != math.MaxInt64 {
		t.Fatalf("header.WorldSeed = %d, want %d", header.WorldSeed, int64(math.MaxInt64))
	}
}

// TestRegression_Snapshot_SeedAboveInt64Max_Rejected is the actual
// BUG-310 attack: a world seed >= 2^63 would silently wrap to a negative
// int64 in the header (disagreeing with the authoritative uint64 seed in
// the meta shard) if Snapshot did not reject it explicitly.
func TestRegression_Snapshot_SeedAboveInt64Max_Rejected(t *testing.T) {
	e := NewEngine(WithWorldSeed(uint64(math.MaxInt64) + 1))
	var buf bytes.Buffer
	_, err := e.Snapshot(&buf, "corr-bug310-seed-overflow")
	if err == nil {
		t.Fatal("Snapshot with seed == MaxInt64+1: want a rejection error, got nil (header would silently wrap negative)")
	}
}
