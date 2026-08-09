package serialize

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestValidateShardNameRejectsTraversal is the SEC-001 table-driven
// rejection test: every ShardMeta.Name that is not a single clean path
// component must be rejected with the registry-sourced MET-F301 error,
// not silently rewritten or allowed through.
func TestValidateShardNameRejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"unix parent traversal", "../x"},
		{"windows parent traversal", `..\x`},
		{"unix subdirectory", "a/b"},
		{"windows subdirectory", `a\b`},
		{"bare dotdot", ".."},
		{"bare dot", "."},
		{"empty string", ""},
		{"absolute unix path", "/etc/passwd"},
		{"drive-relative windows path", "C:foo"},
		{"drive-absolute windows path", `C:\foo`},
		{"UNC windows path", `\\server\share\x`},
		// SEC-013: Windows silently strips trailing '.'/' ' at file
		// creation, so these must be rejected outright rather than
		// trimmed, or a hostile name could alias a legitimate shard's
		// on-disk file.
		{"trailing dot", "citizens.0042."},
		{"trailing space", "citizens.0043 "},
		{"trailing dot after traversal-looking prefix", "citizens.0044.."},
		{"trailing multiple dots", "citizens.0045..."},
		{"trailing multiple spaces", "citizens.0046   "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateShardName(c.in)
			if err == nil {
				t.Fatalf("ValidateShardName(%q): expected rejection, got nil", c.in)
			}
			if !strings.Contains(err.Error(), errShardNameInvalidCode) {
				t.Errorf("ValidateShardName(%q) error %q does not carry the registry code %s", c.in, err.Error(), errShardNameInvalidCode)
			}
		})
	}
}

// TestValidateShardNameAcceptsOrdinaryNames proves the fix does not break
// the existing shard-naming convention: ordinary single-component names
// (including ones with dots, as used for shard sharding like
// "citizens.0042") must still pass.
func TestValidateShardNameAcceptsOrdinaryNames(t *testing.T) {
	cases := []string{
		"citizens.0",
		"citizens.0042",
		"buildings",
		"meta",
		"a-b_c.123",
	}
	for _, name := range cases {
		if err := ValidateShardName(name); err != nil {
			t.Errorf("ValidateShardName(%q): unexpected rejection: %v", name, err)
		}
	}
}

// TestShardPathRejectsTraversal proves ShardPath itself (not just the
// helper) refuses a hostile name, for both the read and write shape —
// ShardPath is the single choke point CreateShardWriter/OpenShardReader/
// validateShardFile all go through.
func TestShardPathRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	_, err := ShardPath(dir, ShardMeta{Name: "../../escape", Encoding: "ndjson+gzip"})
	if err == nil {
		t.Fatal("ShardPath: expected rejection for a traversal name, got nil")
	}
	if !strings.Contains(err.Error(), errShardNameInvalidCode) {
		t.Errorf("ShardPath error %q does not carry the registry code %s", err.Error(), errShardNameInvalidCode)
	}
}

// TestValidateBundleRejectsHostileShardName is the containment test:
// build a bundle whose header.json carries a ShardMeta with a traversal
// Name (as a hostile bundle author would craft it, not via this
// package's normal write path), then prove ValidateBundle refuses to
// read outside shards/ rather than following the escape.
//
// The setup plants a real "secret" file one level above the bundle's
// shards/ directory (i.e. exactly where "../secret" would resolve to)
// so that if validation regressed, this test would actually observe the
// leaked file rather than merely a generic I/O error.
func TestValidateBundleRejectsHostileShardName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hostile-bundle")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	secretPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("outside-shards-should-never-be-read"), 0o644); err != nil {
		t.Fatalf("writing secret fixture: %v", err)
	}

	hostileMeta := ShardMeta{
		Name:     "../secret",
		Kind:     "citizen",
		Encoding: "ndjson+gzip",
		ByteSize: 36,
		SHA256:   "0000000000000000000000000000000000000000000000000000000000000",
	}
	h := NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, hostileMeta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	if _, err := ValidateBundle(dir); err == nil {
		t.Fatal("ValidateBundle: expected rejection of a hostile traversal shard name, got nil")
	} else if !strings.Contains(err.Error(), errShardNameInvalidCode) {
		t.Errorf("ValidateBundle error %q does not carry the registry code %s (validation may not have fired at all)", err.Error(), errShardNameInvalidCode)
	}

	if _, err := OpenShardReader(dir, hostileMeta); err == nil {
		t.Fatal("OpenShardReader: expected rejection of a hostile traversal shard name, got nil")
	} else if !strings.Contains(err.Error(), errShardNameInvalidCode) {
		t.Errorf("OpenShardReader error %q does not carry the registry code %s", err.Error(), errShardNameInvalidCode)
	}
}

