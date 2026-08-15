package chrome

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// wireSchemaVersion is the only schemaVersion ApplyFiguresPatch accepts for
// a "chrome.topbar" figures patch. Bumping the schema is additive-by-new-
// version, the same convention protocol/commands.go documents — a patch
// carrying an unrecognised version is rejected (last-known-good figures
// stand), never guessed at.
const wireSchemaVersion = 1

// wireFiguresPatch is the wire shape of a "chrome.topbar" view patch — the
// JSON carried in a protocol.Delta.Patch for the figures subscription. It is
// this package's own package-local copy of the schema (mirroring
// ui.screen.map's package-local wire shape), NOT a type imported from the
// engine: Chrome never imports internal/engine (GR#20, AC-13). The Figures
// field is the package's own exported Figures type, JSON-tagged directly —
// no separate wire mirror struct exists (the fields are exported and tagged
// on the owning type, matching FEAT-042 AC-27's "no hidden wire mirror"
// preference).
type wireFiguresPatch struct {
	SchemaVersion int     `json:"schemaVersion"`
	Figures       Figures `json:"figures"`
}

// errUnsupportedSchemaVersion is decodeFiguresPatch's error for a
// "chrome.topbar" patch whose schemaVersion this package doesn't
// understand.
func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("chrome: unsupported chrome.topbar schemaVersion %d (want %d)", got, wireSchemaVersion)
}

// decodeFiguresPatch decodes raw as a "chrome.topbar" figures patch. It
// returns an error for invalid JSON or an unrecognised schemaVersion; the
// zero Figures is returned on error and must not be applied.
func decodeFiguresPatch(raw json.RawMessage) (Figures, error) {
	var p wireFiguresPatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return Figures{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return Figures{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	return p.Figures, nil
}

// ApplyFiguresPatch decodes raw as a "chrome.topbar" figures patch and
// replaces the top-bar figures with it (AC-1: each field updates when a new
// delta arrives). A malformed patch (invalid JSON or an unrecognised
// schemaVersion) is logged via a registry-sourced error (ErrMalformedPatch,
// MET-U953, GR#7) and dropped — the top bar keeps its last-known-good
// figures, and ApplyFiguresPatch never panics (mirrors ui.screen.map
// ApplyPatch's malformed-patch posture).
func (c *Chrome) ApplyFiguresPatch(raw json.RawMessage) {
	if err := c.checkNotCopied(map[string]any{"method": "ApplyFiguresPatch"}); err != nil {
		return
	}

	f, err := decodeFiguresPatch(raw)
	if err != nil {
		c.logMalformed(err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "ApplyFiguresPatch"}); err != nil {
		return
	}
	c.figures = f
}

// logMalformed records a registry-sourced ErrMalformedPatch error (GR#7).
// It only reads c.correlationID (immutable after construction), so it is
// safe to call while mu is held or not. It still carries its own
// checkNotCopied guard (rather than relying on ApplyFiguresPatch having
// already validated the receiver): astgate's ratchet is syntactic, and a
// copied receiver must be rejected here too before the error is recorded.
func (c *Chrome) logMalformed(cause error) {
	if err := c.checkNotCopied(map[string]any{"method": "logMalformed"}); err != nil {
		return
	}
	_ = errs.New(ErrMalformedPatch, c.correlationID, map[string]any{
		"cause": cause.Error(),
	})
}
