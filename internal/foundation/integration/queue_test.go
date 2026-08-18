package integration

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// --- test fixtures -------------------------------------------------------

// mockTransport is a fully-controllable protocol.Transport double: SendCommand
// either records the command (success) or returns protocol.ErrCommandQueueFull
// (when full is set), so tests can deterministically force the queue layer's
// spill/backpressure paths without racing real buffered channels.
type mockTransport struct {
	mu       sync.Mutex
	full     bool
	closed   bool
	received []protocol.Command
}

func newMockTransport() *mockTransport { return &mockTransport{} }

func (m *mockTransport) SendCommand(cmd protocol.Command) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return protocol.ErrTransportClosed
	}
	if m.full {
		return protocol.ErrCommandQueueFull
	}
	m.received = append(m.received, cmd)
	return nil
}

func (m *mockTransport) Results() <-chan protocol.CommandResult { return nil }
func (m *mockTransport) Events() <-chan protocol.Event          { return nil }
func (m *mockTransport) Deltas() <-chan protocol.Delta          { return nil }
func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockTransport) SetFull(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.full = v
}

func (m *mockTransport) Received() []protocol.Command {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]protocol.Command, len(m.received))
	copy(out, m.received)
	return out
}

var _ protocol.Transport = (*mockTransport)(nil)

func advanceTicksCmd(corrID string, n int64) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corrID),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
}

func buyCmd(corrID string, x int) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corrID),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: protocol.CellRef{X: x}},
	}
}

func setSpeedCmd(corrID string, speed int) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corrID),
		Kind:            protocol.KindSetSpeed,
		Payload:         protocol.SetSpeedPayload{Speed: speed},
	}
}

func newTestQueue(t *testing.T, inner protocol.Transport, cfg Config) *QueuedTransport {
	t.Helper()
	if cfg.DiskRoot == "" {
		cfg.DiskRoot = t.TempDir()
	}
	return NewQueuedTransport(inner, cfg)
}

// --- (a) FIFO within a tier ----------------------------------------------

func TestQueue_FIFOWithinTier(t *testing.T) {
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	inner.SetFull(true)
	const n = 20
	for i := 0; i < n; i++ {
		if err := q.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("SendCommand(%d): %v", i, err)
		}
	}
	inner.SetFull(false)

	stats, err := q.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if stats.Sent[1] != n {
		t.Fatalf("Sent[T1] = %d, want %d", stats.Sent[1], n)
	}

	got := inner.Received()
	if len(got) != n {
		t.Fatalf("received %d commands, want %d", len(got), n)
	}
	for i, cmd := range got {
		buy, ok := cmd.Payload.(protocol.BuyPayload)
		if !ok {
			t.Fatalf("received[%d] payload type = %T, want BuyPayload", i, cmd.Payload)
		}
		if buy.Cell.X != i {
			t.Fatalf("received[%d].Cell.X = %d, want %d (FIFO order violated)", i, buy.Cell.X, i)
		}
	}
}

// --- (b) cross-tier strict priority ---------------------------------------

