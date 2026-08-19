package crime

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// The §28 justice chain (AC-12) and the courthouse-backlog release mechanism
// (AC-13). Every offender is an identifiable person (a stable uint64 id,
// Option B) that flows arrest → charge → conviction → sentence, and a
// conservation identity holds at every stage. Every term below is the count
// of that stage's OWN log — never a remainder computed to balance an
// identity (the exact anti-pattern AC-12 rejects).

// PrisonIntake is the seam through which the engine.prison module (MOD-056,
// landed) supplies its independently-tracked intake ledger (AC-12's
// cross-check, per engine.prison.md AC-2). CrimeAPI does NOT trust its own
// sentenced-to-prison count as proof of arrival; it cross-checks against
// this independent ledger via VerifyPrisonIntake.
type PrisonIntake interface {
	// IntakeCount reports the independently-tracked number of offenders the
	// prison actually received from the given district for the given month.
	IntakeCount(district DistrictID, month int64) int64
}

// offenderStream derives a per-offender stream for a purpose-tagged draw —
// the same hash(worldSeed, id, month, purposeTag) discipline AC-16 requires.
// It is a pure package-level function (no *CrimeAPI receiver) so the
// copy-guard gate does not treat it as a reachable method needing a guard.
func offenderStream(seed uint64, id uint64, month int64, purpose string) det.Stream {
	return det.NewStream(seed, id, month, purpose)
}

// advanceJusticeLocked runs one district's justice month. Callers must hold
// a.mu. totalActive is the district's post-update active crime stock, which
// the clearance mechanism apprehends from.
func (a *CrimeAPI) advanceJusticeLocked(month int64, st *districtState, in DistrictInput, totalActive float64) {
	if err := a.checkNotCopied("advanceJusticeLocked"); err != nil {
		return
	}
	cfg := a.cfg
	js := &st.justice

	// 1. Arrest (the clearance mechanism's own apprehension log, AC-5/AC-12).
	arrestCount := num.ClampInt64FromFloat(totalActive * st.clearance)
	if arrestCount < 0 {
		arrestCount = 0
	}
	stream := detStream(a.seed, st.id, month, "offender")
	js.arrested = make([]uint64, 0, arrestCount)
	for i := int64(0); i < arrestCount; i++ {
		js.arrested = append(js.arrested, stream.At(uint64(i)))
	}

	// 2. Charging decision (the courthouse's charging log, AC-12): every
	// arrested offender is independently charged or released-no-charge, so
	// arrested == charged + releasedNoCharge holds by partition.
	js.charged = js.charged[:0]
	js.releasedNoCharge = js.releasedNoCharge[:0]
	for _, id := range js.arrested {
		s := offenderStream(a.seed, id, month, "charge")
		if s.Float64() < cfg.Justice.ReleaseNoChargeRate {
			js.releasedNoCharge = append(js.releasedNoCharge, id)
		} else {
			js.charged = append(js.charged, id)
		}
	}

	// 3. Trial (the courthouse's trial-outcome log, AC-12): the courthouse
	// decides up to its throughput of this month's charged cases; the
	// overflow is the awaiting-trial backlog increment (carried to next
	// month's stock).
	decided := len(js.charged)
	if int64(decided) > in.CourthouseThroughput {
		decided = int(in.CourthouseThroughput)
	}
	if decided < 0 {
		decided = 0
	}
	js.convicted = js.convicted[:0]
	js.acquitted = js.acquitted[:0]
	for i := 0; i < decided; i++ {
		id := js.charged[i]
		s := offenderStream(a.seed, id, month, "trial")
		if s.Float64() < cfg.Justice.ConvictionRate {
			js.convicted = append(js.convicted, id)
		} else {
			js.acquitted = append(js.acquitted, id)
		}
	}
	js.awaitingTrial = js.awaitingTrial[:0]
	js.awaitingTrial = append(js.awaitingTrial, js.charged[decided:]...)
	js.backlog = append(js.backlog, js.awaitingTrial...)

	// 4. Backlog release (AC-13): once the awaiting-trial stock exceeds the
	// data-loaded threshold, release the excess (oldest first) to the
	// general population as a distinct, queryable outcome.
	js.releasedOnBacklog = js.releasedOnBacklog[:0]
	if int64(len(js.backlog)) > cfg.Justice.BacklogReleaseThreshold {
		release := int64(len(js.backlog)) - cfg.Justice.BacklogReleaseThreshold
		js.releasedOnBacklog = append(js.releasedOnBacklog, js.backlog[:release]...)
		js.backlog = append([]uint64(nil), js.backlog[release:]...)
	}

	// 5. Sentencing (the sentence log, AC-12): every convicted offender is
	// independently sentenced to prison or non-custodial, so
	// convicted == sentencedToPrison + sentencedNonCustodial by partition.
	js.sentencedToPrison = js.sentencedToPrison[:0]
	js.sentencedNonCustodial = js.sentencedNonCustodial[:0]
	for _, id := range js.convicted {
		s := offenderStream(a.seed, id, month, "sentence")
		if s.Float64() < cfg.Justice.PrisonSentenceRate {
			js.sentencedToPrison = append(js.sentencedToPrison, id)
		} else {
			js.sentencedNonCustodial = append(js.sentencedNonCustodial, id)
		}
	}
}

