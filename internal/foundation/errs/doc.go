// Package errs is the error registry, correlation-ID, and structured
// logging foundation (Golden Rule #7: every error MUST be created from the
// error registry, no exceptions).
//
// Module key: foundation.errors (see code.json)
// Spec ref:   GR#1; GR#7; M0-ENG §3 (log tail)
package errs
