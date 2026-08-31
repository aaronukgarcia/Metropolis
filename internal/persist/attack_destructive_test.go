package persist

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"path/filepath"
	"sync"
	"testing"
)

// This file is a lasting destructive-round regression suite (GR#23) for
// the FEAT-1972079936 durable persistence Store. It pins the
// data-integrity invariants an independent attacker exercised: torn-write
// atomicity at adversarial offsets, CityKey isolation/collision-safety,
// and concurrency of the per-city lock map. All tests here MUST stay
// green; a reddening here means a committed-record loss/corruption
// regression, which is the exact data-loss class this layer exists to end.

// TestAttack_CorruptCRCIntactLengthMidStream proves that a frame whose
// payload is corrupted but whose LENGTH header is intact (CRC mismatch,
// not a length overrun) halts decoding AT that frame — it is never
// surfaced as a present-but-wrong record, and decoding does not blindly
// skip past it to mis-frame the remainder.
func TestAttack_CorruptCRCIntactLengthMidStream(t *testing.T) {
	a := encodeFrame([]byte("alpha"))
	b := encodeFrame([]byte("bravo"))
	c := encodeFrame([]byte("charlie"))

	// Corrupt b's payload in place (length header + CRC field untouched).
	corruptB := make([]byte, len(b))
	copy(corruptB, b)
	corruptB[frameLenSize] ^= 0xFF // flip a payload byte

	stream := append(append(append([]byte{}, a...), corruptB...), c...)
	got := decodeFrames(stream)

	// Only the leading intact frame is recoverable; decoding stops at the
	// corrupt CRC and never returns the wrong "bravo" nor mis-frames "charlie".
	if len(got) != 1 || string(got[0]) != "alpha" {
		t.Fatalf("decodeFrames on corrupt-CRC-intact-length stream = %q, want exactly [alpha] (must stop at the bad frame, never surface a CRC-failing record)", got)
	}
}

// TestAttack_GarbageTailAfterValidFrames confirms arbitrary trailing
// garbage (a partial length header, a length that overruns EOF, and raw
// junk) after N committed frames reads back as exactly N — never a panic,
// never a phantom record from the junk.
func TestAttack_GarbageTailAfterValidFrames(t *testing.T) {
	valid := append(encodeFrame([]byte("one")), encodeFrame([]byte("two"))...)

	tails := map[string][]byte{
		"single_junk_byte":      {0x7f},
		"partial_length_header": {0x00, 0x00, 0x10},
		"length_overruns_eof":   {0xff, 0xff, 0xff, 0xff, 0x01, 0x02},
		"len_ok_but_no_payload": {0x00, 0x00, 0x00, 0x08},
		"random_noise":          []byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08garbage"),
	}
	for name, tail := range tails {
		t.Run(name, func(t *testing.T) {
			stream := append(append([]byte{}, valid...), tail...)
			got := decodeFrames(stream)
			if len(got) != 2 || string(got[0]) != "one" || string(got[1]) != "two" {
				t.Fatalf("garbage tail %q: decodeFrames = %q, want exactly [one two]", name, got)
			}
		})
	}
}

// TestAttack_ZeroLengthTornTailRejected pins the HARDENED behaviour for
// the zero-length torn-tail case an attacker found. The frame CRC now
// covers the 4-byte length header AND the payload together (see
// journal.go). A torn append that zero-extends the file past synced data
// leaves a run of trailing ZERO bytes (len=0, crc=0). Because crc32 of
// the four zero length-bytes is NOT zero, that zero tail now FAILS the
// CRC check and is rejected as a torn tail — it is no longer silently
// promoted to a committed empty record.
//
// Critically, a genuinely-committed empty record still round-trips,
// because encodeFrame and decodeFrames checksum the same length||payload
// span; only the all-zero (crc=0) tail is rejected.
func TestAttack_ZeroLengthTornTailRejected(t *testing.T) {
	valid := encodeFrame([]byte("real-record"))
	zeroTail := make([]byte, 8) // len=0, crc=0 — NOT a valid empty frame now
	stream := append(append([]byte{}, valid...), zeroTail...)

	got := decodeFrames(stream)
	// Hardened behaviour: the zero tail is rejected; only the real record
	// survives.
	if len(got) != 1 || string(got[0]) != "real-record" {
		t.Fatalf("zero-tail must be rejected: got %q, want exactly [real-record] (folding the length into the CRC rejects the len=0/crc=0 torn tail)", got)
	}

	// The ambiguity is closed precisely because crc32 over the zero length
	// header is non-zero, so a zero crc field no longer matches.
	var hdr [frameLenSize]byte
	if crc32.ChecksumIEEE(hdr[:]) == 0 {
		t.Fatalf("crc32(zero length header)=0 — the length-fold would not close the ambiguity")
	}
	if binary.BigEndian.Uint32(hdr[:]) != 0 {
		t.Fatal("zero length header decoded non-zero")
	}

	// And a legitimately-committed empty record still round-trips.
	emptyStream := encodeFrame([]byte{})
	gotEmpty := decodeFrames(emptyStream)
	if len(gotEmpty) != 1 || len(gotEmpty[0]) != 0 {
		t.Fatalf("committed empty record must still round-trip: got %q, want exactly one empty record", gotEmpty)
	}
}

