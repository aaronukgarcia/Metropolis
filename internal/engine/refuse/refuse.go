package refuse

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Stream is one of the three §25 waste streams tracked per cell as a
// distinct sub-stock (AC-3): general (→ landfill/incinerator), recycling
// (with contamination), and food (→ composting). It is the lookup key for
// every per-stream accessor in this package, so the Go identity and the
// data/refuse.json identity are the same value (GR#3).
type Stream string

const (
	StreamGeneral   Stream = "general"
	StreamRecycling Stream = "recycling"
	StreamFood      Stream = "food"
)

// streamOrder is the fixed, documented order the three streams are laid
// out in this package's [3]int64 accounting arrays. Ordered (a slice, not
// a map) so every per-stream accumulation runs in a deterministic order
// (GR#21).
var streamOrder = [3]Stream{StreamGeneral, StreamRecycling, StreamFood}

// streamIndex returns the fixed array index for s, or -1 when s is not one
// of the three registered streams.
func streamIndex(s Stream) int {
	for i, name := range streamOrder {
		if s == name {
			return i
		}
	}
	return -1
}

// LandUse is the land-use type of a registered cell, which determines its
// bin-stock capacity (wheelie/trade/skip, AC-2) and its waste rate. It is
// an extensible key: the three built-in types below are the §25 surface,
// but the capacity/rate for each is read from data/refuse.json, never a
// hardcoded enum of magnitudes (GR#15).
type LandUse string

const (
	LandUseResidential LandUse = "residential"
	LandUseCommercial  LandUse = "commercial"
	LandUseIndustrial  LandUse = "industrial"
)

// MissCause is the typed cause of a missed (or incomplete) collection
// round (AC-6). The spec names four distinct causes with distinct
// downstream ticker framing, so a single generic "collection failed"
// boolean is insufficient; each cause is independently constructible.
type MissCause string

const (
	// MissTruckShortage: refuse-crew staffing left too few trucks to cover
	// the round's tonnage.
	MissTruckShortage MissCause = "truck-shortage"
	// MissGridlockDelay: engine.logistics' movement capacity could not move
	// the round's collected tonnage this tick (the stub-depth analogue of a
	// saturated junction queue).
	MissGridlockDelay MissCause = "gridlock-delay"
	// MissStrike: a strike event is active at the round's depot.
	MissStrike MissCause = "strike"
	// MissDepotUnderfunding: the refuse service's funding is below the
	// data-sourced underfunding threshold.
	MissDepotUnderfunding MissCause = "depot-underfunding"
)

// DisposalKind is the type of one disposal site (AC-8/AC-9): landfill
// (permanent fill, blight, cap-and-reclaim), incinerator (energy for
// airshed pollution), and compost (food waste → compost output).
type DisposalKind string

const (
	DisposalLandfill    DisposalKind = "landfill"
	DisposalIncinerator DisposalKind = "incinerator"
	DisposalCompost     DisposalKind = "compost"
)

// BinStock is the read-only snapshot of one cell's typed bin stock
// (AC-2): the documented capacity, the three per-stream in-bin levels,
// the three per-stream overflow (spilled) amounts, and the accumulated
// vermin index plus the last miss cause that left this cell uncollected
// (AC-6/AC-7). It is the ONLY exported view of the package's internal,
// unexported cell state (AC-1) — consumers can never write a bin field
// directly.
type BinStock struct {
	CellID   string
	LandUse  LandUse
	Street   string
	Capacity int64

	General   int64
	Recycling int64
	Food      int64

	OverflowGeneral   int64
	OverflowRecycling int64
	OverflowFood      int64

	VerminIndex float64
	MissCause   *MissCause
}

// Round is the read-only snapshot of one scheduled collection round
// (AC-4/AC-5): its depot, current (effective) route, whether the route is
// a player override, and the in-transit tonnage collected but not yet
// delivered to a disposal site.
type Round struct {
	ID                 string
	DepotID            string
	Route              []string
	Overridden         bool
	Completed          bool
	InTransitGeneral   int64
	InTransitRecycling int64
	InTransitFood      int64
}

// RoundResult is [RefuseAPI.RunRound]'s return value: what the round
// collected, what it delivered to the disposal site this tick, and the
// shortfall that stays in-transit for the next tick (AC-4's next-day-queue
// analogue). Missed/Cause name a miss when the round failed to complete
// (AC-6).
type RoundResult struct {
	RoundID            string
	Missed             bool
	Cause              *MissCause
	CollectedGeneral   int64
	CollectedRecycling int64
	CollectedFood      int64
	DeliveredGeneral   int64
	DeliveredRecycling int64
	DeliveredFood      int64
	ShortfallGeneral   int64
	ShortfallRecycling int64
	ShortfallFood      int64
}

// TickerEvent is the documented street-naming event a missed collection
// emits (AC-7/US-5): the affected street and cell, the overflowing stream,
// the overflow magnitude, and the cause that left it uncollected. It is an
// exported VALUE from RefuseAPI that engine.news can subscribe to once its
// own edge exists (see engine.refuse.md Escalations) — this package never
// formats or draws it.
type TickerEvent struct {
	Street     string
	CellID     string
	Stream     Stream
	OverflowKg int64
	MissCause  *MissCause
}

