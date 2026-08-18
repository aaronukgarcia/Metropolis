package integration

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the second half of INCREMENT 5 (metrics.go's doc comment
// has the full increment story): a tiny, stdlib-only, debug-gated HTTP
// surface serving metrics.go's Registry snapshot as JSON plus a
// self-contained dashboard page. It mirrors tools/bow-server.js's
// zero-dependency local-web-UI pattern (proposal §2/§7) — one static page
// (dashboard.html, go:embed'd below) that polls a JSON endpoint — but
// implemented in Go, stdlib net/http only, no Node/npm dependency, so the
// composition root can start it in-process alongside the engine itself.
//
// # Debug gate (fail-closed by design)
//
// ServeMetrics refuses to start unless BOTH a non-nil Gate is supplied
// AND that Gate approves — mirroring engine/core's Speed8xGate
// (WithSpeed8xGate/ErrSpeed8xGateNotConfigured's default-deny) and
// engine/debug.State's IsOn()-gated capability set: this package never
// assumes "unconfigured" means "allowed". The composition root is
// expected to wire Gate to feat.devmode's debug.State.IsOn-derived check
// (e.g. a closure that calls state.requireOn or equivalent) — NOT built
// in this increment, which only defines the seam. A nil Gate is treated
// exactly the same as a Gate that returns an error: ServeMetrics never
// starts listening.
//
// # Localhost-only bind
//
// ensureLocalAddr rejects any address whose host is not one of
// 127.0.0.1/localhost/::1, or that has no host at all (a bare ":port",
// which net.Listen would otherwise bind to every interface) — proposal
// §2's "local web dashboard" must never be reachable off-box.
type Gate func(correlationID string) error

// DefaultMetricsAddr is the address ServeMetrics binds when Addr is
// empty — a different port than tools/bow-server.js's BOW UI (8765) so
// both can run side by side during development.
const DefaultMetricsAddr = "127.0.0.1:8766"

// NewMetricsHandler builds the http.Handler ServeMetrics installs: GET
// /metrics.json serves reg.Snapshot() as indented JSON; GET / serves the
// embedded dashboard.html. reg may be nil (the handler still starts, but
// /metrics.json reports 503 rather than panicking) — ServeMetrics itself
// never passes a nil Registry, but NewMetricsHandler is exported
// separately so metrics_test.go (and any future caller) can exercise the
// handler directly via httptest without a real network listener.
func NewMetricsHandler(reg *Registry) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/metrics.json", func(w http.ResponseWriter, r *http.Request) {
		if reg == nil {
			http.Error(w, "metrics registry not configured", http.StatusServiceUnavailable)
			return
		}
		snap := reg.Snapshot()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		// Encode errors here would only ever be a broken client
		// connection (write failure) — Snapshot's own construction
		// cannot fail (plain structs, no custom MarshalJSON that can
		// error) — so there is nothing meaningful to recover from or
		// report back to this same response.
		_ = enc.Encode(snap)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	})

	return mux
}

//go:embed dashboard.html
var dashboardHTML []byte

// Server is the handle ServeMetrics returns: a running localhost-only
// HTTP listener a caller can later Close.
type Server struct {
	httpSrv *http.Server
	ln      net.Listener
}

// Addr reports the address Server is actually listening on (useful when
// ServeMetrics was given a ":0" ephemeral port, e.g. in tests).
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close shuts the server down, releasing its listener.
func (s *Server) Close() error { return s.httpSrv.Close() }

// ServeMetrics starts the localhost-only monitoring dashboard: GET
// /metrics.json (reg's live snapshot) and GET / (the embedded dashboard
// page). It refuses to start — returning a registry-sourced error,
// ErrMetricsServerNotEnabled — unless gate is non-nil AND gate approves
// (this file's header comment; the composition root wires gate under
// feat.devmode only, never unconditionally). addr == "" falls back to
// DefaultMetricsAddr; any addr whose host is not localhost is rejected
// (ErrMetricsAddrNotLocal) before net.Listen is ever attempted.
func ServeMetrics(addr string, gate Gate, reg *Registry) (*Server, error) {
	correlationID := errs.NewCorrelationID()

	if gate == nil {
		return nil, errs.New(ErrMetricsServerNotEnabled, correlationID, map[string]any{
			"reason": "no gate configured (default off)",
		})
	}
	if err := gate(correlationID); err != nil {
		return nil, err
	}
	if reg == nil {
		return nil, errs.New(ErrMetricsServerNotEnabled, correlationID, map[string]any{
			"reason": "no registry configured",
		})
	}

	if addr == "" {
		addr = DefaultMetricsAddr
	}
	if err := ensureLocalAddr(correlationID, addr); err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, errs.Wrap(ErrMetricsListenFailed, correlationID, err, map[string]any{"addr": addr})
	}

	httpSrv := &http.Server{Handler: NewMetricsHandler(reg)}
	go func() {
		// Serve returns http.ErrServerClosed on a normal Close() — that
		// is not a runtime failure this goroutine has anywhere useful to
		// report; a genuinely unexpected Serve error has no caller left
		// to hand it to once ServeMetrics has already returned
		// successfully, so it is deliberately swallowed here, matching
		// tools/bow-server.js's own fire-and-forget server.listen
		// pattern for this local-only, opt-in debug surface.
		_ = httpSrv.Serve(ln)
	}()

	return &Server{httpSrv: httpSrv, ln: ln}, nil
}

// ensureLocalAddr rejects any address that is not explicitly
// localhost-only (127.0.0.1, localhost, or ::1) — including a bare
// ":port" with no host, which net.Listen treats as "every interface".
func ensureLocalAddr(correlationID, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return errs.Wrap(ErrMetricsAddrNotLocal, correlationID, err, map[string]any{"addr": addr})
	}
	host = strings.TrimSpace(host)
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return errs.New(ErrMetricsAddrNotLocal, correlationID, map[string]any{"addr": addr, "host": host})
	}
}
