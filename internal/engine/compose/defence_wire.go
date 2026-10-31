package compose

import (
	"encoding/json"
	"fmt"
	"hash"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/defence"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609836: engine.defence (MOD-067) wired into the composition root.
//
// Before this file engine.defence had zero non-test importers: no grant ever
// arrived and no mandate ever fired. The wiring is:
//
//   - BidForGrant / RespondToMandate protocol Kinds through handleGameplay
//     (handleDefenceCommand). A won bid is a balanced external -> treasury
//     credit under finance.CatGrants ("grants"); the money stock RISES by
//     exactly the award (external inflow, unlike an internal transfer) and
//     that rise is folded into moneyDelta in the same call, so the money
//     invariant sees a tracked flow. engine.fiscal's ledger Sankey reads the
//     same category for NodeGrants (no side-channel into fiscal).
//   - a MONTHLY PhaseFinance hook ("defence", registrationOrder) AFTER
//     fiscal: posts each open facility's PROCUREMENT (treasury -> firms,
//     defence.procurement) as a balanced internal transfer. PhaseFinance, like
//     fiscal, so the month-window ledger reads are safe (BUG-1141): BeginMonth
//     has opened the month's tick log. engine.defence posts nothing for
//     procurement itself (found unposted today, AC-7: closed here).
//   - PERSONNEL PAYROLL IS DELIBERATELY NOT POSTED HERE (double-pay ruling,
//     FEAT-2326609836): the mandate personnel are real citizens minted through
//     the compose id allocator, so their ids sit in baseResidentIDs() and the
//     generic financeHook (markEmploymentAndCount -> PostWages /
//     PostWagesFromFirms, monthlyWageGrossPerEmployedMicropounds per Employed
//     resident) ALREADY pays each of them every month. A second defence.payroll
//     posting paid the same citizen-month twice (measured: wages line AND a
//     1,000,000 defence.payroll line in the same month). The generic wage path
//     is therefore the single payment path; the facility's nominal payroll
//     figure remains a module-side observable only.
//   - mandate personnel are minted through the COMPOSE citizen id allocator
//     (st.nextCitizenID, allocateCoastalCitizenID's three-way namespace
//     discipline) and the measured TotalPopulation change is folded into
//     peopleDelta in the same call (people conservation).
//   - the read-only "f4.defence" view (defence_publish.go) and a save
//     participant ("defence").
//
// Dormancy: mandates fire at 100k/500k/1M population (data/defence.json); a
// default Baseline One city is far below that, so mandates are dormant in real
// play. Tests drive them through the labelled population fixture seam
// (defenceWiring.popFn), which no production path sets.
//
// GR#25 edges (the lead registers in the master plan at port):
//
//	feat.compositionroot -> engine.defence
//	engine.defence       -> engine.fiscal is NOT needed (grants flow via the ledger)

const (
	finCatDefenceProcurement = finance.Category("defence.procurement")
)

// defenceBid is the last bid outcome, for the view.
type defenceBid struct {
	Pot            string  `json:"pot"`
	Won            bool    `json:"won"`
	Award          int64   `json:"awardMicropounds"`
	WinProbability float64 `json:"winProbability"`
	Month          int64   `json:"month"`
}

// defenceWiring is the composition's defence state.
type defenceWiring struct {
	api *defence.DefenceAPI

	// popFn is the TEST-FIXTURE population seam: nil in production (the live
	// citizens population is used). Only same-package tests assign it.
	popFn func() int64

	mu sync.RWMutex
	// self holds the address newDefenceWiring gave this value (stored once,
	// before the pointer is shared). A struct copy of *defenceWiring would get
	// its own independent mu while still aliasing api, so every lock-taking
	// entry point rejects a copy BEFORE touching mu (SEC-020 family, same
	// shape as errs.Logger and coastalWiring; see checkNotCopied).
	self atomic.Pointer[defenceWiring]
	// lastMonth is the last month facilities were posted for (-1 = never):
	// the replay guard against a re-fired hook posting a month twice.
	lastMonth         int64
	postedProcurement int64
	failedMonths      int64
	lastBid           *defenceBid
}

// checkNotCopied reports whether the receiver is the value newDefenceWiring
// constructed rather than a struct copy (SEC-020 family; mirrors
// errs.Logger.checkNotCopied). Lock-free (one atomic.Pointer.Load) so it is
// safe to run BEFORE mu is touched -- a copy taken while mu was held would
// capture it "locked" and could park forever (SEC-016). A bare literal that
// never went through newDefenceWiring has a nil self and is rejected the same
// way.
func (w *defenceWiring) checkNotCopied() bool {
	return w.self.Load() == w
}

// errDefenceCopied is the registry-sourced rejection for a copied receiver.
func errDefenceCopied(cid, entry string) error {
	return errs.New(ErrDefenceStateInvalid, cid, map[string]any{"cause": "struct-copied defenceWiring", "entry": entry})
}

// newDefenceWiring builds the module (the injected instance, or one loaded
// from data/defence.json).
func newDefenceWiring(injected *defence.DefenceAPI, seed uint64, cid string) (*defenceWiring, error) {
	api := injected
	if api == nil {
		var err error
		api, err = defence.LoadDefault(seed, cid)
		if err != nil {
			return nil, err
		}
	}
	w := &defenceWiring{api: api, lastMonth: -1}
	w.self.Store(w) // once, before w is shared with any other goroutine
	return w, nil
}

// bind wires build, finance, citizens and the compose citizen id allocator.
func (w *defenceWiring) bind(st *simState) error {
	if !w.checkNotCopied() {
		return errDefenceCopied(st.cid, "bind")
	}
	if err := w.api.SetBuild(st.buildAPI); err != nil {
		return err
	}
	if err := w.api.SetFinance(st.finance); err != nil {
		return err
	}
	if err := w.api.SetCitizens(st.citizens); err != nil {
		return err
	}
	return w.api.SetCitizenIDAllocator(st.allocateDefenceCitizenID)
}

// allocateDefenceCitizenID is the compose citizen id seam for facility
// personnel: the same counter and namespace guard as coastal's.
func (st *simState) allocateDefenceCitizenID() (uint64, error) {
	id := st.nextCitizenID
	if id >= attract.MigrantIDBase {
		return 0, errs.New(ErrDefenceCitizenIDCollision, st.cid, map[string]any{"id": id, "limit": attract.MigrantIDBase})
	}
	if st.seedResidentIDCount > 0 && id > st.seedResidentIDBase && id <= st.seedResidentIDBase+uint64(st.seedResidentIDCount) {
		return 0, errs.New(ErrDefenceCitizenIDCollision, st.cid, map[string]any{"id": id, "limit": st.seedResidentIDBase})
	}
	st.nextCitizenID++
	return id, nil
}

// defencePopulation is the population the mandate thresholds are read
// against: the live citizen population unless the test fixture overrides it.
func (st *simState) defencePopulation() int64 {
	w := st.defenceW
	if w != nil && !st.defenceW.checkNotCopied() {
		_ = errDefenceCopied(st.cid, "defencePopulation") // fail closed: ignore a copy's fixture seam
		w = nil
	}
	if w != nil && w.popFn != nil {
		return w.popFn()
	}
	if st.citizens == nil {
		return 0
	}
	return int64(st.citizens.TotalPopulation(st.cid))
}

// ---- commands -------------------------------------------------------------

func (st *simState) defenceCommandInvalid(kind protocol.Kind, cause string) error {
	return errs.New(ErrDefenceCommandInvalid, st.cid, map[string]any{"kind": string(kind), "cause": cause})
}

// handleDefenceCommand routes BidForGrant / RespondToMandate.
func (st *simState) handleDefenceCommand(cmd protocol.Command) error {
	w := st.defenceW
	if w == nil {
		return st.defenceCommandInvalid(cmd.Kind, "defence not wired")
	}
	if !st.defenceW.checkNotCopied() { // pre-lock copy guard (fail closed: command rejected)
		return errDefenceCopied(st.cid, "handleDefenceCommand")
	}
	month, err := st.currentMonth()
	if err != nil {
		return err
	}
	switch cmd.Kind {
	case protocol.KindBidForGrant:
		p, ok := cmd.Payload.(protocol.BidForGrantPayload)
		if !ok {
			return st.defenceCommandInvalid(cmd.Kind, "malformed payload")
		}
		return st.withLedgerDelta(func() error {
			res, err := w.api.BidForGrant(defence.GrantBid{Pot: p.Pot, MatchFunding: finance.Money(p.MatchFundingMicropounds), Month: month})
			if err != nil {
				return err
			}
			w.mu.Lock()
			w.lastBid = &defenceBid{Pot: res.Pot, Won: res.Won, Award: int64(res.Award), WinProbability: res.WinProbability, Month: month}
			w.mu.Unlock()
			return nil
		})
	case protocol.KindRespondToMandate:
		p, ok := cmd.Payload.(protocol.RespondToMandatePayload)
		if !ok {
			return st.defenceCommandInvalid(cmd.Kind, "malformed payload")
		}
		// The module does not gate on population; the composition does: only a
		// mandate whose threshold is reached (pending) can be answered. An
		// already-answered one falls through to the module's one-shot refusal.
		if w.api.MandateKnown(p.MandateID) {
			if answered, _ := w.api.MandateAnswered(p.MandateID); !answered {
				pop := st.defencePopulation()
				pending := false
				for _, m := range w.api.PendingMandates(pop) {
					if m.ID == p.MandateID {
						pending = true
						break
					}
				}
				if !pending {
					return errs.New(ErrDefenceMandateNotPending, st.cid, map[string]any{"mandate": p.MandateID, "population": pop})
				}
			}
		}
		req := defence.MandateResponse{MandateID: p.MandateID, Refuse: p.Refuse, Choice: p.Choice, OwnerID: playerOwnerID, Month: month}
		if !p.Refuse {
			tile, local, cerr := st.cellFromRef(p.Cell)
			if cerr != nil {
				return cerr
			}
			req.Site = defence.SiteRef{Tile: tile, Local: local}
		}
		popBefore := int64(st.citizens.TotalPopulation(st.cid))
		rerr := st.withLedgerDelta(func() error {
			_, e := w.api.RespondToMandate(req)
			return e
		})
		// People conservation: every personnel/child citizen minted is an
		// inflow this tick; fold the MEASURED change (robust to a partial
		// failure) into peopleDelta.
		gained := num.SatSub(int64(st.citizens.TotalPopulation(st.cid)), popBefore)
		st.peopleDelta = num.SatAdd(st.peopleDelta, gained)
		return rerr
	default:
		return st.defenceCommandInvalid(cmd.Kind, "unhandled defence kind")
	}
}

// ---- monthly hook ---------------------------------------------------------

type defenceEffect struct{ month int64 }

// defenceHook is the monthly facility payroll/procurement hook (PhaseFinance,
// after fiscal).
type defenceHook struct{ st *simState }

func (h *defenceHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: defenceEffect{month: clock.Month()}}}, nil
}

