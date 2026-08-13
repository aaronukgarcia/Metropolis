package metricsdash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
)

func TestLogNote_WritesRealFeedbackRecord(t *testing.T) {
	dir := t.TempDir()
	fixedNow := func() time.Time { return time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC) }

	if err := LogNote(dir, NoteBug, "the perf dashboard shows a stale timestamp", "cmd/metricsdash", fixedNow); err != nil {
		t.Fatalf("LogNote: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var jsonFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			jsonFiles = append(jsonFiles, e.Name())
		}
	}
	if len(jsonFiles) != 1 {
		t.Fatalf("expected exactly 1 record file, got %v", jsonFiles)
	}

	raw, err := os.ReadFile(filepath.Join(dir, jsonFiles[0]))
	if err != nil {
		t.Fatalf("reading record: %v", err)
	}
	var rec debug.FeedbackRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record is not valid debug.FeedbackRecord JSON: %v", err)
	}

	if rec.SchemaVersion != debug.FeedbackSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d (must match FEAT-065's schema exactly for claude-devfeedback-import.js to accept it)", rec.SchemaVersion, debug.FeedbackSchemaVersion)
	}
	if !strings.Contains(rec.Body, "the perf dashboard shows a stale timestamp") {
		t.Errorf("Body = %q, missing the submitted note text", rec.Body)
	}
	if !strings.Contains(rec.Body, "cmd/metricsdash") {
		t.Errorf("Body = %q, missing the submitted context", rec.Body)
	}
	if rec.Timestamp != "2026-08-12T15:00:00Z" {
		t.Errorf("Timestamp = %q, want the injected clock's value", rec.Timestamp)
	}
	// False-pass guard (AC-7's own warning): a test only checking a
	// "logged!" return value, without reading the file back and
	// unmarshalling it against the real shared schema, would also pass
	// a build that dropped the note on the floor.
	if rec.CorrelationID == "" {
		t.Error("CorrelationID must be populated, not left zero-valued")
	}
}

func TestLogNote_MissingContextDefaultsToUnspecified(t *testing.T) {
	dir := t.TempDir()
	if err := LogNote(dir, NoteFinding, "what does this number mean", "", nil); err != nil {
		t.Fatalf("LogNote: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 record, got %d", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	var rec debug.FeedbackRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(rec.Body, "context: unspecified") {
		t.Errorf("Body = %q, expected the unspecified-context fallback (AC-7)", rec.Body)
	}
	if !strings.Contains(rec.Body, "finding") {
		t.Errorf("Body = %q, expected the submitted kind to be recorded", rec.Body)
	}
}

func TestLogNote_EmptyBodyRejected(t *testing.T) {
	dir := t.TempDir()
	err := LogNote(dir, NoteBug, "   ", "somewhere", nil)
	if err == nil {
		t.Fatal("expected an error for an empty/whitespace-only body, got nil")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected NO file written for a rejected empty submission, found %d", len(entries))
	}
}

// TestLogNote_StampsSourceMkeyAndKind is the regression test for
// ASM-477/BUG-126: LogNote must stamp SourceMkey="feat.metricsdash" and
// Kind=<the submitted NoteKind> onto every record it writes, since these
// are exactly the two fields claude-devfeedback-import.js now reads to
// attribute the resulting BOW item to feat.metricsdash (not
// feat.devmode) and file it as the correct item type (not always "bug").
func TestLogNote_StampsSourceMkeyAndKind(t *testing.T) {
	cases := []struct {
		name string
		kind NoteKind
		want string
	}{
		{"bug", NoteBug, "bug"},
		{"finding", NoteFinding, "finding"},
		{"assumption", NoteAssumption, "assumption"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := LogNote(dir, tc.kind, "a note body", "ctx", nil); err != nil {
				t.Fatalf("LogNote: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("ReadDir: %v, entries=%v", err, entries)
			}
			raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			var rec debug.FeedbackRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if rec.SourceMkey != "feat.metricsdash" {
				t.Errorf("SourceMkey = %q, want %q", rec.SourceMkey, "feat.metricsdash")
			}
			if rec.Kind != tc.want {
				t.Errorf("Kind = %q, want %q", rec.Kind, tc.want)
			}
		})
	}
}

// TestLogNote_UnwritableInboxSurfacesExplicitFailure is AC-10: a failed
// write must surface as an explicit failure, never silently discard the
// note.
func TestLogNote_UnwritableInboxSurfacesExplicitFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// blocked is a FILE, not a directory -- MkdirAll(blocked, ...) must fail.
	err := LogNote(blocked, NoteBug, "this should not vanish", "ctx", nil)
	if err == nil {
		t.Fatal("expected an explicit error when the inbox path is unwritable, got nil")
	}
}
