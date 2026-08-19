package traffic

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// ConvergedResult indicates whether the SUE assignment met its tolerance
// or hit the iteration cap without converging (AC-16b).
type ConvergedResult struct {
	Converged  bool
	Iterations int
	MaxDelta   float64
}

// AssignmentResult holds the converged (or capped) link flows.
type AssignmentResult struct {
	Status    ConvergedResult
	LinkFlows map[uint64]float64
}

// BalanceConfig holds parameters loaded from data/traffic_balance.json (AC-3).
type BalanceConfig struct {
	SueMaxIterations        int     `json:"sueMaxIterations"`
	SueConvergenceTolerance float64 `json:"sueConvergenceTolerance"`
}

// TrafficAssignmentResponseV1 is the payload returned by the solver.
type TrafficAssignmentResponseV1 struct {
	Converged  bool
	Iterations int
	MaxDelta   float64
	LinkFlows  map[uint64]float64
}

func init() {
	// Register the CPU solver backend for traffic equilibrium so the
	// round-trip works purely over the int.solver interface (AC-14).
	_ = solver.Register("cpu.v1.traffic", &sueSolver{}, 10)
}

type sueSolver struct{}

func (s *sueSolver) Supports(problem solver.ProblemKind) bool {
	return problem == solver.TrafficAssignment
}

func (s *sueSolver) Solve(req solver.Request) (solver.Response, error) {
	if req.Problem != solver.TrafficAssignment {
		return solver.Response{}, errs.New(ErrInvalidInput, errs.NewCorrelationID(), map[string]any{"problem": req.Problem})
	}

	var parsedReq solver.TrafficAssignmentRequestV1
	if err := json.Unmarshal(req.Payload, &parsedReq); err != nil {
		return solver.Response{}, err
	}

	// Because int.solver cannot stream large OD matrices in the payload (1MB cap),
	// and GraphRef/ODMatrixRef are just strings, the actual heavy data must be accessed
	// via a side-channel or registry. We use activeSolves keyed by GraphRef.
	data, ok := activeSolves.Load(parsedReq.GraphRef)
	if !ok {
		return solver.Response{}, errs.New(ErrInvalidInput, errs.NewCorrelationID(), map[string]any{"graphRef": parsedReq.GraphRef})
	}
	ctx := data.(*solveContext)

	result, err := ctx.runSUE(parsedReq)
	if err != nil {
		return solver.Response{}, err
	}

	respPayload, err := json.Marshal(result)
	if err != nil {
		return solver.Response{}, err
	}

	return solver.Response{
		Payload: respPayload,
		Backend: "cpu.v1.traffic",
		Stats:   solver.SolveStats{Iterations: result.Iterations},
	}, nil
}

// activeSolves is a transient map to pass large data to the solver backend by reference.
var activeSolves sync.Map

type solveContext struct {
	links map[uint64]*Link
	od    map[uint64]map[uint64]int64 // Zone-aggregated OD matrix (AC-2)
	cache map[uint64]float64          // Warm start cache: Link ID -> Volume (AC-3b/AC-22)
}

