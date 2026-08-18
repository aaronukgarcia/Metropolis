package social

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file holds the case ledger and the case-accounting identity (AC-11):
// the conserved caseload stock. Every case is an append-only record; every
// closure is one of the three documented outcomes (resolved / escalated /
// lost-to-follow-up); an escalation is never a silent close — it is a close
// in the source category paired with a freshly-opened, linked case in the
// destination category in the SAME month.
//
// The identity, per category per month (AC-11):
//
//	OpenCases == OpenCasesLastMonth
//	    + NewCasesOpened
//	    − CasesResolved
//	    − CasesEscalated
//	    − CasesLostToFollowUp
//
// Every term is independently sourced from the ledger by its own field
// (opened by OpenedMonth, resolved/escalated/lost by ClosedMonth + Status),
// so a reconciliation failure is a real tracking bug, never a tautology.

// openCase appends one new open case to the ledger and returns its id. It is
// the single "open" path — steady-state, crisis, and escalation-reopen all
// flow through it, so NewCasesOpened is always independently countable from
// the ledger.
func (a *SocialAPI) openCase(cat Category, month int64, citizenID uint64, source, crisisID string, linked CaseID) CaseID {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.nextCaseID
	a.nextCaseID++
	a.cases = append(a.cases, Case{
		ID:           id,
		Category:     cat,
		OpenedMonth:  month,
		ClosedMonth:  -1,
		Status:       StatusOpen,
		CitizenID:    citizenID,
		Source:       source,
		CrisisID:     crisisID,
		LinkedCaseID: linked,
	})
	return id
}

// lookupLocked returns the case record and whether it exists; the caller
// holds a.mu (RLock or Lock).
func (a *SocialAPI) lookupLocked(id CaseID) (Case, bool) {
	for i := len(a.cases) - 1; i >= 0; i-- {
		if a.cases[i].ID == id {
			return a.cases[i], true
		}
	}
	return Case{}, false
}