func TestQueue_CrossTierPriority(t *testing.T) {
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	inner.SetFull(true)
	// Enqueue T1 first, then T0, to prove priority (not arrival order)
	// decides drain order across tiers.
	if err := q.SendCommand(buyCmd("corr", 100)); err != nil {
		t.Fatalf("enqueue T1 buy: %v", err)
	}
	if err := q.SendCommand(buyCmd("corr", 101)); err != nil {
		t.Fatalf("enqueue T1 buy: %v", err)
	}
	if err := q.SendCommand(setSpeedCmd("corr", 1)); err != nil {
		t.Fatalf("enqueue T2 setSpeed: %v", err)
	}
	if err := q.SendCommand(setSpeedCmd("corr", 2)); err != nil {
		t.Fatalf("enqueue T2 setSpeed: %v", err)
	}
	if err := q.SendCommand(advanceTicksCmd("corr", 1)); err != nil {
		t.Fatalf("enqueue T0 advanceTicks: %v", err)
	}
	if err := q.SendCommand(advanceTicksCmd("corr", 2)); err != nil {
		t.Fatalf("enqueue T0 advanceTicks: %v", err)
	}
	inner.SetFull(false)

	stats, err := q.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if stats.Sent[0] != 2 || stats.Sent[1] != 2 || stats.Sent[2] != 1 {
		t.Fatalf("Sent = %v, want [2 2 1] (T2 coalesces its two SetSpeeds into one)", stats.Sent)
	}

	got := inner.Received()
	if len(got) != 5 {
		t.Fatalf("received %d commands, want 5", len(got))
	}
	// T0 (both, FIFO) must come first, then T1 (both, FIFO), then T2 last.
	wantKinds := []protocol.Kind{
		protocol.KindAdvanceTicks, protocol.KindAdvanceTicks,
		protocol.KindBuy, protocol.KindBuy,
		protocol.KindSetSpeed,
	}
	for i, cmd := range got {
		if cmd.Kind != wantKinds[i] {
			t.Fatalf("received[%d].Kind = %q, want %q (tier priority violated): full order %v", i, cmd.Kind, wantKinds[i], kindsOf(got))
		}
	}
	if n := got[0].Payload.(protocol.AdvanceTicksPayload).N; n != 1 {
		t.Fatalf("first AdvanceTicks.N = %d, want 1 (T0 FIFO violated)", n)
	}
	if n := got[1].Payload.(protocol.AdvanceTicksPayload).N; n != 2 {
		t.Fatalf("second AdvanceTicks.N = %d, want 2 (T0 FIFO violated)", n)
	}
	if x := got[2].Payload.(protocol.BuyPayload).Cell.X; x != 100 {
		t.Fatalf("first Buy.Cell.X = %d, want 100 (T1 FIFO violated)", x)
	}
	if x := got[3].Payload.(protocol.BuyPayload).Cell.X; x != 101 {
		t.Fatalf("second Buy.Cell.X = %d, want 101 (T1 FIFO violated)", x)
	}
	if speed := got[4].Payload.(protocol.SetSpeedPayload).Speed; speed != 2 {
		t.Fatalf("SetSpeed.Speed = %d, want 2 (T2 must coalesce to the LATEST value)", speed)
	}
}

func kindsOf(cmds []protocol.Command) []protocol.Kind {
	out := make([]protocol.Kind, len(cmds))
	for i, c := range cmds {
		out[i] = c.Kind
	}
	return out
}

// --- (c) disk spill + drain reproduces enqueue order ----------------------

func TestQueue_DiskSpillPreservesOrder(t *testing.T) {
	const n = 12

	// Run 1: tiny memory window (memCap=2) -- forces most of the run onto
	// disk.
	spillInner := newMockTransport()
	spillQ := newTestQueue(t, spillInner, Config{T1MemCap: 2})
	spillInner.SetFull(true)
	for i := 0; i < n; i++ {
		if err := spillQ.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("spill run: SendCommand(%d): %v", i, err)
		}
	}
	depth := spillQ.Depth()
	if depth.T1OnDisk == 0 {
		t.Fatalf("expected some T1 backlog to have spilled to disk, T1OnDisk = 0")
	}
	spillInner.SetFull(false)
	if _, err := spillQ.Drain(0); err != nil {
		t.Fatalf("spill run: Drain: %v", err)
	}

	// Run 2: no-spill baseline (memCap comfortably above n).
	noSpillInner := newMockTransport()
	noSpillQ := newTestQueue(t, noSpillInner, Config{T1MemCap: 1000})
	noSpillInner.SetFull(true)
	for i := 0; i < n; i++ {
		if err := noSpillQ.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("no-spill run: SendCommand(%d): %v", i, err)
		}
	}
	if d := noSpillQ.Depth(); d.T1OnDisk != 0 {
		t.Fatalf("no-spill baseline unexpectedly spilled to disk: T1OnDisk = %d", d.T1OnDisk)
	}
	noSpillInner.SetFull(false)
	if _, err := noSpillQ.Drain(0); err != nil {
		t.Fatalf("no-spill run: Drain: %v", err)
	}

	gotSpill := spillInner.Received()
	gotNoSpill := noSpillInner.Received()
	if len(gotSpill) != n || len(gotNoSpill) != n {
		t.Fatalf("received counts: spill=%d no-spill=%d, want %d each", len(gotSpill), len(gotNoSpill), n)
	}
	for i := 0; i < n; i++ {
		xSpill := gotSpill[i].Payload.(protocol.BuyPayload).Cell.X
		xNoSpill := gotNoSpill[i].Payload.(protocol.BuyPayload).Cell.X
		if xSpill != i || xNoSpill != i {
			t.Fatalf("index %d: spill run X=%d, no-spill run X=%d, want both = %d (disk spill must reproduce exact enqueue order)", i, xSpill, xNoSpill, i)
		}
	}
}

