package data

import (
	"fmt"
	"strings"
)

// Validator is implemented by every config struct in this package
// (via a pointer receiver) so the generic [Load] function can run
// schema-level validation immediately after JSON decoding, in addition
// to Go's own structural/type decoding.
type Validator interface {
	// Validate checks required-field presence, types (beyond what JSON
	// decoding already enforces), and any documented value ranges. It
	// returns a *FieldError (or an error wrapping one via errors.As)
	// naming the offending field and the rule it violated, never a bare
	// "validation failed" string (AC-10).
	Validate() error
}

// FieldError names the specific field and rule a config file's content
// violated, so a caller (or the registry-sourced error message built
// from it) can say e.g. "consumption.json: field
// waterLitresPerPersonPerDay must be >= 0, got -5" rather than a
// generic failure.
type FieldError struct {
	Field string
	Rule  string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("field %s: %s", e.Field, e.Rule)
}

// fieldErr is a small constructor for readability at call sites.
func fieldErr(field, rule string) *FieldError {
	return &FieldError{Field: field, Rule: rule}
}

// requireVersion is the first check every config struct's Validate
// method must perform (AC-2's "version field" requirement, and the
// dedicated MissingVersion error code so Load can report it
// distinctly from a general schema violation).
func requireVersion(version int) error {
	if version <= 0 {
		return fieldErr("version", "required, must be a positive integer")
	}
	return nil
}

// requireNonNegative checks a numeric coefficient field is >= 0, the
// common range rule for the §17 consumption coefficients (no negative
// water/electricity/gas/waste rate is ever valid).
func requireNonNegative(field string, v float64) error {
	if v < 0 {
		return fieldErr(field, fmt.Sprintf("must be >= 0, got %v", v))
	}
	return nil
}

// requireNonEmptyString checks a string field is non-blank: not just
// non-empty but not whitespace-only either (SEC-057) — a value of "   ", a
// tab, or a newline is trimmed to "" and rejected the same as "", so the
// package's documented "non-empty non-blank" contract is actually enforced
// rather than checked with a bare == "" that whitespace-only strings pass.
func requireNonEmptyString(field, v string) error {
	if strings.TrimSpace(v) == "" {
		return fieldErr(field, "required, must be non-empty")
	}
	return nil
}

// requireLen checks a slice field has exactly n elements (used for
// seasonal.json's 12-monthly-multiplier curves).
func requireLen[T any](field string, s []T, n int) error {
	if len(s) != n {
		return fieldErr(field, fmt.Sprintf("must have exactly %d elements, got %d", n, len(s)))
	}
	return nil
}
