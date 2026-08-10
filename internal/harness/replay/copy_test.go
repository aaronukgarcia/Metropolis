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

	if recs := cp.Records(); recs != nil {
		t.Errorf("Records() on a struct-copied Recorder = %v, want nil", recs)
	}
	if n := cp.Len(); n != 0 {
		t.Errorf("Len() on a struct-copied Recorder = %d, want 0", n)
	}

	// The original must be entirely unaffected.
	if n := r.Len(); n != 1 {
		t.Errorf("original Recorder's Len() = %d after a copy was misused, want 1", n)
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