// Case returns the ledger record for id (AC-13's queryable-case surface).
// An unknown id returns ErrUnknownCase — never a zero-value case.
func (a *SocialAPI) Case(id CaseID) (Case, error) {
	if err := a.checkNotCopied("Case"); err != nil {
		return Case{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.lookupLocked(id)
	if !ok {
		return Case{}, errs.New(ErrUnknownCase, a.correlationID, map[string]any{"case": uint64(id)})
	}
	return c, nil
}

// ResolveCase closes an open case with a documented outcome (AC-11's
// CasesResolved term). A case already closed is rejected with ErrDoubleClose
// (AC-14); an unknown id with ErrUnknownCase (AC-13).
func (a *SocialAPI) ResolveCase(id CaseID, month int64, resolution string) error {
	if err := a.checkNotCopied("ResolveCase"); err != nil {
		return err
	}
	return a.closeCase(id, month, StatusResolved, resolution, 0)
}

// EscalateCase closes an open case as escalated and reopens it as a linked
// case in the destination category in the same month (AC-11's escalation
// pairing). A nonexistent destination category is rejected with
// ErrInvalidEscalation (AC-14). It returns the reopened case's id.
func (a *SocialAPI) EscalateCase(id CaseID, month int64, dest Category) (CaseID, error) {
	if err := a.checkNotCopied("EscalateCase"); err != nil {
		return 0, err
	}
	if !dest.Valid() {
		return 0, errs.New(ErrInvalidEscalation, a.correlationID, map[string]any{"destination": dest.String()})
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	src, ok := a.lookupLocked(id)
	if !ok {
		return 0, errs.New(ErrUnknownCase, a.correlationID, map[string]any{"case": uint64(id)})
	}
	if src.Status != StatusOpen {
		return 0, errs.New(ErrDoubleClose, a.correlationID, map[string]any{"case": uint64(id), "status": int(src.Status)})
	}
	if month < src.OpenedMonth {
		return 0, errs.New(ErrBackDatedMonth, a.correlationID, map[string]any{
			"case": uint64(id), "month": month, "openedMonth": src.OpenedMonth,
		})
	}

	// The paired reopen is minted FIRST so the source close can point at it.
	reopen := a.nextCaseID
	a.nextCaseID++
	a.cases = append(a.cases, Case{
		ID:           reopen,
		Category:     dest,
		OpenedMonth:  month,
		ClosedMonth:  -1,
		Status:       StatusOpen,
		CitizenID:    src.CitizenID,
		Source:       "escalation:" + idString(id),
		LinkedCaseID: id,
	})
	for i := len(a.cases) - 1; i >= 0; i-- {
		if a.cases[i].ID == id {
			a.cases[i].Status = StatusEscalated
			a.cases[i].ClosedMonth = month
			a.cases[i].LinkedCaseID = reopen
			break
		}
	}
	return reopen, nil
}

// LoseToFollowUp closes an open case under the documented fallback state
// (relocated / untraceable — AC-11's CasesLostToFollowUp term). Never a
// silent drop, never a "resolved" close of an abandoned case.
func (a *SocialAPI) LoseToFollowUp(id CaseID, month int64) error {
	if err := a.checkNotCopied("LoseToFollowUp"); err != nil {
		return err
	}
	return a.closeCase(id, month, StatusLostToFollowUp, "lost-to-follow-up", 0)
}

// closeCase is the shared closure path for ResolveCase/LoseToFollowUp. The
// caller has already passed checkNotCopied.
func (a *SocialAPI) closeCase(id CaseID, month int64, status CaseStatus, resolution string, linked CaseID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeCaseLocked(id, month, status, resolution, linked)
}

// closeCaseLocked closes an open case; the caller holds a.mu (write lock).
// Used by the tick-path routing (RouteHomelessness) so a whole routing pass
// happens under one lock without re-entering closeCase's own lock.
func (a *SocialAPI) closeCaseLocked(id CaseID, month int64, status CaseStatus, resolution string, linked CaseID) error {
	c, ok := a.lookupLocked(id)
	if !ok {
		return errs.New(ErrUnknownCase, a.correlationID, map[string]any{"case": uint64(id)})
	}
	if c.Status != StatusOpen {
		return errs.New(ErrDoubleClose, a.correlationID, map[string]any{"case": uint64(id), "status": int(c.Status)})
	}
	if month < c.OpenedMonth {
		return errs.New(ErrBackDatedMonth, a.correlationID, map[string]any{
			"case": uint64(id), "month": month, "openedMonth": c.OpenedMonth,
		})
	}
	for i := len(a.cases) - 1; i >= 0; i-- {
		if a.cases[i].ID == id {
			a.cases[i].Status = status
			a.cases[i].ClosedMonth = month
			a.cases[i].Resolution = resolution
			a.cases[i].LinkedCaseID = linked
			break
		}
	}
	return nil
}

// idString renders a CaseID without pulling in fmt/strconv for this one
// call site.
func idString(id CaseID) string {
	return uintString(uint64(id))
}

// uintString renders a uint64 in decimal (deterministic, no locale).
func uintString(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// AccountingSnapshot is the per-category per-month accounting term set the
// identity is checked over (AC-11).
type AccountingSnapshot struct {
	Open           int64
	Opened         int64
	Resolved       int64
	Escalated      int64
	LostToFollowUp int64
}

// Accounting returns the independently-sourced accounting terms for one
// category and month (AC-11). Open is the count open at the END of the
// month; Opened/Resolved/Escalated/LostToFollowUp are the counts of that
// month's events, each counted by its own ledger field.
func (a *SocialAPI) Accounting(cat Category, month int64) (AccountingSnapshot, error) {
	if err := a.checkNotCopied("Accounting"); err != nil {
		return AccountingSnapshot{}, err
	}
	if !cat.Valid() {
		return AccountingSnapshot{}, errs.New(ErrUnknownCategory, a.correlationID, map[string]any{"category": cat.String()})
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var s AccountingSnapshot
	for _, c := range a.cases {
		if c.Category != cat {
			continue
		}
		if c.OpenedMonth <= month && (c.Status == StatusOpen || c.ClosedMonth > month) {
			s.Open++
		}
		if c.OpenedMonth == month {
			s.Opened++
		}
		if c.ClosedMonth == month {
			switch c.Status {
			case StatusResolved:
				s.Resolved++
			case StatusEscalated:
				s.Escalated++
			case StatusLostToFollowUp:
				s.LostToFollowUp++
			}
		}
	}
	return s, nil
}

// OpenCaseIDs returns the ids of all currently-open cases in a category, in
// ascending id order (deterministic, GR#21 — never map-iteration order).
func (a *SocialAPI) OpenCaseIDs(cat Category) []CaseID {
	if err := a.checkNotCopied("OpenCaseIDs"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var ids []CaseID
	for _, c := range a.cases {
		if c.Category == cat && c.Status == StatusOpen {
			ids = append(ids, c.ID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Caseload returns the open-case count for a category at the given month
// (the per-category query surface AC-1 names). An unregistered category is
// rejected with ErrUnknownCategory.
func (a *SocialAPI) Caseload(cat Category, month int64) (int64, error) {
	if err := a.checkNotCopied("Caseload"); err != nil {
		return 0, err
	}
	s, err := a.Accounting(cat, month)
	if err != nil {
		return 0, err
	}
	return s.Open, nil
}

// caseloadNow returns a category's current open-case count (month = the last
// month any case has been touched, or 0) — the headline per-category
// accessor. It never errors on a valid category.
func (a *SocialAPI) caseloadNow(cat Category) int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var last int64
	for _, c := range a.cases {
		if c.OpenedMonth > last {
			last = c.OpenedMonth
		}
		if c.ClosedMonth > last {
			last = c.ClosedMonth
		}
	}
	var n int64
	for _, c := range a.cases {
		if c.Category == cat && c.OpenedMonth <= last && (c.Status == StatusOpen || c.ClosedMonth > last) {
			n++
		}
	}
	return n
}

// FamilySupportCaseload returns the current open family-support &
// child-protection caseload (AC-2's five distinct accessors).
func (a *SocialAPI) FamilySupportCaseload() int64 {
	if err := a.checkNotCopied("FamilySupportCaseload"); err != nil {
		return 0
	}
	return a.caseloadNow(CategoryFamilySupport)
}

// HomelessnessCaseload returns the current open homelessness caseload.
func (a *SocialAPI) HomelessnessCaseload() int64 {
	if err := a.checkNotCopied("HomelessnessCaseload"); err != nil {
		return 0
	}
	return a.caseloadNow(CategoryHomelessness)
}

// DisabilityCarersCaseload returns the current open disability & carers
// caseload.
func (a *SocialAPI) DisabilityCarersCaseload() int64 {
	if err := a.checkNotCopied("DisabilityCarersCaseload"); err != nil {
		return 0
	}
	return a.caseloadNow(CategoryDisabilityCarers)
}

// FosteringCaseload returns the current open fostering caseload.
func (a *SocialAPI) FosteringCaseload() int64 {
	if err := a.checkNotCopied("FosteringCaseload"); err != nil {
		return 0
	}
	return a.caseloadNow(CategoryFostering)
}

// AddictionCaseload returns the current open addiction-services caseload
// (AC-4's addiction accessor, responsive to the deprivation/nightlife
// coupling via GenerateCaseload).
func (a *SocialAPI) AddictionCaseload() int64 {
	if err := a.checkNotCopied("AddictionCaseload"); err != nil {
		return 0
	}
	return a.caseloadNow(CategoryAddiction)
}
