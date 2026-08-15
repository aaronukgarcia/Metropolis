package spiral

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file implements AC-5's three distinct recovery commands (demolition,
// targeted investment, tax-relief-district designation) and AC-12's typed
// rejection of a recovery command against a cell with no decay state.

// DemolitionCommand demolishes one decayed cell, removing its decay state
// entirely at the cell's demolition cost (AC-5, AC-3's DemolitionCost).
type DemolitionCommand struct {
	CorrelationID string
	Cell          CellRef
}

// TargetedInvestmentCommand invests in one decayed cell, reducing its decay
// severity by a fixed, data-sourced amount at a fixed, data-sourced cost
// (AC-5).
type TargetedInvestmentCommand struct {
	CorrelationID string
	Cell          CellRef
}

// TaxReliefCommand designates a tax-relief district: every decayed cell in
// the named district has its severity reduced, at a per-cell cost (AC-5).
type TaxReliefCommand struct {
	CorrelationID string
	District      []CellRef
}

// RecoveryResult is a recovery command's outcome: the protocol-shaped
// accept/reject (CommandResult + ErrorRef, AC-12), the cost paid, and the
// post-command severity of the (first) affected cell. Cost and SeverityAfter
// are meaningful only when Result.Accepted is true.
type RecoveryResult struct {
	Result        protocol.CommandResult
	Cost          int64 // micro-pounds spent (0 when rejected)
	SeverityAfter int   // severity of the primary cell after the command
}

// recoveryResult builds a RecoveryResult: an accepted command returns the
// cost and post-severity; a rejection returns a CommandResult carrying the
// registry-sourced ErrorRef (AC-12 — never a silent no-op success).
func recoveryResult(correlationID string, accepted bool, code string, ctx map[string]any, cost int64, severity int) RecoveryResult {
	res := RecoveryResult{
		Result: protocol.CommandResult{
			CorrelationID: protocol.CorrelationID(correlationID),
			Accepted:      accepted,
		},
		Cost:          cost,
		SeverityAfter: severity,
	}
	if !accepted {
		res.Result.Error = toErrorRef(code, correlationID, ctx)
	}
	return res
}

// toErrorRef converts a registry-sourced *errs.E into a protocol.ErrorRef
// (the same shape engine.world's toWorldErrorRef uses), so every rejection
// crosses the command seam as data — a MET-xxxx code plus a display string.
func toErrorRef(code, correlationID string, ctx map[string]any) *protocol.ErrorRef {
	e := errs.New(code, correlationID, ctx)
	return &protocol.ErrorRef{Code: e.Code, Display: e.Display()}
}