func (h *defenceHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(defenceEffect)
	if !ok {
		return
	}
	h.st.runDefenceMonth(p.month)
}

// SingleShard implements core.SingleShardHook.
func (h *defenceHook) SingleShard() bool { return true }

// runDefenceMonth posts each open facility's procurement (payroll is paid by
// the generic wage path -- see the file header).
func (st *simState) runDefenceMonth(month int64) {
	w := st.defenceW
	if w == nil || st.finance == nil {
		return
	}
	if !st.defenceW.checkNotCopied() { // pre-lock copy guard (fail closed: no month is posted)
		_ = errDefenceCopied(st.cid, "runDefenceMonth")
		return
	}
	w.mu.RLock()
	last := w.lastMonth
	w.mu.RUnlock()
	if month == last {
		return // replay guard: this month already posted
	}
	failed := false
	_ = st.withLedgerDelta(func() error {
		for _, id := range w.api.FacilityIDs() { // ascending (GR#21)
			info, ok := w.api.Facility(id)
			if !ok || info.Closed {
				continue
			}
			if !st.postDefenceLine(month, "procurement", finCatDefenceProcurement, finance.AcctFirms, int64(info.Procurement)) {
				failed = true
			}
		}
		return nil
	})
	w.mu.Lock()
	w.lastMonth = month
	if failed {
		w.failedMonths++
	}
	w.mu.Unlock()
}

