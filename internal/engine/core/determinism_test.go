package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// snapshotHash runs a fresh Engine at the given pool size, same seed
// and same command log (AdvanceTicks(63): a couple of month boundaries
// plus a partial month), takes a Snapshot, and returns its sha256 hex
// digest — the hashable/serializable world-state hook AC-15 requires
// engine.core expose for feat.detgate's determinism gate (FEAT-004,
// out of scope here; this is the minimal smoke test AC-15 asks for).
func snapshotHash(t *testing.T, poolSize int) string {
	t.Helper()
	e := NewEngine(WithWorldSeed(20260809), WithPoolSize(poolSize))
	if err := e.AdvanceTicks("corr-det", 63); err != nil {
		t.Fatalf("AdvanceTicks (pool=%d): %v", poolSize, err)
	}
	var buf bytes.Buffer
	if _, err := e.Snapshot(&buf, "corr-det-snap"); err != nil {
		t.Fatalf("Snapshot (pool=%d): %v", poolSize, err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

// TestDeterminism_SameSeedSameCommands_IdenticalSnapshot is AC-15's
// smoke test: same seed, same command log, run twice, sha256(snapshot)
// must match.
func TestDeterminism_SameSeedSameCommands_IdenticalSnapshot(t *testing.T) {
	h1 := snapshotHash(t, 4)
	h2 := snapshotHash(t, 4)
	if h1 != h2 {
		t.Fatalf("same seed, same commands, same pool size: hash1=%s hash2=%s, want equal", h1, h2)
	}
}

// TestDeterminism_PoolSizeInvariance is AC-15's worker-count-invariance
// smoke test: POOL-SIM=1 vs a higher worker count must produce the same
// hash for the same seed and command log.
func TestDeterminism_PoolSizeInvariance(t *testing.T) {
	h1 := snapshotHash(t, 1)
	h14 := snapshotHash(t, 14)
	if h1 != h14 {
		t.Fatalf("POOL-SIM=1 vs POOL-SIM=14: hash1=%s hash14=%s, want equal", h1, h14)
	}
}
