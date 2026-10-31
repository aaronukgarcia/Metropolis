package defence

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-2326609836: save/load snapshot and the read accessors the composition
// root's defence wiring needs. The snapshot carries only DATA state (mandate
// answers, refusals, facilities, the bid counter); the grant-draw stream is
// stateless (det.NewStream per draw, keyed off bidSeq), so restoring bidSeq
// restores the stream position exactly (GR#21).

// MandateAnswer is one saved mandate response.
type MandateAnswer struct {
	MandateID    string `json:"mandateId"`
	Refused      bool   `json:"refused"`
	FacilityID   uint64 `json:"facilityId"`
	Compensation int64  `json:"compensation"`
}

// FacilityState is one saved facility.
type FacilityState struct {
	ID              uint64   `json:"id"`
	Type            string   `json:"type"`
	Tile            [2]int   `json:"tile"`
	Local           [2]int   `json:"local"`
	MandateID       string   `json:"mandateId"`
	ChoiceID        string   `json:"choiceId"`
	NominalPayroll  int64    `json:"nominalPayroll"`
	PayrollFloor    int64    `json:"payrollFloor"`
	Personnel       int64    `json:"personnel"`
	MarriedQuarters int64    `json:"marriedQuarters"`
	SchoolPlaces    int64    `json:"schoolPlaces"`
	Procurement     int64    `json:"procurement"`
	Closed          bool     `json:"closed"`
	IDs             []uint64 `json:"ids"`
}

// Snapshot is the durable defence state.
type Snapshot struct {
	BidSeq            uint64          `json:"bidSeq"`
	PlanningQuality   float64         `json:"planningQuality"`
	WageBillFactor    float64         `json:"wageBillFactor"`
	ReputationPenalty int64           `json:"reputationPenalty"`
	NextFacilityID    uint64          `json:"nextFacilityId"`
	Answers           []MandateAnswer `json:"answers"`
	Facilities        []FacilityState `json:"facilities"`
	BidMonths         []PotBidMonth   `json:"bidMonths"`
}

// PotBidMonth is the saved per-pot bid cap state: the month of the last bid.
type PotBidMonth struct {
	Pot   string `json:"pot"`
	Month int64  `json:"month"`
}

