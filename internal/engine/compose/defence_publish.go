package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-2326609836: the "f4.defence" read-only protocol view -- grant pots,
// formula support, pending mandates (against the live population), grant
// access state, facilities, the last bid and the posted payroll/procurement.
// Protocol view only: there is no UI twin (non-goal of the pack), so the
// schema is compose-owned and versioned independently.
const defenceViewSubscriptionName = "f4.defence"

const defenceWireSchemaVersion = 1

type defenceWirePot struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	AwardMicropounds int64  `json:"awardMicropounds"`
}

type defenceWireChoice struct {
	ID           string `json:"id"`
	FacilityType string `json:"facilityType"`
	Description  string `json:"description"`
}

type defenceWireMandate struct {
	ID                  string              `json:"id"`
	FacilityType        string              `json:"facilityType"`
	PopulationThreshold int64               `json:"populationThreshold"`
	CompensationMicro   int64               `json:"compensationMicropounds"`
	Choices             []defenceWireChoice `json:"choices"`
}

type defenceWireFacility struct {
	ID          uint64 `json:"id"`
	Type        string `json:"type"`
	MandateID   string `json:"mandateId"`
	Personnel   int64  `json:"personnel"`
	Payroll     int64  `json:"payrollMicropounds"`
	Procurement int64  `json:"procurementMicropounds"`
	Closed      bool   `json:"closed"`
}

type defenceWirePatch struct {
	SchemaVersion       int                   `json:"schemaVersion"`
	Population          int64                 `json:"population"`
	Pots                []defenceWirePot      `json:"pots"`
	FormulaSupport      int64                 `json:"formulaSupportMicropounds"`
	PendingMandates     []defenceWireMandate  `json:"pendingMandates"`
	GrantAccessRevoked  bool                  `json:"grantAccessRevoked"`
	ReputationPenalty   int64                 `json:"reputationPenalty"`
	BidSeq              uint64                `json:"bidSeq"`
	LastBid             *defenceBid           `json:"lastBid,omitempty"`
	Facilities          []defenceWireFacility `json:"facilities"`
	PostedProcurement   int64                 `json:"postedProcurementMicropounds"`
	FailedMonths        int64                 `json:"failedMonths"`
	GrantsLedgerMicro   int64                 `json:"grantsLedgerMicropounds"`
	GrantsLedgerLineCnt int                   `json:"grantsLedgerLines"`
}

// buildDefencePatch serves the f4.defence view. A nil defence wiring (a bare
// simState) yields the honest empty patch, never an error.
func (st *simState) buildDefencePatch() (json.RawMessage, error) {
	p := defenceWirePatch{
		SchemaVersion: defenceWireSchemaVersion,
		Pots:          []defenceWirePot{}, PendingMandates: []defenceWireMandate{}, Facilities: []defenceWireFacility{},
	}
	if w := st.defenceW; w != nil && !st.defenceW.checkNotCopied() {
		_ = errDefenceCopied(st.cid, "buildDefencePatch") // fail closed: honest empty patch
	} else if w != nil {
		pop := st.defencePopulation()
		p.Population = pop
		for _, pot := range w.api.Pots() {
			p.Pots = append(p.Pots, defenceWirePot{ID: pot.ID, Name: pot.Name, AwardMicropounds: pot.AwardMicropounds})
		}
		if st.finance != nil {
			p.FormulaSupport = int64(w.api.FormulaSupport(st.finance.TaxRevenue()))
			for _, e := range st.finance.LinesByCategory(finance.CatGrants) {
				if e.Account == finance.AcctTreasury && e.Side == finance.SideCredit {
					p.GrantsLedgerLineCnt++
					p.GrantsLedgerMicro = num.SatAdd(p.GrantsLedgerMicro, int64(e.Amount))
				}
			}
		}
		for _, m := range w.api.PendingMandates(pop) {
			wm := defenceWireMandate{
				ID: m.ID, FacilityType: string(m.FacilityType), PopulationThreshold: m.PopulationThreshold,
				CompensationMicro: int64(m.Compensation), Choices: []defenceWireChoice{},
			}
			for _, c := range m.Choices {
				wm.Choices = append(wm.Choices, defenceWireChoice{ID: c.ID, FacilityType: string(c.FacilityType), Description: c.Description})
			}
			p.PendingMandates = append(p.PendingMandates, wm)
		}
		p.GrantAccessRevoked = w.api.GrantAccessRevoked()
		p.ReputationPenalty = w.api.ReputationPenalty()
		p.BidSeq = w.api.BidSeq()
		for _, id := range w.api.FacilityIDs() {
			if f, ok := w.api.Facility(id); ok {
				p.Facilities = append(p.Facilities, defenceWireFacility{
					ID: uint64(f.ID), Type: string(f.Type), MandateID: f.MandateID, Personnel: f.Personnel,
					Payroll: int64(f.Payroll), Procurement: int64(f.Procurement), Closed: f.Closed,
				})
			}
		}
		w.mu.RLock()
		p.PostedProcurement, p.FailedMonths = w.postedProcurement, w.failedMonths
		if w.lastBid != nil {
			b := *w.lastBid
			p.LastBid = &b
		}
		w.mu.RUnlock()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "defence", "accessor": "json.Marshal"})
	}
	return raw, nil
}
