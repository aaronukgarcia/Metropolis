package menu

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// LoadLayoutProfile reads a dashboard-layout profile JSON file into a
// LayoutProfile (MEN-4, UI-SPEC §4 "F10 → layouts"). The profile's JSON
// schema and mechanics are ui.dash's (MOD-038) — this screen treats the
// profile as a named, opaque document it loads/selects/saves; Data is
// passed through to the layout editor uninterpreted.
func (s *Screen) LoadLayoutProfile(path string) (*LayoutProfile, error) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "LoadLayoutProfile"}); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	p := &LayoutProfile{Name: raw.Name, Data: data}
	s.SelectLayoutProfile(p)
	return p, nil
}

// SelectLayoutProfile holds a defensive copy of p as this screen's
// selected layout profile (MEN-4's "select" action) — the Data slice is
// copied, so the caller's mutable profile is never aliased into screen
// state (SEC-066/GR#16).
func (s *Screen) SelectLayoutProfile(p *LayoutProfile) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectLayoutProfile"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectLayoutProfile"}); err != nil {
		return
	}
	s.selectedLayout = cloneLayoutProfile(p)
}

// SaveLayoutProfile writes p to path as JSON (MEN-4's "save" action).
// ErrProfileWriteFailed (GR#7) wraps any encode/write failure.
func (s *Screen) SaveLayoutProfile(path string, p *LayoutProfile) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveLayoutProfile"}); err != nil {
		return err
	}
	if p == nil {
		return errs.New(ErrProfileWriteFailed, s.correlationID, map[string]any{
			"path": path, "cause": "no layout profile to save",
		})
	}
	if _, err := json.Marshal(p); err != nil {
		return errs.Wrap(ErrProfileWriteFailed, s.correlationID, err, map[string]any{"path": path, "cause": err.Error()})
	}
	if err := os.WriteFile(path, p.Data, 0o644); err != nil {
		return errs.Wrap(ErrProfileWriteFailed, s.correlationID, err, map[string]any{"path": path, "cause": err.Error()})
	}
	return nil
}

// SelectedLayoutProfile returns the layout profile last loaded/selected.
// have is false when none has been held yet (MEN-7's "unavailable" state).
// The returned profile is a defensive copy (GR#16): a caller mutating it
// does not touch the screen's stored state (SEC-066).
func (s *Screen) SelectedLayoutProfile() (p *LayoutProfile, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedLayoutProfile"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SelectedLayoutProfile"}); err != nil {
		return nil, false
	}
	return cloneLayoutProfile(s.selectedLayout), s.selectedLayout != nil
}

// cloneLayoutProfile returns a defensive copy of p (GR#16): Name plus a
// fresh Data slice, so a caller mutating the returned profile cannot
// corrupt the screen's stored state — matching SelectedKeymap's and
// SaveEntries'/SettingValues' copy convention (SEC-066).
func cloneLayoutProfile(p *LayoutProfile) *LayoutProfile {
	if p == nil {
		return nil
	}
	return &LayoutProfile{Name: p.Name, Data: append([]byte(nil), p.Data...)}
}

// OpenLayoutEditor invokes the wired ui.dash layout editor (MEN-4: this
// screen hosts the F10 → layouts entry point; the editor's mechanics are
// MOD-038's). With no editor wired (the default), it reports
// ErrLayoutEditorUnavailable (MEN-7: "unavailable", not a silent no-op)
// rather than fabricating an editor.
func (s *Screen) OpenLayoutEditor() error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "OpenLayoutEditor"}); err != nil {
		return err
	}
	s.mu.Lock()
	editor := s.layoutEditor
	profile := cloneLayoutProfile(s.selectedLayout)
	s.mu.Unlock()
	if editor == nil {
		return errs.New(ErrLayoutEditorUnavailable, s.correlationID, map[string]any{
			"cause": "no ui.dash layout editor wired",
		})
	}
	return editor(profile)
}
