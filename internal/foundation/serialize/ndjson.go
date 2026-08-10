package serialize

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// zeroTime is the zero time.Time value, used to pin gzip.Writer.ModTime for
// deterministic output. Named for clarity at the call site; this is the
// only appearance of the time package in this file and it never reads the
// wall clock.
var zeroTime time.Time

// NDJSONSerializer is the canonical StateSerializer: one gzip-compressed
// NDJSON stream per shard, one JSON object per line. It is canonical for
// configs, protocol debug, fixtures, exports, and saves below the (not yet
// chosen — see binary.go) size threshold (A3, R2).
//
// Determinism: for the same sequence of Records, WriteShard produces
// byte-identical output every time. This matters because save bytes feed
// the CI determinism gate's snapshot hashing (§1.2) — a save taken twice
// from the same seed and command log must hash the same. Two things make
// that true here:
//
//  1. Each record is written as its JSON line exactly once, in the order
//     next() produces them, with Data embedded verbatim as a
//     json.RawMessage (never re-marshalled, so no field-ordering or
//     number-formatting drift from a second encode pass).
//  2. The gzip.Writer header fields that would otherwise vary
//     (ModTime, OS, Name, Comment) are pinned: OS is fixed to 255
//     ("unknown", which is also compress/gzip's own default — pinned
//     here explicitly rather than relied upon, so the invariant holds
//     even if that default ever changes) and ModTime is left at its
//     zero value. Name and Comment are never set. flate compression
//     itself is already deterministic for a given input and level, so
//     pinning the header is the only extra step needed.
//
// Callers relying on byte-determinism must also ensure next() itself is
// deterministic (same records, same order) — this package cannot enforce
// that on the caller's side.
type NDJSONSerializer struct{}

// ndjsonLine is the on-the-wire shape of one NDJSON line: the record kind
// plus its opaque data, embedded raw so it round-trips byte-for-byte.
type ndjsonLine struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// countingWriter counts bytes written through it, so WriteShard can fill in
// ShardMeta.ByteSize without a second pass over the data.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// WriteShard implements StateSerializer. It streams: each record is
// marshalled to one line and written to the gzip stream as soon as it's
// produced; nothing is buffered beyond a single record and gzip's own
// internal window.
func (NDJSONSerializer) WriteShard(w io.Writer, meta ShardMeta, next RecordSource) (ShardMeta, error) {
	hasher := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(w, hasher)}

	gz, err := gzip.NewWriterLevel(counter, gzip.BestCompression)
	if err != nil {
		return meta, fmt.Errorf("serialize: creating gzip writer for shard %q: %w", meta.Name, err)
	}
	// Pin the header fields that would otherwise make output
	// non-deterministic across runs. See the determinism note on
	// NDJSONSerializer.
	gz.OS = 255
	gz.ModTime = zeroTime
	gz.Name = ""
	gz.Comment = ""

	bw := bufio.NewWriter(gz)

	var count int64
	for {
		rec, ok, err := next()
		if err != nil {
			return meta, fmt.Errorf("serialize: reading record %d for shard %q: %w", count, meta.Name, err)
		}
		if !ok {
			break
		}
		line := ndjsonLine{Kind: rec.Kind, Data: json.RawMessage(rec.Data)}
		encoded, err := json.Marshal(line)
		if err != nil {
			return meta, fmt.Errorf("serialize: encoding record %d (kind %q) for shard %q: %w", count, rec.Kind, meta.Name, err)
		}
		if _, err := bw.Write(encoded); err != nil {
			return meta, fmt.Errorf("serialize: writing record %d for shard %q: %w", count, meta.Name, err)
		}
		if err := bw.WriteByte('\n'); err != nil {
			return meta, fmt.Errorf("serialize: writing record %d newline for shard %q: %w", count, meta.Name, err)
		}
		count++
	}

	if err := bw.Flush(); err != nil {
		return meta, fmt.Errorf("serialize: flushing shard %q: %w", meta.Name, err)
	}
	if err := gz.Close(); err != nil {
		return meta, fmt.Errorf("serialize: closing gzip stream for shard %q: %w", meta.Name, err)
	}

	meta.RecordCount = count
	meta.ByteSize = counter.n
	meta.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	meta.Encoding = "ndjson+gzip"
	return meta, nil
}

