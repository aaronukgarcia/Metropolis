package build

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// This file is the screen's player-action surface: the four §13-F3 actions
// (land purchase, zoning, construction, demolition) issued as the
// protocol's real gameplay Kinds (KindBuy/KindZone/KindBuild/KindDemolish
// with their typed payloads — internal/protocol/commands.go, landed in
// commit 613b7d0 for this screen). Unlike ui.screen.trade's create/cancel/
// set-buffer actions (which ride the Debug seam because those Kinds do not
// exist yet), this screen's four Kinds DO exist, so nothing here routes
// gameplay intent through KindDebug.
//
// The engine is the authority on accept/reject: ownership (§7), funds,
// permits, and unlock prerequisites are all resolved engine-side and
// returned on the CommandResult the caller (transport owner) receives —
// never on a payload this screen builds. This screen's own rejections are
// limited to the cases where the COMMAND WOULD BE MALFORMED against the
// view this screen holds (an unknown zone slug, an unknown building ID, a
// demolition cell the view reports no compensation for); those are loud,
// registry-sourced errors (BLD-7), never a silently-dropped command.

// BuyLand issues the §7 land-purchase action (BLD-1) for one cell. It
// carries only WHICH cell — the price is the engine's (f3.build.landPrice
// on the view, engine.finance.LandPrice in the engine) and any rejection
// arrives on the CommandResult. A send failure is returned verbatim, never
// swallowed.
func (s *Screen) BuyLand(send SendCommandFunc, cell protocol.CellRef) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BuyLand"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: cell},
	}
	return send(cmd)
}

// ZonePaint zones a run of cells into zone (BLD-2): it issues exactly one
// KindZone command PER cell, so a painted run is never a silently-dropped
// subset — the caller (and the engine) can count the commands. zone must be
// a zone slug the current f3.build zones view carries; anything else is
// refused loudly with MET-V203 rather than sent as a malformed command. An
// empty cells slice is a no-op (nil) — there is nothing to paint, and
// nothing to drop.
func (s *Screen) ZonePaint(send SendCommandFunc, cells []protocol.CellRef, zone string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ZonePaint"}); err != nil {
		return err
	}
	if !s.hasZone(zone) {
		return errs.New(ErrUnknownZoneType, s.correlationID, map[string]any{"zone": zone})
	}
	for _, cell := range cells {
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(s.correlationID),
			Kind:            protocol.KindZone,
			Payload:         protocol.ZonePayload{Cell: cell, ZoneType: zone},
		}
		if err := send(cmd); err != nil {
			return err
		}
	}
	return nil
}

// BuildOn issues the construction action (BLD-3/BLD-7) for a catalogue
// building on one cell. buildingType must be a building ID the current
// f3.build catalogue view carries; anything else is refused loudly with
// MET-V205 rather than sent as a malformed command. The materials bill,
// labour and lead time are the engine's, not carried here.
func (s *Screen) BuildOn(send SendCommandFunc, cell protocol.CellRef, buildingType string) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BuildOn"}); err != nil {
		return err
	}
	if !s.hasBuilding(buildingType) {
		return errs.New(ErrUnknownBuilding, s.correlationID, map[string]any{"building": buildingType})
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: cell, BuildingType: buildingType},
	}
	return send(cmd)
}

// DemolishCost returns the compensation demolishing cell would incur
// (BLD-4): the figure the f3.build demolition view reports for that cell.
// It is the "surface the cost BEFORE commit" half of BLD-4 — the caller
// shows this figure in a confirmation step, then calls Demolish. found is
// false when the view has no demolition record for cell (or the demolition
// sub-surface is unavailable).
func (s *Screen) DemolishCost(cell protocol.CellRef) (compensationMicropounds int64, found bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "DemolishCost"}); err != nil {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "DemolishCost"}); err != nil {
		return 0, false
	}
	if s.demolition == nil || s.demolition.Cell != cell {
		return 0, false
	}
	return s.demolition.CompensationMicropounds, true
}

// Demolish issues the demolition action (BLD-4) for one cell. It is the
// "commit" half of BLD-4 — the caller is expected to have surfaced the
// compensation via DemolishCost first. A demolish against a cell the
// current view reports no compensation for is refused loudly with
// MET-V204 (the cost-showing step cannot be skipped: no reported cost, no
// demolition command). Because DemolishPayload carries only the cell (the
// engine computes compensation and returns it on the CommandResult), the
// command's "value" is exactly the cell whose cost DemolishCost reported —
// see ASM-1448.
func (s *Screen) Demolish(send SendCommandFunc, cell protocol.CellRef) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Demolish"}); err != nil {
		return err
	}
	if _, found := s.DemolishCost(cell); !found {
		return errs.New(ErrUnknownStructure, s.correlationID, map[string]any{"x": cell.X, "y": cell.Y})
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindDemolish,
		Payload:         protocol.DemolishPayload{Cell: cell},
	}
	return send(cmd)
}

// hasZone reports whether zone is present in the current f3.build zones
// view. Reads s.zones under mu.
func (s *Screen) hasZone(zone string) bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasZone"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasZone"}); err != nil {
		return false
	}
	for _, z := range s.zones {
		if z.Zone == zone {
			return true
		}
	}
	return false
}

// hasBuilding reports whether building is present in the current f3.build
// catalogue view. Reads s.catalogue under mu.
func (s *Screen) hasBuilding(building string) bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasBuilding"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "hasBuilding"}); err != nil {
		return false
	}
	for _, e := range s.catalogue {
		if e.ID == building {
			return true
		}
	}
	return false
}