// Snapshot returns the durable state in a deterministic order (answers by
// mandate id, facilities by id).
func (d *DefenceAPI) Snapshot() Snapshot {
	if err := d.checkNotCopied("Snapshot"); err != nil {
		return Snapshot{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	s := Snapshot{
		BidSeq: d.bidSeq, PlanningQuality: d.planningQuality, WageBillFactor: d.wageBillFactor,
		ReputationPenalty: d.reputationPenalty, NextFacilityID: uint64(d.nextFacilityID),
		Answers: []MandateAnswer{}, Facilities: []FacilityState{}, BidMonths: []PotBidMonth{},
	}
	pots := make([]string, 0, len(d.lastBidMonth))
	for p := range d.lastBidMonth {
		pots = append(pots, p)
	}
	sort.Strings(pots)
	for _, p := range pots {
		s.BidMonths = append(s.BidMonths, PotBidMonth{Pot: p, Month: d.lastBidMonth[p]})
	}
	ids := make([]string, 0, len(d.responses))
	for id := range d.responses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := d.responses[id]
		s.Answers = append(s.Answers, MandateAnswer{MandateID: id, Refused: r.Refused, FacilityID: uint64(r.FacilityID), Compensation: int64(r.Compensation)})
	}
	fids := make([]FacilityID, 0, len(d.facilities))
	for id := range d.facilities {
		fids = append(fids, id)
	}
	sort.Slice(fids, func(i, j int) bool { return fids[i] < fids[j] })
	for _, id := range fids {
		f := d.facilities[id]
		s.Facilities = append(s.Facilities, FacilityState{
			ID: uint64(f.id), Type: string(f.typ),
			Tile: [2]int{f.site.Tile.X, f.site.Tile.Y}, Local: [2]int{f.site.Local.Row, f.site.Local.Col},
			MandateID: f.mandateID, ChoiceID: f.choiceID,
			NominalPayroll: int64(f.nominalPayroll), PayrollFloor: int64(f.payrollFloor),
			Personnel: f.personnel, MarriedQuarters: f.marriedQuarters, SchoolPlaces: f.schoolPlaces,
			Procurement: int64(f.procurement), Closed: f.closed, IDs: append([]uint64(nil), f.ids...),
		})
	}
	return s
}

// Restore replaces the durable state from a snapshot. It validates first and
// applies nothing on any invalid field (fail closed).
func (d *DefenceAPI) Restore(s Snapshot) error {
	if err := d.checkNotCopied("Restore"); err != nil {
		return err
	}
	// Both template params ({field} and {value}) are always supplied so the
	// MET-G3809 message renders (the errs render gate scans every raise).
	bad := func(field string, value any) error {
		return errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "snapshot." + field, "value": value})
	}
	if !num.IsFinite(s.PlanningQuality) || s.PlanningQuality < 0 || s.PlanningQuality > 1 {
		return bad("planningQuality", s.PlanningQuality)
	}
	if !num.IsFinite(s.WageBillFactor) || s.WageBillFactor < 0 || s.WageBillFactor > 1 {
		return bad("wageBillFactor", s.WageBillFactor)
	}
	if s.ReputationPenalty < 0 {
		return bad("reputationPenalty", s.ReputationPenalty)
	}
	if s.NextFacilityID == 0 {
		return bad("nextFacilityId", s.NextFacilityID)
	}
	responses := make(map[string]MandateResult, len(s.Answers))
	refused := make(map[string]bool)
	for _, a := range s.Answers {
		if _, ok := d.mandateByID(a.MandateID); !ok {
			return bad("answers.mandateId", a.MandateID)
		}
		if _, dup := responses[a.MandateID]; dup || a.Compensation < 0 {
			return bad("answers", a.MandateID)
		}
		responses[a.MandateID] = MandateResult{MandateID: a.MandateID, Refused: a.Refused, FacilityID: FacilityID(a.FacilityID), Compensation: finance.Money(a.Compensation)}
		if a.Refused {
			refused[a.MandateID] = true
		}
	}
	bidMonths := make(map[string]int64, len(s.BidMonths))
	for _, b := range s.BidMonths {
		if _, ok := d.potByID(b.Pot); !ok {
			return bad("bidMonths.pot", b.Pot)
		}
		if _, dup := bidMonths[b.Pot]; dup || b.Month < 0 {
			return bad("bidMonths", b.Pot)
		}
		bidMonths[b.Pot] = b.Month
	}
	// Fail closed on a tampered/incomplete save: bids were drawn (BidSeq > 0)
	// but the per-pot bid-month record is absent - restoring default-open
	// would let the same month bid again (the r2 round's F2).
	if s.BidSeq > 0 && len(s.BidMonths) == 0 {
		return bad("bidMonths.missing", s.BidSeq)
	}
	facilities := make(map[FacilityID]*facility, len(s.Facilities))
	for _, f := range s.Facilities {
		if f.ID == 0 || f.ID >= s.NextFacilityID {
			return bad("facilities.id", f.ID)
		}
		if _, dup := facilities[FacilityID(f.ID)]; dup {
			return bad("facilities.duplicate", f.ID)
		}
		if f.NominalPayroll < 0 || f.PayrollFloor < 0 || f.Procurement < 0 || f.Personnel < 0 || f.MarriedQuarters < 0 || f.SchoolPlaces < 0 {
			return bad("facilities.negative", f.ID)
		}
		fac := &facility{
			id: FacilityID(f.ID), typ: FacilityType(f.Type), mandateID: f.MandateID, choiceID: f.ChoiceID,
			nominalPayroll: finance.Money(f.NominalPayroll), payrollFloor: finance.Money(f.PayrollFloor),
			personnel: f.Personnel, marriedQuarters: f.MarriedQuarters, schoolPlaces: f.SchoolPlaces,
			procurement: finance.Money(f.Procurement), closed: f.Closed, ids: append([]uint64(nil), f.IDs...),
		}
		fac.site.Tile.X, fac.site.Tile.Y = f.Tile[0], f.Tile[1]
		fac.site.Local.Row, fac.site.Local.Col = f.Local[0], f.Local[1]
		facilities[fac.id] = fac
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bidSeq = s.BidSeq
	d.lastBidMonth = bidMonths
	d.planningQuality = s.PlanningQuality
	d.wageBillFactor = s.WageBillFactor
	d.reputationPenalty = s.ReputationPenalty
	d.nextFacilityID = FacilityID(s.NextFacilityID)
	d.responses = responses
	d.refusedMandates = refused
	d.facilities = facilities
	return nil
}

// BidSeq returns the monotonic bid counter (the draw stream position).
func (d *DefenceAPI) BidSeq() uint64 {
	if err := d.checkNotCopied("BidSeq"); err != nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.bidSeq
}

// Pots returns the competitive grant pots (id, name) in data-file order.
func (d *DefenceAPI) Pots() []GrantPotConfig {
	if err := d.checkNotCopied("Pots"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]GrantPotConfig(nil), d.cfg.GrantPots...)
}

// FacilityIDs returns every built facility id, ascending.
func (d *DefenceAPI) FacilityIDs() []FacilityID {
	if err := d.checkNotCopied("FacilityIDs"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]FacilityID, 0, len(d.facilities))
	for id := range d.facilities {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// MandateAnswered reports whether a mandate has been accepted or refused, and
// whether it was refused.
func (d *DefenceAPI) MandateAnswered(id string) (answered, refused bool) {
	if err := d.checkNotCopied("MandateAnswered"); err != nil {
		return false, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	r, ok := d.responses[id]
	return ok, ok && r.Refused
}

// MandateKnown reports whether id is one of the data-file mandates.
func (d *DefenceAPI) MandateKnown(id string) bool {
	if err := d.checkNotCopied("MandateKnown"); err != nil {
		return false
	}
	_, ok := d.mandateByID(id)
	return ok
}

// GrantAccessRevoked reports whether a mandate refusal currently gates bids.
func (d *DefenceAPI) GrantAccessRevoked() bool {
	if err := d.checkNotCopied("GrantAccessRevoked"); err != nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.refusedMandates) > 0
}
