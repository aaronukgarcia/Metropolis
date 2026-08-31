package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// version.go — FEAT-1972079936 Phase 0 increment 1: the semver'd wire
// protocol version type, decoupled from the engine's own git-describe
// build string (buildinfo.Version / GR#2). See docs/planning/acceptance/
// feat-1972079936-phase0-protocol-versioning.md, AC-1.
//
// # Why a new type, not a bigger string
//
// Before this file, the ENTIRE wire-compatibility decision — both
// Command.Validate's per-command envelope check (envelope.go) and
// wsserver's connect-time handshake (wsserver/server.go) — was a single
// flat string (ProtocolVersion = "1.0") compared with Go's `!=`. That
// conflated two different questions this codebase's own docs/design/
// protocol.md (lines 73-138) already knew were different in spirit
// ("never bump ProtocolVersion for an additive change — add a new Kind
// instead") but had no actual major/minor pair to express formally:
//   - MAJOR: a breaking change (a field removed/repurposed, a Kind's
//     payload reshaped incompatibly, a framing change). A client whose
//     major differs from what a server serves cannot be served on that
//     connection at all (absent a shim — see AC-3/AC-4, increments 2-3).
//   - MINOR: an additive, back-compatible change (a new field an older
//     client simply never sends/reads, a new Kind an older client never
//     issues, a new capability). An older-minor client keeps working
//     unmodified.
//
// # Window design note (increment 2 landed)
//
// Increment 1 only ever served its own current version (window depth
// 0/1). Increment 2 (FEAT-1972079936 Phase 0, AC-3) adds
// DefaultVersionWindowDepth/CurrentVersionWindowDepth and the
// WindowFloorMajor/InVersionWindow helpers below, plus wsserver's
// versionShim registry keyed per-major-offset-from-current (wsserver/
// shim.go) — this file's Compare/Parse/Equal primitives were exactly
// what that window registry needed to order and bound its supported
// versions, so nothing here changed shape, only grew a sibling, per the
// plan this comment originally laid out.
type WireVersion struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// CurrentWireVersion is the wire protocol version this build speaks. It
// is a var, not a const, deliberately: AC-1's mutation test needs to
// temporarily bump only the minor (then only the major) component and
// observe Command.Validate's behaviour diverge accordingly, on the SAME
// running binary, which a compile-time const cannot support. Production
// code must never assign to it outside of this file's own definition —
// tests that mutate it MUST restore the original value via `defer` (see
// version_test.go's convention).
var CurrentWireVersion = WireVersion{Major: 1, Minor: 0}

// ProtocolVersion is the wire version string of this envelope schema, in
// "major.minor" form. It is DERIVED from CurrentWireVersion — not a
// second, independently hand-maintained string literal — so a version
// bump has exactly one place to happen (GR#3 SSOT). Kept as the same
// exported name/shape (a plain string) that every existing caller
// (Command envelopes, the TS mirror's PROTOCOL_VERSION, fixtures) already
// depends on; only its provenance changed, not its type or current value
// ("1.0", unchanged).
var ProtocolVersion = CurrentWireVersion.String()

// String renders v in "major.minor" form, e.g. "1.0", "2.3".
func (v WireVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero reports whether v is the zero value (major 0, minor 0) — used
// to distinguish "field genuinely absent/unset" from "declared version
// 0.0" at the handshake boundary, since JSON omits an absent field the
// same way it represents an explicit zero for a plain (non-pointer)
// struct; callers that need to tell those apart use a *WireVersion field
// instead (see wsserver/server.go's handshakeParams).
func (v WireVersion) IsZero() bool {
	return v.Major == 0 && v.Minor == 0
}

// Equal reports whether v and other name the same major.minor pair.
func (v WireVersion) Equal(other WireVersion) bool {
	return v.Major == other.Major && v.Minor == other.Minor
}

// Compare returns -1 if v < other, 0 if v == other, 1 if v > other,
// ordering lexicographically on (Major, Minor) — a version window
// (increment 2) sorts and bounds its supported set with this.
func (v WireVersion) Compare(other WireVersion) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	return 0
}

