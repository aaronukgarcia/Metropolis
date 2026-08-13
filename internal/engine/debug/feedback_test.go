package debug

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSubmitFeedback_ConcurrentSameCorrelationID_NoDataLoss is the
// regression test for the Destructive finding that rejected FEAT-065's
// first pass: two goroutines calling SubmitFeedback concurrently with
// the SAME correlationID but different bodies used to collide on one
// filename (derived solely from safeFilenameFragment(correlationID)),
// so exactly one submission survived on disk and the other's body was
// silently discarded even though BOTH calls returned nil.
//
// The fix appends a per-call unique nonce (errs.NewCorrelationID()) to
// the filename, so filename collision-freedom no longer depends on the
// caller supplying a globally-unique correlationID. This test proves
// both submissions now survive as two distinct, fully-readable files —
// run with -race to also confirm no data race on the shared inbox path.
func TestSubmitFeedback_ConcurrentSameCorrelationID_NoDataLoss(t *testing.T) {
	dir := t.TempDir()

	h := newTestHeader()
	s := NewState(WithHeader(h), WithFeedbackInbox(dir))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	const sharedCorrelationID = "collision-corr-id"
	const bodyA = "body-from-goroutine-A"
	const bodyB = "body-from-goroutine-B"

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errCh <- s.SubmitFeedback(sharedCorrelationID, 1, bodyA, "feat.devmode")
	}()
	go func() {
		defer wg.Done()
		errCh <- s.SubmitFeedback(sharedCorrelationID, 2, bodyB, "feat.devmode")
	}()
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("SubmitFeedback with colliding correlationID: got error %v, want nil (both calls must report success under the fix)", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("inbox has %d file(s) after two colliding-correlationID submissions, want 2 (got %v) — this is the exact silent-data-loss shape the Destructive finding reported", len(entries), names)
	}

	gotBodies := make(map[string]bool, 2)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			t.Fatalf("unexpected non-JSON file in inbox: %s", e.Name())
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", e.Name(), err)
		}
		var rec FeedbackRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			t.Fatalf("Unmarshal(%s): %v (file must be well-formed JSON, not partially written)", e.Name(), err)
		}
		if rec.CorrelationID != sharedCorrelationID {
			t.Fatalf("record %s CorrelationID = %q, want %q", e.Name(), rec.CorrelationID, sharedCorrelationID)
		}
		gotBodies[rec.Body] = true
	}

	if !gotBodies[bodyA] || !gotBodies[bodyB] {
		t.Fatalf("recovered bodies = %v, want both %q and %q present (one submission's body went missing)", gotBodies, bodyA, bodyB)
	}
}

// TestSubmitFeedback_SourceMkeyRoundTrips is the regression test for
// ASM-477's fix: SubmitFeedback's new sourceMkey parameter must be
// stamped verbatim onto the written record's SourceMkey field, and must
// survive a JSON round-trip unchanged — this is exactly the field
// claude-devfeedback-import.js now reads to derive per-record
// --codejson/--code-path attribution instead of hardcoding
// "feat.devmode" for every record regardless of origin.
func TestSubmitFeedback_SourceMkeyRoundTrips(t *testing.T) {
	dir := t.TempDir()

	h := newTestHeader()
	s := NewState(WithHeader(h), WithFeedbackInbox(dir))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	const wantSourceMkey = "feat.devmode"
	if err := s.SubmitFeedback("corr-sourcemkey", 7, "body text", wantSourceMkey); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox has %d file(s), want 1", len(entries))
	}

	b, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var rec FeedbackRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.SourceMkey != wantSourceMkey {
		t.Fatalf("rec.SourceMkey = %q, want %q (the whole point of ASM-477's fix is this field surviving to disk)", rec.SourceMkey, wantSourceMkey)
	}
}