// --- (d) torn-write safety -------------------------------------------------

func TestQueue_TornDiskSegmentNeverObservedAsValid(t *testing.T) {
	diskRoot := t.TempDir()
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{DiskRoot: diskRoot, T1MemCap: 1})

	inner.SetFull(true)
	if err := q.SendCommand(buyCmd("corr", 0)); err != nil { // seq 0: lands in memory
		t.Fatalf("enqueue seq0: %v", err)
	}
	if err := q.SendCommand(buyCmd("corr", 1)); err != nil { // seq 1: spills to disk (memCap=1)
		t.Fatalf("enqueue seq1: %v", err)
	}
	if d := q.Depth(); d.T1OnDisk != 1 {
		t.Fatalf("T1OnDisk = %d, want 1 (seq1 should have spilled)", d.T1OnDisk)
	}

	// Corrupt the promoted segment file directly -- simulates a torn/
	// interfered-with write reaching the final path, bypassing this
	// package's own atomic staging writer entirely.
	segPath := segmentPath(diskRoot, 1)
	if err := os.WriteFile(segPath, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("corrupting segment file: %v", err)
	}

	inner.SetFull(false)
	stats, err := q.Drain(0)
	if err == nil {
		t.Fatalf("Drain returned nil error for a corrupt disk segment, want ErrSpillReadFailed")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("Drain error = %v (%T), want an *errs.E", err, err)
	}
	if e.Code != ErrSpillReadFailed {
		t.Fatalf("Drain error code = %q, want %q", e.Code, ErrSpillReadFailed)
	}

	// The valid, uncorrupted seq0 command (from memory) must still have
	// been drained -- a corrupt LATER segment must not retroactively
	// invalidate an earlier, already-committed, valid one.
	if stats.Sent[1] != 1 {
		t.Fatalf("Sent[T1] = %d, want 1 (seq0 only)", stats.Sent[1])
	}
	got := inner.Received()
	if len(got) != 1 {
		t.Fatalf("received %d commands, want 1", len(got))
	}
	if x := got[0].Payload.(protocol.BuyPayload).Cell.X; x != 0 {
		t.Fatalf("received[0].Cell.X = %d, want 0", x)
	}

	// The corrupt bytes must never have been silently treated as a valid
	// command and forwarded to the inner transport.
	for _, c := range got {
		if x, ok := c.Payload.(protocol.BuyPayload); ok && x.Cell.X == 1 {
			t.Fatalf("the corrupt seq1 segment was forwarded to the inner transport as if valid")
		}
	}
}

// --- (e) T2 coalescing ------------------------------------------------------

