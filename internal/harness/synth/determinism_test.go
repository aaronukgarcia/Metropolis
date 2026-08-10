package synth

import (
	"bytes"
	"runtime"
	"testing"
)

// TestGenerate_Deterministic_SameTupleSameBytes is AC-9's core claim:
// the same (Seed, CitizenCount, Sprawl, NetworkShape) tuple always
// generates a byte-identical world, every run.
func TestGenerate_Deterministic_SameTupleSameBytes(t *testing.T) {
	p := Params{CitizenCount: 500, Seed: 987654321, Sprawl: 0.42, NetworkShape: NetworkOrganic}

	var buf1, buf2 bytes.Buffer
	if _, err := Generate("t", p, &buf1); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if _, err := Generate("t", p, &buf2); err != nil {
		t.Fatalf("second Generate: %v", err)
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatalf("Generate produced different bytes for the same (seed, citizenCount, sprawl, networkShape) tuple: len1=%d len2=%d", buf1.Len(), buf2.Len())
	}
}

// TestGenerate_Deterministic_DifferentSeedsDifferentBytes is the
// negative control every determinism test needs (dev-team-process.md's
// standing lesson: "a drift/determinism test that cannot fail is
// decoration"): proves this test suite is actually sensitive to the
// seed, not just always passing regardless of input.
func TestGenerate_Deterministic_DifferentSeedsDifferentBytes(t *testing.T) {
	base := Params{CitizenCount: 500, Seed: 1, Sprawl: 0.42, NetworkShape: NetworkOrganic}
	other := base
	other.Seed = 2

	var buf1, buf2 bytes.Buffer
	if _, err := Generate("t", base, &buf1); err != nil {
		t.Fatalf("Generate(seed=1): %v", err)
	}
	if _, err := Generate("t", other, &buf2); err != nil {
		t.Fatalf("Generate(seed=2): %v", err)
	}
	if bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("Generate produced identical bytes for two different seeds — the determinism test above cannot be trusted if this negative control also passes")
	}
}

// TestGenerate_Deterministic_AcrossGOMAXPROCS is AC-9's "and across
// worker-pool sizes" clause, applied to this package: Generate has no
// worker pool of its own (see generator.go's doc comment — generation is
// single-threaded by design), so this is a defence-in-depth check that
// no incidental parallelism sensitivity has been introduced, rather than
// a test of a real sharding property this package implements.
func TestGenerate_Deterministic_AcrossGOMAXPROCS(t *testing.T) {
	prev := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prev)

	p := Params{CitizenCount: 300, Seed: 55, Sprawl: 0.1, NetworkShape: NetworkRadial}

	runtime.GOMAXPROCS(1)
	var buf1 bytes.Buffer
	if _, err := Generate("t", p, &buf1); err != nil {
		t.Fatalf("GOMAXPROCS=1: Generate: %v", err)
	}

	runtime.GOMAXPROCS(4)
	var buf2 bytes.Buffer
	if _, err := Generate("t", p, &buf2); err != nil {
		t.Fatalf("GOMAXPROCS=4: Generate: %v", err)
	}

	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("Generate's output differed across GOMAXPROCS settings")
	}
}
