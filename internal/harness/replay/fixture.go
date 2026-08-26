package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// shardKind is the nominal ShardMeta.Kind every fixture's single shard is
// written with. Informational only — see serialize.ShardMeta.Kind's doc
// comment ("metadata for humans and tooling, not a constraint checked
// per-record").
const shardKind = "harness.replay.stream"

// fixtureHeader is the on-disk shape of a fixture's <name>.header.json.
// Header is int.serializer's own serialize.Header, reused verbatim
// (FormatVersion/WorldSeed/AppVersion/ShardIndex — AC-4/AC-14, no
// parallel fields invented). ProtocolVersion is the one AC-4 field
// Header has no slot for (protocol versioning is int.protocol's concern,
// not int.serializer's) — carried as a sibling field, NOT via anonymous
// embedding: Header defines its own MarshalJSON/UnmarshalJSON (SEC-024,
// to keep its debugTouched field unexported), and Go promotes an
// embedded type's custom marshaller to the outer type UNCHANGED — it
// would never see ProtocolVersion. A named field avoids that trap while
// still invoking Header's own (un)marshaller correctly for its own
// nested JSON object.
type fixtureHeader struct {
	Header          serialize.Header `json:"header"`
	ProtocolVersion string           `json:"protocolVersion"`
}

// FixtureMeta is the caller-supplied identity for a fixture being saved
// (AC-4): the world seed it was recorded from and the engine identity/
// build string that recorded it (reusing serialize.Header.WorldSeed and
// .AppVersion — see fixtureHeader's doc comment). ProtocolVersion is
// always protocol.ProtocolVersion — the running build's own version, not
// caller-supplied, since a fixture can only ever be recorded against
// whatever protocol version this build speaks.
type FixtureMeta struct {
	WorldSeed  int64
	AppVersion string
}

// fixtureShardPath and fixtureHeaderPath resolve name into the two flat
// file paths a fixture lives at under dir (doc.go's "On-disk layout").
// Both validate name via serialize.ValidateShardName FIRST (AC-2b) — the
// exact same function ShardMeta.Name is checked with, reused rather than
// reimplemented (Out of scope's explicit instruction). A hostile name
// (path separators, "..", a drive marker, a trailing dot/space) is
// rejected outright, wrapped in a registry-sourced error naming the
// fixture name and the reason — never filepath.Clean'd, trimmed, or
// substituted with a fallback.
func fixtureShardPath(dir, name string) (string, error) {
	if err := serialize.ValidateShardName(name); err != nil {
		return "", errs.Wrap(codeInvalidFixtureName, errs.NewCorrelationID(), err, map[string]any{"name": name})
	}
	return filepath.Join(dir, name+".ndjson.gz"), nil
}

func fixtureHeaderPath(dir, name string) (string, error) {
	if err := serialize.ValidateShardName(name); err != nil {
		return "", errs.Wrap(codeInvalidFixtureName, errs.NewCorrelationID(), err, map[string]any{"name": name})
	}
	return filepath.Join(dir, name+".header.json"), nil
}

// saveCloseErr folds the shard file's f.Close() error into Save's own
// return value (BUG-305: previously discarded unconditionally via
// `_ = f.Close()`, even on the success path). A flush/close failure here
// (disk full, a late I/O error that surfaces only at Close, GR#1) means a
// shard whose bytes might not actually be durable on disk was reported as
// a clean Save regardless, with dir/<name>.header.json already written
// pointing at it.
//
// It is a pure function of its three inputs, extracted out of Save's
// defer closure specifically so this decision is directly unit-testable
// without needing to force a real *os.File to fail on Close — there is no
// portable, reliable technique to do that across this codebase's
// supported platforms (POSIX double-close tricks behave differently on
// Windows), so the logic itself is tested here instead of via an
// integration-level forced failure.
//
// closeErr is nil when the Close succeeded (the overwhelmingly common
// case: returns priorErr unchanged, whatever it was). priorErr is Save's
// own err as of the moment the defer runs — when non-nil, Save already
// has a MORE SPECIFIC failure to report (a write, encode, or header
// error) and a close failure on top of that is not new information worth
// overriding it with. Only when priorErr is nil AND closeErr is non-nil
// does this wrap and return the close failure as Save's own error —
// exactly the "Save was otherwise about to report a clean success"
// case this fix targets.
func saveCloseErr(closeErr, priorErr error, shardPath string) error {
	if closeErr == nil || priorErr != nil {
		return priorErr
	}
	return errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), closeErr, map[string]any{"path": shardPath, "cause": "closing fixture shard file"})
}

