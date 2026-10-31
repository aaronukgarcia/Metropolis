package policies

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Save/restore surface (feat.compositionroot save participant, AC-9 of
// docs/planning/acceptance/wire-engine.policies.md). The package kept all of
// its mutable state private, so the composition root's save participant
// needs an explicit, data-only export and an import that re-applies the
// side effects Enact had on engine.projections and engine.tax WITHOUT
// re-posting any money (the ledger is restored by engine.finance's own
// participant; replaying Enact would double-debit).

// EnactmentState is one active enactment in a StateSnapshot.
type EnactmentState struct {
	ID       EnactmentID
	PolicyID PolicyID
	Scope    Scope
	Month    int64
}

// DistrictState is one district in a StateSnapshot.
type DistrictState struct {
	ID    DistrictID
	Name  string
	Cells []CellRef
}

// RoadState is one registered road in a StateSnapshot.
type RoadState struct {
	ID    RoadID
	Edges []EdgeRef
}

// PreviewSeries is one coefficient key's stored Computed points.
type PreviewSeries struct {
	Key    string
	Points []projections.Point
}

// PreviewState is one enactment's stored AC-7 preview snapshot.
type PreviewState struct {
	EnactmentID EnactmentID
	Horizon     int64
	Deltas      []CoefficientDelta
	Series      []PreviewSeries
}

// StateSnapshot is the complete, deterministic (sorted) mutable state of a
// PoliciesAPI.
type StateSnapshot struct {
	Enactments      []EnactmentState
	Districts       []DistrictState
	Roads           []RoadState
	Previews        []PreviewState
	Events          []PreviewDriftEvent
	Warnings        []ConflictWarning
	NextEnactmentID uint64
	NextDistrictID  uint64
	CurrentMonth    int64
	LastPostedMonth int64
}

// ExportState returns the PoliciesAPI's mutable state in a deterministic
// order (GR#21). The returned value shares no memory with the API.
func (a *PoliciesAPI) ExportState() StateSnapshot {
	if err := a.checkNotCopied("ExportState"); err != nil {
		return StateSnapshot{}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	s := StateSnapshot{
		NextEnactmentID: a.nextEnactmentID,
		NextDistrictID:  a.nextDistrictID,
		CurrentMonth:    a.currentMonth,
		LastPostedMonth: a.lastPostedMonth,
		Events:          append([]PreviewDriftEvent(nil), a.events...),
		Warnings:        append([]ConflictWarning(nil), a.warnings...),
	}

	ids := make([]EnactmentID, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		e := a.active[id]
		s.Enactments = append(s.Enactments, EnactmentState{ID: e.id, PolicyID: e.policyID, Scope: e.scope, Month: e.month})
		if sp, ok := a.previews[id]; ok {
			ps := PreviewState{EnactmentID: id, Horizon: sp.horizon, Deltas: append([]CoefficientDelta(nil), sp.deltas...)}
			keys := make([]string, 0, len(sp.points))
			for k := range sp.points {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				ps.Series = append(ps.Series, PreviewSeries{Key: k, Points: append([]projections.Point(nil), sp.points[k]...)})
			}
			s.Previews = append(s.Previews, ps)
		}
	}
	for _, did := range a.sortedDistrictIDsLocked() {
		d := a.districts[did]
		s.Districts = append(s.Districts, DistrictState{ID: did, Name: d.name, Cells: copyCells(d.cells)})
	}
	rids := make([]RoadID, 0, len(a.roads))
	for rid := range a.roads {
		rids = append(rids, rid)
	}
	sort.Slice(rids, func(i, j int) bool { return rids[i] < rids[j] })
	for _, rid := range rids {
		s.Roads = append(s.Roads, RoadState{ID: rid, Edges: copyEdges(a.roads[rid].edges)})
	}
	return s
}

// ImportState REPLACES the PoliciesAPI's mutable state with s. Every
// enactment's policy must exist in the loaded library and every scope must
// be well-formed (a hostile or stale snapshot is rejected before anything
// mutates). The projection decisions and district tax multipliers of the
// enactments being replaced are removed, then those of the incoming
// enactments are applied. No money is posted.
func (a *PoliciesAPI) ImportState(s StateSnapshot) error {
	if err := a.checkNotCopied("ImportState"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	// ---- validate (no mutation) ----
	seen := make(map[EnactmentID]bool, len(s.Enactments))
	for _, e := range s.Enactments {
		if _, ok := a.library[e.PolicyID]; !ok {
			return errs.New(ErrUnknownPolicy, a.correlationID, map[string]any{"policy": string(e.PolicyID)})
		}
		if !e.Scope.valid() || e.ID == "" || seen[e.ID] {
			return errs.New(ErrUnknownScope, a.correlationID, map[string]any{"scope": "invalid restored enactment " + string(e.ID)})
		}
		seen[e.ID] = true
	}
	if len(s.Enactments) > 0 && a.projections == nil {
		return errs.New(ErrProjectionsNotWired, a.correlationID, map[string]any{"operation": "ImportState"})
	}

	// ---- unwind the current enactments' side effects ----
	type touch struct {
		district   tax.DistrictID
		instrument string
	}
	var touches []touch
	for _, e := range a.sortedActiveEnactmentsLocked() {
		def := a.library[e.policyID]
		if def == nil {
			continue
		}
		for _, cd := range def.Mechanism {
			if a.projections != nil {
				_ = a.projections.CancelDecision(string(e.id) + ":" + cd.Key)
			}
			if cd.Tax != nil {
				touches = append(touches, touch{tax.DistrictID(e.scope.District), cd.Tax.Instrument})
			}
		}
	}

	// ---- replace state ----
	a.active = make(map[EnactmentID]*enactment, len(s.Enactments))
	a.previews = make(map[EnactmentID]storedPreview, len(s.Previews))
	for _, e := range s.Enactments {
		a.active[e.ID] = &enactment{id: e.ID, policyID: e.PolicyID, scope: e.Scope, month: e.Month}
	}
	for _, p := range s.Previews {
		sp := storedPreview{deltas: append([]CoefficientDelta(nil), p.Deltas...), points: make(map[string][]projections.Point, len(p.Series)), horizon: p.Horizon}
		for _, se := range p.Series {
			sp.points[se.Key] = append([]projections.Point(nil), se.Points...)
		}
		a.previews[p.EnactmentID] = sp
	}
	a.districts = make(map[DistrictID]*district, len(s.Districts))
	for _, d := range s.Districts {
		a.districts[d.ID] = &district{name: d.Name, cells: copyCells(d.Cells)}
	}
	a.roads = make(map[RoadID]roadDef, len(s.Roads))
	for _, r := range s.Roads {
		a.roads[r.ID] = roadDef{edges: copyEdges(r.Edges)}
	}
	a.events = append([]PreviewDriftEvent(nil), s.Events...)
	a.warnings = append([]ConflictWarning(nil), s.Warnings...)
	a.nextEnactmentID = s.NextEnactmentID
	if a.nextEnactmentID == 0 {
		a.nextEnactmentID = 1
	}
	a.nextDistrictID = s.NextDistrictID
	if a.nextDistrictID == 0 {
		a.nextDistrictID = 1
	}
	a.currentMonth = s.CurrentMonth
	a.lastPostedMonth = s.LastPostedMonth

	// ---- re-apply the incoming enactments' side effects ----
	if a.projections != nil {
		if err := a.projections.SetCurrentMonth(a.currentMonth); err != nil {
			return err
		}
		for _, e := range a.sortedActiveEnactmentsLocked() {
			def := a.library[e.policyID]
			for _, cd := range def.Mechanism {
				if err := a.projections.EnqueueDecision(projections.Decision{
					ID:              string(e.id) + ":" + cd.Key,
					CurveKey:        cd.Key,
					CompletionMonth: e.month,
					Delta:           cd.Delta,
				}); err != nil {
					return err
				}
			}
		}
	}
	if a.tax != nil {
		for _, e := range a.sortedActiveEnactmentsLocked() {
			def := a.library[e.policyID]
			for _, cd := range def.Mechanism {
				if cd.Tax != nil {
					touches = append(touches, touch{tax.DistrictID(e.scope.District), cd.Tax.Instrument})
				}
			}
		}
		for _, t := range touches {
			if err := a.tax.SetDistrictMultiplier(t.district, t.instrument, a.districtMultiplierFromActiveLocked(t.district, t.instrument)); err != nil {
				return err
			}
		}
	}
	return nil
}
