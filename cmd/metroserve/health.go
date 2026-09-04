// health.go is FEAT-2326609775 (Azure cloud engine, inc1) — "one engine
// up, one round-trip measured" — THE ONE Go code change the design doc
// (docs/planning/azure-cloud-engine-design.md §1.1) identifies: metroserve
// serves exactly one route, /ws, and /ws cannot serve as a liveness/
// readiness probe because it demands a full WebSocket upgrade plus the
// version handshake (wsserver.Server.handshake). /health is a plain HTTP
// GET, no upgrade, no handshake, so an Azure Container Apps probe (or a
// human with curl) can answer "is it up?" cheaply.
//
// # What "cheap" means here (the design doc's explicit inc1 gate)
//
// /health must never do anything the design doc itself flags as expensive:
// no full journal read (compose.RestoreLatestSnapshotOrGenesis's own doc
// comment calls a from-genesis replay O(all history) — see
// internal/engine/compose/snapshot.go's persist.go caller), and no long-held
// lock. This is why the response below deliberately reports ONLY fields
// that are already cheap, request-time reads: core.Engine.TicksCompleted()
// (an atomic load, commands.go's tickCounter) and static build-info strings
// -- it does NOT report a durable journal length or a last-snapshot
// timestamp, because computing either honestly, per request, would mean
// either reading the whole journal (persist.Store.ReadJournal is the only
// accessor the registered Store interface exposes -- internal/persist/
// store.go) or listing snapshots (Store.ListSnapshots) on every single probe.
// A real deployment's Container Apps liveness probe fires every few seconds
// (the exact steady-state hot path the design doc's §1.7 warns is already
// expensive against an Azure Files mount) — adding a second, request-driven
// O(history) read there would make /health itself a new source of load
// against the store, the opposite of "cheap, no locks held long." Fuller
// journal/snapshot telemetry (the design doc's wishlist field set) is left
// for a follow-up once a cheap, already-tracked counter exists to report
// (e.g. threaded through internal/persist's write path) rather than adding
// one now by touching several already-tested cmd/metroserve call sites
// (persist.go's wireAndRehydrate, cityhost.go's buildCity,
// snapshotdriver.go's startCommandLoop) beyond what this increment needs.
//
// # No new code.json edge (GR#25 P1)
//
// Every import below is one cmd/metroserve already carries (core,
// buildinfo, errs) or the Go standard library. This file adds no new
// module dependency and therefore registers no new graph edge — it is
// cmd/metroserve's own HTTP surface, exactly like main.go's existing /ws
// route.
//
// Module key: int.protocol (see cmd/metroserve/main.go's own package doc
// comment: "this binary is a transport host for int.protocol's existing
// envelope/subscription machinery, not a new module in its own right" —
// the same convention MET-P035, added for this binary's snapshot-cadence
// error, already follows).
// Error codes: MET-P040 (this file, /health encode failure), MET-P041/
// MET-P042 (portknock.go, pre-upgrade refusals) — claimed via
// `node tools/plan/add-error.js claim-range int.protocol --layer P --size 10`
// (P040-P049), added via `add-error.js add` (GR#7).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ErrHealthEncodeFailed (MET-P040) is raised, non-fatally, when the
// /health JSON response fails to encode. This should never happen for the
// concrete healthResponse shape below (no cyclic types, no unmarshalable
// fields), but GR#1 (aggressive error trapping) requires the write path be
// checked and logged rather than silently swallowed, exactly like every
// other json.Marshal call site in this codebase (e.g. wsserver's
// handleCommand ackBytes).
const ErrHealthEncodeFailed = "MET-P040"

// cityHealthState is the CHEAP, per-city info /health reports for one
// running city (single-city legacy mode has exactly one; CityHost's hosted
// mode has one per live city). It holds only a reference to the engine --
// every field it reports is a fresh, cheap read at snapshot time (never a
// cached/stale value), and it never touches persist.Store (see this file's
// package doc comment for why).
type cityHealthState struct {
	tenantID, cityID string
	engine           *core.Engine
}