// WellbeingAPI is this package's consumer-side seam for code.json's
// registered engine.wellbeing outbound edge (WellbeingAPI, GUID
// da2c5c2a-495b-43b5-b496-2b641a5ec16a). engine.wellbeing has no code yet
// (MOD-034 is draft-ahead), so — exactly like engine.services' UnlockGate
// seam — this package declares the one method of that contract it needs
// and the composition root wires the real implementation once it lands
// (GR#20 contract-first, stub-forever). AC-7's requirement that the
// physical-health consequence cross the registered interface is satisfied
// by this seam: refuse never computes its own health number.
type WellbeingAPI interface {
	// ReportPollutionExposure feeds the physical-health PollutionExposure
	// driver (engine.wellbeing.md AC-5) with a cell's refuse-driven
	// pollution-exposure magnitude (the vermin/overflow consequence). The
	// driver math belongs to engine.wellbeing; refuse only supplies the
	// input through the registered interface.
	ReportPollutionExposure(cellID string, exposure float64) error
}

// cellState is the unexported mutable per-cell refuse state (AC-1). Levels
// and overflow are per-stream [3]int64 in the streamOrder layout. The only
// way another package reaches this state is through RefuseAPI's exported
// methods.
type cellState struct {
	landUse   LandUse
	street    string
	capacity  int64
	levels    [3]int64 // in-bin, kg
	overflow  [3]int64 // spilled, kg
	vermin    float64
	missCause *MissCause
}

// copyMissCause returns a defensive copy of an internal miss-cause pointer so
// an exported snapshot (BinStock.MissCause, TickerEvent.MissCause) never
// aliases cellState.missCause (SEC-138). Without it, a caller holding a
// snapshot could write *snap.MissCause = ... and corrupt the cell's internal
// status field without holding r.mu — contradicting AC-1 (consumers can never
// write a bin field directly) and racing a second holder of the same aliased
// pointer (AC-17). A nil cause stays nil (no miss has been recorded).
func copyMissCause(mc *MissCause) *MissCause {
	if mc == nil {
		return nil
	}
	cp := *mc
	return &cp
}

// roundState is the unexported mutable per-round state: the effective
// route (auto-optimised unless a player override is active), the depot, and
// the per-stream in-transit tonnage collected but not yet delivered.
type roundState struct {
	id            string
	depotID       string
	cells         []string // the round's cell set (schedule)
	route         []string // effective ordered route
	overridden    bool
	overrideRoute []string
	completed     bool
	// active marks a round currently being driven by a RunRound call. It is
	// claimed under r.mu before the collect/deliver work and released when the
	// call returns, so a concurrent RunRound on the same round is rejected
	// rather than re-collecting/re-delivering the same tonnage (AC-17/AC-11).
	active    bool
	inTransit [3]int64
}

// disposalSite is the unexported mutable per-site state (AC-8/AC-9). For a
// landfill, capacity is the total capacity and used is the permanent fill:
// the refuse-owned durable record of fill (not only the logistics shelf)
// that re-seeds the shelf after a Wire re-provision, so RemainingCapacity
// is monotone non-increasing across ANY re-wire (same or different
// instance). used only ever increases. For an incinerator, energy/airshed
// accumulate; for a compost site, compost accumulates.
type disposalSite struct {
	id          string
	kind        DisposalKind
	capacity    int64
	used        int64
	backlog     [3]int64
	reclaimed   bool
	surrounding []string
	energy      int64
	airshed     float64
	compost     int64
}

// RefuseAPI is code.json's "engine.refuse" inbound interface (RefuseAPI,
// GUID 877d2b29-6055-4ec4-9be2-b60b2d53fcf3): per-cell typed bin stock
// and overflow state, collection rounds expressed as engine.logistics
// movements, the overflow→vermin→health/land-value/fire-risk chain, the
// three waste streams, the landfill/incinerator/compost disposal lifecycle,
// and the mass-conservation accounting identity. See doc.go for the
// stub-for-baseline depth.
//
// The zero value is not usable; construct via [Load] or [LoadDefault] and
// inject the registered outbound dependencies via [Wire]. A *RefuseAPI is
// safe for concurrent use (AC-17): every mutable field is guarded by mu,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class).
type RefuseAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           data.RefuseFile

	cells  map[string]*cellState
	rounds map[string]*roundState
	depots map[string]bool
	sites  map[string]*disposalSite

	// per-stream cumulative flow counters (kg), in streamOrder layout.
	generated [3]int64
	collected [3]int64

	contamination   float64 // city-wide, [0,1]
	generalSiteID   string  // active general-waste disposal target
	compostSiteID   string  // active food-waste compost target
	trucksAvailable int64   // refuse-crew-derived truck count this tick

	strike      map[string]bool // depotID -> strike active
	provisioned map[string]bool // siteID -> logistics shelf provisioned

	logistics *logistics.LogisticsAPI
	services  *services.ServicesAPI
	wellbeing WellbeingAPI

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller.
	self atomic.Pointer[RefuseAPI]
}

