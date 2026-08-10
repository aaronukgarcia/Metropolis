package serialize

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CurrentFormatVersion is the FormatVersion this build of Metropolis writes
// into new headers. It is a semver string; see the migration rules below.
const CurrentFormatVersion = "1.0.0"

// Header is the small JSON document bundled alongside a save/fixture's
// shards (see [Bundle]) that describes the bundle as a whole. It is written
// as its own file (header.json) rather than folded into a shard so that
// tooling (metctl, the F12 info panel, a future launcher's save-list
// screen) can read it in isolation without touching any shard data.
//
// Header carries no wall-clock timestamps. CreatedAtTick is the simulation
// tick at which the save was taken — a deterministic, replayable value —
// not time.Now(). This keeps header construction pure and keeps bundle
// bytes reproducible from (worldSeed, command log) alone, which is what the
// determinism gate and byte-determinism tests below rely on.
type Header struct {
	// FormatVersion is the semver of the save/fixture format itself (this
	// package's wire format), independent of AppVersion. See "Migration
	// rules" below.
	FormatVersion string `json:"formatVersion"`

	// WorldSeed is the deterministic seed the world was generated/run
	// from (§5.3 determinism rule: hash(worldSeed, entityId, month,
	// purpose) drives every stochastic draw).
	WorldSeed int64 `json:"worldSeed"`

	// CreatedAtTick is the simulation tick the bundle was written at.
	// Deliberately NOT a wall-clock timestamp — see the package-level
	// note above.
	CreatedAtTick int64 `json:"createdAtTick"`

	// GameMonth is the in-world calendar month counter at write time
	// (cold-pass and life-writing are month-indexed; §5.2/§5.3).
	GameMonth int64 `json:"gameMonth"`

	// AppVersion is the build identity (buildinfo.String()'s Version
	// field, or equivalent) that wrote this bundle. Informational only —
	// never used for format-compatibility decisions, that's
	// FormatVersion's job.
	AppVersion string `json:"appVersion"`

	// debugTouched is STICKY: once true, it must stay true for the
	// lifetime of the save, including through every subsequent save-over
	// and NDJSON export (§14, M0-ENG §3: "debug-touched saves are flagged
	// forever — balance data hygiene").
	//
	// SEC-024: this field is deliberately UNEXPORTED. It used to be a
	// plain exported bool with a doc comment asking callers to please use
	// TouchDebug/MergeDebugTouched instead of assigning it directly — that
	// was convention, not enforcement, and a Destructive agent proved a
	// caller holding the same *Header (e.g. via debug.WithHeader) could
	// clear it with a bare `h.DebugTouched = false`, no error, no log,
	// entirely outside debug.State and every SEC-020 guard. Making the
	// field unexported closes that off structurally: TouchDebug and
	// MergeDebugTouched (below) are now the ONLY way any package other
	// than this one can mutate it, and DebugTouched() (the read-only
	// accessor, also below) is the only way to read it. See ASM-076 for
	// the JSON-marshalling approach this required.
	debugTouched bool

	// ShardIndex lists every shard in the bundle, in write order, with
	// the integrity metadata WriteShard produced for each.
	ShardIndex []ShardMeta `json:"shardIndex"`
}

// NewHeader constructs a Header for a fresh save/fixture write. ShardIndex
// starts empty; append to it (or replace it) as shards are written and
// their returned ShardMeta becomes available. FormatVersion is always set
// to CurrentFormatVersion — headers are never hand-versioned.
func NewHeader(worldSeed, createdAtTick, gameMonth int64, appVersion string) Header {
	return Header{
		FormatVersion: CurrentFormatVersion,
		WorldSeed:     worldSeed,
		CreatedAtTick: createdAtTick,
		GameMonth:     gameMonth,
		AppVersion:    appVersion,
		debugTouched:  false,
		ShardIndex:    nil,
	}
}

// DebugTouched reports whether this header is sticky-flagged debug-touched
// (§14, M0-ENG §3). This is the ONLY way to read the flag from outside this
// package — see the field's doc comment (SEC-024) for why it is unexported.
func (h *Header) DebugTouched() bool {
	return h.debugTouched
}

// TouchDebug marks the header debug-touched. It only ever sets the flag to
// true — there is deliberately no way to clear it through this package's
// API, enforcing the "once true, forever true" rule at the type level
// rather than by convention.
func (h *Header) TouchDebug() {
	h.debugTouched = true
}

// MergeDebugTouched ORs incoming into the header's DebugTouched flag. Use
// this when carrying the flag forward from a prior save (e.g. metctl export
// re-emitting a bundle, or a save-over reusing an existing header) so a
// previously debug-touched save can never come back clean.
func (h *Header) MergeDebugTouched(incoming bool) {
	h.debugTouched = h.debugTouched || incoming
}

// headerWire is the JSON wire shape for Header — an exported shadow DTO
// used only at the marshalling boundary (SEC-024/ASM-076). Header.debugTouched
// is unexported so no package outside serialize can assign it directly
// (TouchDebug/MergeDebugTouched are the only mutation path); encoding/json
// cannot see an unexported field, so MarshalJSON/UnmarshalJSON below copy
// through this DTO instead of relying on default struct-tag reflection.
// Field set and json tags are kept in exact 1:1 correspondence with Header
// — see the two conversions below.
type headerWire struct {
	FormatVersion string      `json:"formatVersion"`
	WorldSeed     int64       `json:"worldSeed"`
	CreatedAtTick int64       `json:"createdAtTick"`
	GameMonth     int64       `json:"gameMonth"`
	AppVersion    string      `json:"appVersion"`
	DebugTouched  bool        `json:"debugTouched"`
	ShardIndex    []ShardMeta `json:"shardIndex"`
}