func TestQueue_T2CoalescesToLatest(t *testing.T) {
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	inner.SetFull(true) // irrelevant for T2 -- it never touches inner directly
	for _, speed := range []int{1, 2, 3, 4, 5} {
		if err := q.SendCommand(setSpeedCmd("corr", speed)); err != nil {
			t.Fatalf("SendCommand(SetSpeed=%d): %v", speed, err)
		}
	}
	if !q.Depth().T2 {
		t.Fatalf("Depth().T2 = false, want true (a SetSpeed is pending)")
	}

	inner.SetFull(false)
	stats, err := q.Drain(0)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if stats.Sent[2] != 1 {
		t.Fatalf("Sent[T2] = %d, want 1 (5 SetSpeeds must coalesce into exactly 1 send)", stats.Sent[2])
	}
	got := inner.Received()
	if len(got) != 1 {
		t.Fatalf("received %d commands, want 1", len(got))
	}
	if speed := got[0].Payload.(protocol.SetSpeedPayload).Speed; speed != 5 {
		t.Fatalf("coalesced SetSpeed.Speed = %d, want 5 (the LATEST value)", speed)
	}
	if q.Depth().T2 {
		t.Fatalf("Depth().T2 = true after Drain, want false")
	}
}

// --- (f) un-enqueueable authoritative command -> registry error, never a silent drop ---

func TestQueue_T0ExhaustedIsRegistryErrorNotSilentDrop(t *testing.T) {
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{T0MemCap: 1})

	inner.SetFull(true)
	if err := q.SendCommand(advanceTicksCmd("corr", 1)); err != nil {
		t.Fatalf("first AdvanceTicks (fills the 1-slot T0 buffer): %v", err)
	}
	if d := q.Depth(); d.T0 != 1 {
		t.Fatalf("Depth().T0 = %d, want 1", d.T0)
	}

	err := q.SendCommand(advanceTicksCmd("corr", 2))
	if err == nil {
		t.Fatalf("second AdvanceTicks with T0 buffer full: got nil error, want ErrT0QueueExhausted (never a silent drop)")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error = %v (%T), want an *errs.E", err, err)
	}
	if e.Code != ErrT0QueueExhausted {
		t.Fatalf("error code = %q, want %q", e.Code, ErrT0QueueExhausted)
	}

	// The rejected command must not have been silently absorbed: depth
	// stays at exactly 1 (only the first command), and draining produces
	// only that first command.
	if d := q.Depth(); d.T0 != 1 {
		t.Fatalf("Depth().T0 after rejected enqueue = %d, want still 1", d.T0)
	}
	inner.SetFull(false)
	if _, err := q.Drain(0); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	got := inner.Received()
	if len(got) != 1 {
		t.Fatalf("received %d commands, want 1 (the rejected command must never appear)", len(got))
	}
	if n := got[0].Payload.(protocol.AdvanceTicksPayload).N; n != 1 {
		t.Fatalf("received[0].N = %d, want 1", n)
	}
}

// --- (g) determinism: identical enqueue sequences -> byte-identical drain order ---

