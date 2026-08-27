package roads

// This file holds the small player-facing command surfaces: the rename
// registry (AC-11) and the player-settable speed limit (AC-2). Both mutate
// state only through these commands, under mu (GR#20).

// RenameCommand records a player-chosen name for any auto-named object.
type RenameCommand struct {
	CorrelationID string
	Kind          ObjectKind
	Seed          uint64
	ID            uint64
	NewName       string
}

// Rename records the player's chosen name for an object, keyed by
// (kind, seed, id). After a rename, the auto-naming service never overwrites
// it on a later naming pass (AC-11): [NameFor]/[NameRoad] consult this
// registry first. An unknown kind is ErrUnknownObjectKind; an empty name is
// ErrInvalidInput.
func (a *RoadsAPI) Rename(cmd RenameCommand) error {
	if err := a.checkNotCopied("Rename"); err != nil {
		return err
	}
	if !cmd.Kind.valid() {
		return roadsErr(a.correlationID, ErrUnknownObjectKind, map[string]any{"kind": uint8(cmd.Kind)})
	}
	if cmd.NewName == "" {
		return invalidInputError(a.correlationID, "NewName", "must be non-empty")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := nameKey{kind: cmd.Kind, seed: cmd.Seed, id: cmd.ID}
	a.renames[key] = cmd.NewName
	// If the rename targets a road already in the graph, reflect it on the
	// road record so [RoadsAPI.RoadInfo] shows the player's name (and clears
	// the continuation-derived name so a renamed road never re-inherits).
	// The reflection keys on the SAME (kind, seed, id) tuple as the registry
	// and NameRoad (SEC-232): every road in the graph is auto-named under
	// a.seed, so a rename for a DIFFERENT seed must not overwrite that road's
	// record — otherwise RoadInfo.Name would diverge from NameRoad (GR#3).
	if cmd.Kind == KindRoad && cmd.Seed == a.seed {
		if rs, ok := a.roads[RoadID(cmd.ID)]; ok {
			rs.name = cmd.NewName
			rs.renamed = true
		}
	}
	return nil
}

// SetSpeedLimitCommand sets a road's speed limit (AC-2's "player-settable
// within class bounds").
type SetSpeedLimitCommand struct {
	CorrelationID string
	RoadID        RoadID
	KPH           int
}

// SetSpeedLimit sets a road's speed limit, clamped to nothing: a KPH outside
// the road class's speedMin..speedMax is rejected with ErrSpeedLimitOutOfBounds
// (never silently clamped — GR#16). An unknown road is ErrRoadNotFound.
func (a *RoadsAPI) SetSpeedLimit(cmd SetSpeedLimitCommand) error {
	if err := a.checkNotCopied("SetSpeedLimit"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rs, ok := a.roads[cmd.RoadID]
	if !ok {
		return roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(cmd.RoadID)})
	}
	cc := a.cfg.classes[rs.class]
	if cmd.KPH < cc.SpeedMin || cmd.KPH > cc.SpeedMax {
		return roadsErr(a.correlationID, ErrSpeedLimitOutOfBounds, map[string]any{
			"road":  uint64(rs.id),
			"kph":   cmd.KPH,
			"min":   cc.SpeedMin,
			"max":   cc.SpeedMax,
			"class": rs.class.String(),
		})
	}
	rs.speedLimit = cmd.KPH
	return nil
}
