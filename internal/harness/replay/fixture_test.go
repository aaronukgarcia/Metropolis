package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func sampleRecorder(t *testing.T) *Recorder {
	t.Helper()
	r := NewRecorder()
	corr := protocol.NewCorrelationID()
	cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: corr, Kind: protocol.KindPause, Payload: protocol.PausePayload{}}
	if err := r.ObserveCommand(cmd); err != nil {
		t.Fatalf("ObserveCommand: %v", err)
	}
	if err := r.ObserveResult(protocol.CommandResult{CorrelationID: corr, Tick: 1, Accepted: true}); err != nil {
		t.Fatalf("ObserveResult: %v", err)
	}
	if err := r.ObserveEvent(protocol.Event{Kind: "debug.op.executed", Tick: 1, Severity: protocol.SeverityInfo}); err != nil {
		t.Fatalf("ObserveEvent: %v", err)
	}
	if err := r.ObserveDelta(protocol.Delta{SubscriptionID: "sub-1", Tick: 1, Seq: 1, Patch: []byte(`{"a":1}`)}); err != nil {
		t.Fatalf("ObserveDelta: %v", err)
	}
	return r
}

// TestSaveLoadFixtureRoundTrip proves AC-2/AC-4/AC-9's happy path: a
// saved fixture loads back with its header fields and every record
// intact.
func TestSaveLoadFixtureRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := sampleRecorder(t)
	meta := FixtureMeta{WorldSeed: 42, AppVersion: "test-build"}
	if err := Save(dir, "roundtrip", r, meta); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "roundtrip.ndjson.gz")); err != nil {
		t.Fatalf("shard file missing: %v", err)
	}

	fx, err := Load(dir, "roundtrip")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if fx.Header.Header.WorldSeed != 42 {
		t.Errorf("WorldSeed = %d, want 42", fx.Header.Header.WorldSeed)
	}
	if fx.Header.Header.AppVersion != "test-build" {
		t.Errorf("AppVersion = %q, want %q", fx.Header.Header.AppVersion, "test-build")
	}
	if fx.Header.ProtocolVersion != protocol.ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", fx.Header.ProtocolVersion, protocol.ProtocolVersion)
	}
	if len(fx.Records) != 4 {
		t.Fatalf("got %d records, want 4", len(fx.Records))
	}

	cmds, err := fx.Commands()
	if err != nil || len(cmds) != 1 {
		t.Fatalf("Commands(): %v, %d", err, len(cmds))
	}
	results, err := fx.Results()
	if err != nil || len(results) != 1 {
		t.Fatalf("Results(): %v, %d", err, len(results))
	}
	events, err := fx.Events()
	if err != nil || len(events) != 1 {
		t.Fatalf("Events(): %v, %d", err, len(events))
	}
	deltas, err := fx.Deltas()
	if err != nil || len(deltas) != 1 {
		t.Fatalf("Deltas(): %v, %d", err, len(deltas))
	}
}

// TestSaveRejectsHostileFixtureNames is AC-2b's table-driven rejection
// test, mirroring savebundle_security_test.go's
// TestValidateShardNameRejectsTraversal cases exactly — this package
// reuses serialize.ValidateShardName rather than re-deriving the rule,
// so the same hostile inputs must be rejected here too.
func TestFixtureNameTraversalRejectedOnSave(t *testing.T) {
	cases := []string{
		"../x", `..\x`, "a/b", `a\b`, "..", ".", "",
		"/etc/passwd", "C:foo", `C:\foo`, `\\server\share\x`,
		"citizens.0042.", "citizens.0043 ",
	}
	dir := t.TempDir()
	r := sampleRecorder(t)
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			err := Save(dir, name, r, FixtureMeta{})
			if err == nil {
				t.Fatalf("Save(name=%q): expected rejection, got nil", name)
			}
			if !strings.Contains(err.Error(), codeInvalidFixtureName) {
				t.Errorf("Save(name=%q) error %q does not carry %s", name, err.Error(), codeInvalidFixtureName)
			}
		})
	}
}

// TestLoadRejectsHostileFixtureNames is AC-2b's Load-side counterpart.
func TestFixtureNameTraversalRejectedOnLoad(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "../escape")
	if err == nil {
		t.Fatal("Load with a traversal name: expected rejection, got nil")
	}
	if !strings.Contains(err.Error(), codeInvalidFixtureName) {
		t.Errorf("error %q does not carry %s", err.Error(), codeInvalidFixtureName)
	}
}

