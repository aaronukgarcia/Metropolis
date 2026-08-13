package debug

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FeedbackSchemaVersion is the on-disk schema version stamped on every
// FeedbackRecord (feat.devmode AC-DM8). Bump this, and document the
// change here, if FeedbackRecord's fields ever change shape — the
// companion import script (claude-devfeedback-import.js) is required to
// check this field before trusting a record's shape.
const FeedbackSchemaVersion = 1

// FeedbackRecord is one in-game feedback submission (feat.devmode
// AC-DM8), written as a single JSON file per submission to the
// configured inbox directory. Every field is populated from this
// package's own already-injected seams (the Clock via nowFunc, the
// wired Header's DebugTouched) — never time.Now(), never a
// caller-fabricated "was this dev-touched" bit — so a record on disk is
// trustworthy provenance for claude-devfeedback-import.js to act on.
type FeedbackRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Timestamp     string `json:"timestamp"` // RFC3339Nano UTC, from the injected Clock — never time.Now()
	Tick          int64  `json:"tick"`
	CorrelationID string `json:"correlationId"`
	Body          string `json:"body"`
	DebugTouched  bool   `json:"debugTouched"`

	// SourceMkey names the code.json module/feature key of the tool that
	// actually produced this record (ASM-477 / Bill's ruling on that item)
	// — e.g. "feat.devmode" for this package's own SubmitFeedback,
	// "feat.metricsdash" for internal/harness/metricsdash's LogNote. It is
	// optional/omitempty and additive: an older record written before this
	// field existed simply has "" here, and
	// claude-devfeedback-import.js's reader falls back to its historical
	// "feat.devmode" default for that case — no FeedbackSchemaVersion bump
	// needed, since this is a backward-compatible shape change (an unknown
	// extra/absent field, not a changed meaning for an existing one).
	SourceMkey string `json:"sourceMkey,omitempty"`

	// Kind names the BOW item type this record should become on import
	// (BUG-126) — "bug", "finding", or "assumption", mirroring
	// metricsdash.NoteKind's values. Optional/omitempty: an older record,
	// or one from a writer that only ever produces bugs (this package's
	// own SubmitFeedback), leaves this "" and
	// claude-devfeedback-import.js falls back to "bug" — its historical,
	// unchanged behavior.
	Kind string `json:"kind,omitempty"`
}

// WithFeedbackInbox wires the directory SubmitFeedback writes one JSON
// file per submission to (AC-DM8). Unset (empty string, the default)
// means SubmitFeedback refuses every request with
// ErrFeedbackInboxNotConfigured — there is no default path this package
// invents on a caller's behalf, since the inbox location is a
// deployment/config concern feat.devmode's caller owns, not this
// package.
func WithFeedbackInbox(dir string) Option {
	return func(s *State) { s.feedbackInbox = dir }
}

// correlationIDFilenameRe matches the safe filename-character subset a
// correlation ID may contain once sanitized (safeFilenameFragment
// below). Anything outside this set is dropped rather than passed
// through to a filesystem path — free-text-adjacent input (a
// correlation ID is caller-supplied, and this package does not control
// every caller) must never reach a path-construction call unsanitized
// (GR#1 / this project's weakness-pattern-#4 discipline, the same
// concern AC-DM10's --desc-file rule exists for on the Node side).
var correlationIDFilenameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeFilenameFragment reduces s to a subset safe for direct use inside
// a single path segment: alphanumerics, dot, underscore, and hyphen
// only, with every run of anything else collapsed to a single
// underscore. It never returns a segment containing a path separator,
// "..", or a leading "." run that could otherwise be read as a hidden
// file or relative traversal token.
func safeFilenameFragment(s string) string {
	cleaned := correlationIDFilenameRe.ReplaceAllString(s, "_")
	for len(cleaned) > 0 && cleaned[0] == '.' {
		cleaned = cleaned[1:]
	}
	if cleaned == "" {
		return "record"
	}
	return cleaned
}

