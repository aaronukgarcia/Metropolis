package menu

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// LoadKeymapFile reads a keymap profile JSON file and applies it to g via
// ui.keys' validated-load path (MEN-3): keys.ParseKeymap for the schema
// check, then g.ApplyKeymap for the per-entry dispatch check (AC-11/AC-11b
// — an entry binding to an unregistered action is rejected for that entry,
// the rest of the profile still loads). It returns the per-entry rejection
// report ([]keys.KeymapEntryError) verbatim from ui.keys — never swallowed
// and never re-derived — and holds the loaded profile as this screen's
// selected keymap. A read/parse failure returns ErrProfileReadFailed
// (MET-U608, GR#7) wrapping the cause — never a raw *os.PathError or
// keys parse error (SEC-212) — without touching the selected keymap (the
// caller decides fallback).
func (s *Screen) LoadKeymapFile(path string, g *keys.KeyGrammar) ([]keys.KeymapEntryError, error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LoadKeymapFile"}); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errs.Wrap(ErrProfileReadFailed, s.correlationID, err, map[string]any{"path": path})
	}
	km, err := keys.ParseKeymap(raw)
	if err != nil {
		return nil, errs.Wrap(ErrProfileReadFailed, s.correlationID, err, map[string]any{"path": path})
	}
	return s.SelectKeymap(km, g)
}

// SelectKeymap applies km to g via ui.keys' per-entry validated path and
// holds it as this screen's selected keymap (MEN-3's "select" action). It
// returns ui.keys' own rejection report verbatim — this screen is a
// consumer of ui.keys' validation, never a re-implementation of it.
//
// It holds a DEFENSIVE COPY of the VALIDATED profile — the rejected
// bindings omitted — not the caller's raw map (SEC-066/GR#3/GR#16): the
// saved keymap therefore equals the keymap ui.keys actually applied, and
// the caller's mutable map is never aliased into screen state.
//
// A nil km or g is rejected with ErrNilKeymapOrGrammar (GR#7) rather than
// a nil-pointer panic inside keys.ApplyKeymap (SEC-079).
func (s *Screen) SelectKeymap(km *keys.Keymap, g *keys.KeyGrammar) ([]keys.KeymapEntryError, error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectKeymap"}); err != nil {
		return nil, err
	}
	if km == nil {
		return nil, errs.New(ErrNilKeymapOrGrammar, s.correlationID, map[string]any{"argument": "keymap"})
	}
	if g == nil {
		return nil, errs.New(ErrNilKeymapOrGrammar, s.correlationID, map[string]any{"argument": "grammar"})
	}
	rejected := g.ApplyKeymap(km)
	clean := cloneKeymap(km)
	for _, r := range rejected {
		delete(clean.Bindings, r.PhysicalKey)
	}
	s.mu.Lock()
	s.selectedKeymap = clean
	s.mu.Unlock()
	return rejected, nil
}

// SaveKeymapFile writes this screen's selected keymap profile to path as
// JSON (MEN-3's "save" action). It returns ErrProfileWriteFailed (GR#7)
// on a write/encode failure, and a plain error naming the cause when no
// keymap has been loaded/selected yet.
func (s *Screen) SaveKeymapFile(path string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveKeymapFile"}); err != nil {
		return err
	}
	s.mu.Lock()
	km := cloneKeymap(s.selectedKeymap)
	s.mu.Unlock()
	if km == nil {
		return errs.New(ErrProfileWriteFailed, s.correlationID, map[string]any{
			"path": path, "cause": "no keymap profile selected",
		})
	}
	encoded, err := json.MarshalIndent(km, "", "  ")
	if err != nil {
		return errs.Wrap(ErrProfileWriteFailed, s.correlationID, err, map[string]any{"path": path})
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return errs.Wrap(ErrProfileWriteFailed, s.correlationID, err, map[string]any{"path": path})
	}
	return nil
}

// SelectedKeymap returns the keymap profile last loaded/selected (MEN-3).
// have is false when none has been held yet — MEN-7's "unavailable" state,
// rendered as such rather than a blank keymap pane. The returned profile
// is a defensive copy (GR#16): a caller mutating it does not touch the
// screen's stored state (SEC-066).
func (s *Screen) SelectedKeymap() (km *keys.Keymap, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedKeymap"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedKeymap"}); err != nil {
		return nil, false
	}
	return cloneKeymap(s.selectedKeymap), s.selectedKeymap != nil
}

// cloneKeymap returns a defensive copy of km (GR#16): a fresh Bindings map
// so a caller mutating the returned profile cannot corrupt the screen's
// stored state — matching this package's SettingValues/SaveEntries copy
// convention and demo.Screen's copy-returning accessors (SEC-066).
func cloneKeymap(km *keys.Keymap) *keys.Keymap {
	if km == nil {
		return nil
	}
	out := &keys.Keymap{Version: km.Version, Bindings: make(map[string]string, len(km.Bindings))}
	for k, v := range km.Bindings {
		out.Bindings[k] = v
	}
	return out
}
