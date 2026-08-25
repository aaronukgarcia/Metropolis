package solvergpu

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// PriorityGPU is the registration priority for the GPU backend: between CPU
// (0) and cloud (100) per the solver-contract.md ladder (AC-1 "≈50"). It
// lives here, not in int.solver, because exporting PriorityCPU/PriorityGPU/
// PriorityCloud from the contract package is the open ES-1 ruling this
// package must not pre-empt by editing a dependency module; until Bill rules
// yes, the composition root consumes THIS constant rather than hand-rolling
// a magic integer at the call site.
const PriorityGPU = 50

// BackendName is the diagnostic backend name reported in Response.Backend
// when the GPU path produces a result (contract.go's example "gpu.sidecar").
const BackendName = "gpu.sidecar"

// Status reports the sidecar's health (ICD §10): up / degraded / down.
type Status int

const (
	// StatusDown: no transport is connected — every Solve uses the local
	// fallback.
	StatusDown Status = iota
	// StatusDegraded: a transport is connected but the most recent Solve
	// fell back to the local solver (the GPU failed that call).
	StatusDegraded
	// StatusUp: a transport is connected and the most recent Solve
	// completed through it.
	StatusUp
)

// String renders a Status for logs/diagnostics. Never used on a
// determinism-sensitive path.
func (s Status) String() string {
	switch s {
	case StatusDown:
		return "down"
	case StatusDegraded:
		return "degraded"
	case StatusUp:
		return "up"
	default:
		return "unknown"
	}
}

// Metrics carries the monitoring signals (ICD §10): throughput (successful
// GPU solves) and the critical local-fallback activation count (GR#17 — a
// rising count means the GPU is not paying for itself). Wall-clock latency
// is deliberately absent: the sidecar never reads the wall clock (ICD §7).
type Metrics struct {
	// Solves is the number of Solve calls the GPU transport answered.
	Solves int64
	// Fallbacks is the number of Solve calls the local fallback answered
	// because the GPU path was unavailable or failed.
	Fallbacks int64
	// TransportErrors is the number of GPU-path failures observed before
	// fallback (diagnostic; the fallback activation count is Fallbacks).
	TransportErrors int64
}

// transportSlot wraps the current Transport so it can be swapped atomically
// (atomic.Pointer requires a concrete type; Transport is an interface).
type transportSlot struct {
	t Transport
}

// Backend is the Go-side GPU solver backend: it implements solver.Solver and
// plugs the int.solver registry at PriorityGPU. It is the sidecar CLIENT —
// it holds no simulation state, reads nothing but the request payload, and
// is safe for concurrent use. Construct via NewBackend; the zero value is
// not usable (a nil local solver would panic on Solve).
//
// See doc.go for why this type deliberately carries no SEC-020 copy guard.
type Backend struct {
	name   string
	local  solver.Solver
	hooks  Hooks
	dial   func(addr string) (Transport, error)
	budget int64 // VRAM budget in bytes; default solver.GPUVRAMEnvelopeBytes

	transport atomic.Pointer[transportSlot]

	solves          atomic.Int64
	fallbacks       atomic.Int64
	transportErrors atomic.Int64
	lastFallback    atomic.Bool
}

// Option configures a Backend. Construct via NewBackend; options specialise
// the defaults for tests or a real gRPC deployment.
type Option func(*Backend)

// WithTransport installs t as the current transport, overriding the default
// in-process worker. Pass a transport whose SolveRemote errors to model
// "GPU down" (AC-5's failing-transport test).
func WithTransport(t Transport) Option {
	return func(b *Backend) { b.transport.Store(&transportSlot{t: t}) }
}

// WithLocal overrides the mandatory local fallback solver (default: the CPU
// backend). It must implement the same deterministic transform for the
// byte-identity contract to hold.
func WithLocal(s solver.Solver) Option {
	return func(b *Backend) { b.local = s }
}

// WithHooks overrides the reconnect Lookup/Authenticate hooks.
func WithHooks(h Hooks) Option {
	return func(b *Backend) { b.hooks = h }
}