// SubmitFeedback gates, timestamps, and durably writes one feedback
// record to the configured inbox directory (feat.devmode AC-DM8/AC-DM9)
// — a new gated capability alongside InvokeCheat/InspectEntity/
// AllowSpeed8x, never a second gating mechanism of its own.
//
//  1. With debug off, the request is rejected with ErrDebugRequired and
//     NO file is written (AC-DM9) — checked before anything else below.
//  2. With no inbox directory configured, rejected with
//     ErrFeedbackInboxNotConfigured — there is no invented default path.
//  3. The record's Timestamp comes from the injected Clock (nowFunc),
//     never time.Now() (AC-DM16/GR#21 — this package's whole
//     determinism discipline: grep -rn "time.Now" internal/engine/debug
//     excluding _test.go must stay empty).
//  4. DebugTouched reflects the wired Header's own DebugTouched() bit at
//     submission time (false if no header is wired), proving the
//     environment was legitimately dev-touched rather than forged.
//  5. The record is marshalled and written via a temp-file-then-rename
//     inside inbox — never appended to a shared file, never overwritten
//     — so concurrent submissions cannot corrupt or interleave, and a
//     reader (claude-devfeedback-import.js) polling the directory never
//     observes a partially written file. The filename is derived from
//     correlationID (sanitized, see safeFilenameFragment) PLUS a
//     per-call unique nonce minted via errs.NewCorrelationID() — never
//     from correlationID alone. A caller-supplied correlationID is not
//     trusted to be globally unique (two goroutines can legitimately
//     race with the same one), so filename collision-freedom is
//     manufactured by this method on every call rather than assumed
//     from the input: two concurrent submissions that happen to share a
//     correlationID still land as two distinct files, and neither
//     submission's body is ever silently discarded.
//
// sourceMkey (ASM-477 / Bill's ruling) is stamped onto the written
// record's SourceMkey field verbatim — the caller (Console.SubmitFeedback
// in internal/ui/screens/devmode/console.go) passes the literal
// "feat.devmode" explicitly rather than this package inventing an
// implicit default, so claude-devfeedback-import.js can attribute the
// resulting BOW item correctly without guessing.
func (s *State) SubmitFeedback(correlationID string, tick int64, body string, sourceMkey string) error {
	if err := s.requireOn(correlationID, "feedback-submit"); err != nil {
		return err
	}

	// SEC-020 wave 2: identity-checked again immediately before this
	// method's own s.mu lock site, mirroring InspectEntity's pattern —
	// requireOn's own internal checks do not cover every subsequent lock
	// site on a call (weakness pattern #3).
	if err := s.checkNotCopied(correlationID, map[string]any{"capability": "feedback-submit"}); err != nil {
		return err
	}
	s.mu.Lock()
	if err := s.checkNotCopied(correlationID, map[string]any{"capability": "feedback-submit"}); err != nil {
		s.mu.Unlock()
		return err
	}
	inbox := s.feedbackInbox
	h := s.header
	s.mu.Unlock()

	if inbox == "" {
		return errs.New(ErrFeedbackInboxNotConfigured, correlationID, nil)
	}

	touched := false
	if h != nil {
		touched = h.DebugTouched()
	}

	rec := FeedbackRecord{
		SchemaVersion: FeedbackSchemaVersion,
		Timestamp:     s.nowFunc().UTC().Format(time.RFC3339Nano),
		Tick:          tick,
		CorrelationID: correlationID,
		Body:          body,
		DebugTouched:  touched,
		SourceMkey:    sourceMkey,
	}

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return errs.Wrap(ErrFeedbackMarshalFailed, correlationID, err, nil)
	}

	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return errs.Wrap(ErrFeedbackInboxUnwritable, correlationID, err, map[string]any{"inbox": inbox})
	}

	// A per-call unique nonce (minted the same way every correlation ID
	// in this codebase is minted, errs.NewCorrelationID — crypto/rand
	// under the hood) is appended so the filename never depends solely
	// on caller-supplied correlationID uniqueness: two concurrent calls
	// sharing a correlationID still get two distinct files instead of
	// one silently clobbering the other (the finding this fixes).
	nonce := errs.NewCorrelationID()
	name := safeFilenameFragment(correlationID) + "-" + safeFilenameFragment(nonce) + ".json"
	final := filepath.Join(inbox, name)
	tmp := final + ".tmp"

	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return errs.Wrap(ErrFeedbackWriteFailed, correlationID, err, map[string]any{"path": tmp})
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return errs.Wrap(ErrFeedbackWriteFailed, correlationID, err, map[string]any{"path": final})
	}
	return nil
}
