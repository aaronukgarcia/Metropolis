package main

// attack_portknock_test.go — FEAT-2326609775 inc1 destructive/regression
// coverage for portknock.go's shared-secret + Origin allow-list middleware.
// The load-bearing property under test is the doc comment's own claim:
// "byte-identical-when-unconfigured" -- every existing invocation (both
// env vars unset) must pass through to the wrapped handler completely
// unchanged, while a CONFIGURED check must fail closed. Every case here is
// RED-provable (see each test's comment).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func passThroughHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // distinctive, unmistakable sentinel
	})
}

// TestWrapPortKnock_Unconfigured_IsPureNoop is the byte-identical-when-
// unconfigured proof the package doc comment claims. RED evidence: adding
// ANY unconditional check inside wrapPortKnock's returned handler (e.g.
// requiring the header even with cfg.secret == "") makes this 418
// assertion fail with 403 instead.
func TestWrapPortKnock_Unconfigured_IsPureNoop(t *testing.T) {
	cfg := portKnockConfig{} // both env vars absent
	wrapped := wrapPortKnock(cfg, passThroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("unconfigured wrapPortKnock: status = %d, want %d (pass-through to next.ServeHTTP)", rr.Code, http.StatusTeapot)
	}
}

// TestWrapPortKnock_SecretConfigured_RejectsMissingOrWrongHeader proves
// the fail-closed half: once a secret IS configured, a request with no
// header, an empty header, or the wrong value is refused with 403 and
// MET-P041, and next.ServeHTTP is NEVER called (asserted via a handler
// that panics if invoked). RED evidence: comparing with `==` instead of
// subtle.ConstantTimeCompare would still pass THIS test (it only checks
// outcome, not timing), but skipping the check entirely (e.g. an early
// `return true` before reading the header) fails every sub-case here.
func TestWrapPortKnock_SecretConfigured_RejectsMissingOrWrongHeader(t *testing.T) {
	cfg := portKnockConfig{secret: "correct-horse-battery-staple"}
	neverCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next.ServeHTTP must not be called when the shared-secret check fails")
	})
	wrapped := wrapPortKnock(cfg, neverCalled)

	cases := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"wrong value", "wrong-secret"},
		{"right value wrong case", "Correct-Horse-Battery-Staple"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/ws", nil)
			if tc.header != "" {
				req.Header.Set(SharedSecretHeader, tc.header)
			}
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 403", tc.name, rr.Code)
			}
			if got := rr.Body.String(); !strings.Contains(got, ErrPortKnockRejected) {
				t.Fatalf("%s: body = %q, want it to carry %s", tc.name, got, ErrPortKnockRejected)
			}
		})
	}
}

// TestWrapPortKnock_SecretConfigured_AcceptsCorrectHeader proves the
// matching path actually reaches next.ServeHTTP -- a check that ALWAYS
// refuses (or always returns false) would pass the rejection test above
// but fail every real client, which this test specifically catches. RED
// evidence: an inverted condition (`if got == cfg.secret { refuse }`)
// fails this while still passing the rejection test in isolation.
func TestWrapPortKnock_SecretConfigured_AcceptsCorrectHeader(t *testing.T) {
	cfg := portKnockConfig{secret: "correct-horse-battery-staple"}
	wrapped := wrapPortKnock(cfg, passThroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set(SharedSecretHeader, "correct-horse-battery-staple")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("matching secret: status = %d, want %d (pass-through)", rr.Code, http.StatusTeapot)
	}
}

// TestWrapPortKnock_SecretConfigured_AcceptsQueryParamFallback proves a
// browser-shaped client (no ability to set a custom header on the request
// that triggers the WS upgrade, RFC 6455 §4.1) can still authenticate via
// ?secret=... . RED evidence: removing the r.URL.Query().Get fallback in
// checkSharedSecret fails this while leaving the header-based test above
// passing -- proving the two paths are tested independently.
func TestWrapPortKnock_SecretConfigured_AcceptsQueryParamFallback(t *testing.T) {
	cfg := portKnockConfig{secret: "correct-horse-battery-staple"}
	wrapped := wrapPortKnock(cfg, passThroughHandler())

	req := httptest.NewRequest(http.MethodGet, "/ws?secret=correct-horse-battery-staple", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("query-param secret: status = %d, want %d (pass-through)", rr.Code, http.StatusTeapot)
	}
}

