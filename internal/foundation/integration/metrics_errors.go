package integration

// Registry error codes for foundation.integration's monitoring surface
// (FEAT-191 increment 5: the Metrics/Registry types, metrics.go, and the
// local web dashboard, metrics_server.go). Range: F900-F919 (see
// queue_errors.go's header comment for the full claim story);
// increment 2 used F900-F904, resilience.go (increment 3, part 1) used
// F905-F907, recovery.go/wal.go (increment 3, part 2) used F908-F914 —
// this file claims the last five codes in the reserved range, F915-F919.
// Checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-F9" internal/ cmd/` before claiming (BUG-008's lesson) —
// F915-F919 were unclaimed.
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrMetricsRegistryCopied: a *Registry method was called on a
	// struct copy of the value NewRegistry returned — SEC-020-class
	// guard, mirrors QueuedTransport.checkNotCopied/Connection.
	// checkNotCopied exactly (metrics.go's Registry doc comment).
	ErrMetricsRegistryCopied = "MET-F915"

	// ErrMetricsEntryCopied: an *IntegrationMetrics method was called on
	// a struct copy of the value Registry.Register returned —
	// SEC-020-class guard, same shape as ErrMetricsRegistryCopied one
	// level down.
	ErrMetricsEntryCopied = "MET-F916"

	// ErrMetricsServerNotEnabled: ServeMetrics (metrics_server.go) was
	// called with no Gate configured, or the configured Gate denied the
	// request. Debug-gated, fail-closed by design (proposal §2's
	// "debug-mode gated"; mirrors engine/core's ErrSpeed8xGateNotConfigured
	// default-deny convention and debug.State's IsOn()-gated capability
	// set) — the monitoring HTTP surface must never start listening on a
	// build/run where the composition root has not explicitly wired
	// feat.devmode's debug gate to it.
	ErrMetricsServerNotEnabled = "MET-F917"

	// ErrMetricsAddrNotLocal: ServeMetrics was asked to bind an address
	// that is not localhost-only (proposal §2's "local web dashboard" —
	// this surface must never be reachable from off-box). Any host other
	// than 127.0.0.1/localhost/::1, or an address with no host at all
	// (which net.Listen would otherwise bind to every interface), is
	// rejected before net.Listen is ever called.
	ErrMetricsAddrNotLocal = "MET-F918"

	// ErrMetricsListenFailed: net.Listen failed for the (already
	// validated localhost-only) address ServeMetrics was configured
	// with — port already in use, permission denied, or another
	// filesystem/socket error.
	ErrMetricsListenFailed = "MET-F919"
)
