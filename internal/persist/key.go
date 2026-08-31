package persist

import (
	"crypto/sha256"
	"encoding/hex"
)

// CityKey identifies exactly one city/savegame belonging to one tenant
// (player/account). Phase 1 accepts a single fixed TenantID as a
// placeholder (see the acceptance doc's Open Question 3) — the shape is
// designed so a real per-account TenantID is a later parameter, not a
// later rewrite.
type CityKey struct {
	TenantID string
	CityID   string
}

// encodeSegment derives a deterministic, fixed-length, filesystem-safe
// directory-name segment from an arbitrary caller-supplied identity
// string. It is intentionally NOT reversible from the segment alone —
// callers that need the original string back (ListCities) get it from
// the per-city metadata file, never by decoding the path.
//
// This is the traversal defense: because the on-disk segment is always
// exactly 64 lowercase hex characters (a SHA-256 digest), no value of
// TenantID or CityID — including "../../etc", an absolute path, a NUL
// byte, or an empty string — can ever produce a path separator, a "..",
// or any other character with special meaning to the filesystem. A
// hostile CityID never leaves the directory tree rooted under its own
// hash segment.
func encodeSegment(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