// Save writes every record rec has captured to dir/<name>.ndjson.gz (one
// NDJSON+gzip shard, via serialize.NDJSONSerializer.WriteShard — AC-2)
// plus dir/<name>.header.json (AC-4). dir is created if it does not
// already exist. name is validated per fixtureShardPath's doc comment
// (AC-2b) before anything is written.
//
// SEC-037 (AC-2): rec.Records() is resolved, and any error it returns
// (a struct-copied rec — see record.go's Records doc comment) is
// propagated BEFORE any file is created, and if anything below fails
// partway through, the deferred cleanup removes whatever was already
// written. Save either succeeds completely (a valid shard AND header
// both on disk) or leaves NOTHING on disk — never a half-written
// fixture that looks valid to a later Load. ONE disclosed exception
// (BUG-305): a Close error surfaced on the otherwise-successful path
// returns an error while the files remain installed — the data was
// fully written and any real corruption is contained by Load's SHA-256
// check; removing installed files for a close-status failure would
// destroy a likely-good fixture.
func Save(dir, name string, rec *Recorder, meta FixtureMeta) (err error) {
	shardPath, err := fixtureShardPath(dir, name)
	if err != nil {
		return err
	}
	headerPath, err := fixtureHeaderPath(dir, name)
	if err != nil {
		return err
	}

	// Resolved first, before any I/O: a rejected (struct-copied) rec
	// must never reach os.Create — see this function's doc comment.
	records, err := rec.Records()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), err, map[string]any{"path": dir, "cause": "creating fixtures directory"})
	}

	f, err := os.Create(shardPath)
	if err != nil {
		return errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), err, map[string]any{"path": shardPath, "cause": "creating fixture shard file"})
	}
	// ok becomes true only once both files are fully and successfully
	// written. Any earlier return leaves ok false, and the cleanup below
	// removes both paths (os.Remove on a file that was never created is
	// a harmless ENOENT, ignored) — AC-2's "no partial artifact"
	// requirement.
	ok := false
	defer func() {
		closeErr := f.Close()
		if !ok {
			_ = os.Remove(shardPath)
			_ = os.Remove(headerPath)
			return
		}
		err = saveCloseErr(closeErr, err, shardPath)
	}()

	idx := 0
	next := func() (serialize.Record, bool, error) {
		if idx >= len(records) {
			return serialize.Record{}, false, nil
		}
		r := records[idx]
		idx++
		return r, true, nil
	}

	shardMeta, err := (serialize.NDJSONSerializer{}).WriteShard(f, serialize.ShardMeta{Name: name, Kind: shardKind}, next)
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, errs.NewCorrelationID(), err, map[string]any{"path": shardPath, "cause": "writing fixture shard"})
	}

	h := serialize.NewHeader(meta.WorldSeed, 0, 0, meta.AppVersion)
	h.ShardIndex = []serialize.ShardMeta{shardMeta}
	fh := fixtureHeader{Header: h, ProtocolVersion: protocol.ProtocolVersion}

	encoded, err := json.MarshalIndent(fh, "", "  ")
	if err != nil {
		return errs.Wrap(codeFixtureCorrupt, errs.NewCorrelationID(), err, map[string]any{"path": headerPath, "cause": "encoding fixture header"})
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(headerPath, encoded, 0o644); err != nil {
		return errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), err, map[string]any{"path": headerPath, "cause": "writing fixture header"})
	}
	ok = true
	return nil
}