func TestQueue_DeterministicAcrossIdenticalRuns(t *testing.T) {
	// A mixed sequence exercising every tier and forcing a disk spill,
	// run twice from scratch with fresh state (separate mock transports,
	// separate temp disk roots).
	build := func(t *testing.T) []protocol.Command {
		t.Helper()
		inner := newMockTransport()
		q := newTestQueue(t, inner, Config{T1MemCap: 3})

		inner.SetFull(true)
		seq := []protocol.Command{
			advanceTicksCmd("corr", 1),
			buyCmd("corr", 0),
			setSpeedCmd("corr", 1),
			buyCmd("corr", 1),
			advanceTicksCmd("corr", 2),
			buyCmd("corr", 2),
			setSpeedCmd("corr", 2),
			buyCmd("corr", 3),
			buyCmd("corr", 4), // beyond T1MemCap=3 -- forces a disk spill
			setSpeedCmd("corr", 3),
			advanceTicksCmd("corr", 3),
			buyCmd("corr", 5),
		}
		for i, cmd := range seq {
			if err := q.SendCommand(cmd); err != nil {
				t.Fatalf("SendCommand(%d): %v", i, err)
			}
		}
		inner.SetFull(false)
		if _, err := q.Drain(0); err != nil {
			t.Fatalf("Drain: %v", err)
		}
		return inner.Received()
	}

	runA := build(t)
	runB := build(t)

	if len(runA) != len(runB) {
		t.Fatalf("run lengths differ: A=%d B=%d", len(runA), len(runB))
	}

	// Byte-identical, not just deep-equal: encode every command with the
	// same deterministic codec (codec.go) fixture-hashing relies on and
	// compare raw bytes.
	for i := range runA {
		bytesA, err := protocol.EncodeCommand(runA[i])
		if err != nil {
			t.Fatalf("EncodeCommand(runA[%d]): %v", i, err)
		}
		bytesB, err := protocol.EncodeCommand(runB[i])
		if err != nil {
			t.Fatalf("EncodeCommand(runB[%d]): %v", i, err)
		}
		if string(bytesA) != string(bytesB) {
			t.Fatalf("index %d: runA=%s runB=%s, want byte-identical", i, bytesA, bytesB)
		}
	}
	if !reflect.DeepEqual(kindsOf(runA), kindsOf(runB)) {
		t.Fatalf("Kind sequences differ:\n A: %v\n B: %v", kindsOf(runA), kindsOf(runB))
	}

	// And it must match the semantically-expected order: T0 (3, FIFO),
	// T1 (6, FIFO, spanning the disk-spill boundary), T2 (1, coalesced
	// to the latest SetSpeed value).
	wantKinds := []protocol.Kind{
		protocol.KindAdvanceTicks, protocol.KindAdvanceTicks, protocol.KindAdvanceTicks,
		protocol.KindBuy, protocol.KindBuy, protocol.KindBuy, protocol.KindBuy, protocol.KindBuy, protocol.KindBuy,
		protocol.KindSetSpeed,
	}
	if !reflect.DeepEqual(kindsOf(runA), wantKinds) {
		t.Fatalf("got kind sequence %v, want %v", kindsOf(runA), wantKinds)
	}
	wantTicks := []int64{1, 2, 3}
	for i, want := range wantTicks {
		if n := runA[i].Payload.(protocol.AdvanceTicksPayload).N; n != want {
			t.Fatalf("AdvanceTicks[%d].N = %d, want %d", i, n, want)
		}
	}
	wantBuys := []int{0, 1, 2, 3, 4, 5}
	for i, want := range wantBuys {
		if x := runA[3+i].Payload.(protocol.BuyPayload).Cell.X; x != want {
			t.Fatalf("Buy[%d].Cell.X = %d, want %d", i, x, want)
		}
	}
	if speed := runA[9].Payload.(protocol.SetSpeedPayload).Speed; speed != 3 {
		t.Fatalf("coalesced SetSpeed.Speed = %d, want 3", speed)
	}
}

// --- ancillary: ClassOf + Backpressure sanity, and directory/path shape ----

