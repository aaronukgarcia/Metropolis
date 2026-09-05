package save

// SaveKind labels which of the three save-trigger paths (US-1/US-2/US-3)
// produced a given bundle (AC-6). Stored in this package's own [Meta]
// sidecar file, never by widening serialize.Header's wire shape (see
// doc.go's "Save-kind and provenance metadata" section) and never
// inferred from directory-name string-matching alone — every bundle
// this package writes carries a typed SaveKind value in its Meta.
type SaveKind string

const (
	// KindManual is a player-triggered save (US-1) — never pruned.
	KindManual SaveKind = "manual"

	// KindAutosave is a rolling yearly autosave (US-2) — subject to the
	// 10-slot retention rotation (AC-4).
	KindAutosave SaveKind = "autosave"

	// KindMilestone is a §4 population-tier-crossing save (US-3) — never
	// pruned by the autosave rotation.
	KindMilestone SaveKind = "milestone"
)

// Meta is feat.saveux's own small JSON sidecar file (save-meta.json),
// written alongside int.serializer's header.json inside every bundle
// this package produces. It carries the provenance metadata (AC-6) that
// is this package's own concept, not int.serializer's: which of the
// three save-kinds produced the bundle, a human-readable display name,
// and — for a milestone save — which §4 tier was crossed.
type Meta struct {
	// SaveKind is which trigger path produced this bundle.
	SaveKind SaveKind `json:"saveKind"`

	// DisplayName is the name a future F10 menu shows for this bundle
	// (US-6) — the manual save's player-given name, or a generated
	// label for autosave/milestone bundles.
	DisplayName string `json:"displayName"`

	// MilestoneTierNumber is the §4 tier number (1-13) this bundle
	// records crossing. Zero (its zero value) for non-milestone saves —
	// tier numbers are 1-indexed per the master doc's table, so zero is
	// never a valid tier and unambiguously means "not a milestone
	// save".
	MilestoneTierNumber int `json:"milestoneTierNumber,omitempty"`

	// MilestoneTierName is the §4 tier's display name (e.g. "Hamlet"),
	// empty for non-milestone saves.
	MilestoneTierName string `json:"milestoneTierName,omitempty"`

	// GameMode is FEAT-143 (mkey feat.gameinit)'s locked initialization
	// mode ("real" or "unlimited") declared on this bundle (AC-4). Every
	// caller of SaveManual/Autosave/Milestone is expected to set
	// [Context.GameMode] to the session's own gameinit.GameInit.Mode()
	// string before saving, so a session's mode is recoverable from any
	// bundle. Empty for a bundle written before FEAT-143 landed (a
	// pre-mode-bearing save) -- see [WithExpectedGameMode]'s doc for how
	// Load treats an absent value as a fail-closed mismatch rather than
	// a silent default to "unlimited" (AC-5).
	GameMode string `json:"gameMode,omitempty"`
}