// TestWrapPortKnock_OriginAllowList proves the origin check: with an
// allow-list configured, an allowed Origin passes, a disallowed or missing
// Origin is refused with 403/MET-P042. RED evidence: treating a missing
// Origin as automatically allowed (the "no browser, exempt it" trap the
// checkOrigin doc comment explicitly rejects) fails the "missing origin"
// sub-case.
func TestWrapPortKnock_OriginAllowList(t *testing.T) {
	cfg := portKnockConfig{allowedOrigins: map[string]struct{}{"https://good.example": {}}}

	t.Run("allowed origin passes", func(t *testing.T) {
		wrapped := wrapPortKnock(cfg, passThroughHandler())
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.Header.Set("Origin", "https://good.example")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusTeapot {
			t.Fatalf("allowed origin: status = %d, want %d", rr.Code, http.StatusTeapot)
		}
	})

	t.Run("disallowed origin refused", func(t *testing.T) {
		neverCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("next.ServeHTTP must not be called for a disallowed origin")
		})
		wrapped := wrapPortKnock(cfg, neverCalled)
		req := httptest.NewRequest(http.MethodGet, "/ws", nil)
		req.Header.Set("Origin", "https://evil.example")
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("disallowed origin: status = %d, want 403", rr.Code)
		}
		if got := rr.Body.String(); !strings.Contains(got, ErrOriginRejected) {
			t.Fatalf("disallowed origin: body = %q, want it to carry %s", got, ErrOriginRejected)
		}
	})

	t.Run("missing origin refused (fail-closed, not exempted)", func(t *testing.T) {
		neverCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("next.ServeHTTP must not be called for a missing Origin when an allow-list is configured")
		})
		wrapped := wrapPortKnock(cfg, neverCalled)
		req := httptest.NewRequest(http.MethodGet, "/ws", nil) // no Origin header
		rr := httptest.NewRecorder()
		wrapped.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("missing origin: status = %d, want 403 (fail-closed)", rr.Code)
		}
	})
}

// TestReadPortKnockConfig_EmptyEnvValueDisablesCheck proves an explicitly
// EMPTY (not merely absent) METROSERVE_SHARED_SECRET disables the check
// the same as an unset one, per readPortKnockConfig's own doc comment: an
// operator who clears the variable to "" rather than unsetting it must not
// get a server that refuses every client with an empty required header
// (which no real client could ever satisfy). RED evidence: dropping the
// `if raw := ...; raw != ""` guard (using os.Getenv's raw "" result
// directly, which happens to already work for secret since Go's zero
// value IS "") is actually fine for secret, but the analogous guard for
// allowedOrigins is load-bearing -- this test exercises both via the env.
func TestReadPortKnockConfig_EmptyEnvValueDisablesCheck(t *testing.T) {
	t.Setenv(SharedSecretEnv, "")
	t.Setenv(AllowedOriginsEnv, "")

	cfg := readPortKnockConfig()
	if cfg.secret != "" {
		t.Fatalf("empty %s must disable the secret check, got secret=%q", SharedSecretEnv, cfg.secret)
	}
	if cfg.allowedOrigins != nil {
		t.Fatalf("empty %s must disable the origin check, got allowedOrigins=%v", AllowedOriginsEnv, cfg.allowedOrigins)
	}
}

// TestReadPortKnockConfig_ParsesCommaSeparatedOrigins proves the
// comma-separated list parsing (incl. trimming whitespace around entries)
// actually produces a lookup set matching every listed origin.
func TestReadPortKnockConfig_ParsesCommaSeparatedOrigins(t *testing.T) {
	t.Setenv(AllowedOriginsEnv, "https://a.example, https://b.example ,https://c.example")

	cfg := readPortKnockConfig()
	for _, want := range []string{"https://a.example", "https://b.example", "https://c.example"} {
		if _, ok := cfg.allowedOrigins[want]; !ok {
			t.Fatalf("allowedOrigins missing %q; parsed set = %v", want, cfg.allowedOrigins)
		}
	}
	if len(cfg.allowedOrigins) != 3 {
		t.Fatalf("len(allowedOrigins) = %d, want 3 (got %v)", len(cfg.allowedOrigins), cfg.allowedOrigins)
	}
}
