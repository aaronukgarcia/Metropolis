package serialize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Bundle layout on disk:
//
//	<dir>/
//	  header.json          -- the Header, see header.go
//	  shards/
//	    <ShardMeta.Name>.ndjson.gz   (NDJSON+gzip encoding)
//	    ...
//
// <dir>'s base name is conventionally the save/fixture name. Every shard
// file name is derived from its ShardMeta.Name plus an extension chosen by
// its Encoding (shardFileExt below) — this package never invents shard
// names on its own.

const shardsSubdir = "shards"

// headerFileName is the fixed name of the header file within a bundle
// directory.
const headerFileName = "header.json"

// HeaderPath returns the path to dir's header.json.
func HeaderPath(dir string) string {
	return filepath.Join(dir, headerFileName)
}

// ShardsDir returns the path to dir's shards subdirectory.
func ShardsDir(dir string) string {
	return filepath.Join(dir, shardsSubdir)
}

// shardFileExt maps a ShardMeta.Encoding to its on-disk file extension.
func shardFileExt(encoding string) (string, error) {
	switch encoding {
	case "ndjson+gzip":
		return ".ndjson.gz", nil
	default:
		return "", fmt.Errorf("serialize: unknown shard encoding %q", encoding)
	}
}

// errShardNameInvalidCode is MET-F301, registered in data/errors.json
// under foundation.serialize's F300-F399 range (GR#7). It is raised by
// ValidateShardName — see that function's doc comment.
const errShardNameInvalidCode = "MET-F301"

