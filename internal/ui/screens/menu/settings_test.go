package menu

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// renderSettingsLines renders the settings panel for the given schema and
// returns its non-empty rows — the "controls" MEN-2 counts.
func renderSettingsLines(schema []SettingSpec) []string {
	buf := core.NewBuffer(80, 16)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 16}
	RenderSettings(buf, rect, schema, nil, tcell.StyleDefault)
	lines := renderedText(buf, rect)
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestSettings_DataDriven_AddingSchemaEntryAddsControl is MEN-2's binding
// check: the panel renders from a data-driven schema, so adding a schema
// entry adds a corresponding rendered control with NO change to the panel
// code (RenderSettings is the same function before and after). GR#15:
// the panel's fields derive from data, not a hardcoded form.
func TestSettings_DataDriven_AddingSchemaEntryAddsControl(t *testing.T) {
	base := []SettingSpec{
		{Key: "audio.enabled", Label: "Audio", Kind: SettingBool, Default: "true"},
		{Key: "display.scale", Label: "Display scale", Kind: SettingInt, Default: "1"},
	}

	before := renderSettingsLines(base)
	if len(before) != len(base) {
		t.Fatalf("rendered %d controls for a %d-entry schema, want %d", len(before), len(base), len(base))
	}

	// Add a third entry to the DATA only — no code change to the panel.
	extended := append(append([]SettingSpec{}, base...), SettingSpec{
		Key: "accessibility.largeGlyphs", Label: "Large glyphs", Kind: SettingBool, Default: "false",
	})

	after := renderSettingsLines(extended)
	if len(after) != len(extended) {
		t.Fatalf("rendered %d controls for a %d-entry schema, want %d (adding a schema entry must add a control, no panel code change)", len(after), len(extended), len(extended))
	}
}

// TestSettings_ChoiceRendersSelectedValue shows a SettingChoice control
// marks its current value (the data drives what is highlighted), and a
// bool renders on/off — the data-driven shape, not a hardcoded form.
func TestSettings_ChoiceRendersSelectedValue(t *testing.T) {
	schema := []SettingSpec{
		{Key: "video.mode", Label: "Video mode", Kind: SettingChoice, Choices: []string{"low", "high"}, Default: "high"},
		{Key: "audio.enabled", Label: "Audio", Kind: SettingBool, Default: "false"},
	}
	values := map[string]string{"video.mode": "low", "audio.enabled": "true"}

	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	RenderSettings(buf, rect, schema, values, tcell.StyleDefault)
	lines := renderedText(buf, rect)

	if !strings.Contains(lines[0], "[low]") || strings.Contains(lines[0], "[high]") {
		t.Errorf("video.mode row %q: want [low] selected (data-driven), not [high]", lines[0])
	}
	if !strings.Contains(lines[1], "on") {
		t.Errorf("audio.enabled row %q: want 'on' for value 'true'", lines[1])
	}
}

// TestSettings_UnavailableWhenNoSchema is MEN-7's settings half: with no
// schema installed, the screen reports "unavailable" rather than a blank
// panel.
func TestSettings_UnavailableWhenNoSchema(t *testing.T) {
	s := New("corr-no-settings")
	if got := s.SettingsUnavailable(); got == "" {
		t.Fatalf("SettingsUnavailable() = %q, want a non-empty reason before a schema is set", got)
	}
	if _, have := s.SettingsSchema(); have {
		t.Fatalf("SettingsSchema() have = true before SetSettingsSchema")
	}
}