// MarshalJSON implements json.Marshaler. See headerWire's doc comment for
// why this exists (Header.debugTouched is unexported, SEC-024).
func (h Header) MarshalJSON() ([]byte, error) {
	return json.Marshal(headerWire{
		FormatVersion: h.FormatVersion,
		WorldSeed:     h.WorldSeed,
		CreatedAtTick: h.CreatedAtTick,
		GameMonth:     h.GameMonth,
		AppVersion:    h.AppVersion,
		DebugTouched:  h.debugTouched,
		ShardIndex:    h.ShardIndex,
	})
}

// UnmarshalJSON implements json.Unmarshaler. See headerWire's doc comment
// for why this exists (Header.debugTouched is unexported, SEC-024).
// Backward compatible with every existing on-disk header.json: the wire
// shape (field names and json tags) is unchanged from before this fix, so
// a save written by an older build decodes exactly as it always did,
// "debugTouched": true included.
//
// SEC-029: debugTouched is OR-merged into h (via MergeDebugTouched), never
// assigned. A plain assignment here would let json.Unmarshal(data, h) on
// an ALREADY-populated *h (h.debugTouched already true) silently clear the
// sticky flag back to false the moment the decoded JSON said "false" —
// structurally the same real-world defect SEC-024 closed on the bare
// field, just reached through this type's own required json.Unmarshaler
// method instead. Not reachable through this package's own API today
// (ReadHeader and harness/replay's Load both decode into a fresh `var h
// Header`), but UnmarshalJSON is exported by necessity and this type's
// own doc comment anticipates exactly the save-over-reusing-an-existing-
// header pattern MergeDebugTouched exists for — a caller reaching for the
// idiomatic json.Unmarshal(bytes, existingHeaderPtr) must not lose data.
// OR-merging is free on the common fresh-target path: OR and assignment
// agree whenever h starts false.
func (h *Header) UnmarshalJSON(data []byte) error {
	var w headerWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	h.FormatVersion = w.FormatVersion
	h.WorldSeed = w.WorldSeed
	h.CreatedAtTick = w.CreatedAtTick
	h.GameMonth = w.GameMonth
	h.AppVersion = w.AppVersion
	h.MergeDebugTouched(w.DebugTouched)
	h.ShardIndex = w.ShardIndex
	return nil
}

// Semver is a parsed MAJOR.MINOR.PATCH version, as used by FormatVersion.
type Semver struct {
	Major int
	Minor int
	Patch int
}

// ParseSemVer parses a strict "MAJOR.MINOR.PATCH" string (no pre-release or
// build-metadata suffixes — the save format doesn't need them and rejecting
// them keeps the migration-rules comparison unambiguous).
func ParseSemVer(s string) (Semver, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("serialize: invalid format version %q: want MAJOR.MINOR.PATCH", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Semver{}, fmt.Errorf("serialize: invalid format version %q: component %q is not a non-negative integer", s, p)
		}
		nums[i] = n
	}
	return Semver{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// Migration rules (V.2 open item 2):
//
//   - A reader accepts any saved FormatVersion whose MAJOR component
//     equals the reader's supported major (this build's
//     CurrentFormatVersion). MINOR/PATCH differences within the same
//     major are always readable — minor bumps are additive
//     (new-optional-field), patch bumps are clarifications/bugfixes, and
//     neither may change the meaning of an existing field.
//   - A reader REFUSES a saved FormatVersion with a newer MAJOR than its
//     own, with a clear error naming both versions — never a silent
//     best-effort read. This is CheckFormatVersion's job.
//   - A reader encountering an OLDER major is also refused by
//     CheckFormatVersion today: no migrator exists yet. When one does,
//     it is expected to live in this package as an explicit
//     MigrateVX toVY step invoked by the bundle-open path before
//     CheckFormatVersion would otherwise reject it — not as a silent
//     fallback inside CheckFormatVersion itself.
//   - Bumping MAJOR is a deliberately rare, reviewed event: it means an
//     existing field's meaning changed or was removed, and every
//     consumer (engine load path, metctl, fixtures) needs a coordinated
//     update.

// CheckFormatVersion validates a saved FormatVersion against
// CurrentFormatVersion per the migration rules above: same major is
// accepted regardless of minor/patch; any other major is refused with a
// clear error identifying both versions.
func CheckFormatVersion(saved string) error {
	got, err := ParseSemVer(saved)
	if err != nil {
		return err
	}
	want, err := ParseSemVer(CurrentFormatVersion)
	if err != nil {
		// CurrentFormatVersion is a package constant; a parse failure
		// here means the constant itself is malformed, which is a build-
		// time bug, not a runtime data problem.
		return fmt.Errorf("serialize: CurrentFormatVersion %q is invalid: %w", CurrentFormatVersion, err)
	}
	if got.Major != want.Major {
		return fmt.Errorf("serialize: save format version %q (major %d) is not compatible with this build's format major %d (current %q); refusing to load", saved, got.Major, want.Major, CurrentFormatVersion)
	}
	return nil
}