// ValidateShardName rejects any ShardMeta.Name that is not a single
// clean path component, closing SEC-001 (path traversal / zip-slip):
// ShardMeta.Name is not trusted local data — ReadHeader/ValidateBundle
// decode it straight out of a bundle's header.json, which may have come
// from a shared save file or bug report, i.e. from an attacker. Every
// place a shard name becomes a filesystem path must call this first.
// ShardPath does so below, which covers every read/write call site that
// goes through it (validateShardFile, CreateShardWriter,
// OpenShardReader); cmd/metctl's export destination path is built
// separately (it targets an output directory, not ShardsDir) and calls
// this directly.
//
// This deliberately does NOT lean on filepath.Clean (or any other
// "fix up the name" transform) to sanitise a hostile name: Clean("../x")
// is still "../x", so cleaning alone does not stop an upward walk, and
// silently rewriting a hostile name into a plausible-looking one would
// hide the attack rather than reject it — worse than doing nothing.
// Every rule below is an explicit rejection, never a rewrite.
//
// Cross-platform note (this project is Windows-first, but a bundle's
// header.json can be authored on any OS and read on any other): Go's
// path/filepath is OS-dependent — filepath.IsAbs, filepath.VolumeName,
// and what counts as a separator all vary by GOOS. A check that only
// used those functions would hold on the OS it was tested on and not
// necessarily elsewhere. So the primary checks here are OS-independent
// string tests (explicit "/" and "\" rejection, since "\" is a
// separator on Windows but not Unix; explicit ":" rejection, since Go's
// own filepath.IsAbs never flags a Windows drive-relative name like
// "C:foo" as absolute — on Windows that resolves relative to whatever
// the current directory on the C: drive happens to be, which is exactly
// the kind of surprising escape this function exists to close), with
// filepath.IsAbs/filepath.VolumeName layered on top as a second,
// OS-native line of defence (catches UNC "\\server\share" forms when
// actually running on Windows).
//
// SEC-013 (Tester-1, 2026-08-09 / ASM-025): trailing dots and trailing
// whitespace are also rejected, for a different reason than the above —
// not directory escape, but same-directory ALIASING. Windows' file APIs
// silently strip a trailing "." or " " when a file is actually created
// (verified empirically: os.Create("citizens.0042.") materialises on
// disk as "citizens.0042"), but filepath.Base does not perform that
// stripping, so "citizens.0042." satisfies every check above and only
// collides with a legitimately-named "citizens.0042" shard once the OS
// gets involved — a hostile bundle could shadow or overwrite a real
// shard's file this way. Rejected outright rather than trimmed, for the
// same "never rewrite a hostile name" reason as every other check here.
func ValidateShardName(name string) error {
	reject := func(reason string) error {
		return fmt.Errorf("%s: shard name %q is not a valid single path component (%s)", errShardNameInvalidCode, name, reason)
	}
	if name == "" || name == "." || name == ".." {
		return reject("empty or dot/dotdot rejected, SEC-001")
	}
	if strings.ContainsAny(name, `/\`) {
		return reject("path separator rejected, SEC-001")
	}
	if strings.Contains(name, ":") {
		return reject("drive/volume marker rejected, SEC-001")
	}
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return reject("absolute or volume-qualified path rejected, SEC-001")
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return reject("trailing dot or space rejected — Windows silently strips these on file creation, which would let this name alias a different shard's on-disk file, SEC-013")
	}
	if filepath.Base(name) != name {
		return reject("not a single clean path component rejected, SEC-001")
	}
	return nil
}

// ShardPath returns the path a shard with the given ShardMeta lives (or
// should be written) at within bundle directory dir. meta.Name is run
// through ValidateShardName first (SEC-001) — it is decoded from
// untrusted bundle data (see that function's doc comment), so this is
// the single choke point every shard read/write path goes through.
func ShardPath(dir string, meta ShardMeta) (string, error) {
	if err := ValidateShardName(meta.Name); err != nil {
		return "", err
	}
	ext, err := shardFileExt(meta.Encoding)
	if err != nil {
		return "", err
	}
	return filepath.Join(ShardsDir(dir), meta.Name+ext), nil
}

// CreateBundleDir creates a fresh bundle directory (and its shards
// subdirectory) at dir. It fails if dir already exists, to avoid silently
// merging into a stale bundle — callers that intentionally want to
// overwrite a save should remove it first.
func CreateBundleDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("serialize: bundle directory %q already exists", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("serialize: checking bundle directory %q: %w", dir, err)
	}
	if err := os.MkdirAll(ShardsDir(dir), 0o755); err != nil {
		return fmt.Errorf("serialize: creating bundle directory %q: %w", dir, err)
	}
	return nil
}

// WriteHeader marshals h as indented JSON and writes it to dir/header.json.
// The bundle directory (and its shards subdirectory) must already exist —
// see CreateBundleDir.
func WriteHeader(dir string, h Header) error {
	encoded, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize: encoding header for %q: %w", dir, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(HeaderPath(dir), encoded, 0o644); err != nil {
		return fmt.Errorf("serialize: writing header for %q: %w", dir, err)
	}
	return nil
}

// ReadHeader reads and decodes dir/header.json and checks its
// FormatVersion against CurrentFormatVersion per the migration rules
// documented on CheckFormatVersion. It does not touch any shard file — use
// ValidateBundle for that.
func ReadHeader(dir string) (Header, error) {
	raw, err := os.ReadFile(HeaderPath(dir))
	if err != nil {
		return Header{}, fmt.Errorf("serialize: reading header for %q: %w", dir, err)
	}
	var h Header
	if err := json.Unmarshal(raw, &h); err != nil {
		return Header{}, fmt.Errorf("serialize: decoding header for %q: %w", dir, err)
	}
	if err := CheckFormatVersion(h.FormatVersion); err != nil {
		return Header{}, err
	}
	return h, nil
}

// ValidateBundle reads dir's header (applying the same version check as
// ReadHeader) and then, for every shard listed in its ShardIndex, opens the
// shard file and rehashes it, comparing the result against the recorded
// SHA256 and ByteSize. It streams each shard file through the hash (never
// loading a whole shard into memory) and does not decode shard contents —
// this is an integrity check of the encoded bytes, not a semantic replay.
//
// All shard mismatches are collected and returned together via
// errors.Join, so a caller (e.g. metctl verify) can report every problem
// in one pass rather than stopping at the first.
func ValidateBundle(dir string) (Header, error) {
	h, err := ReadHeader(dir)
	if err != nil {
		return Header{}, err
	}

	var problems []error
	for _, meta := range h.ShardIndex {
		if err := validateShardFile(dir, meta); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return h, fmt.Errorf("serialize: bundle %q failed validation: %w", dir, errors.Join(problems...))
	}
	return h, nil
}

func validateShardFile(dir string, meta ShardMeta) error {
	path, err := ShardPath(dir, meta)
	if err != nil {
		return fmt.Errorf("shard %q: %w", meta.Name, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("shard %q: opening %q: %w", meta.Name, path, err)
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	n, err := io.Copy(hasher, f)
	if err != nil {
		return fmt.Errorf("shard %q: reading %q: %w", meta.Name, path, err)
	}

	if n != meta.ByteSize {
		return fmt.Errorf("shard %q: size mismatch: header says %d bytes, file %q is %d bytes", meta.Name, meta.ByteSize, path, n)
	}
	gotHash := hex.EncodeToString(hasher.Sum(nil))
	if gotHash != meta.SHA256 {
		// meta.SHA256 is decoded straight from a hostile bundle's
		// header.json, exactly like meta.Name (SEC-001/SEC-013) — never
		// validated as hex anywhere upstream. %q (not %s) so a crafted
		// SHA256 containing terminal control bytes (OSC/SGR escape
		// sequences, etc.) renders as an escaped literal instead of
		// being interpreted by whatever terminal this error's %v
		// eventually gets printed to (SEC-022). gotHash is our own
		// hex.EncodeToString output, so it can't carry hostile bytes,
		// but it's %q too — matching verbs on both sides of the
		// comparison makes the invariant ("nothing here is trusted
		// enough for %s") obvious to the next reader, not just correct
		// today.
		return fmt.Errorf("shard %q: SHA256 mismatch: header says %q, file %q hashes to %q", meta.Name, meta.SHA256, path, gotHash)
	}
	return nil
}

// CreateShardWriter opens (creating parent directories via
// CreateBundleDir/WriteHeader beforehand) the file a shard with the given
// ShardMeta should be written to, ready to pass to a StateSerializer's
// WriteShard as the io.Writer. The caller is responsible for closing the
// returned file.
func CreateShardWriter(dir string, meta ShardMeta) (*os.File, error) {
	path, err := ShardPath(dir, meta)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("serialize: creating shard file %q: %w", path, err)
	}
	return f, nil
}

// OpenShardReader opens the file a shard with the given ShardMeta was
// written to, ready to pass to a StateSerializer's ReadShard as the
// io.Reader. The caller is responsible for closing the returned file.
func OpenShardReader(dir string, meta ShardMeta) (*os.File, error) {
	path, err := ShardPath(dir, meta)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("serialize: opening shard file %q: %w", path, err)
	}
	return f, nil
}
