package education

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Pupil is engine.education's per-citizen pipeline record — the single
// source of truth for a pupil's stage and quality-weighted attainment
// (AC-6). The citizen record's education snapshot is citizens-owned; this
// module computes and holds the attainment score it derives from realised
// funding-quality, and writes the personality effect through citizens'
// command path (AC-7).
type Pupil struct {
	CitizenID  uint64
	Stage      Stage
	EnrolMonth int64
	// Attainment is the cumulative quality-weighted attainment score
	// (signed: positive = above-baseline schooling, negative = below).
	Attainment int32
	// LastDrift is the ambition-axis drift delta applied on the most recent
	// stage transition (positive = widening, negative = narrowing) — the
	// per-stage, immediately-inspectable drift AC-7 checks (AC-7's "sign and
	// shape" is checkable here, not after a 20-year compounding).
	LastDrift int32
	// SchoolID is the school this pupil attends (0 = none); school-run
	// trip generation keys off it (AC-5).
	SchoolID uint64
}

// DepartureReason is why a pupil left the pipeline (AC-10's independent
// departure terms).
type DepartureReason uint8

const (
	DepartureDeceased DepartureReason = iota
	DepartureEmigrated
	DepartureDroppedOut
)

// valid reports whether reason is one of the three documented departure
// reasons (deceased, emigrated, dropped out). A caller-supplied out-of-range
// value (e.g. DepartureReason(99)) is rejected at the write boundary so the
// ledger switch in StageLedger can never silently skip a departure term and
// break the AC-10 conservation identity.
func (r DepartureReason) valid() bool {
	switch r {
	case DepartureDeceased, DepartureEmigrated, DepartureDroppedOut:
		return true
	default:
		return false
	}
}

// cohortEventKind is the taxonomy of cohort-accounting mutations. Each kind
// feeds exactly ONE term of the AC-10 identity — none is ever derived as a
// balancing remainder.
type cohortEventKind uint8

const (
	// eventIntake: a pupil entered the stage (fresh enrolment or promotion-in).
	eventIntake cohortEventKind = iota
	// eventPromoted: a pupil left the stage for its single next stage.
	eventPromoted
	// eventForkedOut: a pupil left secondary via the three-way fork.
	eventForkedOut
	// eventDeparted: a pupil left the stage via death/emigration/dropout.
	eventDeparted
)

// cohortEvent is one row of the AC-10 accounting log. Stage is the stage
// whose enrolled count the event mutates; Reason is meaningful only for
// eventDeparted.
type cohortEvent struct {
	Month     int64
	Stage     Stage
	Kind      cohortEventKind
	CitizenID uint64
	Reason    DepartureReason
}

// MonthlyTerms is the per-month decomposition of the AC-10 identity for one
// stage:
//
//	EnrolledThisMonth == EnrolledLastMonth
//	    + Intake - Promoted - ForkedOut - DroppedOut - Deceased - Emigrated
//
// Every term is summed independently from its own event kind.
type MonthlyTerms struct {
	Month      int64
	Intake     int64
	Promoted   int64
	ForkedOut  int64
	DroppedOut int64
	Deceased   int64
	Emigrated  int64
}

// Enrolment returns the live enrolled-pupil count for a stage (AC-1's
// enrolment query). An out-of-range Stage (a malformed uint8 value in
// [numStages, 255]) is rejected with ErrStageNotRegistered rather than
// indexing enrolled[s] out of bounds and panicking (AC-12).
func (a *EducationAPI) Enrolment(s Stage) (int64, error) {
	if err := a.checkNotCopied("Enrolment"); err != nil {
		return 0, err
	}
	if !validStage(s) {
		return 0, errs.New(ErrStageNotRegistered, a.correlationID, map[string]any{"stage": s.String()})
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.enrolled[s], nil
}

// StageLedger returns the per-month accounting terms for a stage, in
// ascending month order (GR#21), derived from the event log. It is the
// "independently sourced" half of AC-10: each term is summed from its own
// event kind, so a reconciliation failure is a real tracking bug, not a
// tautology.
func (a *EducationAPI) StageLedger(s Stage) []MonthlyTerms {
	if err := a.checkNotCopied("StageLedger"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	byMonth := make(map[int64]*MonthlyTerms)
	var order []int64
	for _, ev := range a.events {
		if ev.Stage != s {
			continue
		}
		t, ok := byMonth[ev.Month]
		if !ok {
			t = &MonthlyTerms{Month: ev.Month}
			byMonth[ev.Month] = t
			order = append(order, ev.Month)
		}
		switch ev.Kind {
		case eventIntake:
			t.Intake++
		case eventPromoted:
			t.Promoted++
		case eventForkedOut:
			t.ForkedOut++
		case eventDeparted:
			switch ev.Reason {
			case DepartureDeceased:
				t.Deceased++
			case DepartureEmigrated:
				t.Emigrated++
			case DepartureDroppedOut:
				t.DroppedOut++
			}
		}
	}

	// Sort the month keys (they are appended in event order, but events are
	// appended in deterministic order so this is belt-and-braces for GR#21).
	seen := make(map[int64]bool)
	uniq := order[:0]
	for _, m := range order {
		if !seen[m] {
			seen[m] = true
			uniq = append(uniq, m)
		}
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })

	out := make([]MonthlyTerms, 0, len(uniq))
	for _, m := range uniq {
		out = append(out, *byMonth[m])
	}
	return out
}

// recordEvent appends one accounting event and applies its delta to the
// live enrolled count. The caller holds a.mu.
func (a *EducationAPI) recordEvent(ev cohortEvent) {
	a.events = append(a.events, ev)
	switch ev.Kind {
	case eventIntake:
		a.enrolled[ev.Stage]++
	default:
		a.enrolled[ev.Stage]--
	}
}

// removePupilLocked removes a pupil from the pipeline and records the
// departure term on the pupil's current stage. The caller holds a.mu.
func (a *EducationAPI) removePupilLocked(id uint64, reason DepartureReason, month int64) {
	p, ok := a.pupils[id]
	if !ok {
		return
	}
	a.recordEvent(cohortEvent{Month: month, Stage: p.Stage, Kind: eventDeparted, CitizenID: id, Reason: reason})
	delete(a.pupils, id)
}

// RemovePupil removes a pupil from the pipeline for a documented reason
// (death, emigration, or dropout) — the education-side mirror of the
// corresponding engine.citizens life event. It records the departure as its
// own independently-sourced AC-10 term.
func (a *EducationAPI) RemovePupil(id uint64, reason DepartureReason, month int64) error {
	if err := a.checkNotCopied("RemovePupil"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Validate the reason at the write boundary BEFORE any mutation: an
	// out-of-range DepartureReason would otherwise delete the pupil while the
	// ledger switch recorded no term, breaking AC-10's conservation identity.
	if !reason.valid() {
		return errs.New(ErrInvalidDepartureReason, a.correlationID, map[string]any{
			"citizen": id,
			"reason":  int(reason),
		})
	}
	if _, ok := a.pupils[id]; !ok {
		return nil // already gone: no-op, not a corruption
	}
	a.removePupilLocked(id, reason, month)
	return nil
}
