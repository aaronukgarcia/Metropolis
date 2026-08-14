package consumption

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// SourceType names a §17/§2.2 source node class. The list is
// network-specific by convention (documented per constant), not enforced
// here — a Source's Type is informational identity for engine.build and
// the generation-mix/pollution consumers (§18/§32, out of scope for this
// module), never a gate on what a network may host.
type SourceType string

const (
	// Water sources (§17/§2.2).
	SourceBorehole     SourceType = "borehole"     // chalk aquifer, turn 1
	SourceReservoir    SourceType = "reservoir"    // valley dam storage
	SourceDesalination SourceType = "desalination" // 3.8 kWh/m³ (§17.2)
	// Power sources (§2.2/§17).
	SourceSellindgeGrid SourceType = "sellindgeGrid" // off-map converter tranche
	SourceGenset        SourceType = "genset"        // diesel
	SourceWind          SourceType = "wind"
	SourceSolar         SourceType = "solar"
	SourceCCGT          SourceType = "ccgt" // gas combined-cycle
	SourceNuclear       SourceType = "nuclear"
	// Gas sources (§2.2).
	SourceOffMapPipeline SourceType = "offMapPipeline" // gas pipeline tranche
	SourceLNG            SourceType = "lng"            // port-fed
	// Wastewater sources.
	SourceTreatmentWorks SourceType = "treatmentWorks"
)

// Source is one supply node. Capacity is the nominal per-tick supply
// (litres or kWh); when Aquifer is non-nil (a borehole), the effective
// capacity is the aquifer's current, possibly-degraded sustainable yield
// (AC-8), and solving draws through [AquiferYield.Abstract].
type Source struct {
	ID       string
	Type     SourceType
	Capacity float64
	Aquifer  *AquiferYield // optional; boreholes only
}

// Edge is one pipe/wire run between two nodes. LengthKm drives loss
// (AC-6) and, for a cross-country run, land/routing cost (AC-7/§2.2).
type Edge struct {
	From     string
	To       string
	LengthKm float64
	Corridor bool // true = built within a road corridor (§2.2: free)
}

// Storage is one storage node (reservoir, water tower, battery, gas
// holder). Modelled as capacity for engine.build's sizing queries; the
// intra-tick charge/discharge arbitrage belongs to a later module and is
// out of scope here (AC-6's "storage nodes" are present and counted, but
// the daily solve treats stored supply as produced-in-tick).
type Storage struct {
	ID       string
	Capacity float64
}

// Network is one of the four §17 utility networks: a stateful set of
// sources, edges, and storage nodes plus the last solved allocation.
// Construct with [NewNetwork]; a zero-value Network is not usable.
//
// A *Network is safe for concurrent use by a single solver goroutine at a
// time; [Solve] mutates the last-solve state, so concurrent solves against
// the SAME Network require external serialisation (the four networks are
// solved independently, so four goroutines each owning its own Network
// need no shared lock — see the concurrency test, AC-17).
type Network struct {
	kind          Utility
	sources       []Source
	edges         []Edge
	storages      []Storage
	lastSolve     *SolveResult
	correlationID string
}

// NewNetwork constructs an empty network of the given kind (one of the
// four §17 utilities — UtilityWater/UtilityWastewater/UtilityPower/
// UtilityGas).
func NewNetwork(kind Utility, correlationID string) *Network {
	return &Network{kind: kind, correlationID: correlationID}
}

// Kind returns the utility this network models.
func (n *Network) Kind() Utility { return n.kind }

// AddSource appends a source to the network, rejecting (without appending)
// a source whose capacity is negative or non-finite — a negative capacity
// would produce a negative delivery (units destroyed) in the conserved
// solve (GR#1/GR#16). A source with a non-nil Aquifer must be a borehole
// in practice, but AddSource does not enforce that (a network is data, and
// its builder owns the mix).
func (n *Network) AddSource(s Source) error {
	if !num.IsFinite(s.Capacity) || s.Capacity < 0 {
		return errs.New(ErrInvalidSource, n.correlationID, map[string]any{
			"id":    s.ID,
			"value": s.Capacity,
		})
	}
	n.sources = append(n.sources, s)
	return nil
}