// RecoverDemolition demolishes a decayed cell (AC-5): it removes the cell's
// decay state entirely, at the cell's own DemolitionCost (a real money
// figure growing with severity/age). A cell with no decay state is rejected
// with ErrNoDecayToRecover (AC-12), never a silent success that would let
// the player "recover" a healthy district for free.
func (d *DecayAPI) RecoverDemolition(cmd DemolitionCommand) RecoveryResult {
	if err := d.checkNotCopied("RecoverDemolition"); err != nil {
		return recoveryResult(cmd.CorrelationID, false, ErrCopiedValue, map[string]any{"method": "RecoverDemolition"}, 0, 0)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoverDemolitionLocked(cmd)
}

func (d *DecayAPI) recoverDemolitionLocked(cmd DemolitionCommand) RecoveryResult {
	if err := d.checkNotCopied("recoverDemolitionLocked"); err != nil {
		return recoveryResult(cmd.CorrelationID, false, ErrCopiedValue, map[string]any{"method": "recoverDemolitionLocked"}, 0, 0)
	}
	st, ok := d.decay[cmd.Cell.key()]
	if !ok {
		return recoveryResult(cmd.CorrelationID, false, ErrNoDecayToRecover, map[string]any{"cell": cmd.Cell.String()}, 0, 0)
	}
	cost := d.DemolitionCost(st.severity, st.age)
	delete(d.decay, cmd.Cell.key())
	return recoveryResult(cmd.CorrelationID, true, "", nil, cost, 0)
}

// RecoverTargetedInvestment invests in one decayed cell (AC-5), reducing
// its severity by the data-sourced reduction amount (clamped at zero) at
// the data-sourced cost — existing decay actually improves, not merely
// stops spreading. A cell with no decay state is rejected (AC-12).
func (d *DecayAPI) RecoverTargetedInvestment(cmd TargetedInvestmentCommand) RecoveryResult {
	if err := d.checkNotCopied("RecoverTargetedInvestment"); err != nil {
		return recoveryResult(cmd.CorrelationID, false, ErrCopiedValue, map[string]any{"method": "RecoverTargetedInvestment"}, 0, 0)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoverInvestmentLocked(cmd.CorrelationID, cmd.Cell)
}

func (d *DecayAPI) recoverInvestmentLocked(correlationID string, cell CellRef) RecoveryResult {
	if err := d.checkNotCopied("recoverInvestmentLocked"); err != nil {
		return recoveryResult(correlationID, false, ErrCopiedValue, map[string]any{"method": "recoverInvestmentLocked"}, 0, 0)
	}
	st, ok := d.decay[cell.key()]
	if !ok {
		return recoveryResult(correlationID, false, ErrNoDecayToRecover, map[string]any{"cell": cell.String()}, 0, 0)
	}
	_ = st.severity
	st.severity -= d.cfg.Recovery.InvestmentSeverityReduction
	if st.severity < 0 {
		st.severity = 0
	}
	if st.severity == 0 {
		// Fully recovered: the cell is no longer decayed, so its decay
		// record is removed (a cell at severity 0 is indistinguishable from
		// healthy).
		delete(d.decay, cell.key())
	}
	return recoveryResult(correlationID, true, "", nil, d.cfg.Recovery.InvestmentCostMicropounds, st.severity)
}

// RecoverTaxRelief designates a tax-relief district (AC-5): every decayed
// cell in the district has its severity reduced by the data-sourced amount
// (clamped at zero), at a per-cell cost. A district with NO decayed cells
// at all is rejected (AC-12) — the player cannot relieve a healthy district
// for free. The cost scales with the number of decayed cells actually
// relieved, not the district's total size.
func (d *DecayAPI) RecoverTaxRelief(cmd TaxReliefCommand) RecoveryResult {
	if err := d.checkNotCopied("RecoverTaxRelief"); err != nil {
		return recoveryResult(cmd.CorrelationID, false, ErrCopiedValue, map[string]any{"method": "RecoverTaxRelief"}, 0, 0)
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	relieved := 0
	var after int
	for _, cell := range cmd.District {
		st, ok := d.decay[cell.key()]
		if !ok {
			continue
		}
		relieved++
		st.severity -= d.cfg.Recovery.TaxReliefSeverityReduction
		if st.severity < 0 {
			st.severity = 0
		}
		if st.severity == 0 {
			delete(d.decay, cell.key())
		}
		after = st.severity
	}
	if relieved == 0 {
		return recoveryResult(cmd.CorrelationID, false, ErrNoDecayToRecover, map[string]any{"district": "no decayed cells"}, 0, 0)
	}
	cost, _ := num.SafeMul(int64(relieved), d.cfg.Recovery.TaxReliefCostPerCellMicropounds)
	return recoveryResult(cmd.CorrelationID, true, "", nil, cost, after)
}

// DecayState returns a cell's exported decay-state snapshot, and whether the
// cell is currently decayed (AC-1's per-cell decay-state query surface).
func (d *DecayAPI) DecayState(cell CellRef) (DecayState, bool) {
	if err := d.checkNotCopied("DecayState"); err != nil {
		return DecayState{}, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	st, ok := d.decay[cell.key()]
	if !ok {
		return DecayState{}, false
	}
	return d.snapshot(st), true
}

// DecayedCellCount returns the number of currently-decayed cells (AC-1).
func (d *DecayAPI) DecayedCellCount() int {
	if err := d.checkNotCopied("DecayedCellCount"); err != nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.decay)
}

// NeighbourLandValueDrag returns the total land-value drag (micro-pounds)
// imposed on cell by every decayed neighbour — the concrete "measurable
// reduction to adjacent cells' land value" §12 names, exposed so a consumer
// (or test) can see the drag the neighbourhood actually experiences, not
// just the per-cell figure (AC-3).
func (d *DecayAPI) NeighbourLandValueDrag(cell CellRef) int64 {
	if err := d.checkNotCopied("NeighbourLandValueDrag"); err != nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var total int64
	for _, n := range neighbours(cell) {
		if st, ok := d.decay[n.key()]; ok {
			total = num.SatAdd(total, d.LandValueDrag(st.severity))
		}
	}
	return total
}
