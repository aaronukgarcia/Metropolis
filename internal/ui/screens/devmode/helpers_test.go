package devmode

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
)

// rfc3339NanoLayout mirrors feedback.go's Timestamp formatting exactly,
// so tests can compute the expected string independently.
const rfc3339NanoLayout = time.RFC3339Nano

// fixedNow is a deterministic stand-in Clock (never time.Now()) used to
// prove FeedbackRecord.Timestamp comes from the injected clock, not the
// wall clock (AC-DM8/AC-DM16).
func fixedNow() time.Time {
	return time.Date(2026, 8, 12, 10, 30, 0, 0, time.UTC)
}

// readFeedbackRecord reads and JSON-decodes a feedback record file
// written by debug.State.SubmitFeedback, failing the test on any error.
func readFeedbackRecord(t *testing.T, path string) debug.FeedbackRecord {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var rec debug.FeedbackRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("Unmarshal feedback record %s: %v (raw: %s)", path, err, b)
	}
	return rec
}
