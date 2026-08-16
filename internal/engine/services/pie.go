package services

import "fmt"

// This file is the §54 Public Service Pie (AC-5, AC-6, AC-17): the default
// per-1,000-population benchmark staffing ratios and the scale-dependent
// consequence curve that turns a relative staffing shortfall into a
// village-mild vs city-systemic outcome.
//
// The ratios are a DEFAULT BENCHMARK, not a hard requirement — that
// distinction is the whole point of §54's "the player adjusts every slice"
// framing: the player deviates from the benchmark and feels the
// consequence, so there is no floor the sim enforces. The police ~2.4/1k
// figure is transcribed from §54 (the only number the spec gives); the
// other seven categories are present as data-driven placeholders pending
// the M2 Batch (see data/services.json's per-benchmark placeholder flag
// and engine.services.md AC-5's escalation).

// benchmarkFor resolves a Pie benchmark id against the loaded pie table,
// returning ErrUnknownServiceKind for an id that is not present (a Pie
// category and a service kind are distinct namespaces; a dangling
// kind.benchmark reference is a data-authoring error, rejected loudly).
func (a *ServicesAPI) benchmarkFor(id string) (PieBenchmark, error) {
	if err := a.checkNotCopied("benchmarkFor"); err != nil {
		return PieBenchmark{}, err
	}
	for _, b := range a.pie {
		if b.ID == id {
			return b, nil
		}
	}
	return PieBenchmark{}, serviceErr(a.correlationID, ErrServiceDataInvalid, map[string]any{
		"benchmark": id,
		"cause":     fmt.Sprintf("benchmark %q not present in services.json pie table", id),
	})
}

// BenchmarkRatio returns the loaded Pie benchmark with the given id
// (AC-5's "all eight named categories present, police = 2.4" query path).
// It returns a registry-sourced error for an id that is not present.
func (a *ServicesAPI) BenchmarkRatio(id string) (PieBenchmark, error) {
	if err := a.checkNotCopied("BenchmarkRatio"); err != nil {
		return PieBenchmark{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.benchmarkFor(id)
}

// StaffingNeed returns the benchmark staffing requirement for a service
// kind at a population: the kind's Pie benchmark ratio × population
// (per 1,000 for per-1k categories). For a per-pupil benchmark (teachers)
// the generic framework has no pupil count, so the caller supplies the
// pupil population as the second argument — staff = PerPupil × pupils.
// A kind with no Benchmark (or a negative population) yields zero staff
// with no error: "no benchmark ⇒ no per-1k staffing requirement" is a
// legitimate empty result, distinct from the unregistered-kind error
// (AC-11) which a negative/unknown kind still surfaces elsewhere.
func (a *ServicesAPI) StaffingNeed(kind ServiceKind, population float64) (float64, error) {
	if err := a.checkNotCopied("StaffingNeed"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	def, ok := a.kinds[kind]
	if !ok {
		return 0, serviceErr(a.correlationID, ErrUnknownServiceKind, map[string]any{"kind": string(kind)})
	}
	if def.Benchmark == "" || population < 0 {
		return 0, nil
	}
	b, err := a.benchmarkFor(def.Benchmark)
	if err != nil {
		return 0, err
	}
	if b.PerThousand != 0 {
		return b.PerThousand * population / 1000.0, nil
	}
	return b.PerPupil * population, nil
}

// ShortfallImpact computes §54's scale-dependent consequence of a relative
// staffing shortfall: the shortfall fraction (e.g. 0.10 for "10% below
// benchmark") multiplied by a severity that saturates with population —
// severity(pop) = pop / (pop + halfPoint). At a village population the
// severity is near zero ("a quiet month"); at city scale it approaches one
// ("§28 arithmetic"). halfPoint is data/services.json's
// severityHalfPointPopulation (GR#15: the curve's shape is data, not a
// hardcoded constant).
//
// The result is a pure function of its three inputs (AC-14, AC-6): an
// identical shortfall produces a materially smaller impact at a small
// population than at a large one.
func ShortfallImpact(shortfallFraction, population, halfPoint float64) float64 {
	if shortfallFraction <= 0 || population <= 0 || halfPoint <= 0 {
		return 0
	}
	severity := population / (population + halfPoint)
	impact := shortfallFraction * severity
	return clamp01(impact)
}
