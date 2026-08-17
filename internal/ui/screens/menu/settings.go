package menu

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// SetSettingsSchema installs the data-driven settings schema (MEN-2,
// GR#15): the panel renders one control per schema entry, so adding an
// entry adds a rendered control with no change to RenderSettings. The
// field set itself is content-TBD (see ui.screen.menu.md's "Out of
// scope"); this method only establishes the schema-driven shape.
func (s *Screen) SetSettingsSchema(schema []SettingSpec) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetSettingsSchema"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetSettingsSchema"}); err != nil {
		return
	}
	s.settings = cloneSettingsSchema(schema)
	s.haveSettings = true
}

// SettingsSchema returns the installed settings schema. have is false when
// none has been set (MEN-7's "unavailable" state).
func (s *Screen) SettingsSchema() (schema []SettingSpec, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingsSchema"}); err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingsSchema"}); err != nil {
		return nil, false
	}
	return cloneSettingsSchema(s.settings), s.haveSettings
}

// SetSettingValue records the current value for one settings key (MEN-2's
// write path). Unknown keys are recorded but inert — the schema, not this
// map, decides what renders.
func (s *Screen) SetSettingValue(key, value string) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetSettingValue"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetSettingValue"}); err != nil {
		return
	}
	s.settingValues[key] = value
}

// SettingValues returns a copy of the current settings values (the map a
// caller hands to RenderSettings).
func (s *Screen) SettingValues() map[string]string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingValues"}); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingValues"}); err != nil {
		return nil
	}
	out := make(map[string]string, len(s.settingValues))
	for k, v := range s.settingValues {
		out[k] = v
	}
	return out
}

// SettingsUnavailable returns a non-empty reason when the settings panel
// has no schema installed yet, else "" (MEN-7: "unavailable", not a blank
// panel).
func (s *Screen) SettingsUnavailable() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingsUnavailable"}); err != nil {
		return "unavailable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SettingsUnavailable"}); err != nil {
		return "unavailable"
	}
	if !s.haveSettings {
		return "settings schema not configured"
	}
	return ""
}

// settingValue resolves the value a settings row should render: the
// explicitly-set value, else the spec's Default. A value is "set" only by
// SetSettingValue — an absent map entry falls through to Default.
func settingValue(values map[string]string, spec SettingSpec) string {
	if v, ok := values[spec.Key]; ok {
		return v
	}
	return spec.Default
}

// RenderSettings draws the data-driven settings panel (MEN-2): exactly one
// row per schema entry, derived from the schema and values maps, never a
// hardcoded form. It is a pure function of (buf, rect, schema, values) —
// identical inputs render identically (SF-8) — and adding an entry to
// schema adds a row with no change to this function. A nil buf or a
// degenerate (zero/negative) rect renders nothing.
func RenderSettings(buf *core.Buffer, rect core.Rect, schema []SettingSpec, values map[string]string, style tcell.Style) {
	if buf == nil || rect.W <= 0 || rect.H <= 0 {
		return
	}
	y := rect.Y
	limit := rect.Y + rect.H
	for _, spec := range schema {
		if y >= limit {
			break
		}
		line := settingRowText(spec, settingValue(values, spec))
		drawText(buf, rect, rect.X, y, line, style)
		y++
	}
}

// settingRowText renders one schema entry's control row. The kind selects
// the rendered shape; the value is always shown after the label so the
// row is a control (read/write surface), not a static label.
func settingRowText(spec SettingSpec, value string) string {
	switch spec.Kind {
	case SettingBool:
		return fmt.Sprintf("%-24s [%s]", spec.Label, boolDisplay(value))
	case SettingChoice:
		choices := ""
		for i, c := range spec.Choices {
			if i > 0 {
				choices += "/"
			}
			if c == value {
				choices += "[" + c + "]"
			} else {
				choices += c
			}
		}
		return fmt.Sprintf("%-24s %s", spec.Label, choices)
	case SettingInt, SettingString:
		return fmt.Sprintf("%-24s %s", spec.Label, value)
	default:
		// Unknown kind: still render the label + value rather than
		// dropping the row — the schema is data, and a future kind added
		// to the data must degrade to a visible row, not vanish (GR#15:
		// render from data, never hardcode the form).
		return fmt.Sprintf("%-24s %s", spec.Label, value)
	}
}

// boolDisplay renders a bool value as "on"/"off" (a stable, terminal-safe
// ASCII pair — no glyph dependency).
func boolDisplay(value string) string {
	switch value {
	case "true", "1", "on", "yes":
		return "on"
	default:
		return "off"
	}
}

// cloneSettingsSchema returns a defensive copy of schema (GR#16): a fresh
// slice where each entry's Choices backing array is copied too, so a caller
// mutating its input after SetSettingsSchema — or the returned schema from
// SettingsSchema — cannot corrupt the screen's stored state. Matches
// cloneKeymap/cloneLayoutProfile, the copy convention SEC-066 established
// for this package's reference-type fields.
func cloneSettingsSchema(schema []SettingSpec) []SettingSpec {
	if schema == nil {
		return nil
	}
	out := make([]SettingSpec, len(schema))
	for i, spec := range schema {
		out[i] = spec
		out[i].Choices = append([]string(nil), spec.Choices...)
	}
	return out
}
