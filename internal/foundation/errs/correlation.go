package errs

import (
	"context"
	"crypto/rand"
	"fmt"
	"regexp"
)

// uuidV4Format matches the canonical 8-4-4-4-12 hex UUID string layout
// with the version (4) and variant (8/9/a/b) nibbles fixed, as produced
// by NewCorrelationID.
var uuidV4Format = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// NewCorrelationID mints a new UUIDv4 (RFC 4122) correlation ID using
// crypto/rand, stdlib only — no external UUID library. Every protocol
// command, CLI invocation, and test setup that originates work should
// mint one here and propagate it through the call chain (via
// ContextWithCorrelationID or an explicit parameter) rather than
// generating IDs ad hoc.
//
// crypto/rand.Read failing is effectively unreachable on every platform
// Metropolis targets, but per GR#1 (never fail silently, never panic)
// this still degrades to a deterministic-looking-but-clearly-degraded ID
// built from the current time rather than crashing the caller.
func NewCorrelationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return degradedCorrelationID()
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10xx
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// degradedCorrelationID is the crypto/rand-failure fallback: still
// UUIDv4-shaped (so downstream format validation and logging keep
// working), but derived from clock nanoseconds instead of entropy. Per
// GR#21/M0-ENG §1.1, even this catastrophic-path fallback must not read
// the wall clock directly — it goes through the package's injectable
// now() (see errs.go's SetClock), the same clock engine bootstrap
// overrides with sim-time before the first tick.
func degradedCorrelationID() string {
	n := uint64(now().UnixNano())
	var b [16]byte
	for i := 0; i < 16; i++ {
		b[i] = byte(n >> (uint(i%8) * 8))
		n = n*6364136223846793005 + 1 // cheap LCG spread, no crypto claim
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// IsValidCorrelationID reports whether id is a well-formed UUIDv4
// correlation ID as produced by NewCorrelationID.
func IsValidCorrelationID(id string) bool {
	return uuidV4Format.MatchString(id)
}

// correlationCtxKey is an unexported type so ContextWithCorrelationID's
// value can never collide with a key set by another package.
type correlationCtxKey struct{}

// ContextWithCorrelationID returns a copy of ctx carrying id, retrievable
// with CorrelationIDFromContext. This is the propagation mechanism for
// code that already threads a context.Context (e.g. protocol handlers).
func ContextWithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationCtxKey{}, id)
}

// CorrelationIDFromContext retrieves a correlation ID previously stored
// with ContextWithCorrelationID. ok is false if none was ever set.
func CorrelationIDFromContext(ctx context.Context) (id string, ok bool) {
	id, ok = ctx.Value(correlationCtxKey{}).(string)
	return id, ok
}