func newCityHealthState(tenantID, cityID string, e *core.Engine) *cityHealthState {
	return &cityHealthState{tenantID: tenantID, cityID: cityID, engine: e}
}

// snapshot renders this city's current health fields. The only read here
// is core.Engine.TicksCompleted(), an atomic load (commands.go's
// tickCounter) -- no lock is held across this call, and nothing here
// touches persist.Store.
func (s *cityHealthState) snapshot() healthCityInfo {
	return healthCityInfo{
		TenantID: s.tenantID,
		CityID:   s.cityID,
		Tick:     s.engine.TicksCompleted(),
	}
}

// errHealthRegistryCopied is returned by healthRegistry's guarded methods
// when the value has been copied after construction (SEC-020) -- a copied
// healthRegistry would carry its sync.Mutex in a possibly-locked state and
// alias the states map. Mirrors CityHost.checkNotCopied/errCityHostCopied
// exactly (cityhost.go) -- the same self atomic.Pointer + checkNotCopied
// convention every mutex-guarded, cross-goroutine-reachable type in this
// codebase uses (astgate's copyguard scan, SEC-049/BUG-024's ratchet).
var errHealthRegistryCopied = errors.New("metroserve: healthRegistry used after being copied (SEC-020: must be used via the *healthRegistry from newHealthRegistry)")

// healthRegistry is the thread-safe set of cityHealthState this process is
// currently tracking. Single-city legacy mode (main.go's run()) registers
// exactly one entry for the process's lifetime; hosted mode (cityhost.go)
// registers one per city as CityHost builds it and unregisters it on
// eviction/shutdown, so a /health snapshot never reports a city that has
// actually been torn down.
//
// Guarded by a plain sync.Mutex, held only for the map mutation/copy below
// -- never across engine or store calls (matches CityHost's own "short
// critical section, I/O outside the lock" discipline, cityhost.go's
// evictIdle doc comment). self is the SEC-020 copy guard (see
// errHealthRegistryCopied's doc comment).
type healthRegistry struct {
	self atomic.Pointer[healthRegistry]

	mu     sync.Mutex
	states map[string]*cityHealthState // key: tenantID + "/" + cityID
}

func newHealthRegistry() *healthRegistry {
	r := &healthRegistry{states: make(map[string]*cityHealthState)}
	r.self.Store(r)
	return r
}

// checkNotCopied guards against a healthRegistry value being copied after
// construction. SEC-020: every method that touches mu/states calls this
// FIRST, before ever locking or reading a field -- mirrors CityHost's own
// checkNotCopied (cityhost.go).
func (r *healthRegistry) checkNotCopied() error {
	if r.self.Load() != r {
		return errHealthRegistryCopied
	}
	return nil
}

func healthRegistryKey(tenantID, cityID string) string { return tenantID + "/" + cityID }

// register adds (or replaces) the tracked state for one city. A copied
// receiver is a silent no-op (mirrors CityHost.Acquire/Release's own
// "a copied host cannot safely mutate; drop" policy) -- register/unregister
// are best-effort diagnostics wiring, not the durability path GR#1 reserves
// hard failure for.
func (r *healthRegistry) register(s *cityHealthState) {
	if err := r.checkNotCopied(); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[healthRegistryKey(s.tenantID, s.cityID)] = s
}

// unregister removes a city's tracked state (hosted-mode eviction/shutdown
// — FEAT-1972079942's idle evictor and Shutdown/Close both tear a city
// down; /health must stop listing it the moment it is gone).
func (r *healthRegistry) unregister(tenantID, cityID string) {
	if err := r.checkNotCopied(); err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, healthRegistryKey(tenantID, cityID))
}

