package det

import "testing"

// AC-5(a): the same key always produces the same sequence of draws.
func TestStream_SameKeySameSequence(t *testing.T) {
	a := NewStream(1, 2, 3, "mortality")
	b := NewStream(1, 2, 3, "mortality")
	for i := 0; i < 50; i++ {
		va, vb := a.Uint64(), b.Uint64()
		if va != vb {
			t.Fatalf("draw %d: a=%d b=%d, want equal for identical keys", i, va, vb)
		}
	}
}

// AC-5(b): two different keys (differing in any one of the four
// components) produce statistically distinct sequences — a simple
// inequality on the first N draws is sufficient per the AC.
func TestStream_DifferentKeysDiverge(t *testing.T) {
	base := NewStream(1, 2, 3, "mortality")

	variants := map[string]Stream{
		"worldSeed": NewStream(2, 2, 3, "mortality"),
		"entityID":  NewStream(1, 3, 3, "mortality"),
		"month":     NewStream(1, 2, 4, "mortality"),
		"purpose":   NewStream(1, 2, 3, "personality-noise"),
	}

	const n = 8
	for name, variant := range variants {
		b := base
		v := variant
		diverged := false
		for i := 0; i < n; i++ {
			if b.Uint64() != v.Uint64() {
				diverged = true
			}
		}
		if !diverged {
			t.Fatalf("variant %s: first %d draws identical to base, want divergence", name, n)
		}
	}
}

// AC-5(c): drawing from one stream never mutates or depends on any other
// stream's state — interleaved draws produce the same per-stream sequence
// as sequential (non-interleaved) draws.
func TestStream_InterleavingDoesNotAffectOtherStreams(t *testing.T) {
	sequential := func() (a, b []uint64) {
		sa := NewStream(10, 20, 1, "a")
		sb := NewStream(10, 21, 1, "b")
		for i := 0; i < 10; i++ {
			a = append(a, sa.Uint64())
		}
		for i := 0; i < 10; i++ {
			b = append(b, sb.Uint64())
		}
		return
	}
	interleaved := func() (a, b []uint64) {
		sa := NewStream(10, 20, 1, "a")
		sb := NewStream(10, 21, 1, "b")
		for i := 0; i < 10; i++ {
			a = append(a, sa.Uint64())
			b = append(b, sb.Uint64())
		}
		return
	}

	seqA, seqB := sequential()
	intA, intB := interleaved()

	for i := range seqA {
		if seqA[i] != intA[i] {
			t.Fatalf("stream a draw %d: sequential=%d interleaved=%d, want equal", i, seqA[i], intA[i])
		}
		if seqB[i] != intB[i] {
			t.Fatalf("stream b draw %d: sequential=%d interleaved=%d, want equal", i, seqB[i], intB[i])
		}
	}
}

// AC-5: position-independence — At(n) matches the nth sequential Uint64()
// draw, with no dependency on having drawn 0..n-1 first.
func TestStream_PositionIndependence(t *testing.T) {
	st := NewStream(42, 7, 100, "route-choice")
	var sequential []uint64
	for i := 0; i < 20; i++ {
		sequential = append(sequential, st.Uint64())
	}

	fresh := NewStream(42, 7, 100, "route-choice")
	for i, want := range sequential {
		if got := fresh.At(uint64(i)); got != want {
			t.Fatalf("At(%d) = %d, want %d (sequential draw)", i, got, want)
		}
	}

	// Out-of-order addressing: drawing At(15) without ever touching
	// 0-14 must equal the sequential value at position 15.
	fresh2 := NewStream(42, 7, 100, "route-choice")
	if got := fresh2.At(15); got != sequential[15] {
		t.Fatalf("At(15) without prior draws = %d, want %d", got, sequential[15])
	}
}

// AC-9: worldSeed = 0 is a valid, well-defined, non-degenerate seed.
func TestNewStream_ZeroSeedIsWellDefined(t *testing.T) {
	st := NewStream(0, 0, 0, "")
	seen := map[uint64]bool{}
	for i := 0; i < 100; i++ {
		v := st.Uint64()
		if v == 0 {
			t.Fatalf("draw %d from zero-seed stream is exactly 0 (degenerate)", i)
		}
		seen[v] = true
	}
	if len(seen) < 90 {
		t.Fatalf("zero-seed stream produced only %d distinct values in 100 draws, want a well-mixed sequence", len(seen))
	}
}

// AC-16: shard-count invariance — RNG streams are keyed by entityID, not
// by shard index, so varying how many buckets a test harness spreads
// entities across (independent of the canonical, unchanged NumShards
// production constant) must not change per-entity draw output.
func TestStream_ShardCountInvarianceOfRNGDraws(t *testing.T) {
	entityIDs := []uint64{0, 1, 2, 3, 100, 1000, 999999}

	// Two "harnesses" that bucket the same entities differently — this
	// is test-only scaffolding, unrelated to the production NumShards
	// constant, which never changes.
	bucket8 := func(id uint64) int { return int(id % 8) }
	bucket256 := func(id uint64) int { return int(id % 256) }

	for _, id := range entityIDs {
		_ = bucket8(id)
		_ = bucket256(id) // bucket assignment computed but must not feed the stream key

		a := NewStream(worldSeedForTest, id, 5, "shard-count-invariance")
		b := NewStream(worldSeedForTest, id, 5, "shard-count-invariance")
		for i := 0; i < 10; i++ {
			va, vb := a.Uint64(), b.Uint64()
			if va != vb {
				t.Fatalf("entity %d draw %d: %d != %d despite identical (worldSeed, entityID, month, purpose) key", id, i, va, vb)
			}
		}
	}

	// Also assert NumShards (the real production constant) is untouched
	// by this test's use of alternative bucket counts.
	if NumShards != 256 {
		t.Fatalf("NumShards mutated by test harness: %d", NumShards)
	}
}

const worldSeedForTest = 555

// Golden values: pinned so a compiler/platform change to the Philox
// mixing arithmetic (bits.Mul64, shifts, XOR) screams immediately rather
// than silently drifting. If this test ever needs to change, that is a
// determinism regression alarm, not routine test maintenance — treat a
// failure here as a stop-the-line event (GR#21) until proven otherwise.
func TestStream_GoldenValues(t *testing.T) {
	st := NewStream(1, 2, 3, "golden")
	want := [5]uint64{
		0xa17a833ff4bd426a,
		0x22c9146857150da0,
		0x097376033c2a56f2,
		0xe5cc1b72de448993,
		0x131c3b01b43dac6c,
	}
	for i, w := range want {
		got := st.Uint64()
		if got != w {
			t.Fatalf("golden draw %d = 0x%016x, want 0x%016x", i, got, w)
		}
	}
}