func TestClassOf(t *testing.T) {
	cases := []struct {
		kind protocol.Kind
		want Class
	}{
		{protocol.KindAdvanceTicks, ClassT0Critical},
		{protocol.KindPause, ClassT0Critical},
		{protocol.KindResume, ClassT0Critical},
		{protocol.KindSetSpeed, ClassT2Coalescible},
		{protocol.KindBuy, ClassT1Batchable},
		{protocol.KindZone, ClassT1Batchable},
		{protocol.KindBuild, ClassT1Batchable},
		{protocol.KindDemolish, ClassT1Batchable},
		{protocol.KindSubscribe, ClassT1Batchable},
		{protocol.KindUnsubscribe, ClassT1Batchable},
		{protocol.KindInspectEntity, ClassT1Batchable},
		{protocol.KindDebug, ClassT1Batchable},
		{protocol.Kind("SomeFutureKind"), ClassT1Batchable}, // default
	}
	for _, tc := range cases {
		if got := ClassOf(tc.kind); got != tc.want {
			t.Errorf("ClassOf(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestQueue_BackpressureSignal(t *testing.T) {
	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{T1MemCap: 1})

	if q.Backpressure() {
		t.Fatalf("Backpressure() = true on an empty queue, want false")
	}

	inner.SetFull(true)
	if err := q.SendCommand(buyCmd("corr", 0)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if q.Backpressure() {
		t.Fatalf("Backpressure() = true with only 1 item in the 1-slot memory window (no spill yet), want false")
	}
	if err := q.SendCommand(buyCmd("corr", 1)); err != nil {
		t.Fatalf("enqueue (forces spill): %v", err)
	}
	if !q.Backpressure() {
		t.Fatalf("Backpressure() = false after a disk spill, want true")
	}
}

// --- concurrency: drainMu closes the destructive-REJECT TOCTOU (2026-08-18) ---
//
// The four tests below reproduce and then prove fixed the P0-class bug a
// Destructive reviewer found in increment 2's original Drain: peek ->
// inner.SendCommand -> commit spanned SEPARATE lock acquisitions per tier
// with NO QueuedTransport-wide guard serialising concurrent Drain calls,
// so two goroutines racing Drain(1) could both peek the SAME
// not-yet-committed command, both send it to inner (double delivery), and
// both commit (double-advancing nextDrainSeq, silently orphaning whatever
// command landed on the skipped-over sequence number). See queue.go's
// drainMu doc comment for the fix.

// TestQueue_ConcurrentDrainExactlyOnce is the exact repro from the
// Destructive report: 500 T1 Buy commands enqueued, N goroutines each
// looping Drain(1) concurrently until the queue is empty. Every command
// must be delivered EXACTLY ONCE, in FIFO order, with Depth().T1 landing
// on exactly 0 (never negative — a negative Depth is the original bug's
// signature of nextDrainSeq overrunning nextSeq).
func TestQueue_ConcurrentDrainExactlyOnce(t *testing.T) {
	const n = 500
	const goroutines = 4

	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	// Force every command to actually land in the queue rather than
	// taking sendOrEnqueue's direct-send fast path (an empty tier's
	// first SendCommand tries inner.SendCommand immediately) — the
	// concurrency this test is stressing is Drain-vs-Drain, which needs
	// a real backlog to race over.
	inner.SetFull(true)
	for i := 0; i < n; i++ {
		if err := q.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("SendCommand(%d): %v", i, err)
		}
	}
	if d := q.Depth(); d.T1 != n {
		t.Fatalf("Depth().T1 = %d, want %d before draining", d.T1, n)
	}
	inner.SetFull(false)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				stats, err := q.Drain(1)
				if err != nil {
					t.Errorf("Drain(1): %v", err)
					return
				}
				if stats.Total() == 0 {
					return // queue observed empty; nothing left for this goroutine
				}
			}
		}()
	}
	wg.Wait()

	if d := q.Depth(); d.T1 != 0 {
		t.Fatalf("Depth().T1 after concurrent drain = %d, want 0 (negative or nonzero means commands were lost or double-committed)", d.T1)
	}

	got := inner.Received()
	if len(got) != n {
		t.Fatalf("received %d commands, want exactly %d (no duplicates, no drops)", len(got), n)
	}
	// FIFO-within-tier must survive concurrent draining: the single
	// winning drainer for each sequence number still delivers commands
	// to inner in nextDrainSeq order, so the OVERALL delivery order must
	// still be exactly 0..n-1 even though multiple goroutines raced to
	// produce it.
	seen := make(map[int]bool, n)
	for i, cmd := range got {
		x := cmd.Payload.(protocol.BuyPayload).Cell.X
		if x != i {
			t.Fatalf("received[%d].Cell.X = %d, want %d (FIFO order violated under concurrent Drain)", i, x, i)
		}
		if seen[x] {
			t.Fatalf("Cell.X = %d delivered more than once (double delivery)", x)
		}
		seen[x] = true
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			t.Fatalf("Cell.X = %d never delivered (silent loss)", i)
		}
	}
}