// AddEdge appends an edge to the network, rejecting (without appending) an
// edge whose length is negative or non-finite — a negative length would
// produce a negative loss fraction (GR#1/GR#16).
func (n *Network) AddEdge(e Edge) error {
	if !num.IsFinite(e.LengthKm) || e.LengthKm < 0 {
		return errs.New(ErrInvalidEdge, n.correlationID, map[string]any{
			"from":  e.From,
			"to":    e.To,
			"value": e.LengthKm,
		})
	}
	n.edges = append(n.edges, e)
	return nil
}

// AddStorage appends a storage node to the network, rejecting (without
// appending) a node whose capacity is negative or non-finite (GR#1/GR#16).
func (n *Network) AddStorage(st Storage) error {
	if !num.IsFinite(st.Capacity) || st.Capacity < 0 {
		return errs.New(ErrInvalidStorage, n.correlationID, map[string]any{
			"id":    st.ID,
			"value": st.Capacity,
		})
	}
	n.storages = append(n.storages, st)
	return nil
}

// Sources returns the network's sources in insertion order.
func (n *Network) Sources() []Source { return append([]Source(nil), n.sources...) }

// Edges returns the network's edges in insertion order.
func (n *Network) Edges() []Edge { return append([]Edge(nil), n.edges...) }

// Storages returns the network's storage nodes in insertion order.
func (n *Network) Storages() []Storage { return append([]Storage(nil), n.storages...) }

// TotalEdgeLengthKm returns the summed length of every edge — the single
// distance figure [LossFraction] and [Edge.Cost] scale by.
func (n *Network) TotalEdgeLengthKm() float64 {
	var total float64
	for _, e := range n.edges {
		total += e.LengthKm
	}
	return total
}

// LossFraction returns the fraction of source supply lost over this
// network's edges, monotonically increasing with total edge length (AC-6:
// a longer run loses more than a shorter one). Model:
//
//	loss = 1 - 1/(1 + lossPerKm * totalLengthKm)
//
// which grows toward 1 without ever reaching it. lossPerKm is a documented
// placeholder (see its constant) — §17 states only that networks HAVE
// losses, not a rate, so the exact magnitude is a candidate M2 Batch
// tuning figure, never a §17-transcribed number.
func (n *Network) LossFraction() float64 {
	x := lossPerKm * n.TotalEdgeLengthKm()
	return 1 - 1/(1+x)
}

// TotalCapacity returns the gross per-tick supply across all sources: each
// source contributes its Capacity, except a borehole (non-nil Aquifer)
// whose contribution is capped at the aquifer's CURRENT degraded yield
// (AC-8). This is the supply figure Solve draws from.
func (n *Network) TotalCapacity() float64 {
	var total float64
	for _, s := range n.sources {
		if s.Aquifer != nil {
			total += s.Aquifer.Current()
			continue
		}
		total += s.Capacity
	}
	return total
}