// --- justice-chain accessors (AC-12/AC-13) ---

func (a *CrimeAPI) justiceCount(id DistrictID, method string, fn func(*districtState) int64) (int64, error) {
	if err := a.checkNotCopied(method); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, err := a.requireDistrict(id)
	if err != nil {
		return 0, err
	}
	return fn(st), nil
}

// OffendersArrested returns the offenders arrested this month (clearance log).
func (a *CrimeAPI) OffendersArrested(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersArrested"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersArrested", func(st *districtState) int64 { return int64(len(st.justice.arrested)) })
}

// OffendersCharged returns the offenders charged this month (charging log).
func (a *CrimeAPI) OffendersCharged(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersCharged"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersCharged", func(st *districtState) int64 { return int64(len(st.justice.charged)) })
}

// OffendersReleasedNoCharge returns offenders released without charge this month.
func (a *CrimeAPI) OffendersReleasedNoCharge(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersReleasedNoCharge"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersReleasedNoCharge", func(st *districtState) int64 { return int64(len(st.justice.releasedNoCharge)) })
}

// OffendersConvicted returns the offenders convicted this month (trial log).
func (a *CrimeAPI) OffendersConvicted(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersConvicted"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersConvicted", func(st *districtState) int64 { return int64(len(st.justice.convicted)) })
}

// OffendersAcquitted returns the offenders acquitted this month (trial log).
func (a *CrimeAPI) OffendersAcquitted(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersAcquitted"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersAcquitted", func(st *districtState) int64 { return int64(len(st.justice.acquitted)) })
}

// OffendersAwaitingTrial returns the awaiting-trial backlog increment this
// month — the charged-this-month overflow carried into next month's backlog
// stock (AC-12's identity-2 term). It is read from the courthouse's own
// throughput overflow, never computed as an identity-balancing remainder.
func (a *CrimeAPI) OffendersAwaitingTrial(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersAwaitingTrial"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersAwaitingTrial", func(st *districtState) int64 { return int64(len(st.justice.awaitingTrial)) })
}

// Backlog returns the awaiting-trial stock carried to next month (the
// accumulated increment minus releases).
func (a *CrimeAPI) Backlog(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("Backlog"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "Backlog", func(st *districtState) int64 { return int64(len(st.justice.backlog)) })
}

// OffendersReleasedOnBacklog returns the offenders released on bail/lapsed
// charge this month (AC-13's distinct outcome, separate from acquittals).
func (a *CrimeAPI) OffendersReleasedOnBacklog(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersReleasedOnBacklog"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersReleasedOnBacklog", func(st *districtState) int64 { return int64(len(st.justice.releasedOnBacklog)) })
}

// OffendersSentencedToPrison returns the offenders sentenced to prison this
// month (sentence log). It is cross-checked against the independent
// PrisonIntake ledger by VerifyPrisonIntake (AC-12).
func (a *CrimeAPI) OffendersSentencedToPrison(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersSentencedToPrison"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersSentencedToPrison", func(st *districtState) int64 { return int64(len(st.justice.sentencedToPrison)) })
}

// OffendersSentencedNonCustodial returns offenders sentenced non-custodially this month.
func (a *CrimeAPI) OffendersSentencedNonCustodial(id DistrictID) (int64, error) {
	if err := a.checkNotCopied("OffendersSentencedNonCustodial"); err != nil {
		return 0, err
	}
	return a.justiceCount(id, "OffendersSentencedNonCustodial", func(st *districtState) int64 { return int64(len(st.justice.sentencedNonCustodial)) })
}

// VerifyPrisonIntake cross-checks this module's sentenced-to-prison figure
// against the independently-wired PrisonIntake ledger for the given month
// (AC-12): the identity is NOT trusted as this module's own say-so. It
// errors with ErrPrisonIntakeMissing before a ledger is wired, and returns
// false (no error) on a mismatch — a genuine cross-module discrepancy is a
// data finding, not a swallowed truth.
func (a *CrimeAPI) VerifyPrisonIntake(id DistrictID, month int64) (bool, error) {
	if err := a.checkNotCopied("VerifyPrisonIntake"); err != nil {
		return false, err
	}
	a.mu.RLock()
	ledger := a.prison
	st, err := a.requireDistrict(id)
	a.mu.RUnlock()
	if err != nil {
		return false, err
	}
	if ledger == nil {
		return false, errs.New(ErrPrisonIntakeMissing, a.correlationID, nil)
	}
	return int64(len(st.justice.sentencedToPrison)) == ledger.IntakeCount(id, month), nil
}