// DefaultVersionWindowDepth is how many MAJOR versions back (in addition
// to CurrentWireVersion) the server simultaneously serves via compat
// shims (FEAT-1972079936 Phase 0 increment 2, AC-3). Aaron's ruling
// (2026-08-31, compute-offload architecture doc section 3 point 3): the
// window is 3 MAJOR versions wide — current plus 2 back — so N=2 here.
// This is a PLACEHOLDER count Aaron may retune; it is a plain int
// constant (not date/schedule-based) because deprecation in this project
// is event-driven, not scheduled (Aaron's ruling, same DD).
const DefaultVersionWindowDepth = 2

// CurrentVersionWindowDepth is the window depth this build actually
// enforces. A var, not a const, for the same reason CurrentWireVersion is
// a var: AC-3's mutation tests need to temporarily narrow the window
// (e.g. N=1) and observe the floor move, on the SAME running binary.
// Production code must never assign to it outside of this file's own
// definition; tests that mutate it MUST restore the original via `defer`.
var CurrentVersionWindowDepth = DefaultVersionWindowDepth

// WindowFloorMajor returns the oldest MAJOR version still served
// (inclusive) given current's major and a window depth of n majors back.
// Clamped at 0 — there is no such thing as a negative major, and a
// window deeper than the current major simply means "every major ever
// issued is still in-window."
func WindowFloorMajor(current WireVersion, n int) int {
	floor := current.Major - n
	if floor < 0 {
		floor = 0
	}
	return floor
}

// InVersionWindow reports whether v's MAJOR falls within the server's
// currently-served window: [WindowFloorMajor(current, n), current.Major],
// inclusive on both ends (AC-4: the floor version itself is never
// refused; a version whose major is newer than current is never
// in-window either — this build genuinely does not understand it).
// Minor is deliberately ignored here: a shim (wsserver's versionShim
// registry) is keyed per-MAJOR, not per-minor, so any minor of an
// in-window older major is treated as servable by that major's shim.
func InVersionWindow(v, current WireVersion, n int) bool {
	if v.Major > current.Major {
		return false
	}
	return v.Major >= WindowFloorMajor(current, n)
}

// ErrMalformedWireVersion is returned by ParseWireVersion when s is not a
// well-formed "major.minor" pair of non-negative integers.
var ErrMalformedWireVersion = errors.New("protocol: malformed wire version, want \"major.minor\"")

// ParseWireVersion parses a "major.minor" string (e.g. "1.0", "2.3") into
// a WireVersion. It deliberately does NOT accept the legacy git-describe
// shape ("v0.3.0-153-gABCD[-dirty]", wsserver's normalizeVersion/BAR-4
// territory) — that string identifies a BUILD (engineVersion/
// buildinfo.Version), a different, still-separate concept per this
// file's package doc (Aaron ruling 5: the build string stays
// client-visible but is no longer what gates accept/refuse). Only the
// two-part numeric wire version belongs here.
func ParseWireVersion(s string) (WireVersion, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return WireVersion{}, fmt.Errorf("%w: %q", ErrMalformedWireVersion, s)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return WireVersion{}, fmt.Errorf("%w: %q", ErrMalformedWireVersion, s)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return WireVersion{}, fmt.Errorf("%w: %q", ErrMalformedWireVersion, s)
	}
	return WireVersion{Major: major, Minor: minor}, nil
}

// IntersectCapabilities returns the set intersection of a and b as a
// sorted-by-first-occurrence, de-duplicated slice — never their union,
// never either side's raw set alone (AC-5). Increment 1 introduces this
// helper and the capability-set FIELD in the handshake shape (Overview,
// "capabilities = fine-grained per-feature flags — inc1 introduces the
// capability SET in the handshake shape"); increment 3 is where real
// capability tokens start populating both sides in earnest, but the
// intersection semantics must be correct from day one (AC-2's mutation
// already exercises the empty-intersection case: a client with an empty
// capability set must negotiate an empty set, not the server's full set).
// A nil or empty a or b correctly yields an empty (non-nil) result.
func IntersectCapabilities(a, b []string) []string {
	inA := make(map[string]struct{}, len(a))
	for _, c := range a {
		inA[c] = struct{}{}
	}
	seen := make(map[string]struct{}, len(b))
	out := make([]string, 0, len(b))
	for _, c := range b {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		if _, ok := inA[c]; ok {
			out = append(out, c)
		}
	}
	return out
}