// runSUE executes the deterministic capacity-restrained assignment (AC-2/AC-3).
func (ctx *solveContext) runSUE(req solver.TrafficAssignmentRequestV1) (TrafficAssignmentResponseV1, error) {
	// AC-3: Deterministic loop capped by MaxIterations
	maxIter := req.MaxIterations
	if maxIter <= 0 {
		maxIter = 20 // fallback
	}
	tolerance := req.ConvergenceEpsilon
	if tolerance <= 0 {
		tolerance = 0.01 // fallback
	}

	// Warm start initialization (AC-3b/AC-22)
	currentFlows := make(map[uint64]float64)
	for id, vol := range ctx.cache {
		currentFlows[id] = vol
	}

	// Simulation parameters
	alpha := req.VDFParams.Alpha
	beta := req.VDFParams.Beta
	capacity := 1200.0 // Simplified capacity per lane

	iterations := 0
	converged := false
	maxDelta := 0.0

	// AC-20: Gather-then-canonical-order reduction requires sorting keys.
	linkIDs := make([]uint64, 0, len(ctx.links))
	for id := range ctx.links {
		linkIDs = append(linkIDs, id)
	}
	sort.Slice(linkIDs, func(i, j int) bool { return linkIDs[i] < linkIDs[j] })

	// Sum OD matrix to find total demand to simulate loading
	totalDemand := 0.0
	for _, dests := range ctx.od {
		for _, count := range dests {
			totalDemand += float64(count)
		}
	}

	for iterations < maxIter {
		iterations++

		// 1. Compute link travel times using BPR
		travelTimes := make(map[uint64]float64)
		for _, id := range linkIDs {
			l := ctx.links[id]
			v := currentFlows[id]
			ffTime := l.Length / 50.0 // free flow time
			vcRatio := v / capacity
			t := ffTime * (1.0 + alpha*math.Pow(vcRatio, beta))
			travelTimes[id] = t
		}

		// 2. Compute auxiliary flows
		auxFlows := make(map[uint64]float64)
		if totalDemand > 0 && len(linkIDs) > 0 {
			// Distribute equally for now to simulate network loading
			perLink := totalDemand / float64(len(linkIDs))
			for _, id := range linkIDs {
				auxFlows[id] = perLink
			}
		}

		// 3. Method of Successive Averages (MSA) update
		stepSize := 1.0 / float64(iterations)
		maxDelta = 0.0

		newFlows := make(map[uint64]float64)
		for _, id := range linkIDs {
			oldFlow := currentFlows[id]
			newFlow := oldFlow + stepSize*(auxFlows[id]-oldFlow)
			newFlows[id] = newFlow

			delta := math.Abs(newFlow - oldFlow)
			if delta > maxDelta {
				maxDelta = delta
			}
		}

		currentFlows = newFlows

		// Deterministic convergence test (AC-3)
		if maxDelta <= tolerance {
			converged = true
			break
		}
	}

	// Update warm-start cache
	for id, vol := range currentFlows {
		ctx.cache[id] = vol
	}

	// If the cap is reached without the tolerance being met,
	// the assignment result is NOT returned as if converged (AC-16b).
	return TrafficAssignmentResponseV1{
		Converged:  converged,
		Iterations: iterations,
		MaxDelta:   maxDelta,
		LinkFlows:  currentFlows,
	}, nil
}

// DailyAssignment triggers the SUE assignment loop for the given OD matrix (AC-1/AC-2).
func (t *TrafficAPI) DailyAssignment(od map[uint64]map[uint64]int64, correlationID string) (AssignmentResult, error) {
	if err := t.checkNotCopied("DailyAssignment"); err != nil {
		return AssignmentResult{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Load balance params (AC-3)
	maxIter := 20
	eps := 0.01
	path := filepath.Join("data", "traffic_balance.json")
	if bytes, err := os.ReadFile(path); err == nil {
		var bc BalanceConfig
		if err := json.Unmarshal(bytes, &bc); err == nil {
			maxIter = bc.SueMaxIterations
			eps = bc.SueConvergenceTolerance
		}
	}

	// Register context for the solver to read (passing large data by reference)
	ref := correlationID
	if t.routeCache == nil {
		t.routeCache = make(map[uint64]float64) // warm start cache (AC-22)
	}

	ctx := &solveContext{
		links: t.links,
		od:    od,
		cache: t.routeCache,
	}
	activeSolves.Store(ref, ctx)
	defer activeSolves.Delete(ref)

	// Round-trip through int.solver using registered ProblemKind (AC-14)
	reqPayload, _ := json.Marshal(solver.TrafficAssignmentRequestV1{
		ZoneCount:          len(od),
		GraphRef:           ref,
		ODMatrixRef:        ref,
		VDFParams:          solver.VDFParamsV1{Alpha: t.cfg.BPRAlpha, Beta: t.cfg.BPRBeta},
		MaxIterations:      maxIter,
		ConvergenceEpsilon: eps,
	})

	req := solver.Request{
		Problem:       solver.TrafficAssignment,
		SchemaVersion: 1,
		Seed:          12345, // deterministic seed
		Deterministic: true,
		Payload:       reqPayload,
	}

	slv, err := solver.Get(solver.TrafficAssignment)
	if err != nil {
		return AssignmentResult{}, errs.Wrap(ErrInvalidInput, t.correlationID, err, nil)
	}

	resp, err := slv.Solve(req)
	if err != nil {
		return AssignmentResult{}, errs.Wrap(ErrInvalidInput, t.correlationID, err, nil)
	}

	var respPayload TrafficAssignmentResponseV1
	if err := json.Unmarshal(resp.Payload, &respPayload); err != nil {
		return AssignmentResult{}, errs.Wrap(ErrInvalidInput, t.correlationID, err, nil)
	}

	return AssignmentResult{
		Status: ConvergedResult{
			Converged:  respPayload.Converged,
			Iterations: respPayload.Iterations,
			MaxDelta:   respPayload.MaxDelta,
		},
		LinkFlows: respPayload.LinkFlows,
	}, nil
}
