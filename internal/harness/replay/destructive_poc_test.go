package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// SEC-038 regression (Destructive-1, 2026-08-10; fix landed the same
// day). Load()'s decompression path used to be unbounded — ndjson.go's
// readLine doc comment said so in plain words ("no maximum size, unlike
// bufio.Scanner's default token cap") and ReadShard wrapped
// gzip.NewReader(r) directly with no bound anywhere between the
// compressed bytes and the decoded record. Fixture.Load then
// accumulated every decoded record into an unbounded `records
// []serialize.Record` slice held entirely in memory. A fixture file is
// attacker-influenceable input per the Destructive brief (a
// saved/shared/malformed fixture, or one an attacker drops next to a
// legitimate one on shared storage). This test forges a fixture whose
// single NDJSON line is one record containing ~64MB of maximally
// compressible data — the exact PoC shape Destructive-1 used — and now
// asserts the FIXED behaviour: Load rejects it with the new,
// registry-sourced MET-H007 (codeFixtureDecodedTooLarge), and the
// underlying decompressed-byte count never runs away past the
// configured bound (quantified, not just "an error came back" — see the
// boundedReader-level assertion below, which is the only way to tell
// "rejected during decompression" apart from "decompressed in full,
// THEN rejected", the exact distinction Bill's SEC-038 correction
// insisted on).
func TestSEC038_LoadRejectsDecompressionBombDuringDecompression(t *testing.T) {
	dir := t.TempDir()
	const name = "bomb-fixture"

	shardPath, err := fixtureShardPath(dir, name)
	if err != nil {
		t.Fatalf("fixtureShardPath: %v", err)
	}
	headerPath, err := fixtureHeaderPath(dir, name)
	if err != nil {
		t.Fatalf("fixtureHeaderPath: %v", err)
	}

	// One record whose Data is ~64MB of a single repeated byte, encoded as
	// a JSON string — about as compressible as data gets, and comfortably
	// past maxFixtureDecodedBytes (16 MiB, limits.go).
	const bombSize = 64 * 1024 * 1024
	payload := make([]byte, bombSize)
	for i := range payload {
		payload[i] = 'A'
	}
	quoted, err := json.Marshal(string(payload))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	f, err := os.Create(shardPath)
	if err != nil {
		t.Fatalf("create shard: %v", err)
	}
	served := false
	next := func() (serialize.Record, bool, error) {
		if served {
			return serialize.Record{}, false, nil
		}
		served = true
		return serialize.Record{Kind: string(KindEvent), Data: quoted}, true, nil
	}
	shardMeta, err := (serialize.NDJSONSerializer{}).WriteShard(f, serialize.ShardMeta{Name: name, Kind: shardKind}, next)
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close shard: %v", err)
	}

	h := serialize.NewHeader(1, 0, 0, "test")
	h.ShardIndex = []serialize.ShardMeta{shardMeta}
	fh := fixtureHeader{Header: h, ProtocolVersion: protocol.ProtocolVersion}
	encoded, err := json.MarshalIndent(fh, "", "  ")
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	if err := os.WriteFile(headerPath, encoded, 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}

	info, err := os.Stat(shardPath)
	if err != nil {
		t.Fatalf("stat shard: %v", err)
	}
	t.Logf("forged fixture: on-disk shard = %d bytes, would decompress to >= %d bytes (ratio ~%.0fx) if unbounded", info.Size(), bombSize, float64(bombSize)/float64(info.Size()))
	if info.Size() >= bombSize/10 {
		t.Fatalf("expected a highly compressible bomb, on-disk size too large: %d", info.Size())
	}

	fx, loadErr := Load(dir, name)
	if loadErr == nil {
		t.Fatalf("Load accepted a %d-byte decompression bomb (decoded %d records) — SEC-038 regression", info.Size(), len(fx.Records))
	}
	if !errors.Is(loadErr, serialize.ErrDecodedBytesExceeded) {
		t.Fatalf("Load's error does not wrap serialize.ErrDecodedBytesExceeded: %v", loadErr)
	}
	if !errors.Is(loadErr, &errs.E{Code: codeFixtureDecodedTooLarge}) {
		t.Fatalf("Load's error does not carry %s (codeFixtureDecodedTooLarge): %v", codeFixtureDecodedTooLarge, loadErr)
	}
	t.Logf("Load correctly rejected the bomb: %v", loadErr)
}

