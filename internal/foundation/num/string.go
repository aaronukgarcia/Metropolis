package num

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// MaxEventIDLength bounds the byte length of a caller-supplied traceable
// event identifier (SEC-203). It is a resource ceiling, not a balance or
// schema number: a traceable id is a short, sim- or caller-minted
// discriminator, so 64 bytes is several orders of magnitude beyond any
// legitimate value while capping the worst-case retained allocation one id
// can reach when it is byte-copied once per case into a conserved ledger —
// the "crisis:"+id-in-loop amplification SEC-203 found. Named constant per
// GR#15 / FEAT-135 AC-6 — never a bare literal.
const MaxEventIDLength = 64

// Reject-form string-boundary error codes, in foundation.num's reserved
// F800-F899 sub-range (coerce.go holds F800-F803; these continue the same
// reject-form family for the string-length boundary). Registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7);
// the errs source-scan test guards against drift (BUG-008).
const (
	// codeStringEmpty: a caller-supplied traceable id is empty.
	codeStringEmpty = "MET-F804"
	// codeStringTooLong: a caller-supplied traceable id exceeds
	// MaxEventIDLength bytes.
	codeStringTooLong = "MET-F805"
)

// SanitizeEventID is the BoundedString boundary-validation helper (SEC-203 /
// FEAT-135): it hard-fails — never trims, never silently truncates — an id
// that is empty (codeStringEmpty) or longer than MaxEventIDLength bytes
// (codeStringTooLong), returning a registry-sourced error (GR#7, never a bare
// fmt.Errorf). It REJECTS rather than sanitizes because an event id is
// identity/structural data: trimming a hostile id into a plausible one hides
// the attack and destroys the per-event traceability the id exists to provide
// (weakness pattern #4). It bounds LENGTH only — the resource concern SEC-203
// is about — and does not police id *content*, which is a separate concern for
// a separate boundary.
//
// It is a pure, deterministic function of its input (GR#21): the value and
// error code returned depend only on id. The correlation ID on a returned
// error is freshly minted per call (GR#1) and is an audit field, not part of
// the result — matching [SafeInt64] and [BoundedFloat].
func SanitizeEventID(id string) (string, error) {
	if id == "" {
		return "", errs.New(codeStringEmpty, errs.NewCorrelationID(), map[string]any{"field": "eventID"})
	}
	if len(id) > MaxEventIDLength {
		return "", errs.New(codeStringTooLong, errs.NewCorrelationID(), map[string]any{
			"length": len(id),
			"max":    MaxEventIDLength,
		})
	}
	return id, nil
}