// snapshot returns every currently-tracked city's health info, sorted by
// (tenantID, cityID) for deterministic output (GR#21: this is a
// diagnostics response, not sim state, but a stable field order still
// makes byte-for-byte smoke-test diffing possible). The lock is held only
// long enough to copy out the pointer slice -- the actual per-city
// snapshot() calls (each a cheap atomic load) run AFTER it is released. A
// copied receiver returns an empty slice (never a partial/stale read) --
// see register's doc comment for why this is a silent no-op rather than a
// panic/error return on this diagnostics-only path.
func (r *healthRegistry) snapshot() []healthCityInfo {
	if err := r.checkNotCopied(); err != nil {
		return nil
	}
	r.mu.Lock()
	states := make([]*cityHealthState, 0, len(r.states))
	for _, s := range r.states {
		states = append(states, s)
	}
	r.mu.Unlock()

	out := make([]healthCityInfo, len(states))
	for i, s := range states {
		out[i] = s.snapshot()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].CityID < out[j].CityID
	})
	return out
}

// healthCityInfo is one city's reportable health snapshot.
type healthCityInfo struct {
	TenantID string `json:"tenantId"`
	CityID   string `json:"cityId"`
	// Tick is core.Engine.TicksCompleted() -- an already-atomic counter
	// (commands.go's tickCounter), so this is always present and always
	// current as of the request.
	Tick uint64 `json:"tick"`
}

// healthResponse is the /health JSON body (design doc §7 inc1: "Returns
// buildinfo.String(), current tick, hosted-city count" -- see this file's
// package doc comment for why the fuller §1.1 wishlist, journal length and
// snapshot age, is explicitly deferred rather than paid for with a
// per-request O(history) store read).
type healthResponse struct {
	Status string `json:"status"` // always "ok" -- reaching this handler at all IS the liveness signal
	// Version/Commit/Branch/BuildTime/Host mirror buildinfo's own fields
	// (GR#2: git-describe via ldflags, never hand-maintained) so a deployed
	// container's exact build is identifiable from curl alone, matching the
	// design doc's "one round-trip measured against a known build" smoke
	// test (§6.5).
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Branch    string `json:"branch"`
	BuildTime string `json:"buildTime"`
	Host      string `json:"host"`
	// Mode distinguishes the legacy single-city path (persist-dir empty,
	// main.go's run()) from the multi-city CityHost path (runHosted) --
	// the two run() functions this binary has always had (main.go's own
	// package doc comment).
	Mode string `json:"mode"`
	// CityCount is len(Cities) surfaced as its own field so a probe can
	// check "is at least the default city up?" without counting the array
	// itself (design doc §7: "hosted-city count").
	CityCount int `json:"cityCount"`
	// Cities lists every currently-tracked city. Single-city mode always
	// has exactly one entry; hosted mode has one per live (non-evicted)
	// city, which may be zero right after boot before the pre-created
	// default city finishes building.
	Cities []healthCityInfo `json:"cities"`
}

// healthHandler serves GET /health. It never blocks on the engine or the
// store (see this file's package doc comment) -- every field is either a
// static build-info string or a cheap atomic load via healthRegistry.
type healthHandler struct {
	mode     string
	registry *healthRegistry
	// stderr is where a (should-never-happen) encode failure is logged;
	// overridable by tests (an io.Writer, not *os.File, so a test can
	// inject a buffer or a failing writer), defaulting to os.Stderr in
	// production (newHealthHandler).
	stderr io.Writer
}

func newHealthHandler(mode string, registry *healthRegistry) *healthHandler {
	return &healthHandler{mode: mode, registry: registry, stderr: os.Stderr}
}

func (h *healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cities := h.registry.snapshot()
	resp := healthResponse{
		Status:    "ok",
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		Branch:    buildinfo.Branch,
		BuildTime: buildinfo.BuildTime,
		Host:      buildinfo.Host,
		Mode:      h.mode,
		CityCount: len(cities),
		Cities:    cities,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// The status line and Content-Type header are already flushed at
		// this point, so there is nothing further to send the caller --
		// this is a server-side-only failure, registry-logged per GR#1
		// rather than swallowed (mirrors wsserver.writeNotification's own
		// "log/skip, never crash the handler over one bad encode" policy).
		e := errs.Wrap(ErrHealthEncodeFailed, errs.NewCorrelationID(), err, map[string]any{"reason": err.Error()})
		_, _ = fmt.Fprintf(h.stderr, "metroserve: %v\n", e)
	}
}