// TestValidateShardNameCoversRegisteredHeaderRoundTrip guards against a
// future header.go change silently widening ShardMeta.Name's JSON
// decoding (e.g. via a custom UnmarshalJSON) in a way that would bypass
// ValidateShardName — decodes a hostile Name through the exact
// json.Unmarshal path ReadHeader uses, then re-validates it, to keep the
// containment test above honest about what "decoded straight out of
// header.json" actually means.
func TestValidateShardNameCoversRegisteredHeaderRoundTrip(t *testing.T) {
	raw := []byte(`{"Name":"../../../../etc/passwd","Encoding":"ndjson+gzip"}`)
	var decoded ShardMeta
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := ValidateShardName(decoded.Name); err == nil {
		t.Fatal("expected rejection of a JSON-decoded traversal name, got nil")
	}
}

// TestWindowsStripsTrailingDotAndSpace is the empirical proof underlying
// SEC-013 (ASM-025): it demonstrates, on this OS, that a file created
// with a trailing '.' or ' ' in its name materialises on disk WITHOUT
// that trailing character — the exact aliasing primitive ValidateShardName
// must now refuse to let a shard name exploit. Skipped on non-Windows,
// since the stripping behaviour is a Win32-specific filesystem quirk
// (this project is Windows-first — see ValidateShardName's SEC-013
// doc comment).
func TestWindowsStripsTrailingDotAndSpace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("trailing dot/space stripping is a Windows-specific filesystem behaviour")
	}

	dir := t.TempDir()

	dotPath := filepath.Join(dir, "citizens.0042.")
	if f, err := os.Create(dotPath); err != nil {
		t.Fatalf("os.Create(%q): %v", dotPath, err)
	} else {
		_ = f.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, "citizens.0042")); err != nil {
		t.Fatalf("expected trailing-dot name to materialise as the stripped form; os.Stat: %v", err)
	}

	spacePath := filepath.Join(dir, "citizens.0043 ")
	if f, err := os.Create(spacePath); err != nil {
		t.Fatalf("os.Create(%q): %v", spacePath, err)
	} else {
		_ = f.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, "citizens.0043")); err != nil {
		t.Fatalf("expected trailing-space name to materialise as the stripped form; os.Stat: %v", err)
	}
}

// TestShardPathRejectsAliasingBeforeOSNormalization is SEC-013's
// containment test: prove ValidateShardName (via ShardPath) refuses a
// trailing-dot/space name BEFORE it ever reaches an OS file-creation
// call, since by the time os.Create runs, the name has already been
// silently normalised and no longer looks hostile to the OS at all —
// the check has to happen at the name-validation layer, not by
// inspecting what actually got created afterward.
func TestShardPathRejectsAliasingBeforeOSNormalization(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	legit := ShardMeta{Name: "citizens.0042", Encoding: "ndjson+gzip"}
	legitFile, err := CreateShardWriter(dir, legit)
	if err != nil {
		t.Fatalf("creating legitimate shard: %v", err)
	}
	if err := legitFile.Close(); err != nil {
		t.Fatalf("closing legitimate shard file: %v", err)
	}

	hostile := ShardMeta{Name: "citizens.0042.", Encoding: "ndjson+gzip"}
	if _, err := ShardPath(dir, hostile); err == nil {
		t.Fatal("ShardPath: expected rejection of a trailing-dot alias name, got nil")
	} else if !strings.Contains(err.Error(), errShardNameInvalidCode) {
		t.Errorf("ShardPath error %q does not carry the registry code %s", err.Error(), errShardNameInvalidCode)
	}

	hostileSpace := ShardMeta{Name: "citizens.0042 ", Encoding: "ndjson+gzip"}
	if _, err := ShardPath(dir, hostileSpace); err == nil {
		t.Fatal("ShardPath: expected rejection of a trailing-space alias name, got nil")
	} else if !strings.Contains(err.Error(), errShardNameInvalidCode) {
		t.Errorf("ShardPath error %q does not carry the registry code %s", err.Error(), errShardNameInvalidCode)
	}
}