// TestLoadRejectsTruncatedFixture is AC-9: a truncated (corrupted) shard
// file fails loudly, never a panic, and names the fixture path.
func TestLoadRejectsCorruptTruncatedFixture(t *testing.T) {
	dir := t.TempDir()
	r := sampleRecorder(t)
	if err := Save(dir, "truncated", r, FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	shardPath := filepath.Join(dir, "truncated.ndjson.gz")
	raw, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("reading shard: %v", err)
	}
	if len(raw) < 4 {
		t.Fatalf("shard too small to truncate meaningfully: %d bytes", len(raw))
	}
	if err := os.WriteFile(shardPath, raw[:len(raw)-4], 0o644); err != nil {
		t.Fatalf("writing truncated shard: %v", err)
	}

	if _, err := Load(dir, "truncated"); err == nil {
		t.Fatal("Load of a truncated fixture: expected rejection, got nil")
	} else if !strings.Contains(err.Error(), codeFixtureCorrupt) {
		t.Errorf("error %q does not carry %s", err.Error(), codeFixtureCorrupt)
	}
}

// TestLoadRejectsTamperedShard is AC-9's hash-mismatch case: bytes that
// still decode as valid gzip+NDJSON but no longer match the header's
// recorded SHA256/size must still be rejected.
func TestLoadRejectsCorruptTamperedShard(t *testing.T) {
	dir := t.TempDir()
	r1 := sampleRecorder(t)
	if err := Save(dir, "victim", r1, FixtureMeta{}); err != nil {
		t.Fatalf("Save victim: %v", err)
	}
	r2 := sampleRecorder(t)
	if err := r2.ObserveCommand(cmdFixture("extra")); err != nil {
		t.Fatalf("ObserveCommand: %v", err)
	}
	if err := Save(dir, "donor", r2, FixtureMeta{}); err != nil {
		t.Fatalf("Save donor: %v", err)
	}

	// Swap victim's shard bytes for donor's — same header, different
	// (still validly gzip/NDJSON-encoded) content, so only the hash
	// check can catch it.
	donorBytes, err := os.ReadFile(filepath.Join(dir, "donor.ndjson.gz"))
	if err != nil {
		t.Fatalf("reading donor shard: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "victim.ndjson.gz"), donorBytes, 0o644); err != nil {
		t.Fatalf("overwriting victim shard: %v", err)
	}

	if _, err := Load(dir, "victim"); err == nil {
		t.Fatal("Load of a hash-tampered fixture: expected rejection, got nil")
	} else if !strings.Contains(err.Error(), codeFixtureCorrupt) {
		t.Errorf("error %q does not carry %s", err.Error(), codeFixtureCorrupt)
	}
}

// TestLoadRejectsFormatVersionMismatch is AC-8: a header claiming an
// incompatible FormatVersion major fails loudly.
func TestLoadRejectsFormatVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	r := sampleRecorder(t)
	if err := Save(dir, "verfix", r, FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	headerPath := filepath.Join(dir, "verfix.header.json")
	raw, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("reading header: %v", err)
	}
	tampered := strings.Replace(string(raw), `"formatVersion": "1.0.0"`, `"formatVersion": "99.0.0"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup bug: formatVersion replacement did not match header.json's actual shape")
	}
	if err := os.WriteFile(headerPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("writing tampered header: %v", err)
	}

	if _, err := Load(dir, "verfix"); err == nil {
		t.Fatal("Load with an incompatible FormatVersion: expected rejection, got nil")
	} else if !strings.Contains(err.Error(), codeFixtureVersionMismatch) {
		t.Errorf("error %q does not carry %s", err.Error(), codeFixtureVersionMismatch)
	}
}

// TestLoadRejectsProtocolVersionMismatch is AC-8's protocol-version half.
func TestLoadRejectsProtocolVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	r := sampleRecorder(t)
	if err := Save(dir, "protover", r, FixtureMeta{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	headerPath := filepath.Join(dir, "protover.header.json")
	raw, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("reading header: %v", err)
	}
	tampered := strings.Replace(string(raw), `"protocolVersion": "`+protocol.ProtocolVersion+`"`, `"protocolVersion": "999.0"`, 1)
	if tampered == string(raw) {
		t.Fatal("test setup bug: protocolVersion replacement did not match header.json's actual shape")
	}
	if err := os.WriteFile(headerPath, []byte(tampered), 0o644); err != nil {
		t.Fatalf("writing tampered header: %v", err)
	}

	if _, err := Load(dir, "protover"); err == nil {
		t.Fatal("Load with a mismatched ProtocolVersion: expected rejection, got nil")
	} else if !strings.Contains(err.Error(), codeFixtureVersionMismatch) {
		t.Errorf("error %q does not carry %s", err.Error(), codeFixtureVersionMismatch)
	}
}