// TestQueue_ConcurrentDrainWithDiskSpillExactlyOnce is (a) again but with a
// tiny T1 memory window (memCap=4 against 200 commands), forcing most of
// the run onto disk — proving the drainMu fix holds across the
// memory<->disk boundary tierQueue.peekLocked's worked example describes,
// not just the pure-memory case.
func TestQueue_ConcurrentDrainWithDiskSpillExactlyOnce(t *testing.T) {
	const n = 200
	const goroutines = 4

	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{T1MemCap: 4})

	inner.SetFull(true)
	for i := 0; i < n; i++ {
		if err := q.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("SendCommand(%d): %v", i, err)
		}
	}
	if d := q.Depth(); d.T1OnDisk == 0 {
		t.Fatalf("expected some T1 backlog to have spilled to disk, T1OnDisk = 0")
	}
	inner.SetFull(false)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				stats, err := q.Drain(1)
				if err != nil {
					t.Errorf("Drain(1): %v", err)
					return
				}
				if stats.Total() == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	if d := q.Depth(); d.T1 != 0 || d.T1OnDisk != 0 {
		t.Fatalf("Depth() after concurrent drain = T1:%d T1OnDisk:%d, want 0/0", d.T1, d.T1OnDisk)
	}

	got := inner.Received()
	if len(got) != n {
		t.Fatalf("received %d commands, want exactly %d", len(got), n)
	}
	for i, cmd := range got {
		x := cmd.Payload.(protocol.BuyPayload).Cell.X
		if x != i {
			t.Fatalf("received[%d].Cell.X = %d, want %d (order violated across the memory/disk boundary under concurrent Drain)", i, x, i)
		}
	}
}

// TestQueue_ConcurrentEnqueueAndDrainNoLoss runs producers (SendCommand)
// and consumers (Drain) concurrently against each other — proving
// drainMu's placement (Drain-only) does NOT block enqueues: SendCommand
// only ever takes a tierQueue's own mu, never drainMu, so producers keep
// making progress throughout, and every enqueued command still gets
// delivered exactly once.
func TestQueue_ConcurrentEnqueueAndDrainNoLoss(t *testing.T) {
	const perProducer = 250
	const producers = 4
	const drainers = 4
	const total = perProducer * producers

	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	var enqueueWg sync.WaitGroup
	for p := 0; p < producers; p++ {
		enqueueWg.Add(1)
		go func(p int) {
			defer enqueueWg.Done()
			for i := 0; i < perProducer; i++ {
				// Cell.X encodes (producer, index) uniquely so we can
				// verify exactly-once delivery without relying on
				// cross-producer ordering (concurrent producers have no
				// defined relative order against each other — only
				// within-producer... actually within-tier FIFO is by
				// arrival at the tier's own lock, so we verify via a
				// uniqueness/completeness set instead of position).
				x := p*perProducer + i
				if err := q.SendCommand(buyCmd("corr", x)); err != nil {
					t.Errorf("producer %d: SendCommand(%d): %v", p, i, err)
					return
				}
			}
		}(p)
	}

	done := make(chan struct{})
	var drainWg sync.WaitGroup
	for d := 0; d < drainers; d++ {
		drainWg.Add(1)
		go func() {
			defer drainWg.Done()
			for {
				select {
				case <-done:
					// Drain any remainder after producers finish and the
					// stop signal fires, so nothing is left stranded.
					for {
						stats, err := q.Drain(0)
						if err != nil {
							t.Errorf("final Drain: %v", err)
							return
						}
						if stats.Total() == 0 {
							return
						}
					}
				default:
					if _, err := q.Drain(1); err != nil {
						t.Errorf("Drain(1): %v", err)
						return
					}
				}
			}
		}()
	}

	enqueueWg.Wait()
	close(done)
	drainWg.Wait()

	if d := q.Depth(); d.T1 != 0 {
		t.Fatalf("Depth().T1 after concurrent enqueue+drain = %d, want 0", d.T1)
	}

	got := inner.Received()
	if len(got) != total {
		t.Fatalf("received %d commands, want exactly %d (concurrent enqueue+drain must lose nothing)", len(got), total)
	}
	seen := make(map[int]bool, total)
	for _, cmd := range got {
		x := cmd.Payload.(protocol.BuyPayload).Cell.X
		if seen[x] {
			t.Fatalf("Cell.X = %d delivered more than once", x)
		}
		seen[x] = true
	}
	for i := 0; i < total; i++ {
		if !seen[i] {
			t.Fatalf("Cell.X = %d never delivered (silent loss)", i)
		}
	}
}