// Solve runs one conserved daily-tick allocation (AC-6). For every unit of
// demand, the result accounts for it as either delivered or shortfall:
//
//	Delivered + ShortfallTotal == Demand
//
// exactly (ShortfallTotal is computed as Demand - Delivered in a single
// subtraction). Losses are subtracted from what sources produce BEFORE
// delivery (never invented or dropped silently): the gross supply is
// reduced by [LossFraction], and that post-loss figure caps delivery.
//
// Allocation order under shortfall is deterministic (AC-15): consumers are
// served in sorted EntityRef order (ties broken by descending Demand —
// the larger draw is served first), never Go-map-iteration order. Sources
// are drawn in insertion order (a slice, not a map) and only up to the
// demand actually needing supply, so an aquifer's degradation keys off the
// water ACTUALLY abstracted, not the borehole's nominal capacity (AC-8).
func (n *Network) Solve(consumers []Consumer) (SolveResult, error) {
	var result SolveResult
	if len(n.sources) == 0 {
		return result, errs.New(ErrNoSource, n.correlationID, map[string]any{
			"network": string(n.kind),
		})
	}

	// Re-validate every source and edge (GR#1/GR#16, defence-in-depth):
	// AddSource/AddEdge reject invalid figures, but this guards against a
	// caller who ignored those errors (or a future direct-construction path),
	// exactly as engine.market guards its Load-time invariants at query time.
	for _, s := range n.sources {
		if !num.IsFinite(s.Capacity) || s.Capacity < 0 {
			return result, errs.New(ErrInvalidSource, n.correlationID, map[string]any{
				"id":    s.ID,
				"value": s.Capacity,
			})
		}
	}
	for _, e := range n.edges {
		if !num.IsFinite(e.LengthKm) || e.LengthKm < 0 {
			return result, errs.New(ErrInvalidEdge, n.correlationID, map[string]any{
				"from":  e.From,
				"to":    e.To,
				"value": e.LengthKm,
			})
		}
	}

	// Deterministic order FIRST (AC-15): every downstream step — validation
	// blame, the total-demand sum, and the per-consumer allocation — walks
	// consumers in a total order on (EntityRef asc, Demand desc), so a solve
	// is byte-identical regardless of the caller's input order (which may
	// itself come from a map iteration) and regardless of which of two
	// equal-ref consumers appeared first. Never Go-map-iteration-dependent.
	ordered := append([]Consumer(nil), consumers...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].EntityRef != ordered[j].EntityRef {
			return ordered[i].EntityRef < ordered[j].EntityRef
		}
		return ordered[i].Demand > ordered[j].Demand
	})

	// Validate every demand figure up front (GR#1/GR#16): a negative or
	// non-finite demand must not reach the conserved accounting, where it
	// would silently poison the Delivered + ShortfallTotal == Demand
	// invariant.
	for _, c := range ordered {
		if !num.IsFinite(c.Demand) || c.Demand < 0 {
			return result, errs.New(ErrInvalidDemand, n.correlationID, map[string]any{
				"network": string(n.kind),
				"entity":  c.EntityRef,
				"value":   c.Demand,
			})
		}
	}

	totalDemand := 0.0
	for _, c := range ordered {
		totalDemand += c.Demand
	}
	// Re-check the AGGREGATE after summation: two individually-finite
	// demands can sum to +Inf, which would silently poison the conserved
	// accounting if it ever reached it (GR#1/GR#16).
	if !num.IsFinite(totalDemand) {
		return result, errs.New(ErrInvalidDemand, n.correlationID, map[string]any{
			"network": string(n.kind),
			"entity":  "(aggregate)",
			"value":   totalDemand,
		})
	}

	// Draw only the gross supply the demand actually needs, from sources in
	// insertion order (deterministic). A borehole draws through its aquifer,
	// which degrades yield on over-abstraction — keyed off the actual draw
	// request, not the borehole's nominal capacity (AC-8).
	lossFraction := n.LossFraction()
	survive := 1 - lossFraction
	if survive < 0 {
		survive = 0
	}

	var gross, loss, postLoss float64
	if survive == 0 {
		// 100% (or effectively 100%) loss: nothing survives delivery, so
		// nothing is drawn and nothing is delivered — never an Inf-Inf NaN
		// masked as full delivery (round-2 sweep).
		gross, loss, postLoss = 0, 0, 0
	} else {
		requiredGross := totalDemand / survive
		if !num.IsFinite(requiredGross) {
			// Degenerate: meeting demand would require more gross supply than
			// float64 can represent. Reject rather than let gross overflow
			// into a non-finite loss/postLoss.
			return result, errs.New(ErrSolveOverflow, n.correlationID, map[string]any{
				"network": string(n.kind),
				"demand":  totalDemand,
				"survive": survive,
			})
		}

		for i := range n.sources {
			if gross >= requiredGross {
				break
			}
			s := &n.sources[i]
			want := requiredGross - gross
			if s.Aquifer != nil {
				draw, err := s.Aquifer.Abstract(minFloat(want, s.Capacity))
				if err != nil {
					return result, err
				}
				gross += draw
				continue
			}
			gross += minFloat(want, s.Capacity)
		}

		// Loss and surviving supply are computed multiplicatively (never
		// gross - loss), so an overflowed gross cannot produce Inf-Inf = NaN.
		loss = gross * lossFraction
		postLoss = gross * survive

		// Defence-in-depth: the draw loop caps gross at requiredGross (both
		// finite), so these are finite by construction — but guard anyway so
		// a future edit cannot reintroduce a non-finite result.
		if !num.IsFinite(loss) || !num.IsFinite(postLoss) {
			return result, errs.New(ErrSolveOverflow, n.correlationID, map[string]any{
				"network": string(n.kind),
				"demand":  totalDemand,
				"survive": survive,
			})
		}
	}

	delivered := minFloat(postLoss, totalDemand)
	result.Demand = totalDemand
	result.Produced = gross
	result.Loss = loss
	result.Delivered = delivered
	result.ShortfallTotal = totalDemand - delivered

	remaining := postLoss
	alloc := make([]ConsumerAllocation, 0, len(ordered))
	for _, c := range ordered {
		d := minFloat(remaining, c.Demand)
		remaining -= d
		alloc = append(alloc, ConsumerAllocation{
			EntityRef: c.EntityRef,
			Demand:    c.Demand,
			Delivered: d,
			Shortfall: c.Demand - d,
		})
	}
	result.PerConsumer = alloc

	n.lastSolve = &result
	return result, nil
}