// postDefenceLine posts one balanced treasury -> counterparty transfer.
func (st *simState) postDefenceLine(month int64, line string, cat finance.Category, counter finance.AccountID, amount int64) bool {
	if amount <= 0 {
		return true
	}
	_, err := st.finance.Post(finance.Transaction{
		Month:       month,
		Description: "defence " + line,
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(amount), Category: cat},
			{Account: counter, Side: finance.SideCredit, Amount: finance.Money(amount), Category: cat},
		},
	})
	if err != nil {
		_ = errs.New(ErrDefenceFacilityUnposted, st.cid, map[string]any{
			"line": line, "month": month, "amountMicropounds": amount, "cause": err.Error(),
		})
		return false
	}
	w := st.defenceW
	if !st.defenceW.checkNotCopied() { // pre-lock copy guard (fail closed: line reported unposted)
		_ = errDefenceCopied(st.cid, "postDefenceLine")
		return false
	}
	w.mu.Lock()
	w.postedProcurement = num.SatAdd(w.postedProcurement, amount)
	w.mu.Unlock()
	return true
}

// Defence returns the composition's engine.defence instance (read-mostly
// accessor for headless drivers and tests).
func (c *Composition) Defence() *defence.DefenceAPI {
	w := c.state.defenceW
	if w == nil {
		return nil
	}
	if !w.checkNotCopied() {
		_ = errDefenceCopied(c.state.cid, "Defence")
		return nil // fail closed: no module handle from a copy
	}
	return w.api
}

// --- save participant ---

