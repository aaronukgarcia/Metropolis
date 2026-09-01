package converge

import (
	"fmt"
	"math"
	"sort"
)

// FieldDiff names one divergence Compare found: a field, the tick it
// was observed at (0 for a contract-registration problem that is not
// tied to any one tick — see Compare's codeUnknownTolerance path), the
// reference and candidate values (0 when one side is simply absent —
// Reason always says which), and a human-readable Reason.
type FieldDiff struct {
	Field  string
	Tier   Tier
	Tick   int64
	Ref    int64
	Got    int64
	Delta  int64
	Reason string
}

// String renders one diff line, e.g.
// "field=treasury tick=3 tier=bounded ref=1000000 got=1000250 delta=250 mismatch exceeds epsilon".
func (d FieldDiff) String() string {
	return fmt.Sprintf("field=%s tick=%d tier=%s ref=%d got=%d delta=%d %s",
		d.Field, d.Tick, d.Tier, d.Ref, d.Got, d.Delta, d.Reason)
}

// Report is Compare's result: the domain name, whether parity held
// (Pass == len(Diffs) == 0), and every divergence found — the readable
// before/after diff GR#27's rationale wants, and the gate a domain must
// clear before its store-flip (docs/planning/phase3-convergence-plan.md
// §4).
type Report struct {
	Domain string
	Pass   bool
	Diffs  []FieldDiff
}

func (r *Report) addDiff(d FieldDiff) {
	r.Diffs = append(r.Diffs, d)
}

// Compare checks candidate against ref (the Go engine's own reference
// trajectory for a Journal) under contract, and returns a Report naming
// every divergence found. Pure function of its three inputs (GR#21):
// iteration is driven by ref's own tick order (Trajectory.ticksInOrder)
// and by contract's field names in SORTED order (never Go's randomised
// map-range order), so two calls with the same inputs produce a
// byte-identical Report every time.
//
// A field ref reports (in any of its samples' Values) that has no entry
// in contract is a fail-closed condition (codeUnknownTolerance,
// MET-H502): Compare never silently treats an unconstrained field as
// passing (GR#15 spirit — a comparator's coverage must come from the
// contract's own data, never an implicit "whatever wasn't named is
// fine" default).
func Compare(domain string, ref, candidate Trajectory, contract Contract) *Report {
	rep := &Report{Domain: domain}
	ticks := ref.ticksInOrder()
	refIdx := ref.indexByTick()
	candIdx := candidate.indexByTick()

	for _, field := range refReportedFields(ref) {
		tol, ok := contract[field]
		if !ok {
			rep.addDiff(FieldDiff{
				Field:  field,
				Reason: fmt.Sprintf("no tolerance registered for field (%s)", codeUnknownTolerance),
			})
			continue
		}
		switch tol.Tier {
		case TierExact:
			compareExact(rep, field, ticks, refIdx, candIdx)
		case TierBounded:
			compareBounded(rep, field, ticks, refIdx, candIdx, tol)
		case TierDistribution:
			compareDistribution(rep, field, ticks, refIdx, candIdx, tol)
		default:
			rep.addDiff(FieldDiff{Field: field, Reason: fmt.Sprintf("unrecognised tolerance tier %d", tol.Tier)})
		}
	}

	rep.Pass = len(rep.Diffs) == 0
	return rep
}

// refReportedFields returns the sorted, deduplicated set of field names
// appearing in any of ref's samples' Values maps — the set Compare
// checks coverage for.
func refReportedFields(ref Trajectory) []string {
	seen := make(map[string]bool)
	for _, s := range ref {
		for k := range s.Values {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func compareExact(rep *Report, field string, ticks []int64, refIdx, candIdx map[int64]Sample) {
	for _, tick := range ticks {
		rv, ok := refIdx[tick].Values[field]
		if !ok {
			continue
		}
		cs, present := candIdx[tick]
		if !present {
			rep.addDiff(FieldDiff{Field: field, Tier: TierExact, Tick: tick, Ref: rv, Reason: "candidate has no sample for this tick"})
			continue
		}
		cv, ok := cs.Values[field]
		if !ok {
			rep.addDiff(FieldDiff{Field: field, Tier: TierExact, Tick: tick, Ref: rv, Reason: "candidate sample does not report this field"})
			continue
		}
		if rv != cv {
			rep.addDiff(FieldDiff{Field: field, Tier: TierExact, Tick: tick, Ref: rv, Got: cv, Delta: cv - rv, Reason: "exact mismatch"})
		}
	}
}

func compareBounded(rep *Report, field string, ticks []int64, refIdx, candIdx map[int64]Sample, tol Tolerance) {
	for _, tick := range ticks {
		rv, ok := refIdx[tick].Values[field]
		if !ok {
			continue
		}
		cs, present := candIdx[tick]
		if !present {
			rep.addDiff(FieldDiff{Field: field, Tier: TierBounded, Tick: tick, Ref: rv, Reason: "candidate has no sample for this tick"})
			continue
		}
		cv, ok := cs.Values[field]
		if !ok {
			rep.addDiff(FieldDiff{Field: field, Tier: TierBounded, Tick: tick, Ref: rv, Reason: "candidate sample does not report this field"})
			continue
		}
		delta := cv - rv
		abs := delta
		if abs < 0 {
			abs = -abs
		}
		if abs > tol.Epsilon {
			rep.addDiff(FieldDiff{
				Field: field, Tier: TierBounded, Tick: tick, Ref: rv, Got: cv, Delta: delta,
				Reason: fmt.Sprintf("|delta|=%d exceeds epsilon=%d", abs, tol.Epsilon),
			})
		}
	}
}

func compareDistribution(rep *Report, field string, ticks []int64, refIdx, candIdx map[int64]Sample, tol Tolerance) {
	window := tol.Window
	if window < 1 {
		window = 1
	}
	var refSeries, candSeries []int64

	for _, tick := range ticks {
		rv, ok := refIdx[tick].Values[field]
		if !ok {
			continue
		}
		cs, present := candIdx[tick]
		if !present {
			rep.addDiff(FieldDiff{Field: field, Tier: TierDistribution, Tick: tick, Ref: rv, Reason: "candidate has no sample for this tick"})
			continue
		}
		cv, ok := cs.Values[field]
		if !ok {
			rep.addDiff(FieldDiff{Field: field, Tier: TierDistribution, Tick: tick, Ref: rv, Reason: "candidate sample does not report this field"})
			continue
		}
		refSeries = append(refSeries, rv)
		candSeries = append(candSeries, cv)

		n := len(refSeries)
		if n < window {
			continue // not enough trailing history yet — never misreported as a divergence
		}
		refMean := meanOf(refSeries[n-window:])
		candMean := meanOf(candSeries[n-window:])

		var bandOK bool
		if refMean == 0 {
			bandOK = candMean == 0
		} else {
			band := tol.BandPct * math.Abs(refMean)
			bandOK = math.Abs(refMean-candMean) <= band
		}
		if !bandOK {
			rep.addDiff(FieldDiff{
				Field: field, Tier: TierDistribution, Tick: tick,
				Ref: int64(math.Round(refMean)), Got: int64(math.Round(candMean)),
				Delta:  int64(math.Round(candMean - refMean)),
				Reason: fmt.Sprintf("trailing-%d-sample mean diverges: refMean=%.4f candMean=%.4f band=%.4f%%", window, refMean, candMean, tol.BandPct*100),
			})
		}
	}
}

func meanOf(vals []int64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum int64
	for _, v := range vals {
		sum += v
	}
	return float64(sum) / float64(len(vals))
}
