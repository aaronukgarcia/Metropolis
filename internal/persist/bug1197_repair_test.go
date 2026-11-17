package persist

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// BUG-1197: a torn journal tail is truncated to the last good frame, so a
// later append is readable instead of landing after garbage.

func journalFile(t *testing.T, s *DiskStore, city CityKey) string {
	t.Helper()
	return filepath.Join(s.cityDir(city), journalFileName)
}

func appendRaw(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestBug1197_AppendAfterTornTailIsReadable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	city := CityKey{TenantID: "t", CityID: "c"}
	s1, err := NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"one", "two", "three"} {
		if err := s1.AppendJournal(ctx, city, []byte(r)); err != nil {
			t.Fatal(err)
		}
	}
	good, _ := os.ReadFile(journalFile(t, s1, city))
	// crash mid-append: length header says 40, only 4 payload bytes present
	appendRaw(t, journalFile(t, s1, city), []byte{0, 0, 0, 40, 'x', 'x', 'x', 'x'})

	// New process: a fresh store appends once, which repairs first.
	s2, err := NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got JournalRepair
	hooked := 0
	s2.SetTornTailHook(func(c CityKey, r JournalRepair) { got, hooked = r, hooked+1 })
	if err := s2.AppendJournal(ctx, city, []byte("four")); err != nil {
		t.Fatal(err)
	}
	recs, err := s2.ReadJournal(ctx, city)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "three", "four"}
	if len(recs) != len(want) {
		t.Fatalf("read %d records, want %d (append landed after the torn tail)", len(recs), len(want))
	}
	for i, w := range want {
		if string(recs[i]) != w {
			t.Fatalf("record %d = %q, want %q", i, recs[i], w)
		}
	}
	if hooked != 1 || got.TruncatedBytes != 8 || got.GoodBytes != int64(len(good)) {
		t.Fatalf("hook calls=%d repair=%+v, want 1 call, 8 truncated, %d good", hooked, got, len(good))
	}
	after, _ := os.ReadFile(journalFile(t, s2, city))
	if !bytes.HasPrefix(after, good) {
		t.Fatal("repair altered the good prefix")
	}
}

func TestBug1197_RepairJournalCleanAndMissingAreNoOps(t *testing.T) {
	ctx := context.Background()
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	city := CityKey{TenantID: "t", CityID: "c"}
	if r, err := s.RepairJournal(ctx, city); err != nil || r.TruncatedBytes != 0 {
		t.Fatalf("missing journal: %+v %v", r, err)
	}
	_ = s.AppendJournal(ctx, city, []byte("a"))
	if r, err := s.RepairJournal(ctx, city); err != nil || r.TruncatedBytes != 0 {
		t.Fatalf("clean journal: %+v %v", r, err)
	}
	// a zero-extended tail (len=0, crc=0) is torn, not a committed empty record
	appendRaw(t, journalFile(t, s, city), make([]byte, 8))
	if r, err := s.RepairJournal(ctx, city); err != nil || r.TruncatedBytes != 8 {
		t.Fatalf("zero tail: %+v %v", r, err)
	}
	recs, _ := s.ReadJournal(ctx, city)
	if len(recs) != 1 {
		t.Fatalf("records after repair = %d, want 1", len(recs))
	}
}