// Shortfall returns the per-entity shortfall (demand - delivered) from the
// most recent solve for entityRef (AC-12) — the gap between demand and
// delivered supply that engine.wellbeing later consumes. Returns
// (0, ErrNoSolve) if no solve has run, and (0, ErrUnknownEntity) if the
// entity was not part of the last solve.
func (n *Network) Shortfall(entityRef string) (float64, error) {
	if n.lastSolve == nil {
		return 0, errs.New(ErrNoSolve, n.correlationID, map[string]any{"network": string(n.kind)})
	}
	for _, a := range n.lastSolve.PerConsumer {
		if a.EntityRef == entityRef {
			return a.Shortfall, nil
		}
	}
	return 0, errs.New(ErrUnknownEntity, n.correlationID, map[string]any{
		"network": string(n.kind),
		"entity":  entityRef,
	})
}

// LastSolve returns the most recent solve result and whether one exists.
func (n *Network) LastSolve() (SolveResult, bool) {
	if n.lastSolve == nil {
		return SolveResult{}, false
	}
	return *n.lastSolve, true
}

// BaseLoad returns the minimum (always-on) load across a demand profile —
// the floor generation/storage must serve every tick (§17.2/§2.2 sizing).
func (n *Network) BaseLoad(profile []float64) float64 {
	if len(profile) == 0 {
		return 0
	}
	base := profile[0]
	for _, v := range profile[1:] {
		base = minFloat(base, v)
	}
	return base
}

// PeakLoad returns the maximum load across a demand profile — the figure
// generation/storage must be sized against (AC-9), never the average.
func (n *Network) PeakLoad(profile []float64) float64 {
	if len(profile) == 0 {
		return 0
	}
	peak := profile[0]
	for _, v := range profile[1:] {
		peak = maxFloat(peak, v)
	}
	return peak
}

// Cost returns the §2.2 land/routing cost of this edge: zero for a
// corridor-routed edge (pipes/wires in road corridors are free — §2.2
// "built in road corridors for free"), and
// crossCountryCostPerKm × LengthKm for a cross-country run (AC-7).
func (e Edge) Cost() float64 {
	if e.Corridor {
		return 0
	}
	return e.LengthKm * crossCountryCostPerKm
}

// lossPerKm is the §17 "losses" placeholder: the per-kilometre loss
// coefficient feeding [Network.LossFraction]. §17 states networks have
// losses but gives no rate, so this is a plausible v1 default pending M2
// Batch tuning (the same convention as engine.season/engine.market's
// unstated-number placeholders) — a directional figure, never a
// spec-transcribed one. AC-6's test asserts monotonicity (longer run =>
// higher loss), which holds for any positive value.
const lossPerKm = 0.01 // 1% per km

// crossCountryCostPerKm is the §2.2 "cross-country at cost" placeholder:
// the per-kilometre extra land/routing cost of an off-road-corridor run.
// §2.2 gives no figure ("at cost"), so this is a plausible v1 default
// pending M2 Batch tuning — a directional figure (positive) so the AC-7
// corridor-vs-cross-country inequality holds, never a spec-transcribed one.
const crossCountryCostPerKm = 1000.0 // cost units per km (v1 placeholder)