const (
	kindComposeDefence = "defence"
	recComposeDefence  = "defence.state"
)

// defenceWire is the durable defence record.
type defenceWire struct {
	Snapshot          defence.Snapshot `json:"snapshot"`
	LastMonth         int64            `json:"lastMonth"`
	PostedProcurement int64            `json:"postedProcurement"`
	LastBid           *defenceBid      `json:"lastBid,omitempty"`
	NextCitizenID     uint64           `json:"nextCitizenID"`
}

type defenceParticipant struct{ st *simState }

func newDefenceParticipant(st *simState) *defenceParticipant { return &defenceParticipant{st: st} }

func (p *defenceParticipant) Kind() string { return kindComposeDefence }

func (p *defenceParticipant) Source() serialize.RecordSource {
	w := p.st.defenceW
	var wire defenceWire
	if w != nil && !w.checkNotCopied() {
		_ = errDefenceCopied(p.st.cid, "Source")
		w = nil // fail closed: emit no record rather than a copy's state
	}
	if w != nil {
		w.mu.RLock()
		wire = defenceWire{
			Snapshot: w.api.Snapshot(), LastMonth: w.lastMonth,
			PostedProcurement: w.postedProcurement, NextCitizenID: p.st.nextCitizenID,
		}
		if w.lastBid != nil {
			b := *w.lastBid
			wire.LastBid = &b
		}
		w.mu.RUnlock()
	}
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted || w == nil {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(wire)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recComposeDefence, err)
		}
		emitted = true
		return serialize.Record{Kind: recComposeDefence, Data: data}, true, nil
	}
}

func (p *defenceParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recComposeDefence {
			return fmt.Errorf("compose: unknown defence save record kind %q", rec.Kind)
		}
		w := p.st.defenceW
		if w == nil {
			return nil
		}
		if !w.checkNotCopied() {
			return errDefenceCopied(p.st.cid, "Handler")
		}
		var wire defenceWire
		if err := json.Unmarshal(rec.Data, &wire); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		if wire.PostedProcurement < 0 {
			return errs.New(ErrDefenceStateInvalid, p.st.cid, map[string]any{"cause": "negative posted counter"})
		}
		if wire.NextCitizenID == 0 || wire.NextCitizenID >= attract.MigrantIDBase {
			return errs.New(ErrDefenceStateInvalid, p.st.cid, map[string]any{"cause": "citizen id allocator position out of range"})
		}
		if err := w.api.Restore(wire.Snapshot); err != nil {
			return errs.Wrap(ErrDefenceStateInvalid, p.st.cid, err, map[string]any{"cause": err.Error()})
		}
		w.mu.Lock()
		w.lastMonth = wire.LastMonth
		w.postedProcurement = wire.PostedProcurement
		w.lastBid = wire.LastBid
		w.mu.Unlock()
		p.st.nextCitizenID = wire.NextCitizenID
		return nil
	}
}

// digestInto folds the defence observables into the composition state digest.
func (w *defenceWiring) digestInto(h hash.Hash) {
	if !w.checkNotCopied() {
		_ = errDefenceCopied("", "digestInto")
		return
	}
	s := w.api.Snapshot()
	w.mu.RLock()
	defer w.mu.RUnlock()
	digestInt64(h, int64(s.BidSeq))
	digestInt64(h, int64(len(s.BidMonths)))
	for _, b := range s.BidMonths {
		digestInt64(h, int64(len(b.Pot)))
		_, _ = h.Write([]byte(b.Pot))
		digestInt64(h, b.Month)
	}
	digestFloat(h, s.PlanningQuality)
	digestFloat(h, s.WageBillFactor)
	digestInt64(h, s.ReputationPenalty)
	digestInt64(h, int64(s.NextFacilityID))
	digestInt64(h, int64(len(s.Answers)))
	for _, a := range s.Answers {
		digestInt64(h, int64(len(a.MandateID)))
		_, _ = h.Write([]byte(a.MandateID))
		if a.Refused {
			digestInt64(h, 1)
		} else {
			digestInt64(h, 0)
		}
		digestInt64(h, int64(a.FacilityID))
		digestInt64(h, a.Compensation)
	}
	digestInt64(h, int64(len(s.Facilities)))
	for _, f := range s.Facilities {
		digestInt64(h, int64(f.ID))
		digestInt64(h, f.Personnel)
		digestInt64(h, f.NominalPayroll)
		digestInt64(h, f.Procurement)
		digestInt64(h, int64(len(f.IDs)))
		for _, id := range f.IDs {
			digestInt64(h, int64(id))
		}
	}
	digestInt64(h, w.lastMonth)
	digestInt64(h, w.postedProcurement)
}
