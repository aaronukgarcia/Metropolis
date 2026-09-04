// portknock.go is FEAT-2326609775 inc1 deliverable 4: the "port knock"
// hardening the design doc's §5.5/§7 item 6 calls for -- a shared-secret
// header checked BEFORE the WebSocket upgrade, plus an Origin allow-list,
// both env-var configured and FAIL-CLOSED once configured / OPEN when
// unconfigured (so a bare `metroserve` invocation with neither env var set
// -- every local-dev and existing-test call site -- is byte-for-byte
// unchanged).
//
// # Why this lives in cmd/metroserve, not internal/protocol/wsserver
//
// wsserver.Server.upgrader.CheckOrigin already exists at the layer the
// design doc points at (server.go's New, "Dev-loopback tool ... origin
// checking is deliberately permissive in v1"). This file deliberately does
// NOT add an Option there: an http.Handler wrapper in front of the
// existing /ws mux entry achieves the identical externally-observable
// security property (a request that fails either check never reaches the
// WebSocket upgrade, exactly the design doc's "before the WS upgrade"
// requirement) with a strictly smaller, lower-risk diff confined to a
// binary that already owns its own HTTP mux (main.go's `mux.Handle("/ws",
// ...)`) -- no change to internal/protocol's public API, no new test
// surface inside a package several other callers (webconsole's
// protocolClient.ts, the wsserver test suite) already depend on. GR#25: no
// new import, no new module edge -- net/http is already imported by
// main.go.
//
// /health is deliberately NOT behind this middleware: Container Apps'
// readiness/liveness probe (and a human curl-ing to check "is it up")
// must never need a secret, and /health leaks nothing sensitive (build
// info + tick counters, no city content).
package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ErrPortKnockRejected (MET-P041) is logged when a connection is refused
// for a missing/mismatched shared-secret header.
const ErrPortKnockRejected = "MET-P041"

// ErrOriginRejected (MET-P042) is logged when a connection is refused
// because its Origin header is not in the configured allow-list.
const ErrOriginRejected = "MET-P042"

// SharedSecretEnv is the environment variable naming the required value of
// the SharedSecretHeader on every /ws connection attempt. Unset (the
// default for every existing invocation, incl. every current test and
// `metro` dev loopback) disables the check entirely -- see
// wrapPortKnock's doc comment for the exact byte-identical-when-unset
// proof obligation.
const SharedSecretEnv = "METROSERVE_SHARED_SECRET"

// SharedSecretHeader is the HTTP request header a client must set to the
// value configured via SharedSecretEnv.
const SharedSecretHeader = "X-Metroserve-Secret"

// AllowedOriginsEnv is the environment variable naming a comma-separated,
// exact-match list of acceptable Origin header values. Unset disables the
// origin check entirely (today's wide-open wsserver.CheckOrigin
// behaviour, preserved for local dev -- see this file's package doc
// comment for why the check lives here rather than in wsserver itself).
const AllowedOriginsEnv = "METROSERVE_ALLOWED_ORIGINS"

// portKnockConfig is read once at process start (readPortKnockConfig,
// called from main/runHosted) so a request-path check never re-reads
// environment variables (cheap, and immune to a concurrent os.Setenv
// mid-run -- config is fixed for the process's life, matching every other
// flag-derived value in this binary).
type portKnockConfig struct {
	// secret is the required SharedSecretHeader value, or "" when
	// SharedSecretEnv is unset (check disabled).
	secret string
	// allowedOrigins is the parsed AllowedOriginsEnv list, or nil when
	// unset (check disabled). Stored as a set for O(1) lookup.
	allowedOrigins map[string]struct{}
}

// readPortKnockConfig reads both env vars once. A present-but-empty
// SharedSecretEnv ("") is treated the SAME as unset (disabled) -- an
// operator clearing the variable via `set METROSERVE_SHARED_SECRET=`
// rather than unsetting it must not silently become "every client refused
// with an empty required header", which no real client could ever satisfy
// and which would look like the server was simply broken (GR#1: no
// confident-wrong failure mode).
func readPortKnockConfig() portKnockConfig {
	cfg := portKnockConfig{secret: os.Getenv(SharedSecretEnv)}
	if raw := os.Getenv(AllowedOriginsEnv); raw != "" {
		set := make(map[string]struct{})
		for _, o := range strings.Split(raw, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				set[o] = struct{}{}
			}
		}
		if len(set) > 0 {
			cfg.allowedOrigins = set
		}
	}
	return cfg
}