// WithDial overrides the transport re-establishment function Reconnect
// calls after Lookup/Authenticate succeed.
func WithDial(d func(addr string) (Transport, error)) Option {
	return func(b *Backend) { b.dial = d }
}

// WithName overrides the diagnostic backend name.
func WithName(n string) Option {
	return func(b *Backend) { b.name = n }
}

// WithVRAMBudget overrides the VRAM budget the sidecar refuses over-envelope
// jobs against (default: solver.GPUVRAMEnvelopeBytes). In production this is
// the card's actually-available VRAM queried at runtime (cudaMemGetInfo,
// AC-9/ASM-805); the injectable budget models that query headlessly.
func WithVRAMBudget(bytes int64) Option {
	return func(b *Backend) { b.budget = bytes }
}

// NewBackend constructs a ready-to-use GPU sidecar backend with sane
// defaults: name "gpu.sidecar", local fallback = CPU, no-op reconnect hooks,
// an in-process worker transport, and the spec VRAM envelope (consumed from
// int.solver's sizing.go, never re-hardcoded — AC-9/GR#15).
func NewBackend(opts ...Option) *Backend {
	b := &Backend{
		name:   BackendName,
		local:  solver.NewCPUBackend(),
		hooks:  noopHooks{},
		budget: solver.GPUVRAMEnvelopeBytes,
	}
	for _, o := range opts {
		o(b)
	}
	if b.dial == nil {
		b.dial = func(addr string) (Transport, error) {
			return newLocalTransport(b.name), nil
		}
	}
	// Unless the caller installed an explicit transport (WithTransport),
	// establish the default in-process worker now so a fresh backend starts
	// "up" rather than "down". A dial error leaves the sidecar down and the
	// next Solve uses the local fallback — never a hung or half-built state.
	if b.transport.Load() == nil {
		if t, err := b.dial(b.name); err == nil {
			b.transport.Store(&transportSlot{t: t})
		}
	}
	return b
}

func (b *Backend) loadTransport() Transport {
	s := b.transport.Load()
	if s == nil {
		return nil
	}
	return s.t
}

// Supports reports whether the sidecar can accelerate problem. It declares
// the offload subset (AC-4) — TrafficAssignment and ColdPassBatch — plus
// EchoProblem, the plumbing-proof kind int.solver itself uses to prove the
// registry/fallback/determinism seam end-to-end before any real algorithm
// lands (AC-2/ES-2's "EchoProblem-adjacent plumbing"). DeepProjection and
// LifeWriting stay CPU-only until their schemas firm up (TODO-SPEC). See
// doc.go's open decision note.
func (b *Backend) Supports(problem solver.ProblemKind) bool {
	return supports(problem)
}

// supports is the single declaration of the sidecar's offload subset, shared
// by Backend and Stub (GR#3 — one source of truth).
func supports(problem solver.ProblemKind) bool {
	switch problem {
	case solver.TrafficAssignment, solver.ColdPassBatch, solver.EchoProblem:
		return true
	default:
		return false
	}
}

// Solve implements solver.Solver. It is the mandatory-local-fallback path
// (ICD §9, AC-5): try the GPU transport; on any failure, re-run the
// identical seeded function on the local solver and return its
// byte-identical result, differing only in the diagnostic Backend label and
// a non-fatal Warning. It never returns a nil Response with a nil error, and
// it never reads the wall clock (ICD §7).
func (b *Backend) Solve(req solver.Request) (solver.Response, error) {
	if err := b.validate(req); err != nil {
		return solver.Response{}, err
	}

	if t := b.loadTransport(); t != nil {
		resp, err := t.SolveRemote(req)
		if err == nil {
			b.solves.Add(1)
			b.lastFallback.Store(false)
			return resp, nil
		}
		b.transportErrors.Add(1)
	}

	resp, err := b.local.Solve(req)
	if err != nil {
		// Both the GPU path and the local fallback failed (e.g. a real
		// problem kind whose algorithm no backend has built yet — the local
		// solver's ErrNotImplemented). Propagate the local error unchanged;
		// the caller's fallback chain sees the same failure CPU would have
		// returned, keeping behaviour byte-identical to CPU.
		return solver.Response{}, err
	}
	b.fallbacks.Add(1)
	b.lastFallback.Store(true)
	resp.Warnings = append([]string{BackendName + " unavailable; local fallback used"}, resp.Warnings...)
	return resp, nil
}

