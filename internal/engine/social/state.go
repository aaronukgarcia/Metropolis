package social

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// State export / import for the composition root's save participant
// (FEAT-2326609834, AC-9). Only the conserved case ledger and the monthly
// provision counters are state; dependencies, config and the category
// registration flags are re-derived by the composition on construction.

// State is the durable social record. All fields are plain data (JSON-safe).
type State struct {
	Cases               []Case `json:"cases"`
	NextCaseID          CaseID `json:"nextCaseID"`
	PreventionEnabled   bool   `json:"preventionEnabled"`
	HousingFirstEnabled bool   `json:"housingFirstEnabled"`
	HostelOccupancy     int64  `json:"hostelOccupancy"`
	HousingFirstPlaced  int64  `json:"housingFirstPlaced"`
	Prevented           int64  `json:"prevented"`
	FosterPlacements    int64  `json:"fosterPlacements"`
	LastHostelMonth     int64  `json:"lastHostelMonth"`
	LastFosterMonth     int64  `json:"lastFosterMonth"`
}

// ExportState returns a deep copy of the module's durable state.
func (a *SocialAPI) ExportState() State {
	if err := a.checkNotCopied("ExportState"); err != nil {
		return State{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return State{
		Cases:               append([]Case(nil), a.cases...),
		NextCaseID:          a.nextCaseID,
		PreventionEnabled:   a.preventionEnabled,
		HousingFirstEnabled: a.housingFirstEnabled,
		HostelOccupancy:     a.hostelOccupancy,
		HousingFirstPlaced:  a.housingFirstPlaced,
		Prevented:           a.prevented,
		FosterPlacements:    a.fosterPlacements,
		LastHostelMonth:     a.lastHostelMonth,
		LastFosterMonth:     a.lastFosterMonth,
	}
}

// ImportState replaces the durable state with s. The ledger is validated:
// case ids strictly ascending and below NextCaseID, categories valid, the
// closure status consistent with ClosedMonth. Nothing is applied on failure.
func (a *SocialAPI) ImportState(s State) error {
	if err := a.checkNotCopied("ImportState"); err != nil {
		return err
	}
	var prev CaseID
	for i, c := range s.Cases {
		if !c.Category.Valid() || (i > 0 && c.ID <= prev) || c.ID >= s.NextCaseID ||
			(c.Status == StatusOpen) != (c.ClosedMonth < 0) {
			return errs.New(ErrSocialDataInvalid, a.correlationID, map[string]any{
				"field": "state.cases", "dir": "", "cause": "invalid case ledger record", "index": i,
			})
		}
		prev = c.ID
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cases = append([]Case(nil), s.Cases...)
	a.nextCaseID = s.NextCaseID
	a.preventionEnabled = s.PreventionEnabled
	a.housingFirstEnabled = s.HousingFirstEnabled
	a.hostelOccupancy = s.HostelOccupancy
	a.housingFirstPlaced = s.HousingFirstPlaced
	a.prevented = s.Prevented
	a.fosterPlacements = s.FosterPlacements
	a.lastHostelMonth = s.LastHostelMonth
	a.lastFosterMonth = s.LastFosterMonth
	return nil
}