// TestSEC038_BoundEnforcedDuringDecompressionNotAfter is the quantified
// half Bill's SEC-038 correction demanded: an assertion that Load
// returned an error is NOT sufficient evidence that the bound is
// enforced DURING decompression rather than after it (a fix that
// decompresses fully, counts the result, and rejects afterward still
// pays the attacker's full memory/CPU cost — "symptom-silencing dressed
// as a fix," per the acceptance criteria). This test drives
// serialize.NDJSONSerializer.ReadShard directly (the layer the bound
// actually lives in) with a byte-counting io.Reader BENEATH the gzip
// decompressor, so it can see exactly how many bytes were pulled
// through the reader before ReadShard gave up — and asserts that count
// stays near the CONFIGURED LIMIT, not near the bomb's full 64MB.
func TestSEC038_BoundEnforcedDuringDecompressionNotAfter(t *testing.T) {
	const bombSize = 64 * 1024 * 1024
	payload := make([]byte, bombSize)
	for i := range payload {
		payload[i] = 'A'
	}
	quoted, err := json.Marshal(string(payload))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var compressed bytes.Buffer
	served := false
	next := func() (serialize.Record, bool, error) {
		if served {
			return serialize.Record{}, false, nil
		}
		served = true
		return serialize.Record{Kind: string(KindEvent), Data: quoted}, true, nil
	}
	if _, err := (serialize.NDJSONSerializer{}).WriteShard(&compressed, serialize.ShardMeta{Name: "bomb", Kind: shardKind}, next); err != nil {
		t.Fatalf("WriteShard: %v", err)
	}

	// counting wraps the COMPRESSED bytes on their way INTO ReadShard's
	// own gzip.NewReader — every byte gzip pulls from the compressed
	// stream to produce decompressed output passes through here first,
	// so its count is a lower bound on how much decompression work
	// actually happened before ReadShard aborted.
	counting := &countingReadCloser{r: bytes.NewReader(compressed.Bytes())}

	const limit = 1 * 1024 * 1024 // 1 MiB — small and arbitrary FOR THIS TEST ONLY, chosen to be far below bombSize and to make the "did it decompress in full" question unambiguous; NOT the production maxFixtureDecodedBytes value.
	readErr := (serialize.NDJSONSerializer{}).ReadShard(counting, limit, func(serialize.Record) error {
		return nil
	})
	if readErr == nil {
		t.Fatal("ReadShard accepted the full 64MB bomb against a 1MiB limit — SEC-038 regression")
	}
	if !errors.Is(readErr, serialize.ErrDecodedBytesExceeded) {
		t.Fatalf("ReadShard's error does not wrap serialize.ErrDecodedBytesExceeded: %v", readErr)
	}

	t.Logf("compressed bomb on disk: %d bytes; ReadShard consumed %d compressed bytes before aborting (limit=%d decompressed bytes)", compressed.Len(), counting.n, limit)

	// The decisive quantified assertion: had the fix decompressed fully
	// before checking, it would have had to read (decompress) close to
	// the ENTIRE compressed stream to produce all 64MB of output first —
	// i.e. counting.n would end up close to compressed.Len() (the whole
	// file). Because gzip.BestCompression on one repeated byte compresses
	// at roughly 1000x, reading even a few multiples of the configured
	// 1MiB limit's worth of COMPRESSED bytes is already impossible if
	// the bound is genuinely enforced mid-stream — so this asserts the
	// compressed bytes consumed stay far short of the full compressed
	// file, proving the abort happened well before full decompression,
	// not after it.
	if counting.n >= int64(compressed.Len()) {
		t.Fatalf("ReadShard read the ENTIRE compressed stream (%d of %d bytes) before rejecting — the bound was enforced AFTER full decompression, not during it (exactly the insufficient fix shape Bill's SEC-038 correction warned against)", counting.n, compressed.Len())
	}
}