// validate enforces the shared payload bound at the sidecar's own entry
// point, before any allocation sized from len(req.Payload), mirroring
// solver's validateRequestPayload so a caller reaching the backend directly
// (bypassing a Registry) is still bounded (GR#1, weakness pattern #6).
func (b *Backend) validate(req solver.Request) error {
	if len(req.Payload) > solver.MaxRequestPayloadBytes {
		return errs.New(solver.ErrRequestPayloadTooLarge, errs.NewCorrelationID(), map[string]any{
			"problem":      req.Problem.String(),
			"payloadBytes": len(req.Payload),
			"maxBytes":     solver.MaxRequestPayloadBytes,
		})
	}
	return nil
}

// Reconnect re-establishes the transport (ICD §9): name lookup, then
// re-authentication, then dial. Any failure maps to the registry-sourced
// errSidecarUnavailable and leaves the sidecar in a local-only state
// (transport untouched), so the next Solve uses the local fallback rather
// than a half-connected worker.
func (b *Backend) Reconnect(name string) error {
	corrID := errs.NewCorrelationID()

	addr, err := b.hooks.Lookup(name)
	if err != nil {
		return errs.Wrap(errSidecarUnavailable, corrID, err, map[string]any{"phase": "lookup", "name": name})
	}
	if err := b.hooks.Authenticate(addr); err != nil {
		return errs.Wrap(errSidecarUnavailable, corrID, err, map[string]any{"phase": "authenticate", "addr": addr})
	}
	t, err := b.dial(addr)
	if err != nil {
		return errs.Wrap(errSidecarUnavailable, corrID, err, map[string]any{"phase": "dial", "addr": addr})
	}
	b.transport.Store(&transportSlot{t: t})
	return nil
}

// Status reports the sidecar's current health (ICD §10): down when no
// transport is connected, degraded when the most recent Solve fell back,
// and up when the most recent Solve completed through the transport.
func (b *Backend) Status() Status {
	if b.loadTransport() == nil {
		return StatusDown
	}
	if b.lastFallback.Load() {
		return StatusDegraded
	}
	return StatusUp
}

// Metrics returns a snapshot of the monitoring counters (ICD §10). The
// local-fallback activation count (Fallbacks) is the critical signal: a
// rising count means the GPU is not paying for itself (GR#17).
func (b *Backend) Metrics() Metrics {
	return Metrics{
		Solves:          b.solves.Load(),
		Fallbacks:       b.fallbacks.Load(),
		TransportErrors: b.transportErrors.Load(),
	}
}

// FitsEnvelope reports whether a job whose referenced data totals totalBytes
// fits the sidecar's configured VRAM budget. The default budget is the
// spec's GPUVRAMEnvelopeBytes (consumed from int.solver's sizing.go — never
// re-hardcoded, AC-9/GR#15); in production the budget is the card's
// actually-available VRAM queried at runtime (cudaMemGetInfo), modelled here
// by WithVRAMBudget. A budget <= 0 falls back to int.solver's static
// FitsGPUEnvelope check.
func (b *Backend) FitsEnvelope(totalBytes int64) bool {
	if b.budget <= 0 {
		return solver.FitsGPUEnvelope(totalBytes)
	}
	return totalBytes >= 0 && totalBytes < b.budget
}

// Register installs a GPU backend into r at PriorityGPU. The composition
// root calls this; the sidecar is opt-in and never auto-registers itself
// (ICD §2: "an opt-in accelerator, not a new execution path").
func Register(r *solver.Registry, opts ...Option) error {
	return r.Register(BackendName, NewBackend(opts...), PriorityGPU)
}

var _ solver.Solver = (*Backend)(nil)
