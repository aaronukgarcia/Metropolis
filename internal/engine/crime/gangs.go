package crime

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// The §28 gang lifecycle (AC-6 formation, AC-7 four effects, AC-8 full-stack
// removal, AC-9 decapitation-without-regeneration respawn). A gang is a
// stateful entity with a real negative direction: it can be removed, and it
// can respawn under an incomplete removal — never a one-way "gangs appear"
// flag.

// advanceGangsLocked runs one district's gang month. Callers must hold a.mu.
func (a *CrimeAPI) advanceGangsLocked(month int64, st *districtState, in DistrictInput) {
	if err := a.checkNotCopied("advanceGangsLocked"); err != nil {
		return
	}
	cfg := a.cfg

	// Gang removal (AC-8): strength trends toward the removed state ONLY
	// when all four stack components are concurrently above their
	// thresholds. Clearance pressure alone, at any magnitude, does not
	// remove the gang.
	if g := a.gangInDistrictLocked(st.id); g != nil {
		fullStack :=
			st.clearance >= cfg.Gangs.LowClearanceThreshold && // clearance pressure (reversed low-clearance condition)
				in.PrisonAbsorption >= cfg.Gangs.RegenerationThreshold &&
				in.RegenerationInvestment >= cfg.Gangs.RegenerationThreshold &&
				in.PreventionInfrastructure >= cfg.Gangs.RegenerationThreshold
		if fullStack {
			g.Strength -= cfg.Gangs.StrengthDecayPerMonth
			if g.Strength <= cfg.Gangs.RemovedStrengthThreshold {
				a.removeGangLocked(g.ID)
			}
		}
		// Apply the four AC-7 effects for the surviving gang (below).
	}

	// Formation condition (AC-6): high youth unemployment + blight + low
	// clearance holding SIMULTANEOUSLY for a sustained run. Regeneration
	// investment above threshold breaks the run (which is exactly why
	// decapitation-without-regeneration respawns and with-regeneration does
	// not, AC-9).
	conditionsHold :=
		in.YouthUnemployment >= cfg.Gangs.YouthUnemploymentThreshold &&
			in.Blight >= cfg.Gangs.BlightThreshold &&
			st.effectiveClearance < cfg.Gangs.LowClearanceThreshold &&
			in.RegenerationInvestment < cfg.Gangs.RegenerationThreshold

	if conditionsHold {
		st.sustainedMonths++
	} else {
		st.sustainedMonths = 0
	}

	// A gang forms only if conditions held for the full consecutive run AND
	// no gang already holds the district (AC-6's sustained requirement, not
	// a peak-detection shortcut).
	if st.sustainedMonths >= cfg.Gangs.FormationMonths && a.gangInDistrictLocked(st.id) == nil {
		a.formGangLocked(month, st)
		st.sustainedMonths = 0
	}

	// AC-7 effects for any gang now holding the district.
	if g := a.gangInDistrictLocked(st.id); g != nil {
		a.applyGangEffectsLocked(month, st, g, in)
	}
}

// formGangLocked creates a named, tracked gang with a stable id (AC-6).
func (a *CrimeAPI) formGangLocked(month int64, st *districtState) {
	if err := a.checkNotCopied("formGangLocked"); err != nil {
		return
	}
	id := a.nextGangID
	a.nextGangID++

	name := a.cfg.Gangs.Names[int(id)%len(a.cfg.Gangs.Names)]

	// A deterministic territory cell-set (AC-7a): cell ids are drawn from
	// the counter-based stream, so the set is stable per gang and never
	// re-derived nondeterministically.
	stream := detStream(a.seed, st.id, month, "gang-territory")
	cellCount := 3 + int(stream.IntN(5)) // 3..7 cells
	territory := make([]uint64, 0, cellCount)
	seen := map[uint64]bool{}
	for i := 0; i < cellCount; i++ {
		cell := stream.At(uint64(i)) & 0xFFFF
		if seen[cell] {
			continue
		}
		seen[cell] = true
		territory = append(territory, cell)
	}

	g := &Gang{
		ID:        id,
		Name:      name,
		District:  st.id,
		FormedAt:  month,
		Strength:  1.0,
		Territory: territory,
	}
	a.gangs[id] = g
}

// removeGangLocked deletes the gang entity (used by full-stack removal).
func (a *CrimeAPI) removeGangLocked(id GangID) {
	if err := a.checkNotCopied("removeGangLocked"); err != nil {
		return
	}
	delete(a.gangs, id)
}

// applyGangEffectsLocked applies the four AC-7 effects for a surviving gang.
func (a *CrimeAPI) applyGangEffectsLocked(month int64, st *districtState, g *Gang, in DistrictInput) {
	if err := a.checkNotCopied("applyGangEffectsLocked"); err != nil {
		return
	}
	cfg := a.cfg

	// (c) Tax levy: a queryable levy on local businesses that raises
	// closures. Both are deterministic functions of the gang's strength and
	// territory size, queryable via Gang().
	levy := int64(float64(len(g.Territory)) * 1000 * g.Strength)
	g.TaxLevyMicroPounds = levy
	closures := int64(float64(len(g.Territory)) * g.Strength)
	if closures < 1 {
		closures = 1
	}
	g.BusinessClosures = closures

	// (d) Recruitment: draws from the matching demographic, reducing the
	// district's eligible pool (the figure AC-2/AC-3 drivers read from) for
	// the REST of this month. recruitedCumulative is the persistent record
	// (districtState's doc comment): next month's AdvanceMonth recomputes
	// eligiblePool fresh from the live pushed pool discounted by this
	// running total, rather than continuing to mutate a value that would
	// otherwise go stale the moment the live push stops matching it
	// (destructive round r1 fix).
	recruit := int64(float64(st.eligiblePool) * cfg.Gangs.RecruitmentRate * g.Strength)
	if recruit < 0 {
		recruit = 0
	}
	if recruit > st.eligiblePool {
		recruit = st.eligiblePool
	}
	st.eligiblePool -= recruit
	st.recruitedCumulative = num.SatAdd(st.recruitedCumulative, recruit)
	g.Recruited = num.SatAdd(g.Recruited, recruit)
}

// Decapitate removes a gang's leadership/territorial presence (AC-9). It is
// a hard delete of the entity — but it does NOT touch the underlying
// generative conditions (the sustained-months counter is left standing), so
// a decapitation issued WITHOUT concurrent regeneration investment lets a
// fresh gang re-form (a new id) within the respawn window, while the same
// decapitation WITH regeneration investment breaks the run and does not
// respawn. A nonexistent gang id is ErrInvalidDecapitation (AC-15).
func (a *CrimeAPI) Decapitate(id GangID) error {
	if err := a.checkNotCopied("Decapitate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.gangs[id]; !ok {
		return errs.New(ErrInvalidDecapitation, a.correlationID, map[string]any{"gang": id})
	}
	a.removeGangLocked(id)
	return nil
}