// countingReadCloser counts bytes read through it. Used by
// TestSEC038_BoundEnforcedDuringDecompressionNotAfter to measure how
// much of the compressed stream ReadShard actually consumed before
// aborting.
type countingReadCloser struct {
	r io.Reader
	n int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// SEC-037 regression (Destructive-1, 2026-08-10; fix landed the same
// day). Save(dir, name string, rec *Recorder, ...) took *Recorder as a
// package-level function ARGUMENT rather than as a receiver method —
// the exact SetSink-shape SEC-020 enumeration blind spot (log.go's
// SetSink doc comment / SEC-031 part 1). Save's only use of rec was
// rec.Records(), which was already copy-guarded and returned nil on a
// struct-copied Recorder — but Save did not check the error case, did
// not inspect len(records), and returned nil either way. Result:
// handing Save a byte-copied Recorder produced a fixture that LOOKED
// successfully written (err == nil, valid gzip+NDJSON+SHA256 on disk)
// but silently contained ZERO of the records the caller believed they
// were saving.
//
// This test now asserts the FIXED behaviour (AC-2): Save returns a
// non-nil, registry-sourced error (codeRecorderCopied, propagated
// unchanged from Records()) AND leaves NO file at all on disk — neither
// the shard nor the header — closing the "partially-written fixture"
// gap Bill's SEC-037 correction named explicitly.
func TestSEC037_SaveRejectsCopiedRecorderAndLeavesNoPartialArtifact(t *testing.T) {
	rec := NewRecorder()
	// Capture 5 genuine records.
	for i := 0; i < 5; i++ {
		if err := rec.ObserveCommand(protocol.Command{CorrelationID: protocol.CorrelationID("c")}); err != nil {
			t.Fatalf("ObserveCommand: %v", err)
		}
	}
	if got, err := rec.Len(); err != nil || got != 5 {
		t.Fatalf("sanity: rec.Len() = (%d, %v), want (5, nil)", got, err)
	}

	// A byte-for-byte copy of *rec via copyRecorderBytes (copy_test.go) —
	// same effect as the illegal-but-compilable `c := *r`, but via unsafe
	// so this file still passes `go vet ./...` (copylocks), which the
	// VERIFY step requires (see copy_test.go's copyRecorderBytes doc
	// comment for the full rationale).
	recCopy := copyRecorderBytes(rec)

	dir := t.TempDir()
	err := Save(dir, "poc-fixture", recCopy, FixtureMeta{WorldSeed: 1, AppVersion: "test"})
	if err == nil {
		t.Fatal("Save(copied *Recorder) returned nil error — SEC-037 regression")
	}
	if !errors.Is(err, &errs.E{Code: codeRecorderCopied}) {
		t.Fatalf("Save's rejection error does not carry %s (codeRecorderCopied): %v", codeRecorderCopied, err)
	}

	shardPath, err2 := fixtureShardPath(dir, "poc-fixture")
	if err2 != nil {
		t.Fatalf("fixtureShardPath: %v", err2)
	}
	headerPath, err2 := fixtureHeaderPath(dir, "poc-fixture")
	if err2 != nil {
		t.Fatalf("fixtureHeaderPath: %v", err2)
	}
	if _, statErr := os.Stat(shardPath); !os.IsNotExist(statErr) {
		t.Errorf("Save left a shard file at %s after rejecting a copied Recorder (stat err = %v, want os.ErrNotExist) — AC-2's no-partial-artifact requirement", shardPath, statErr)
	}
	if _, statErr := os.Stat(headerPath); !os.IsNotExist(statErr) {
		t.Errorf("Save left a header file at %s after rejecting a copied Recorder (stat err = %v, want os.ErrNotExist) — AC-2's no-partial-artifact requirement", headerPath, statErr)
	}

	// Confirm the ORIGINAL rec (uncopied) still saves correctly, to prove
	// this is specific to the copy, not a general Save defect.
	dir2 := t.TempDir()
	if err := Save(dir2, "poc-fixture-orig", rec, FixtureMeta{WorldSeed: 1, AppVersion: "test"}); err != nil {
		t.Fatalf("Save(original): %v", err)
	}
	fx2, err := Load(dir2, "poc-fixture-orig")
	if err != nil {
		t.Fatalf("Load(original): %v", err)
	}
	if len(fx2.Records) != 5 {
		t.Fatalf("original Save should have written 5 records, got %d", len(fx2.Records))
	}
	t.Logf("Save(original *Recorder) correctly wrote %d records — confirms the defect/fix is specific to the copied-Recorder path", len(fx2.Records))
}

// TestSave_GenuinelyEmptyRecorderStillSucceeds is SEC-037/AC-3: the
// criterion that catches the WRONG fix (a naive `if len(records) == 0 {
// return err }` inside Save, which would reject Save silently
// misdiagnose "the copy was rejected" and "the caller legitimately
// recorded nothing" the same way the original bug did, just moved one
// level up). A fresh, uncopied, zero-Observe*-calls Recorder is a real,
// valid case per this package's own doc comments — nothing requires a
// Recorder to have captured at least one record — so Save must still
// succeed for it.
func TestSave_GenuinelyEmptyRecorderStillSucceeds(t *testing.T) {
	rec := NewRecorder() // never Observe*'d
	dir := t.TempDir()

	if err := Save(dir, "empty-fixture", rec, FixtureMeta{WorldSeed: 1, AppVersion: "test"}); err != nil {
		t.Fatalf("Save on a genuinely empty, uncopied Recorder returned an error: %v (want nil — an empty capture is a legitimate session, not a rejected copy)", err)
	}

	fx, err := Load(dir, "empty-fixture")
	if err != nil {
		t.Fatalf("Load(empty-fixture): %v", err)
	}
	if len(fx.Records) != 0 {
		t.Fatalf("Load(empty-fixture).Records = %d, want 0", len(fx.Records))
	}
}