// wrapPortKnock wraps next (the /ws handler) with the shared-secret and
// origin checks, run in that order, both BEFORE next.ServeHTTP (and
// therefore before wsserver ever attempts the WebSocket upgrade).
//
// Byte-identical-when-unconfigured proof: with cfg == portKnockConfig{}
// (both env vars absent, the state of every pre-existing call site --
// main_test.go, cityhost_test.go, and every real `metro`/`metroserve` dev
// invocation before this change), checkSharedSecret and checkOrigin below
// each hit their own `if cfg.secret == ""` / `if cfg.allowedOrigins ==
// nil` early-return-true branch unconditionally, so wrapPortKnock's
// handler body reduces to exactly `next.ServeHTTP(w, r)` with two dead
// branches never taken -- provable by attack_portknock_test.go's
// "unconfigured" cases hitting the wrapped /ws exactly as the unwrapped
// handler would.
func wrapPortKnock(cfg portKnockConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !checkSharedSecret(cfg, w, r) {
			return
		}
		if !checkOrigin(cfg, w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SharedSecretQueryParam is the fallback way to supply the shared secret,
// for clients that cannot set a custom header on the request that
// triggers the WebSocket upgrade -- concretely, a BROWSER's WebSocket API
// (RFC 6455 §4.1's handshake is issued by the user agent itself; browser
// JS has no way to attach arbitrary headers to it, unlike a server-side or
// CLI client, which is why this file's own smoke-test tool,
// tools/azure/smoke.mjs, can use either form but the webconsole's browser
// client can only ever use this one). Checked ONLY when SharedSecretHeader
// is absent -- a client that sends the header uses it, never both.
const SharedSecretQueryParam = "secret"

// checkSharedSecret returns true iff the request may proceed. On failure
// it has already written the HTTP refusal and the caller must not call
// next.ServeHTTP.
//
// subtle.ConstantTimeCompare (not ==) so a mismatched-length or
// mismatched-content secret cannot be distinguished by response timing --
// a cheap hardening for a value whose entire job is to be a secret,
// costing nothing on the (overwhelmingly common, once configured) matching
// path.
func checkSharedSecret(cfg portKnockConfig, w http.ResponseWriter, r *http.Request) bool {
	if cfg.secret == "" {
		return true // check disabled -- see readPortKnockConfig's doc comment
	}
	got := r.Header.Get(SharedSecretHeader)
	if got == "" {
		// Fall back to the query parameter -- see SharedSecretQueryParam's
		// doc comment for why a browser client has no other option.
		got = r.URL.Query().Get(SharedSecretQueryParam)
	}
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(cfg.secret)) != 1 {
		reason := "missing header/query secret"
		if got != "" {
			reason = "secret did not match configured value"
		}
		e := errs.New(ErrPortKnockRejected, errs.NewCorrelationID(), map[string]any{
			"path":   r.URL.Path,
			"reason": reason,
		})
		writePortKnockRefusal(w, e)
		return false
	}
	return true
}

// checkOrigin returns true iff the request may proceed. On failure it has
// already written the HTTP refusal and the caller must not call
// next.ServeHTTP.
//
// A request with NO Origin header while an allow-list IS configured is
// FAIL-CLOSED (rejected), not treated as "no browser, therefore exempt":
// a real browser opening a cross-origin WebSocket always sends Origin
// (RFC 6455 §4.1), so an armed allow-list with a missing Origin is either
// a same-origin request (which the operator can add to the list
// explicitly) or a non-browser client the allow-list was never meant to
// exempt -- silently letting it through would make the allow-list
// decorative rather than enforced.
func checkOrigin(cfg portKnockConfig, w http.ResponseWriter, r *http.Request) bool {
	if cfg.allowedOrigins == nil {
		return true // check disabled -- see readPortKnockConfig's doc comment
	}
	origin := r.Header.Get("Origin")
	if _, ok := cfg.allowedOrigins[origin]; !ok {
		e := errs.New(ErrOriginRejected, errs.NewCorrelationID(), map[string]any{
			"path":   r.URL.Path,
			"origin": origin,
		})
		writePortKnockRefusal(w, e)
		return false
	}
	return true
}

// writePortKnockRefusal writes a 403 with a small JSON body carrying e's
// registry code + display message -- refused BEFORE any WebSocket
// upgrade, so this is a plain HTTP response, not wsserver's JSON-RPC
// refusal frame (writeRefusal in server.go, which only applies once a
// connection has already upgraded).
func writePortKnockRefusal(w http.ResponseWriter, e *errs.E) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`, e.Code, e.Display())
}
