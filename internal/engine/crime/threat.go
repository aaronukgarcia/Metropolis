package crime

import "github.com/aaronukgarcia/Metropolis/internal/foundation/num"

// The MI5-analogue Security-Service threat dial (AC-11). A terror-threat
// event is NEVER an unconditioned per-tick coin flip wearing a cosmetic
// readout: it fires only after the threat level has been nonzero and rising
// for at least the data-loaded lead window, and its trigger probability is
// a pure function of exposure, Security Service funding, and liaison level.

// TriggerProbabilityFor is the pure trigger-probability function (AC-11):
// it rises with exposure and falls strictly with Security Service funding
// and liaison level. The coefficients are data-loaded (GR#15). The result
// is the probability USED when the precursor lead window has been met —
// before that, the probability is zero regardless of this value.
func TriggerProbabilityFor(exposure, funding, liaison float64, t threatConfig) float64 {
	ex := clampUnit(exposure)
	fd := clampUnit(funding) * t.FundingProbabilityDamping
	li := clampUnit(liaison) * t.DampingPerLiaison
	p := t.BaseTriggerProbability * (1 + ex) * (1 - fd) * (1 - li)
	if !num.IsFinite(p) || p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// advanceThreatLocked advances the citywide threat dial one month. Callers
// must hold a.mu.
func (a *CrimeAPI) advanceThreatLocked(month int64) {
	if err := a.checkNotCopied("advanceThreatLocked"); err != nil {
		return
	}
	cfg := a.cfg.Threat
	st := &a.threat
	sec := a.security

	// The threat level grows with exposure and with the city's organised
	// crime + smuggling activity (aggregated over sorted districts), and is
	// damped by Security Service funding and liaison level.
	activity := a.cityCrimeActivityLocked()
	growth := clampUnit(sec.Exposure)*cfg.GrowthPerExposure +
		clampUnit(activity)*cfg.GrowthPerExposure
	damping := clampUnit(sec.Funding)*cfg.DampingPerFunding +
		clampUnit(sec.Liaison)*cfg.DampingPerLiaison

	next := st.level + growth - damping
	if next < 0 {
		next = 0
	}
	if next > cfg.MaxLevel {
		next = cfg.MaxLevel
	}

	// The precursor is "the threat level has been nonzero for a sustained
	// run" — the visible rising/elevated intel that must precede any event.
	// A level that has RISEN off the quiet baseline and stays elevated keeps
	// the run alive; the run's start month is recorded for the lead-window
	// assertion (AC-11).
	if next > 0 {
		if st.elevatedMonths == 0 {
			st.lastRiseMonth = month
		}
		st.elevatedMonths++
	} else {
		st.elevatedMonths = 0
	}
	st.level = next

	// The precursor requirement: no draw is attempted until the threat level
	// has been nonzero for at least the lead window (AC-11's "never
	// random-spam"). Before that, the probability is effectively zero.
	if st.elevatedMonths < cfg.MinLeadMonths {
		return
	}

	p := TriggerProbabilityFor(sec.Exposure, sec.Funding, sec.Liaison, cfg)
	if p <= 0 {
		return
	}

	stream := detStream(a.seed, 0, month, "threat-event")
	if stream.Float64() < p {
		st.lastEventMonth = month
		st.elevatedMonths = 0
		st.level = 0
	}
}

// cityCrimeActivityLocked returns a normalised citywide organised-crime +
// smuggling pressure (deterministic sorted-district iteration, GR#21).
func (a *CrimeAPI) cityCrimeActivityLocked() float64 {
	if err := a.checkNotCopied("cityCrimeActivityLocked"); err != nil {
		return 0
	}
	total := 0.0
	for _, id := range a.sortedDistrictIDs() {
		st := a.districts[id]
		total += st.active[CrimeOrganised] + st.active[CrimeSmuggling]
	}
	// Normalise against the safety half-saturation so the activity fraction
	// is a comparable [0,1] pressure.
	return clampUnit(total / (total + a.cfg.Safety.HalfSaturationActiveCrime))
}