// Fixture is a fully loaded, integrity-checked fixture: its header plus
// every record it carries, in the order Save/the original Recorder wrote
// them (AC-1's ordering guarantee, preserved through the round trip).
type Fixture struct {
	Name    string // fixture name (for error reporting)
	Header  fixtureHeader
	Records []serialize.Record
}

// Load reads and integrity-checks dir/<name>.header.json and
// dir/<name>.ndjson.gz (AC-9), and version-checks the result (AC-8)
// before returning it. A hostile name (AC-2b), a missing/unreadable
// file, a bad gzip stream, a SHA256/size mismatch against the header's
// recorded ShardMeta, or a FormatVersion/ProtocolVersion mismatch all
// fail loudly with a registry-sourced error naming the fixture and the
// cause — never a partial or best-effort result, never a panic.
func Load(dir, name string) (Fixture, error) {
	headerPath, err := fixtureHeaderPath(dir, name)
	if err != nil {
		return Fixture{}, err
	}
	shardPath, err := fixtureShardPath(dir, name)
	if err != nil {
		return Fixture{}, err
	}

	raw, err := os.ReadFile(headerPath)
	if err != nil {
		return Fixture{}, errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), err, map[string]any{"path": headerPath, "cause": "reading fixture header"})
	}
	var fh fixtureHeader
	if err := json.Unmarshal(raw, &fh); err != nil {
		return Fixture{}, errs.Wrap(codeFixtureCorrupt, errs.NewCorrelationID(), err, map[string]any{"path": headerPath, "cause": "decoding fixture header"})
	}

	if err := serialize.CheckFormatVersion(fh.Header.FormatVersion); err != nil {
		return Fixture{}, errs.Wrap(codeFixtureVersionMismatch, errs.NewCorrelationID(), err, map[string]any{
			"name": name, "cause": "fixture FormatVersion is not compatible with this build",
		})
	}
	if fh.ProtocolVersion != protocol.ProtocolVersion {
		return Fixture{}, errs.New(codeFixtureVersionMismatch, errs.NewCorrelationID(), map[string]any{
			"name": name, "got": fh.ProtocolVersion, "want": protocol.ProtocolVersion,
			"cause": "fixture was recorded against a different protocol.ProtocolVersion",
		})
	}
	if len(fh.Header.ShardIndex) != 1 {
		cause := fmt.Sprintf("fixture header does not describe exactly one shard (got %d)", len(fh.Header.ShardIndex))
		return Fixture{}, fixtureCorruptError(errs.NewCorrelationID(), headerPath, cause)
	}
	meta := fh.Header.ShardIndex[0]

	f, err := os.Open(shardPath)
	if err != nil {
		return Fixture{}, errs.Wrap(codeFixtureLoadFailed, errs.NewCorrelationID(), err, map[string]any{"path": shardPath, "cause": "opening fixture shard"})
	}
	defer func() { _ = f.Close() }()

	// Single streaming pass: the shard's raw bytes are hashed AND
	// gzip-decoded/decoded simultaneously via io.TeeReader, so integrity
	// checking never requires buffering the whole shard or reading it
	// twice (mirrors serialize.validateShardFile's technique, adapted to
	// a single-pass reader rather than a separate hash-only pass since
	// this package does not go through serialize.ValidateBundle's
	// directory-shaped API — see doc.go's "On-disk layout").
	hasher := sha256.New()
	counted := &countingReader{r: io.TeeReader(f, hasher)}

	// SEC-038: maxFixtureDecodedBytes bounds the DECOMPRESSED stream
	// during decompression itself (see limits.go for the derivation and
	// why this package supplies its own bound rather than relying on a
	// shared constant inside foundation.serialize). A shard that would
	// decompress past that bound is rejected loudly (MET-H007) instead
	// of being allowed to finish decompressing first.
	var records []serialize.Record
	decodeErr := (serialize.NDJSONSerializer{}).ReadShard(counted, maxFixtureDecodedBytes, func(rec serialize.Record) error {
		records = append(records, rec)
		return nil
	})
	if decodeErr != nil {
		if errors.Is(decodeErr, serialize.ErrDecodedBytesExceeded) {
			return Fixture{}, errs.Wrap(codeFixtureDecodedTooLarge, errs.NewCorrelationID(), decodeErr, map[string]any{
				"path": shardPath, "maxDecodedBytes": maxFixtureDecodedBytes,
			})
		}
		return Fixture{}, errs.Wrap(codeFixtureCorrupt, errs.NewCorrelationID(), decodeErr, map[string]any{"path": shardPath, "cause": "decoding fixture shard (bad gzip stream or malformed record)"})
	}
	if counted.n != meta.ByteSize {
		return Fixture{}, errs.New(codeFixtureCorrupt, errs.NewCorrelationID(), map[string]any{
			"path": shardPath, "headerByteSize": meta.ByteSize, "actualByteSize": counted.n,
			"cause": "fixture shard size does not match header",
		})
	}
	gotHash := hex.EncodeToString(hasher.Sum(nil))
	if gotHash != meta.SHA256 {
		return Fixture{}, errs.New(codeFixtureCorrupt, errs.NewCorrelationID(), map[string]any{
			"path": shardPath, "headerSHA256": meta.SHA256, "actualSHA256": gotHash,
			"cause": "fixture shard SHA256 does not match header (corrupt or tampered fixture)",
		})
	}

	return Fixture{Name: shardPath, Header: fh, Records: records}, nil
}

