package errs

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Entry is one structured log line: {ts, level, code, correlationId,
// module, msg, ctx} — the shape M0-ENG §3 specifies for logs/engine.ndjson
// and logs/ui.ndjson, one JSON object per line (NDJSON), so the F12 info
// panel's error tail can just tail-and-pretty-print.
type Entry struct {
	Ts            string         `json:"ts"`
	Level         string         `json:"level"`
	Code          string         `json:"code"`
	CorrelationID string         `json:"correlationId"`
	Module        string         `json:"module"`
	Msg           string         `json:"msg"`
	Ctx           map[string]any `json:"ctx,omitempty"`
}

// Logger writes Entry values as NDJSON to an underlying writer. It is
// safe for concurrent use.
type Logger struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time

	// File-backed rotation state; zero value (path == "") means Logger
	// wraps an arbitrary io.Writer and never rotates.
	path       string
	maxBytes   int64
	maxBackups int
	size       int64
	file       *os.File
}

// NewLogger wraps an arbitrary io.Writer as an NDJSON sink. Rotation is
// not available for a bare writer (there is nothing to rename) — use
// NewFileLogger for a rotating file sink.
func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w, now: time.Now}
}

// defaultMaxBackups is the "keep N=3" rotation depth from the task brief.
const defaultMaxBackups = 3

// NewFileLogger opens (creating/appending to) path as a rotating NDJSON
// sink: once the file reaches maxBytes, it is rotated to path+".1"
// (existing .1..maxBackups-1 shift up by one; anything beyond
// maxBackups is dropped) and a fresh file is opened at path.
//
// maxBytes <= 0 disables size-based rotation (the file grows unbounded).
// maxBackups <= 0 defaults to 3.
func NewFileLogger(path string, maxBytes int64, maxBackups int) (*Logger, error) {
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}
	f, size, err := openAppend(path)
	if err != nil {
		return nil, err
	}
	return &Logger{
		w:          f,
		now:        time.Now,
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
		size:       size,
		file:       f,
	}, nil
}

func openAppend(path string) (*os.File, int64, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// SetClock overrides the logger's timestamp source. Per M0-ENG §1.1,
// engine simulation code must never call the wall clock directly on the
// tick path — inject the sim clock's Now method here for any logger the
// engine writes through. Defaults to time.Now.
func (l *Logger) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// Log writes one NDJSON line for e. If e.Ts is empty it is filled in
// from the logger's clock. Safe for concurrent use.
func (l *Logger) Log(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e.Ts == "" {
		e.Ts = l.now().UTC().Format(time.RFC3339Nano)
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal log entry: %w", err)
	}
	line = append(line, '\n')

	n, err := l.w.Write(line)
	if err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}

	if l.file != nil {
		l.size += int64(n)
		if l.maxBytes > 0 && l.size >= l.maxBytes {
			if rerr := l.rotateLocked(); rerr != nil {
				return fmt.Errorf("rotate %s: %w", l.path, rerr)
			}
		}
	}

	return nil
}

// rotateLocked performs the rename chain (path.N-1 -> path.N, ...,
// path -> path.1) and reopens a fresh file at path. Caller must hold l.mu.
func (l *Logger) rotateLocked() error {
	if err := l.file.Close(); err != nil {
		return err
	}

	oldest := fmt.Sprintf("%s.%d", l.path, l.maxBackups)
	_ = os.Remove(oldest) // fine if it doesn't exist

	for i := l.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		dst := fmt.Sprintf("%s.%d", l.path, i+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}

	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return err
	}

	f, size, err := openAppend(l.path)
	if err != nil {
		return err
	}
	l.file = f
	l.w = f
	l.size = size
	return nil
}

// Close closes the underlying file, if this Logger owns one.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// --- package-level auto-log sink (errs.New/Wrap write through this) ---

const ringCapacity = 200

// ring is a fixed-capacity, mutex-protected circular buffer of the most
// recent log entries. It is the in-memory fallback the F12 debug tail
// reads via Recent() whenever no file sink is configured, or whenever a
// configured sink's write fails — see the "ring buffer" design note in
// docs/design/errors.md for why write failures also fall back here.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []Entry
	start int
	count int
}

func newRingBuffer(cap int) *ringBuffer {
	return &ringBuffer{buf: make([]Entry, cap)}
}

func (r *ringBuffer) push(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.start + r.count) % len(r.buf)
	r.buf[idx] = e
	if r.count < len(r.buf) {
		r.count++
	} else {
		r.start = (r.start + 1) % len(r.buf)
	}
}

func (r *ringBuffer) snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}

var (
	sinkMu sync.Mutex
	sink   *Logger
	ring   = newRingBuffer(ringCapacity)
)

// SetSink configures the package-level ErrorSink that every error
// constructed by New/Wrap is automatically logged through. Pass nil to
// go back to the in-memory-only ring buffer (e.g. in tests).
func SetSink(l *Logger) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sink = l
}

// Recent returns a snapshot of the most recent (up to 200) log entries
// held in the in-memory ring buffer — populated whenever no file sink is
// configured, and as a fail-safe whenever a configured sink's write
// fails. This is the primitive the F12 debug info panel's error tail
// (M0-ENG §3) reads.
func Recent() []Entry {
	return ring.snapshot()
}

// logEntry is the single choke point every error constructed by New/Wrap
// passes through: write to the configured sink if there is one, and fall
// back to the ring buffer if there isn't one, or if the sink write fails.
// Errors are stored, never printed-and-lost (GR#1).
func logEntry(e Entry) {
	sinkMu.Lock()
	s := sink
	sinkMu.Unlock()

	if s == nil {
		ring.push(e)
		return
	}
	if err := s.Log(e); err != nil {
		ring.push(e)
	}
}

// resetSinkForTest restores the package-level sink state to its zero
// value (no configured sink, empty ring buffer). Test-only.
func resetSinkForTest() {
	sinkMu.Lock()
	sink = nil
	sinkMu.Unlock()
	ring = newRingBuffer(ringCapacity)
}
