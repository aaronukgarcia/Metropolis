package errs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLogger_NDJSONLineShape(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)
	l.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })

	err := l.Log(Entry{
		Level:         "error",
		Code:          "MET-F900",
		CorrelationID: "corr-1",
		Module:        "foundation.errors",
		Msg:           "boom",
		Ctx:           map[string]any{"key": "value"},
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	line := buf.String()
	if !bytes.HasSuffix([]byte(line), []byte("\n")) {
		t.Fatal("expected line to end in a newline (NDJSON)")
	}
	if bytes.Count([]byte(line), []byte("\n")) != 1 {
		t.Fatalf("expected exactly one line, got %q", line)
	}

	var parsed Entry
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("line did not parse back as JSON: %v", err)
	}
	if parsed.Code != "MET-F900" || parsed.CorrelationID != "corr-1" || parsed.Module != "foundation.errors" || parsed.Msg != "boom" {
		t.Errorf("round-tripped entry mismatch: %+v", parsed)
	}
	if parsed.Ts == "" {
		t.Error("expected ts to be auto-filled")
	}
}

func TestLogger_MultipleLinesAreValidNDJSON(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf)

	for i := 0; i < 5; i++ {
		if err := l.Log(Entry{Level: "info", Code: "MET-F900", CorrelationID: "c", Module: "m", Msg: "m"}); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}

	scanner := bufio.NewScanner(&buf)
	count := 0
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("line %d not valid JSON: %v", count, err)
		}
		count++
	}
	if count != 5 {
		t.Errorf("expected 5 lines, got %d", count)
	}
}

func TestFileLogger_RotationTrigger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ndjson")

	// Small maxBytes so a couple of entries trigger rotation.
	l, err := NewFileLogger(path, 80, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	entry := Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr", Module: "foundation.errors", Msg: "a fairly long message to fill bytes"}
	for i := 0; i < 10; i++ {
		if err := l.Log(entry); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("expected rotated file %s.1 to exist: %v", path, err)
	}
}

func TestFileLogger_RotationKeepsAtMostNBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.ndjson")

	l, err := NewFileLogger(path, 40, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	entry := Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr", Module: "foundation.errors", Msg: "filler message text"}
	for i := 0; i < 40; i++ {
		if err := l.Log(entry); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}

	if _, err := os.Stat(path + ".4"); err == nil {
		t.Error("expected no .4 backup to exist (N=3 retention)")
	}
	for _, suffix := range []string{".1", ".2", ".3"} {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Errorf("expected %s%s to exist after heavy rotation: %v", path, suffix, err)
		}
	}
}

func TestRingBuffer_FallbackWhenNoSink(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	for i := 0; i < 5; i++ {
		logEntry(Entry{Code: "MET-F900", Msg: "m"})
	}
	if got := len(Recent()); got != 5 {
		t.Errorf("Recent() len = %d, want 5", got)
	}
}

func TestRingBuffer_CapsAt200(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	for i := 0; i < 250; i++ {
		logEntry(Entry{Code: "MET-F900", Msg: "m"})
	}
	if got := len(Recent()); got != 200 {
		t.Errorf("Recent() len = %d, want 200", got)
	}
}

func TestLogEntry_FallsBackToRingOnSinkWriteFailure(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	dir := t.TempDir()
	l, err := NewFileLogger(filepath.Join(dir, "x.ndjson"), 0, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	l.Close() // closed file -> subsequent writes fail
	SetSink(l)

	logEntry(Entry{Code: "MET-F900", Msg: "write should fail"})

	if got := len(Recent()); got != 1 {
		t.Errorf("expected the failed-write entry to fall back into the ring buffer, Recent() len = %d", got)
	}
}

func TestLogger_ConcurrentWritesAreSafe(t *testing.T) {
	dir := t.TempDir()
	l, err := NewFileLogger(filepath.Join(dir, "concurrent.ndjson"), 1<<20, 3)
	if err != nil {
		t.Fatalf("NewFileLogger: %v", err)
	}
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = l.Log(Entry{Level: "info", Code: "MET-F900", CorrelationID: "c", Module: "m", Msg: "concurrent"})
			}
		}(i)
	}
	wg.Wait()
}

func TestConstruct_ConcurrentAutoLoggingIsSafe(t *testing.T) {
	setupTestRegistry(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = New("MET-F900", "corr", nil)
			}
		}()
	}
	wg.Wait()
}