// TestValidateBundleEscapesHostileSHA256InErrorMessage is SEC-022's
// containment test: meta.SHA256 is decoded from a hostile bundle's
// header.json exactly like meta.Name (SEC-001/SEC-013), and it was the
// only attacker-controlled field in validateShardFile's SHA256-mismatch
// message still rendered with %s instead of %q — letting a crafted
// SHA256 smuggle terminal escape sequences (OSC "set title", SGR colour
// codes, ...) into text that main() prints straight to stderr.
//
// This builds a real, otherwise-valid bundle (real shard bytes, ByteSize
// matched so the size check passes and execution actually reaches the
// SHA256 comparison) whose header.json claims a SHA256 containing raw
// ESC (0x1B) and BEL (0x07) bytes, and asserts the property that matters
// — the resulting error string contains no raw control bytes, i.e. a
// terminal it's printed to can never be made to interpret them — rather
// than pinning the exact %q-escaped text, so this survives message
// rewording.
func TestValidateBundleEscapesHostileSHA256InErrorMessage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hostile-sha256-bundle")
	if err := CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir: %v", err)
	}

	meta := ShardMeta{Name: "citizens.0", Kind: "citizen", Encoding: "ndjson+gzip"}
	f, err := CreateShardWriter(dir, meta)
	if err != nil {
		t.Fatalf("CreateShardWriter: %v", err)
	}
	meta, err = (NDJSONSerializer{}).WriteShard(f, meta, recordSourceFromSlice(sampleRecords(3)))
	if err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing shard file: %v", err)
	}

	// The header claims a SHA256 that (a) will not match the real
	// shard's hash, so execution reaches the mismatch branch, and (b)
	// carries raw terminal escape sequences: ESC ]0;pwned BEL (OSC set-
	// title) plus an SGR colour-reset sequence. ByteSize is left
	// matching the real shard so the earlier size check passes and
	// doesn't short-circuit before the SHA256 comparison.
	const esc = "\x1b"
	const bel = "\x07"
	hostileSHA256 := esc + "]0;pwned" + bel + esc + "[31mFAKE-RED-TEXT" + esc + "[0m"
	meta.SHA256 = hostileSHA256

	h := NewHeader(1, 1, 1, "test-build")
	h.ShardIndex = append(h.ShardIndex, meta)
	if err := WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	_, err = ValidateBundle(dir)
	if err == nil {
		t.Fatal("ValidateBundle: expected a SHA256 mismatch error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "SHA256 mismatch") {
		t.Fatalf("expected a SHA256 mismatch error, got: %v", err)
	}
	for _, control := range []string{esc, bel} {
		if strings.Contains(msg, control) {
			t.Fatalf("SEC-022 NOT closed: error message contains a raw control byte %q verbatim: %q", control, msg)
		}
	}
	// The %q-escaped form (e.g. \x1b) should still be present somewhere
	// so the hostile SHA256's content isn't silently dropped either —
	// escaped and visible, not raw and invisible-but-executed.
	if !strings.Contains(msg, `\x1b`) {
		t.Errorf("expected the escaped ESC byte (\\x1b) to appear in the %%q-rendered message, got: %q", msg)
	}
}
