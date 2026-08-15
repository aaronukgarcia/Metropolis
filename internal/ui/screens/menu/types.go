package menu

// SaveEntry is one save slot in the F10 browser (MEN-1). Every field is
// sourced from int.serializer's Header (via serialize.ReadHeader) plus the
// bundle directory's base name — never from an internal/engine type and
// never from the wall clock. See doc.go's SF-2 table for the exact source
// field per entry field.
type SaveEntry struct {
	// Name is the save slot's display name — the bundle directory's base
	// name on disk.
	Name string
	// Path is the bundle directory path, used by Load/Delete to address
	// the slot.
	Path string
	// CreatedAtTick is the simulation tick the save was taken at (the
	// "timestamp" MEN-1 lists — a deterministic sim tick, not wall-clock,
	// mirroring serialize.Header's own design; ASM-525).
	CreatedAtTick int64
	// GameMonth is the in-world calendar month at save time (the
	// "sim-date" MEN-1 lists).
	GameMonth int64
	// WorldSeed is the deterministic seed the world was generated/run
	// from.
	WorldSeed int64
	// AppVersion is the build identity that wrote the bundle.
	AppVersion string
	// DebugTouched is true when the save has ever been debug-touched
	// (§14, sticky — reported, never cleared, by serialize).
	DebugTouched bool
	// Summary is a compact, header-derived one-line summary (seed, month,
	// tick, debug flag) — see summaryOf in saves.go. It is derived from
	// the Header fields above, never invented or read from anywhere else.
	Summary string
}

// Session is the running game's current-session summary (the one
// engine-derived figure this screen renders), sourced from the f10.session
// view (wire.go). The zero value means "no session data applied yet."
type Session struct {
	WorldSeed int64
	Tick      int64
	GameMonth int64
	Paused    bool
	Speed     int
}

// SettingKind names the control a settings-schema entry renders as (MEN-2).
// The panel is data-driven: RenderSettings draws one control per schema
// entry, so adding an entry adds a control with no code change. The field
// set is deliberately minimal for Sprint 8 — the actual settings the game
// exposes arrive as their owning subsystems land (see ui.screen.menu.md's
// "Out of scope").
type SettingKind string

const (
	// SettingBool renders a boolean toggle.
	SettingBool SettingKind = "bool"
	// SettingInt renders an integer numeric field.
	SettingInt SettingKind = "int"
	// SettingString renders a free-text field.
	SettingString SettingKind = "string"
	// SettingChoice renders a fixed enumeration, one row per choice.
	SettingChoice SettingKind = "choice"
)

// SettingSpec is one entry of the data-driven settings schema (MEN-2,
// GR#15): the panel's fields derive from a schema, not a hardcoded form.
type SettingSpec struct {
	// Key is the stable identifier a consumer reads/writes the value by
	// (e.g. "audio.enabled").
	Key string
	// Label is the human-readable row label.
	Label string
	// Kind selects the rendered control shape (see SettingKind).
	Kind SettingKind
	// Choices is the fixed value list for SettingChoice entries (the
	// rendered rows), ignored for other kinds.
	Choices []string
	// Default is the value shown when no explicit value has been set.
	Default string
}

// LayoutProfile is one dashboard-layout profile (MEN-4, UI-SPEC §4 "F10 →
// layouts"). The layout's own mechanics and JSON schema belong to ui.dash
// (MOD-038); this screen treats a layout profile as a named, opaque JSON
// document it loads/selects/saves — Data is interpreted only by MOD-038.
type LayoutProfile struct {
	// Name is the profile's display name.
	Name string
	// Data is the profile's raw JSON payload, opaque to this screen.
	Data []byte
}

// NewGameRequest is the new-game setup form's input (MEN-5, ASM-255): the
// field set is deliberately limited to the spec's own parenthetical
// (seed + debug flag), not expanded.
type NewGameRequest struct {
	// Seed is the world seed string the player entered (kept as the
	// player typed it — the engine owns parsing/validation).
	Seed string
	// Debug requests a debug-touched new game (§14).
	Debug bool
}

// DrillTarget is one (widget, source) registration pair this screen
// supplies to ui.dash's (MOD-038) drill-through graph, per SF-5. This
// package only produces the pair list — registration, navigation, and
// dead-end detection are MOD-038's job (consumed, not reimplemented).
type DrillTarget struct {
	// WidgetID identifies the on-screen figure (stable across renders).
	WidgetID string
	// Target is the drill-through destination MOD-038's registration API
	// expects (an opaque string in this screen's scope).
	Target string
}