// countingReader counts bytes read through it, so Load can verify the
// shard's on-disk size against ShardMeta.ByteSize without a second pass.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// Commands returns every KindCommand record in f, decoded in order via
// protocol.DecodeCommand. A malformed command record fails the whole
// call rather than silently skipping it.
func (f Fixture) Commands() ([]protocol.Command, error) {
	var out []protocol.Command
	for i, rec := range f.Records {
		if RecordKind(rec.Kind) != KindCommand {
			continue
		}
		cmd, err := protocol.DecodeCommand(rec.Data)
		if err != nil {
			cause := fmt.Sprintf("decoding recorded Command at record %d: %v", i, err)
			return nil, fixtureCorruptError(errs.NewCorrelationID(), f.Name, cause)
		}
		out = append(out, cmd)
	}
	return out, nil
}

// Results returns every KindResult record in f, decoded in order.
func (f Fixture) Results() ([]protocol.CommandResult, error) {
	var out []protocol.CommandResult
	for i, rec := range f.Records {
		if RecordKind(rec.Kind) != KindResult {
			continue
		}
		var r protocol.CommandResult
		if err := json.Unmarshal(rec.Data, &r); err != nil {
			cause := fmt.Sprintf("decoding recorded CommandResult at record %d: %v", i, err)
			return nil, fixtureCorruptError(errs.NewCorrelationID(), f.Name, cause)
		}
		out = append(out, r)
	}
	return out, nil
}

// Events returns every KindEvent record in f, decoded in order.
func (f Fixture) Events() ([]protocol.Event, error) {
	var out []protocol.Event
	for i, rec := range f.Records {
		if RecordKind(rec.Kind) != KindEvent {
			continue
		}
		var e protocol.Event
		if err := json.Unmarshal(rec.Data, &e); err != nil {
			cause := fmt.Sprintf("decoding recorded Event at record %d: %v", i, err)
			return nil, fixtureCorruptError(errs.NewCorrelationID(), f.Name, cause)
		}
		out = append(out, e)
	}
	return out, nil
}

// Deltas returns every KindDelta record in f, decoded in order.
func (f Fixture) Deltas() ([]protocol.Delta, error) {
	var out []protocol.Delta
	for i, rec := range f.Records {
		if RecordKind(rec.Kind) != KindDelta {
			continue
		}
		var d protocol.Delta
		if err := json.Unmarshal(rec.Data, &d); err != nil {
			cause := fmt.Sprintf("decoding recorded Delta at record %d: %v", i, err)
			return nil, fixtureCorruptError(errs.NewCorrelationID(), f.Name, cause)
		}
		out = append(out, d)
	}
	return out, nil
}
