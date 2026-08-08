package serialize

import (
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

	// DebugTouched is STICKY: once true, it must stay true for the
	// lifetime of the save, including through every subsequent save-over
	// and NDJSON export (§14, M0-ENG §3: "debug-touched saves are flagged
	// forever — balance data hygiene"). Use TouchDebug or
	// MergeDebugTouched to set it rather than assigning the field
	// directly, so the sticky invariant can't be accidentally dropped.
	DebugTouched bool `json:"debugTouched"`

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
		DebugTouched:  false,
		ShardIndex:    nil,
	}
}

// TouchDebug marks the header debug-touched. It only ever sets the flag to
// true — there is deliberately no way to clear it through this package's
// API, enforcing the "once true, forever true" rule at the type level
// rather than by convention.
func (h *Header) TouchDebug() {
	h.DebugTouched = true
}

// MergeDebugTouched ORs incoming into the header's DebugTouched flag. Use
// this when carrying the flag forward from a prior save (e.g. metctl export
// re-emitting a bundle, or a save-over reusing an existing header) so a
// previously debug-touched save can never come back clean.
func (h *Header) MergeDebugTouched(incoming bool) {
	h.DebugTouched = h.DebugTouched || incoming
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