// TestAttack_HostileKeysDistinctDirsNoCollision proves the SHA-256
// segment encoding both (a) never escapes the root for hostile ids and
// (b) maps two DISTINCT CityKeys to two DISTINCT directories — no hostile
// id can be crafted to collide onto another city's namespace.
func TestAttack_HostileKeysDistinctDirsNoCollision(t *testing.T) {
	hostile := []CityKey{
		{TenantID: "../../etc", CityID: "passwd"},
		{TenantID: "..\\..\\windows", CityID: "system32"},
		{TenantID: "/absolute/root", CityID: "/etc/shadow"},
		{TenantID: "", CityID: ""},
		{TenantID: "a", CityID: ""},
		{TenantID: "", CityID: "a"},
		{TenantID: "null\x00byte", CityID: "x\x00y"},
		{TenantID: string(make([]byte, 100000)), CityID: "huge"},
		{TenantID: "café", CityID: "caﬁe"}, // unicode lookalike vs normal
		{TenantID: "..", CityID: "."},
	}
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	seenDir := map[string]CityKey{}
	for _, k := range hostile {
		dir := s.cityDir(k)
		// Must be strictly under root.
		rel, err := filepath.Rel(s.root, dir)
		if err != nil || rel == ".." || filepath.IsAbs(rel) || hasDotDotPrefix(rel) {
			t.Fatalf("key %+v mapped to dir escaping root: %s (rel %q)", k, dir, rel)
		}
		if prev, ok := seenDir[dir]; ok && prev != k {
			t.Fatalf("collision: distinct keys %+v and %+v mapped to same dir %s", prev, k, dir)
		}
		seenDir[dir] = k
	}
}

func hasDotDotPrefix(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.'
}

// TestAttack_HostileCityRoundTripsIsolated writes to a hostile-keyed city
// and a benign city and confirms neither leaks into the other and both
// read back exactly their own records through a fresh (cross-process)
// store.
func TestAttack_HostileCityRoundTripsIsolated(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	hostile := CityKey{TenantID: "../../../etc", CityID: "..\\..\\passwd"}
	benign := CityKey{TenantID: "tenant", CityID: "city"}

	if err := s.AppendJournal(ctx, hostile, []byte("H")); err != nil {
		t.Fatalf("append hostile: %v", err)
	}
	if err := s.AppendJournal(ctx, benign, []byte("B")); err != nil {
		t.Fatalf("append benign: %v", err)
	}

	fresh, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	h, _ := fresh.ReadJournal(ctx, hostile)
	b, _ := fresh.ReadJournal(ctx, benign)
	if len(h) != 1 || string(h[0]) != "H" {
		t.Fatalf("hostile journal = %q, want [H]", h)
	}
	if len(b) != 1 || string(b[0]) != "B" {
		t.Fatalf("benign journal = %q, want [B]", b)
	}
}

// TestAttack_ConcurrentDifferentCitiesAndSameCity stresses the per-city
// lock map: many goroutines append to a mix of shared and distinct
// cities. Run under -race, it catches both a lost/duplicated frame on the
// shared city AND any data race in lockFor's map. Different cities must
// not corrupt each other's journals.
func TestAttack_ConcurrentDifferentCitiesAndSameCity(t *testing.T) {
	ctx := context.Background()
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	const cities = 8
	const perCity = 40

	var wg sync.WaitGroup
	errCh := make(chan error, cities*perCity)
	for c := 0; c < cities; c++ {
		city := CityKey{TenantID: "t", CityID: fmt.Sprintf("city-%d", c)}
		for i := 0; i < perCity; i++ {
			wg.Add(1)
			go func(city CityKey, i int) {
				defer wg.Done()
				errCh <- s.AppendJournal(ctx, city, []byte(fmt.Sprintf("rec-%03d", i)))
			}(city, i)
		}
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			t.Fatalf("concurrent append error: %v", e)
		}
	}

	for c := 0; c < cities; c++ {
		city := CityKey{TenantID: "t", CityID: fmt.Sprintf("city-%d", c)}
		got, err := s.ReadJournal(ctx, city)
		if err != nil {
			t.Fatalf("ReadJournal city-%d: %v", c, err)
		}
		if len(got) != perCity {
			t.Fatalf("city-%d has %d records, want %d (lost/duplicated frame under concurrency)", c, len(got), perCity)
		}
		seen := map[string]bool{}
		for _, r := range got {
			if len(r) != len("rec-000") {
				t.Fatalf("city-%d frame corrupted (len %d): %q", c, len(r), r)
			}
			if seen[string(r)] {
				t.Fatalf("city-%d duplicate frame %q — interleaved/corrupt write", c, r)
			}
			seen[string(r)] = true
		}
	}
}

// TestAttack_ReaderConcurrentWithWriter opens reads on a city while a
// writer is actively appending. No read may ever return a torn frame or
// error; every read must return a prefix-consistent count that only grows.
func TestAttack_ReaderConcurrentWithWriter(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writer, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore writer: %v", err)
	}
	city := CityKey{TenantID: "t", CityID: "rw"}
	const n = 60

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			if err := writer.AppendJournal(ctx, city, []byte(fmt.Sprintf("f-%02d", i))); err != nil {
				t.Errorf("append %d: %v", i, err)
				return
			}
		}
	}()

	// A separate store instance (fresh process) reading the same root.
	reader, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore reader: %v", err)
	}
	last := 0
	for {
		recs, err := reader.ReadJournal(ctx, city)
		if err != nil {
			t.Fatalf("concurrent ReadJournal: %v", err)
		}
		for _, r := range recs {
			if len(r) != len("f-00") {
				t.Fatalf("reader saw a torn/partial frame: %q (len %d)", r, len(r))
			}
		}
		if len(recs) < last {
			t.Fatalf("committed record count went backwards: %d < %d", len(recs), last)
		}
		last = len(recs)
		select {
		case <-done:
			final, _ := reader.ReadJournal(ctx, city)
			if len(final) != n {
				t.Fatalf("final record count = %d, want %d", len(final), n)
			}
			return
		default:
		}
	}
}