// TestQueue_ConcurrentDrainBackpressureKeepsPeekedCommandQueued exercises
// the ErrCommandQueueFull backpressure path DURING concurrent draining:
// inner rejects sends for a while (mid-drain), so Drain must return with
// the peeked-but-not-yet-committed command still safely queued rather
// than lost, and once inner stops rejecting, a later Drain (from any
// goroutine) must still deliver every command exactly once.
func TestQueue_ConcurrentDrainBackpressureKeepsPeekedCommandQueued(t *testing.T) {
	const n = 100
	const goroutines = 4

	inner := newMockTransport()
	q := newTestQueue(t, inner, Config{})

	for i := 0; i < n; i++ {
		if err := q.SendCommand(buyCmd("corr", i)); err != nil {
			t.Fatalf("SendCommand(%d): %v", i, err)
		}
	}

	// Flip inner full/not-full on a short cadence WHILE goroutines are
	// mid-drain, forcing Drain to repeatedly hit ErrCommandQueueFull,
	// return with the peeked command still queued, and get re-attempted
	// by a later Drain call.
	stop := make(chan struct{})
	var flipWg sync.WaitGroup
	flipWg.Add(1)
	go func() {
		defer flipWg.Done()
		full := false
		for {
			select {
			case <-stop:
				inner.SetFull(false)
				return
			default:
			}
			full = !full
			inner.SetFull(full)
		}
	}()

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n*10; i++ { // bounded retry budget, not infinite spin
				stats, err := q.Drain(1)
				if err != nil {
					t.Errorf("Drain(1): %v", err)
					return
				}
				_ = stats
				if q.Depth().T1 == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	flipWg.Wait()

	// Final cleanup drain in case the last goroutine exited on the
	// retry-budget bound while inner was still (transiently) full.
	inner.SetFull(false)
	for {
		stats, err := q.Drain(0)
		if err != nil {
			t.Fatalf("cleanup Drain: %v", err)
		}
		if stats.Total() == 0 {
			break
		}
	}

	if d := q.Depth(); d.T1 != 0 {
		t.Fatalf("Depth().T1 = %d, want 0 (a peeked-but-rejected command must never be lost)", d.T1)
	}
	got := inner.Received()
	if len(got) != n {
		t.Fatalf("received %d commands, want exactly %d (backpressure must never cause a drop OR a duplicate)", len(got), n)
	}
	seen := make(map[int]bool, n)
	for _, cmd := range got {
		x := cmd.Payload.(protocol.BuyPayload).Cell.X
		if seen[x] {
			t.Fatalf("Cell.X = %d delivered more than once under backpressure", x)
		}
		seen[x] = true
	}
	for i := 0; i < n; i++ {
		if !seen[i] {
			t.Fatalf("Cell.X = %d never delivered (lost under backpressure)", i)
		}
	}
}

func TestQueue_SegmentPathIsDeterministicAndSorted(t *testing.T) {
	root := t.TempDir()
	p0 := segmentPath(root, 0)
	p1 := segmentPath(root, 1)
	p2 := segmentPath(root, 2)
	if filepath.Dir(p0) != filepath.Dir(p1) || filepath.Dir(p1) != filepath.Dir(p2) {
		t.Fatalf("segment paths do not share a directory: %s / %s / %s", p0, p1, p2)
	}
	if !(p0 < p1 && p1 < p2) {
		t.Fatalf("segment paths are not lexically sorted in sequence order: %s, %s, %s", p0, p1, p2)
	}
}