// Load reads and validates data/refuse.json (via foundation/data.
// LoadRefuseFile) and returns a ready-to-wire *RefuseAPI whose per-cell
// bin capacities, waste rates, stream mix, and every other balance figure
// are populated from that data (GR#15 — none are hardcoded in Go). The
// registered outbound dependencies (engine.logistics, engine.services,
// engine.wellbeing) are injected separately via [Wire] by the composition
// root. Every failure is a registry-sourced *errs.E — never a silent
// default substitution (AC-13/GR#7).
func Load(dir, correlationID string) (*RefuseAPI, error) {
	f, err := data.LoadRefuseFile(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrRefuseDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	api := &RefuseAPI{
		correlationID: correlationID,
		cfg:           f,
		cells:         make(map[string]*cellState),
		rounds:        make(map[string]*roundState),
		depots:        make(map[string]bool),
		sites:         make(map[string]*disposalSite),
		strike:        make(map[string]bool),
		provisioned:   make(map[string]bool),
	}
	// Armed exactly once, before api is returned to any caller (SEC-020).
	api.self.Store(api)
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*RefuseAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// Wire injects the registered outbound dependencies: engine.logistics
// (rounds ARE logistics movements, AC-4), engine.services (refuse funding
// and the Public Service Pie refuse-crew benchmark, US-4), and the
// engine.wellbeing seam (the overflow health consequence, AC-7). It is
// idempotent (re-wiring replaces the previous dependencies) and rejects a
// nil logistics/services dependency so an unwired tick path can never
// silently treat a missing dependency as "empty".
//
// Idempotence extends to disposal-site state (AC-8): re-wiring the SAME
// logistics instance does not reset an already-provisioned landfill's fill
// — a full landfill stays full across a re-wire, so RemainingCapacity is
// monotone non-increasing.
func (r *RefuseAPI) Wire(l *logistics.LogisticsAPI, s *services.ServicesAPI, w WellbeingAPI) error {
	if err := r.checkNotCopied("Wire"); err != nil {
		return err
	}
	if l == nil || s == nil {
		return errs.New(ErrDependencyNotWired, r.correlationID, map[string]any{
			"method": "Wire",
		})
	}
	r.mu.Lock()
	changed := r.logistics != l
	r.logistics = l
	r.services = s
	r.wellbeing = w
	// A fresh logistics dependency needs its per-site movement shelves
	// re-provisioned (the old shelves belonged to the previous instance).
	// Re-wiring the SAME instance must NOT reset an already-provisioned
	// disposal site's fill (AC-8: a landfill fills permanently, so
	// RemainingCapacity is monotone non-increasing across re-wires).
	if changed {
		r.provisioned = make(map[string]bool)
	}
	r.mu.Unlock()

	// Register this package's refuse service against engine.services
	// (idempotent across re-wires) so funding/staffing queries work. The
	// services pointer is taken from the argument s (which is exactly what
	// was stored above), never re-read from r.services after the unlock —
	// a re-read would race a concurrent Wire (AC-17).
	return r.registerService(s)
}

// checkNotCopied rejects a method call on a struct-copied *RefuseAPI
// (SEC-020 family, mirroring engine.services' ServicesAPI.checkNotCopied).
// Lock-free — a single atomic.Pointer.Load — and therefore safe to run
// before mu is ever touched.
func (r *RefuseAPI) checkNotCopied(method string) error {
	if r.self.Load() != r {
		return errs.New(ErrCopiedValue, r.correlationID, map[string]any{"method": method})
	}
	return nil
}

// requireWired rejects a call that needs the registered outbound edges
// before Wire injected them (GR#20: never silently consume a nil
// dependency).
func (r *RefuseAPI) requireWired(method string) error {
	if err := r.checkNotCopied(method); err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.logistics == nil || r.services == nil {
		return errs.New(ErrDependencyNotWired, r.correlationID, map[string]any{"method": method})
	}
	return nil
}

// depsSnapshot is a point-in-time snapshot of the injected outbound
// dependency POINTERS (r.logistics, r.services), taken under r.mu.RLock.
// Wire replaces those fields under r.mu.Lock, so any method that calls into
// engine.logistics / engine.services MUST act on this snapshot and never
// re-read the field after releasing the lock — an unsynchronized re-read is
// an AC-17 data race (Destructive-MOD039 r6). r.wellbeing is deliberately
// absent: it is the WellbeingAPI interface seam, read only inside
// [RefuseAPI.Generate] under r.mu, so it needs no snapshot (the field was
// captured-but-never-consumed, SEC-138 clean-up).
type depsSnapshot struct {
	logistics *logistics.LogisticsAPI
	services  *services.ServicesAPI
}

// snapshotDeps captures the injected dependencies under r.mu.RLock so a
// caller can make outbound calls without racing a concurrent Wire.
func (r *RefuseAPI) snapshotDeps() depsSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return depsSnapshot{logistics: r.logistics, services: r.services}
}
