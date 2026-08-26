package metricsdash

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DefaultFeedbackInbox mirrors claude-devfeedback-import.js's own
// DEFAULT_INBOX_DIR (data/devfeedback/inbox, gitignored). This package
// writes into the SAME inbox FEAT-065's debug.State.SubmitFeedback
// (internal/engine/debug/feedback.go) already writes to, so the
// already-shipped claude-devfeedback-import.js script (FEAT-065's own
// BOW-write mechanism) picks a submission up on its next run and files
// a real BOW item — AC-8: reuse, not a second writer/importer.
var DefaultFeedbackInbox = filepath.Join("data", "devfeedback", "inbox")

// NoteKind is the BOW item type a logged note becomes (AC-7). A bare
// free-text note with no type hint defaults to NoteBug per the item's
// own "triage-later" intent (AC-7's exact wording).
type NoteKind string

const (
	NoteBug        NoteKind = "bug"
	NoteFinding    NoteKind = "finding"
	NoteAssumption NoteKind = "assumption"
)

// feedbackSourceMkey is the code.json key every note this package logs
// is attributed to. Stamped onto FeedbackRecord.SourceMkey (ASM-477 /
// Bill's ruling) so claude-devfeedback-import.js derives the imported
// BOW item's --codejson/--code-path from this package's own declared
// identity instead of its historical "feat.devmode" default — the fix
// that closes ASM-477 (the misattribution that was previously only
// papered over with a body-text prefix, see below).
const feedbackSourceMkey = "feat.metricsdash"

// LogNote is the low-friction defect/query logging entry point
// (AC-7/AC-9). It does NOT require a live debug.State/game session, a
// paused simulation, or an entity selection (AC-9) — it writes one JSON
// record, in the exact shape internal/engine/debug's FeedbackRecord
// uses, directly to inbox (DefaultFeedbackInbox if empty), so the
// already-shipped claude-devfeedback-import.js script picks it up on
// its next run. now defaults to time.Now if nil (this package is not
// on any tick/determinism path — AC-13 — so, unlike engine code, a
// direct wall-clock read here is fine; the injectable seam exists only
// so tests can assert a specific timestamp deterministically).
//
// ASM-477 RESOLVED (Bill's ruling, previously a KNOWN LIMITATION here):
// claude-devfeedback-import.js now reads FeedbackRecord.SourceMkey (set
// to feedbackSourceMkey below) and FeedbackRecord.Kind (set from kind
// below, BUG-126) per-record, so an imported note is genuinely tagged
// --codejson feat.metricsdash / --code-path pointing at this package,
// and filed as the correct BOW item type (bug/finding/assumption) —
// not implicitly misattributed to feat.devmode as a "bug" regardless of
// what was actually submitted. The body no longer needs the
// "[feat.metricsdash]" human-readable prefix this function used to add
// as a stop-gap, now that the real metadata carries this — see the
// git history on this file if that mitigation text is ever needed again
// for reference.
func LogNote(inbox string, kind NoteKind, body, context string, now func() time.Time) error {
	correlationID := errs.NewCorrelationID()
	if inbox == "" {
		inbox = DefaultFeedbackInbox
	}
	if now == nil {
		now = time.Now
	}

	trimmedBody := strings.TrimSpace(body)
	if trimmedBody == "" {
		return errs.New(codeFeedbackEmptyBody, correlationID, nil)
	}
	if kind == "" {
		kind = NoteBug
	}
	if context == "" {
		context = "unspecified"
	}

	fullBody := fmt.Sprintf("(%s, context: %s) %s", kind, context, trimmedBody)

	rec := debug.FeedbackRecord{
		SchemaVersion: debug.FeedbackSchemaVersion,
		Timestamp:     now().UTC().Format(time.RFC3339Nano),
		Tick:          0, // no live simulation tick outside a running game session — 0 is the documented "not applicable" sentinel for an out-of-band submission
		CorrelationID: correlationID,
		Body:          fullBody,
		DebugTouched:  false, // this package never runs inside a debug-touched save
		SourceMkey:    feedbackSourceMkey,
		Kind:          string(kind), // BUG-126: lets the importer file the correct BOW item type instead of always "bug"
	}

	b, merr := json.MarshalIndent(rec, "", "  ")
	if merr != nil {
		return feedbackWriteFailed(correlationID, "json marshal", merr)
	}
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return feedbackWriteFailed(correlationID, inbox, err)
	}

	name := "metricsdash-" + correlationID + ".json"
	final := filepath.Join(inbox, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return errs.Wrap(codeFeedbackWriteFailed, correlationID, err, map[string]any{"path": tmp})
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return errs.Wrap(codeFeedbackWriteFailed, correlationID, err, map[string]any{"path": final})
	}
	return nil
}