// r2 F1 (round r1 finding): a bad frame FOLLOWED by complete valid frames is
// mid-file corruption, not a torn tail. Repair must refuse, return a typed
// error, leave the file byte-identical and not fire the torn-tail hook; and
// AppendJournal must refuse too (fail closed) rather than append after it.
func TestBug1197_MidFileCorruptionFailsClosedAndLeavesFileUntouched(t *testing.T) {
	ctx := context.Background()
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	city := CityKey{TenantID: "t", CityID: "c"}
	for i := 0; i < 5; i++ {
		if err := s.AppendJournal(ctx, city, []byte(fmt.Sprintf("rec-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	p := journalFile(t, s, city)
	data, _ := os.ReadFile(p)
	frame := len(encodeFrame([]byte("rec-0")))
	data[frame+5] ^= 0xFF // flip a payload byte of rec-1; rec-2..4 stay valid
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var fired int32
	s2, _ := NewDiskStore(s.root)
	s2.SetTornTailHook(func(CityKey, JournalRepair) { atomic.AddInt32(&fired, 1) })

	_, err = s2.RepairJournal(ctx, city)
	var ce *JournalCorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("RepairJournal err = %v, want *JournalCorruptError", err)
	}
	if ce.GoodBytes != int64(frame) || ce.FileBytes != int64(len(data)) {
		t.Fatalf("error offsets %+v, want good=%d file=%d", ce, frame, len(data))
	}
	if err := s2.AppendJournal(ctx, city, []byte("more")); !errors.As(err, &ce) {
		t.Fatalf("AppendJournal err = %v, want *JournalCorruptError (fail closed)", err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(after, data) {
		t.Fatalf("journal changed: %d -> %d bytes; mid-file corruption must leave the file byte-identical", len(data), len(after))
	}
	if fired != 0 {
		t.Fatalf("torn-tail hook fired %d times for non-tail corruption", fired)
	}
}

// A corrupted LENGTH header mid-file (declares a length running past EOF)
// with valid frames after it is still mid-file corruption, not a torn tail.
func TestBug1197_CorruptLengthHeaderMidFileFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, _ := NewDiskStore(t.TempDir())
	city := CityKey{TenantID: "t", CityID: "c"}
	for i := 0; i < 4; i++ {
		_ = s.AppendJournal(ctx, city, []byte(fmt.Sprintf("rec-%d", i)))
	}
	p := journalFile(t, s, city)
	data, _ := os.ReadFile(p)
	frame := len(encodeFrame([]byte("rec-0")))
	data[frame] = 0x7F // rec-1 now declares a ~2 GB payload
	_ = os.WriteFile(p, data, 0o600)
	s2, _ := NewDiskStore(s.root)
	var ce *JournalCorruptError
	if _, err := s2.RepairJournal(ctx, city); !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *JournalCorruptError", err)
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, data) {
		t.Fatal("journal changed")
	}
}

// A torn tail made of garbage that contains no valid frame is still cut (the
// genuine crash case must keep working under the stricter rule).
func TestBug1197_GarbageTailWithNoValidFrameIsStillTruncated(t *testing.T) {
	ctx := context.Background()
	s, _ := NewDiskStore(t.TempDir())
	city := CityKey{TenantID: "t", CityID: "c"}
	_ = s.AppendJournal(ctx, city, []byte("a"))
	_ = s.AppendJournal(ctx, city, []byte("b"))
	p := journalFile(t, s, city)
	good := mustRead(p)
	// a complete-length frame with a wrong CRC, then trailing junk
	bad := encodeFrame([]byte("zzzz"))
	bad[len(bad)-1] ^= 0xFF
	appendRaw(t, p, append(bad, 1, 2, 3))
	r, err := s.RepairJournal(ctx, city)
	if err != nil || r.TruncatedBytes != int64(len(bad)+3) {
		t.Fatalf("repair %+v err %v", r, err)
	}
	if !bytes.Equal(mustRead(p), good) {
		t.Fatal("good prefix altered")
	}
}

// Round r1 (kept): valid journal byte-identical after repair; hook fires once on a torn tail.
func TestBug1197_CleanJournalByteIdenticalAndHookFiresOnce(t *testing.T) {
	ctx := context.Background()
	s, _ := NewDiskStore(t.TempDir())
	city := CityKey{TenantID: "t", CityID: "c"}
	for i := 0; i < 50; i++ {
		_ = s.AppendJournal(ctx, city, []byte(fmt.Sprintf("r%d", i)))
	}
	p := journalFile(t, s, city)
	before, _ := os.ReadFile(p)
	s2, _ := NewDiskStore(s.root)
	var fired int32
	s2.SetTornTailHook(func(CityKey, JournalRepair) { atomic.AddInt32(&fired, 1) })
	if _, err := s2.RepairJournal(ctx, city); err != nil {
		t.Fatal(err)
	}
	mid, _ := os.ReadFile(p)
	if !bytes.Equal(before, mid) || fired != 0 {
		t.Fatalf("clean journal changed or hook fired")
	}
	appendRaw(t, p, []byte{0, 0, 0, 9, 1, 2})
	_, _ = s2.RepairJournal(ctx, city)
	_ = s2.AppendJournal(ctx, city, []byte("x"))
	_, _ = s2.RepairJournal(ctx, city)
	if fired != 1 {
		t.Fatalf("hook fired %d", fired)
	}
	recs, _ := s2.ReadJournal(ctx, city)
	if len(recs) != 51 || !bytes.HasPrefix(append([]byte{}, mustRead(p)...), before) {
		t.Fatalf("recs=%d", len(recs))
	}
}

func mustRead(p string) []byte { b, _ := os.ReadFile(p); return b }

// Round r1 (kept): concurrent repair + append, two cities on one store, two stores on one dir.
func TestBug1197_ConcurrentRepairAndAppend(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, _ := NewDiskStore(root)
	cities := []CityKey{{TenantID: "t", CityID: "a"}, {TenantID: "t", CityID: "b"}}
	for _, c := range cities {
		_ = s.AppendJournal(ctx, c, []byte("seed"))
		appendRaw(t, journalFile(t, s, c), []byte{0, 0, 1})
	}
	s2, _ := NewDiskStore(root)
	var wg sync.WaitGroup
	for _, c := range cities {
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(c CityKey, g int) {
				defer wg.Done()
				for i := 0; i < 25; i++ {
					if g%2 == 0 {
						_, _ = s2.RepairJournal(ctx, c)
					} else if err := s2.AppendJournal(ctx, c, []byte(fmt.Sprintf("%d-%d", g, i))); err != nil {
						t.Error(err)
					}
				}
			}(c, g)
		}
	}
	wg.Wait()
	for _, c := range cities {
		recs, _ := s2.ReadJournal(ctx, c)
		b := mustRead(journalFile(t, s2, c))
		_, good := decodeFramesWithOffset(b)
		if len(recs) != 51 || good != len(b) {
			t.Fatalf("%v recs=%d good=%d len=%d", c, len(recs), good, len(b))
		}
	}
}

// r3 M3: the next valid frame after mid-file corruption starts at an odd
// offset (13 + 16 = 29) and is the ONLY valid frame after the damage (a
// later frame could land on a stepped offset), so a scan that skips bytes (every 2nd or 4th) would
// miss it and wrongly truncate committed frames.
func TestBug1197_MidFileCorruptionWithUnalignedNextFrameFailsClosed(t *testing.T) {
	ctx := context.Background()
	s, _ := NewDiskStore(t.TempDir())
	city := CityKey{TenantID: "t", CityID: "c"}
	payloads := [][]byte{[]byte("rec-0"), []byte("rec-1-xy"), []byte("rec-2")}
	for _, p := range payloads {
		if err := s.AppendJournal(ctx, city, p); err != nil {
			t.Fatal(err)
		}
	}
	p := journalFile(t, s, city)
	data, _ := os.ReadFile(p)
	f0 := len(encodeFrame(payloads[0]))
	f1 := len(encodeFrame(payloads[1]))
	next := f0 + f1
	if next%4 == 0 || next%2 == 0 {
		t.Fatalf("test setup: next valid frame offset %d must be odd/unaligned", next)
	}
	data[f0+5] ^= 0xFF // damage rec-1's payload; rec-2 stays valid
	_ = os.WriteFile(p, data, 0o600)
	s2, _ := NewDiskStore(s.root)
	var ce *JournalCorruptError
	if _, err := s2.RepairJournal(ctx, city); !errors.As(err, &ce) {
		t.Fatalf("err = %v, want *JournalCorruptError", err)
	}
	if after, _ := os.ReadFile(p); !bytes.Equal(after, data) {
		t.Fatal("journal changed; valid frames after the damage were cut")
	}
}
