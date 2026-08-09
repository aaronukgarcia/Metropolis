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

// ShardPath returns the path a shard with the given ShardMeta lives (or
// should be written) at within bundle directory dir.
func ShardPath(dir string, meta ShardMeta) (string, error) {
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
		return fmt.Errorf("shard %q: SHA256 mismatch: header says %s, file %q hashes to %s", meta.Name, meta.SHA256, path, gotHash)
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