// ErrDecodedBytesExceeded is returned (wrapped with line/position
// context) by ReadShard when a caller-supplied maxDecodedBytes bound
// (SEC-038) is exceeded while reading the DECOMPRESSED stream —
// decompression is bounded DURING decompression, not by decompressing
// fully and measuring the result afterward (the latter still pays the
// attack's full memory/CPU cost before reporting it, which is
// symptom-silencing dressed as a fix). Callers that want a distinct,
// registry-sourced error for this specific case (rather than this
// package's own plain fmt.Errorf, which predates
// internal/foundation/errs — see errNotImplementedCode's doc comment in
// binary.go for why) should errors.Is against this sentinel.
var ErrDecodedBytesExceeded = errors.New("serialize: decompressed shard stream exceeded the caller's maxDecodedBytes bound")

// boundedReader wraps r, failing with ErrDecodedBytesExceeded once more
// than max cumulative bytes have been requested from it, rather than
// silently truncating. io.LimitReader's EOF-on-exhaustion behaviour was
// deliberately NOT reused here: it would make an over-budget stream
// indistinguishable from a clean, undersized one to every downstream
// check (readLine would see a plain io.EOF, json.Unmarshal would then
// either fail on a truncated fragment with a generic parse error, or —
// worse, if the truncation happened to land on a line boundary —
// silently succeed with a partial record set) — exactly the ambiguous,
// silently-truncated-but-looks-valid failure mode SEC-037 already named
// as worse than a loud, specific rejection.
type boundedReader struct {
	r   io.Reader
	max int64 // remaining budget, decremented as bytes are consumed
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.max <= 0 {
		return 0, ErrDecodedBytesExceeded
	}
	if int64(len(p)) > b.max {
		p = p[:b.max]
	}
	n, err := b.r.Read(p)
	b.max -= int64(n)
	return n, err
}

// ReadShard implements StateSerializer. It streams lines out of the gzip
// stream one at a time via a growable line reader (not bufio.Scanner, whose
// default token-size cap would silently mis-split unusually large
// records), decoding and dispatching each to handle before reading the
// next line.
//
// maxDecodedBytes (SEC-038) bounds the total number of DECOMPRESSED
// bytes this call will read from the gzip stream before failing with
// ErrDecodedBytesExceeded — gzip decompression is otherwise unbounded (a
// few KB of maximally-compressible input can decompress to gigabytes),
// and readLine's own no-maximum-line-size design (below) means a single
// record could otherwise balloon without limit too. Every caller MUST
// choose a limit appropriate to ITS OWN population: this package is
// shared by callers ranging from a few-KB test fixture (harness.replay)
// to saves sized for 100 M citizens (§5.3) — no single package-level
// constant here could be correct for both, which is precisely the wrong
// lesson SEC-033 already taught this codebase once. Pass 0 to mean "no
// limit"; no current caller does.
func (NDJSONSerializer) ReadShard(r io.Reader, maxDecodedBytes int64, handle RecordHandler) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("serialize: opening gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	var decompressed io.Reader = gz
	if maxDecodedBytes > 0 {
		decompressed = &boundedReader{r: gz, max: maxDecodedBytes}
	}

	br := bufio.NewReader(decompressed)
	var lineNo int64
	for {
		lineBytes, err := readLine(br)
		if err != nil && err != io.EOF {
			return fmt.Errorf("serialize: reading line %d: %w", lineNo, err)
		}
		if len(lineBytes) > 0 {
			var line ndjsonLine
			if unmarshalErr := json.Unmarshal(lineBytes, &line); unmarshalErr != nil {
				return fmt.Errorf("serialize: decoding line %d: %w", lineNo, unmarshalErr)
			}
			if handleErr := handle(Record{Kind: line.Kind, Data: []byte(line.Data)}); handleErr != nil {
				return fmt.Errorf("serialize: handling record at line %d (kind %q): %w", lineNo, line.Kind, handleErr)
			}
			lineNo++
		}
		if err == io.EOF {
			break
		}
	}
	return nil
}

// readLine reads one '\n'-delimited line (delimiter stripped, trailing
// '\r' tolerated) from br with no maximum LINE size of its own, unlike
// bufio.Scanner's default token cap — br's underlying source may itself
// be a boundedReader (above), which bounds the TOTAL stream instead, so
// an individual line can still grow arbitrarily large only up to
// whatever budget the caller configured for the whole shard, never
// truly unbounded end to end. It returns io.EOF alongside any trailing
// partial line (or an empty slice with io.EOF at a clean end of
// stream), matching bufio.Reader.ReadBytes semantics that callers
// already expect.
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadBytes('\n')
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, err
}
