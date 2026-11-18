package converge

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
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
	// Evaluated (BUG-1140) is how many field-by-tick comparisons were
	// actually made (exact and bounded: one per tick+field present on both
	// sides; distribution: one per full trailing window). A comparison
	// that evaluated nothing is never a pass (MET-H507/H508/H509).
	Evaluated int
	// FieldEvaluated (BUG-1140 r2) is the per-field evaluation count: one
	// entry per declared or reference-reported field. A passing report has
	// every count >= 1 (MET-H512 otherwise).
	FieldEvaluated map[string]int
	// ReferenceBuild / CandidateBuild (BUG-1097 AC-8) stamp which builds were
	// compared (reference = Go engine, candidate = browser). Set only by
	// CompareStamped; plain Compare leaves them empty.
	ReferenceBuild string
	CandidateBuild string
}

// CompareStamped is Compare plus build stamps: it refuses (validation
// error, MET-H506) when either build string is empty, then returns the
// Compare report carrying both stamps. Text() prints them.
func CompareStamped(domain, referenceBuild, candidateBuild string, ref, candidate Trajectory, contract Contract) (*Report, error) {
	if referenceBuild == "" || candidateBuild == "" {
		return nil, errs.New(codeBuildStampMissing, errs.NewCorrelationID(), map[string]any{
			"domain":         domain,
			"referenceBuild": referenceBuild,
			"candidateBuild": candidateBuild,
		})
	}
	rep := Compare(domain, ref, candidate, contract)
	rep.ReferenceBuild = referenceBuild
	rep.CandidateBuild = candidateBuild
	return rep, nil
}

// Text renders the report for humans: domain, both build stamps (when
// set), the verdict and every diff line, in Diffs order (deterministic).
func (r *Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "domain=%s pass=%t evaluated=%d\n", r.Domain, r.Pass, r.Evaluated)
	if r.ReferenceBuild != "" || r.CandidateBuild != "" {
		fmt.Fprintf(&b, "referenceBuild=%s\ncandidateBuild=%s\n", r.ReferenceBuild, r.CandidateBuild)
	}
	if len(r.FieldEvaluated) > 0 {
		names := make([]string, 0, len(r.FieldEvaluated))
		for k := range r.FieldEvaluated {
			names = append(names, k)
		}
		sort.Strings(names)
		b.WriteString("fieldEvaluated:")
		for _, k := range names {
			fmt.Fprintf(&b, " %s=%d", k, r.FieldEvaluated[k])
		}
		b.WriteString("\n")
	}
	for _, d := range r.Diffs {
		b.WriteString(d.String())
		b.WriteString("\n")
	}
	return b.String()
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
	rep := &Report{Domain: domain, FieldEvaluated: make(map[string]int)}
	ticks := ref.ticksInOrder()
	refIdx := ref.indexByTick()
	candIdx := candidate.indexByTick()

	// BUG-1140 r2: a duplicate tick on either side is a failure, never a
	// silent keep-the-last.
	checkDuplicateTicks(rep, "reference", ref)
	checkDuplicateTicks(rep, "candidate", candidate)

	reported := refReportedFields(ref)
	for _, field := range coveredFields(reported, contract) {
		tol, ok := contract[field]
		if !ok {
			rep.addDiff(FieldDiff{
				Field:  field,
				Reason: fmt.Sprintf("no tolerance registered for field (%s)", codeUnknownTolerance),
			})
			continue
		}
		checkReferenceCarriesField(rep, field, ticks, refIdx, reported)
		before := rep.Evaluated
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
		n := rep.Evaluated - before
		rep.FieldEvaluated[field] = n
		if n == 0 {
			rep.addDiff(FieldDiff{Field: field, Tier: tol.Tier, Reason: fmt.Sprintf("field never evaluated: zero comparisons made (%s)", codeFieldNeverEvaluated)})
		}
	}

	// BUG-1140: a gate that cannot evaluate must not pass. Each route to
	// zero evaluations is its own registry-coded failure.
	if rep.Evaluated == 0 {
		switch {
		case len(ticks) == 0:
			rep.addDiff(FieldDiff{Reason: fmt.Sprintf("empty reference trajectory: zero ticks evaluated (%s)", codeEmptyReference)})
		case len(refReportedFields(ref)) == 0:
			rep.addDiff(FieldDiff{Reason: fmt.Sprintf("reference reports no fields in %d samples: nothing evaluated (%s)", len(ref), codeNoFieldsReported)})
		default:
			rep.addDiff(FieldDiff{Reason: fmt.Sprintf("zero ticks evaluated of %d reference ticks (candidate has %d): nothing was compared (%s)", len(ticks), len(candidate.ticksInOrder()), codeZeroEvaluated)})
		}
	}

	rep.Pass = len(rep.Diffs) == 0
	return rep
}

// coveredFields is the sorted union of the fields the reference reports
// and the fields the contract declares: each must be evaluated.
func coveredFields(reported []string, contract Contract) []string {
	seen := make(map[string]bool, len(reported)+len(contract))
	for _, f := range reported {
		seen[f] = true
	}
	for f := range contract {
		seen[f] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// checkDuplicateTicks fails the report once per tick that appears more
// than once in t (MET-H510), in first-seen order.
func checkDuplicateTicks(rep *Report, side string, t Trajectory) {
	count := make(map[int64]int, len(t))
	for _, s := range t {
		count[s.Tick]++
	}
	for _, tick := range t.ticksInOrder() {
		if count[tick] > 1 {
			rep.addDiff(FieldDiff{Tick: tick, Reason: fmt.Sprintf("duplicate tick in %s trajectory: %d samples (%s)", side, count[tick], codeDuplicateTick)})
		}
	}
}

// checkReferenceCarriesField fails (MET-H511) when the reference lacks a
// declared/reported field: once at tick 0 when no reference sample ever
// reports it, else once per sample that omits it (a sparse reference).
func checkReferenceCarriesField(rep *Report, field string, ticks []int64, refIdx map[int64]Sample, reported []string) {
	any := false
	for _, f := range reported {
		if f == field {
			any = true
			break
		}
	}
	if !any {
		if len(ticks) > 0 {
			rep.addDiff(FieldDiff{Field: field, Reason: fmt.Sprintf("declared field never reported by the reference (%s)", codeFieldMissingFromReference)})
		}
		return
	}
	for _, tick := range ticks {
		if _, ok := refIdx[tick].Values[field]; !ok {
			rep.addDiff(FieldDiff{Field: field, Tick: tick, Reason: fmt.Sprintf("reference sample does not report this field (%s)", codeFieldMissingFromReference)})
		}
	}
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
		rep.Evaluated++
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
		rep.Evaluated++
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
		rep.Evaluated++

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
