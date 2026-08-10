package replay

import (
	"context"
	"strings"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// copyRecorderBytes performs a raw byte-for-byte memcpy of a Recorder —
// identical in effect to the illegal-but-compilable `c := *r` (both
// alias records's backing array; both give the copy its own,
// independent mu byte-pattern), but via unsafe rather than a literal
// struct-copy expression. Same technique and same reason as
// internal/protocol/sec020_test.go's copyTransportBytes: this package
// cannot contain a literal `*r`/`*p` and still pass `go vet ./...`
// (copylocks), which the VERIFY step requires, so the byte-level copy is
// the sanctioned way to exercise the regression this guard exists to
// prevent.
func copyRecorderBytes(r *Recorder) *Recorder {
	c := new(Recorder)
	*(*[unsafe.Sizeof(Recorder{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(Recorder{})]byte)(unsafe.Pointer(r))
	return c
}

// copyEnginePlayerBytes is copyRecorderBytes' EnginePlayer counterpart.
func copyEnginePlayerBytes(p *EnginePlayer) *EnginePlayer {
	c := new(EnginePlayer)
	*(*[unsafe.Sizeof(EnginePlayer{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(EnginePlayer{})]byte)(unsafe.Pointer(p))
	return c
}

// TestRecorderCopyRejected is AC-13b: a struct-copied Recorder must be
// rejected, never silently allowed to operate its own independent
// mutex over the ALIASED records backing array.
func TestRecorderCopyRejected(t *testing.T) {
	r := NewRecorder()
	if err := r.ObserveCommand(cmdFixture("x")); err != nil {
		t.Fatalf("ObserveCommand: %v", err)
	}

	cp := copyRecorderBytes(r) // byte-for-byte copy — the misuse SEC-020 guards against

	if err := cp.ObserveCommand(cmdFixture("y")); err == nil {
		t.Fatal("ObserveCommand on a struct-copied Recorder: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeRecorderCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeRecorderCopied)
	}

	// SEC-037/AC-1: Records() and Len() must report the copy-rejection as
	// a distinct, non-nil ERROR — not silently collapse it into the same
	// zero-value ("nil"/"0") a genuinely empty, uncopied Recorder would
	// also report. TestRecordsAndLen_DistinguishCopyRejectionFromGenuinelyEmpty
	// below is the direct AC-1 proof (rejected vs. empty, side by side);
	// this test's job is narrower — confirm the copy path specifically
	// still errors and still touches nothing.
	if recs, err := cp.Records(); err == nil {
		t.Errorf("Records() on a struct-copied Recorder: err = nil (recs=%v), want a non-nil error", recs)
	} else if !strings.Contains(err.Error(), codeRecorderCopied) {
		t.Errorf("Records() rejection error %q does not carry %s", err.Error(), codeRecorderCopied)
	}
	if n, err := cp.Len(); err == nil {
		t.Errorf("Len() on a struct-copied Recorder: err = nil (n=%d), want a non-nil error", n)
	} else if !strings.Contains(err.Error(), codeRecorderCopied) {
		t.Errorf("Len() rejection error %q does not carry %s", err.Error(), codeRecorderCopied)
	}

	// The original must be entirely unaffected.
	if n, err := r.Len(); err != nil || n != 1 {
		t.Errorf("original Recorder's Len() = (%d, %v) after a copy was misused, want (1, nil)", n, err)
	}
}

// TestRecordsAndLen_DistinguishCopyRejectionFromGenuinelyEmpty is
// SEC-037/AC-1's direct check: Records() and Len() must make "the
// receiver is a struct-copied Recorder, rejected" and "the receiver is
// a genuine, uncopied, empty Recorder" observable as two DIFFERENT
// outcomes through the public API alone — no side channel (a log line,
// the errs ring) required to tell them apart. Before this fix both
// cases returned the identical (nil, 0) pair; Save (fixture.go) relied
// on exactly that ambiguity being absent and got it wrong (SEC-037).
func TestRecordsAndLen_DistinguishCopyRejectionFromGenuinelyEmpty(t *testing.T) {
	empty := NewRecorder() // genuinely empty: zero Observe* calls, never copied

	copied := copyRecorderBytes(NewRecorder()) // rejected: struct-copied, also zero records

	emptyRecs, emptyErr := empty.Records()
	_, copiedErr := copied.Records()

	if emptyErr != nil {
		t.Fatalf("a genuinely empty, uncopied Recorder's Records() returned an error: %v (want nil error, empty slice)", emptyErr)
	}
	if len(emptyRecs) != 0 {
		t.Fatalf("a genuinely empty Recorder's Records() = %v, want empty", emptyRecs)
	}
	if copiedErr == nil {
		t.Fatalf("a struct-copied Recorder's Records() returned err=nil — indistinguishable from the genuinely-empty case above, which is exactly SEC-037's ambiguity")
	}

	emptyLen, emptyLenErr := empty.Len()
	copiedLen, copiedLenErr := copied.Len()
	if emptyLenErr != nil || emptyLen != 0 {
		t.Fatalf("a genuinely empty, uncopied Recorder's Len() = (%d, %v), want (0, nil)", emptyLen, emptyLenErr)
	}
	if copiedLenErr == nil {
		t.Fatalf("a struct-copied Recorder's Len() returned err=nil (n=%d) — indistinguishable from the genuinely-empty case, SEC-037's ambiguity", copiedLen)
	}
}

// TestEnginePlayerCopyRejected is AC-13b's EnginePlayer counterpart.
func TestEnginePlayerCopyRejected(t *testing.T) {
	p := &EnginePlayer{
		commands: []protocol.Command{cmdFixture("only")},
		recorded: []protocol.CommandResult{{CorrelationID: "only", Accepted: true}},
		cmdCh:    make(chan protocol.Command, 1),
		notify:   make(chan struct{}, 1),
	}
	p.self.Store(p)

	cp := copyEnginePlayerBytes(p)

	if ch := cp.Commands(); ch != closedCommandCh {
		t.Error("Commands() on a struct-copied EnginePlayer did not return the fail-closed channel")
	}
	if ok := cp.SendResult(protocol.CommandResult{CorrelationID: "only", Accepted: true}); ok {
		t.Error("SendResult on a struct-copied EnginePlayer returned true, want false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already-cancelled: Replay must reject the copy before touching ctx at all
	if _, err := cp.Replay(ctx); err == nil {
		t.Fatal("Replay on a struct-copied EnginePlayer: expected rejection, got nil error")
	} else if !strings.Contains(err.Error(), codeEnginePlayerCopied) {
		t.Errorf("rejection error %q does not carry %s", err.Error(), codeEnginePlayerCopied)
	}
}
