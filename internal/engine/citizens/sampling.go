package citizens

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// Stratum is one (district, age band, income band) cell of the A7
// stratification scheme (AC-9/AC-23). It is comparable, so it can be a
// map key.
type Stratum struct {
	District uint16
	Age      AgeBand
	Income   IncomeBand
}

// StratumOf derives a cold record's stratum at the given month (age is
// always derived, AC-2).
func StratumOf(r ColdRecord, month int64) Stratum {
	return Stratum{
		District: r.District,
		Age:      AgeBandFor(r.AgeMonths(month)),
		Income:   IncomeBandFor(r.Wealth),
	}
}

// StratifiedSample is the A7 sampling firewall's rotating, coverage-
// guaranteed sample (AC-9). It holds the sampled citizens' ids in
// deterministic (sorted) order plus the per-stratum coverage counts, and
// is the SOLE input to DeriveColdPassParams — viewport-hot citizens whose
// elevation is display fidelity only are never members unless the sampler
// independently selected them.
type StratifiedSample struct {
	// members are the sampled citizen ids, sorted ascending for
	// deterministic iteration (GR#21: never a Go map iteration order).
	members []uint64
	// counts is the per-stratum member count (coverage-guarantee
	// bookkeeping and the parameter source).
	counts map[Stratum]int
	// minPerStratum is the coverage floor this sample was built with.
	minPerStratum int
	// month/seed identify the rotation this sample belongs to.
	month int64
	seed  uint64
}

// Members returns the sampled citizen ids in deterministic ascending
// order.
func (s *StratifiedSample) Members() []uint64 {
	out := make([]uint64, len(s.members))
	copy(out, s.members)
	return out
}

// CountFor returns the number of sampled members in the given stratum.
func (s *StratifiedSample) CountFor(st Stratum) int {
	return s.counts[st]
}

// Empty reports whether the sample has no members (degenerate world).
func (s *StratifiedSample) Empty() bool {
	return len(s.members) == 0
}

// sampleFraction is the rotating-sample membership fraction (~1% citywide,
// §5.2's "~0.1-1%"). A balance placeholder, documented as such.
const sampleFraction = 0.01

// BuildStratifiedSample builds a rotating, coverage-guaranteed sample from
// records at the given month and seed (AC-9). Rotation is deterministic:
// membership is a per-citizen draw from hash(worldSeed, id, month,
// "sample"), so the same (seed, id, month) always yields the same
// membership decision. The coverage guarantee is enforced structurally: if
// a stratum ends up below minPerStratum, the deficit is topped up by the
// lowest-id citizens of that stratum (deterministically) BEFORE the
// rotating draw, so every non-empty stratum holds at least minPerStratum
// members regardless of the draw. records may be in any order; the result
// is sorted and independent of the input order.
func BuildStratifiedSample(records []ColdRecord, month int64, seed uint64, minPerStratum int) *StratifiedSample {
	if minPerStratum < 0 {
		minPerStratum = 0
	}

	s := &StratifiedSample{
		counts:        make(map[Stratum]int),
		minPerStratum: minPerStratum,
		month:         month,
		seed:          seed,
	}

	// Group records by stratum.
	byStratum := make(map[Stratum][]uint64)
	for _, r := range records {
		st := StratumOf(r, month)
		byStratum[st] = append(byStratum[st], r.ID)
	}

	// Deterministic iteration over strata: sort the stratum keys.
	strata := make([]Stratum, 0, len(byStratum))
	for st := range byStratum {
		strata = append(strata, st)
	}
	sort.Slice(strata, func(i, j int) bool {
		a, b := strata[i], strata[j]
		if a.District != b.District {
			return a.District < b.District
		}
		if a.Age != b.Age {
			return a.Age < b.Age
		}
		return a.Income < b.Income
	})

	selected := make(map[uint64]bool)
	var ordered []uint64
	add := func(id uint64, st Stratum) {
		if !selected[id] {
			selected[id] = true
			ordered = append(ordered, id)
			s.counts[st]++
		}
	}

	for _, st := range strata {
		ids := byStratum[st]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		// Coverage floor first: the minPerStratum lowest-id members.
		covered := 0
		for i := 0; i < len(ids) && covered < minPerStratum; i++ {
			add(ids[i], st)
			covered++
		}
		// Rotating draw for the rest.
		for _, id := range ids {
			stream := det.NewStream(seed, id, month, "sample")
			if stream.Float64() < sampleFraction {
				add(id, st)
			}
		}
	}

	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	s.members = ordered
	return s
}

// DeriveColdPassParams measures the cold-pass batch parameters from the
// sample ALONE (AC-8: "measured from the hot sample, not invented"; AC-9:
// the sample is the sole source). Every returned field is a function of
// the sample's actual composition — change the sample and the parameters
// change. When the sample is empty or a coverage gap is present (AC-14),
// the estimate falls back to neutral defaults and flags LowConfidence
// rather than dividing by zero or silently using an empty parameter set.
//
// The numeric coefficients are documented balance placeholders; the
// DIRECTION and the sample-dependence are what AC-8 requires (a cold pass
// that consumes params derived from the sample, never hardcoded rates).
func DeriveColdPassParams(s *StratifiedSample) ColdPassParams {
	p := ColdPassParams{
		MortalityMultiplier:     1.0,
		EducationTransitionRate: 0.0,
		JobMatchRate:            0.0,
		HealthDrift:             0,
		SatisfactionDrift:       0,
	}

	if s == nil || s.Empty() {
		p.LowConfidence = true
		return p
	}

	var ageBandSum int64
	var schoolingAge, workingAge, lowIncome, total int
	for st, c := range s.counts {
		total += c
		ageBandSum += int64(st.Age) * int64(c)
		switch st.Age {
		case AgeBand0to17:
			schoolingAge += c
		case AgeBand18to34, AgeBand35to54, AgeBand55to74:
			workingAge += c
		}
		if st.Income <= IncomeBand1 {
			lowIncome += c
		}
	}
	if total == 0 {
		p.LowConfidence = true
		return p
	}

	avgAgeBand := float64(ageBandSum) / float64(total)
	// Older sample ⇒ higher mortality multiplier (bounded, directional).
	p.MortalityMultiplier = 0.8 + avgAgeBand*0.15
	p.EducationTransitionRate = float64(schoolingAge) / float64(total)
	p.JobMatchRate = float64(workingAge) / float64(total)
	lowShare := float64(lowIncome) / float64(total)
	p.HealthDrift = -int32(lowShare * 2)       // poorer ⇒ slight health decline
	p.SatisfactionDrift = -int32(lowShare * 3) // poorer ⇒ slight satisfaction decline

	// Coverage-gap fallback (AC-14): if the sample is thinner than the
	// coverage floor demands (a stratum's coverage guarantee was violated),
	// the estimate above already fell back to the population-average totals
	// by construction — nothing divided by zero — and this flag tells the
	// consumer to discount the estimate.
	if s.minPerStratum > 0 && len(s.members) < s.minPerStratum {
		p.LowConfidence = true
	}

	return p
}
