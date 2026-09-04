package main

// attack_health_test.go — FEAT-2326609775 inc1 destructive/regression
// coverage for health.go's /health handler and healthRegistry. Every case
// here is RED-provable: reverting the corresponding health.go change makes
// the test fail (see each test's own comment for the specific mutation).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
)

// TestHealthHandler_ReportsBuildInfoAndMode proves the two static fields
// (Version and Mode) actually flow from buildinfo/the handler's own
// construction argument into the JSON body, not a hardcoded placeholder.
// RED evidence: hardcoding resp.Version = "" or resp.Mode = "" in
// ServeHTTP fails this immediately.
func TestHealthHandler_ReportsBuildInfoAndMode(t *testing.T) {
	orig := buildinfo.Version
	buildinfo.Version = "TEST-VERSION-h1"
	t.Cleanup(func() { buildinfo.Version = orig })

	reg := newHealthRegistry()
	h := newHealthHandler("hosted", reg)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode /health body: %v (body=%q)", err, rr.Body.String())
	}
	if resp.Status != "ok" {
		t.Fatalf("Status = %q, want \"ok\"", resp.Status)
	}
	if resp.Version != "TEST-VERSION-h1" {
		t.Fatalf("Version = %q, want the injected buildinfo.Version", resp.Version)
	}
	if resp.Mode != "hosted" {
		t.Fatalf("Mode = %q, want %q", resp.Mode, "hosted")
	}
	if resp.CityCount != 0 || len(resp.Cities) != 0 {
		t.Fatalf("empty registry must report zero cities, got CityCount=%d Cities=%v", resp.CityCount, resp.Cities)
	}
}

// TestHealthHandler_ReportsRegisteredCitiesSorted proves every registered
// city appears in the response, with its live tick, in deterministic
// (tenantID, cityID) order regardless of registration order. RED evidence:
// dropping the sort.Slice call in healthRegistry.snapshot makes the
// two-city case flaky/order-dependent (fails under -count with map
// iteration randomization); omitting a city from the returned slice (e.g.
// registering into a fixed-size array) fails the length assertion
// outright.
func TestHealthHandler_ReportsRegisteredCitiesSorted(t *testing.T) {
	reg := newHealthRegistry()

	eB := core.NewEngine(core.WithWorldSeed(2))
	eA := core.NewEngine(core.WithWorldSeed(1))
	// Advance A by a known number of ticks so its Tick field is
	// distinguishable from B's (both start at 0).
	if err := eA.AdvanceTicks("attack-health-1", 3); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	// Register in "wrong" (B before A) order to prove sorting, not
	// insertion order, decides the output order. wantCities derives from the
	// registrations below (GR#15) rather than a hand-typed count.
	registered := []struct {
		id string
		e  *core.Engine
	}{{"cityB", eB}, {"cityA", eA}}
	for _, r := range registered {
		reg.register(newCityHealthState("local", r.id, r.e))
	}
	wantCities := len(registered)

	h := newHealthHandler("hosted", reg)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/health", nil))

	var resp healthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.CityCount != wantCities || len(resp.Cities) != wantCities {
		t.Fatalf("CityCount/len(Cities) = %d/%d, want %d/%d", resp.CityCount, len(resp.Cities), wantCities, wantCities)
	}
	if resp.Cities[0].CityID != "cityA" || resp.Cities[1].CityID != "cityB" {
		t.Fatalf("Cities order = [%s, %s], want [cityA, cityB] (sorted)", resp.Cities[0].CityID, resp.Cities[1].CityID)
	}
	if resp.Cities[0].Tick != 3 {
		t.Fatalf("cityA.Tick = %d, want 3 (AdvanceTicks(3) was called on its engine)", resp.Cities[0].Tick)
	}
	if resp.Cities[1].Tick != 0 {
		t.Fatalf("cityB.Tick = %d, want 0 (never advanced)", resp.Cities[1].Tick)
	}
}

// TestHealthRegistry_UnregisterRemovesCity is the FEAT-1972079942 idle-
// evictor integration proof: once a city is unregistered (mirroring
// runningCity.stop's unregisterHealth call), /health must stop listing it
// -- a torn-down city must never look alive. RED evidence: a no-op
// unregister (or one that only marks a flag without deleting from the map)
// fails this: the city would still appear in the snapshot.
func TestHealthRegistry_UnregisterRemovesCity(t *testing.T) {
	reg := newHealthRegistry()
	e := core.NewEngine(core.WithWorldSeed(1))
	reg.register(newCityHealthState("local", "gone", e))

	if got := len(reg.snapshot()); got != 1 {
		t.Fatalf("after register: len(snapshot()) = %d, want 1", got)
	}

	reg.unregister("local", "gone")

	if got := len(reg.snapshot()); got != 0 {
		t.Fatalf("after unregister: len(snapshot()) = %d, want 0 -- a torn-down city must not still report as healthy", got)
	}
}

// failingResponseWriter's Write always fails, forcing json.Encoder.Encode
// in healthHandler.ServeHTTP to return a non-nil error -- the ONLY way to
// exercise the MET-P040 error path for the current healthResponse shape
// (which has no field that can fail json.Marshal on its own). This proves
// GR#1's "never silently swallow" requirement is actually wired: the
// failure is logged via errs.Wrap(ErrHealthEncodeFailed, ...) to the
// handler's stderr sink rather than panicking or being silently dropped.
// RED evidence: replacing the `if err := ...; err != nil { ... }` block
// with a bare `_ = json.NewEncoder(w).Encode(resp)` (swallowing the error)
// fails this test's log-content assertion.
type failingResponseWriter struct {
	header http.Header
}

func (f *failingResponseWriter) Header() http.Header        { return f.header }
func (f *failingResponseWriter) WriteHeader(statusCode int) {}
func (f *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("attack_health_test: simulated write failure")
}

func TestHealthHandler_EncodeFailureIsLoggedNotSwallowed(t *testing.T) {
	reg := newHealthRegistry()
	h := newHealthHandler("single", reg)

	var stderr strings.Builder
	h.stderr = &stderr

	fw := &failingResponseWriter{header: make(http.Header)}
	h.ServeHTTP(fw, httptest.NewRequest(http.MethodGet, "/health", nil))

	if !strings.Contains(stderr.String(), ErrHealthEncodeFailed) {
		t.Fatalf("expected the logged encode failure to carry %s, got stderr=%q", ErrHealthEncodeFailed, stderr.String())
	}
}
