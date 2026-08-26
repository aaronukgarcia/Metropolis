package mining

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the geology-gated extraction siting half of engine.mining
// (MOD-046 core, AC-2/AC-3/AC-9/AC-10): the six §32 extraction types are
// sited through [BlightAPI.SiteExtraction], gated by engine.world's
// prospected geology (never a mining-local geology copy — GR#3), and each
// carries a data-sourced capacity; deep coal additionally carries a
// spoil-tip blighting object and a subsidence-risk radius; exhausted sites
// reclaim to lake/park; deep-mine closure exposes the workforce-at-risk
// figure.

// ExtractionSite is the read-only snapshot of one sited extraction facility.
type ExtractionSite struct {
	Key               string
	TypeKey           string
	Tile              world.TileCoord
	Local             world.CellLocal
	OutputCommodity   string
	OutputRate        float64 // t/day — the distinct output accessor (AC-2)
	Capacity          float64 // tonnes — data-sourced (outputRate × capacityDays)
	Extracted         float64 // cumulative tonnes produced
	Exhausted         bool
	Reclaimed         *ReclaimOption // nil until Reclaim succeeds
	Jobs              int
	SubsidenceRadiusM float64 // deep coal only, 0 otherwise (AC-3)
	SpoilTipFootprint int     // deep coal only, 0 otherwise (AC-3)
	Closed            bool
}

// SiteCommand is the siting command (AC-2): site the named mine type at a
// tile/local cell, gated by that type's geology class against world's
// prospected geology.
type SiteCommand struct {
	CorrelationID string
	Key           string // caller-chosen, unique site key (also the blight object key)
	TypeKey       string // catalogue key (chalk/sand_gravel/clay_brickworks/ragstone/deep_coal/offshore_dredger)
	Tile          world.TileCoord
	Local         world.CellLocal
}

// extractionSite is the internal mutable site record.
type extractionSite struct {
	key               string
	typeKey           string
	tile              world.TileCoord
	local             world.CellLocal
	outputCommodity   string
	outputRate        float64
	capacity          float64
	extracted         float64
	exhausted         bool
	reclaimed         *ReclaimOption
	jobs              int
	subsidenceRadiusM float64
	spoilTipFootprint int
	spoilTipKey       string // blight object key of the registered spoil tip ("" when none)
	closed            bool
}

// exhaustedAndReclaimed reports whether the site is no longer extractable.
func (s *extractionSite) exhaustedAndReclaimed() bool { return s.exhausted || s.reclaimed != nil }

// snapshot builds the exported read-only view.
func (s *extractionSite) snapshot() ExtractionSite {
	return ExtractionSite{
		Key:               s.key,
		TypeKey:           s.typeKey,
		Tile:              s.tile,
		Local:             s.local,
		OutputCommodity:   s.outputCommodity,
		OutputRate:        s.outputRate,
		Capacity:          s.capacity,
		Extracted:         s.extracted,
		Exhausted:         s.exhausted,
		Reclaimed:         s.reclaimed,
		Jobs:              s.jobs,
		SubsidenceRadiusM: s.subsidenceRadiusM,
		SpoilTipFootprint: s.spoilTipFootprint,
		Closed:            s.closed,
	}
}

// SiteExtraction sites one extraction type at a cell (AC-2). The availability
// gate is engine.world's prospected geology query: the type's geology class
// (chalk/clay/gravel/deep_coal from data/minetypes.json) must match the
// tile's revealed pocket geology, and an unprospected tile is rejected
// (AC-11). On success the mine is registered against BlightAPI (its own
// blight class + data-sourced profile), and a deep coal mine additionally
// registers its spoil tip as a minor blighting object and carries its
// subsidence radius.
func (b *BlightAPI) SiteExtraction(cmd SiteCommand) error {
	if err := b.checkNotCopied("SiteExtraction"); err != nil {
		return err
	}
	corr := cmd.CorrelationID
	if corr == "" {
		corr = b.correlationID
	}
	if cmd.Key == "" {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{"field": "key", "rule": "must be non-empty"})
	}
	if !cmd.Tile.InExtent() || !cmd.Local.InBounds() {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{
			"field": "tile/local", "tile": cmd.Tile, "local": cmd.Local, "rule": "out of extent",
		})
	}

	// Resolve the type from the loaded catalogue, then gate on geology.
	params, err := b.resolveType(cmd.TypeKey, corr)
	if err != nil {
		return err
	}
	if err := b.checkGeologyGate(params, cmd.Tile, corr); err != nil {
		return err
	}

	// Capacity is data-sourced: the type's output rate × data/mining.json's
	// capacity days (ASM-317), never a hardcoded tonne figure.
	capacity := params.OutputRate * b.cfg.Extraction.CapacityDays

	// Build the site's blight profile from its class (data-sourced).
	prof, ok := b.cfg.classProfile(params.BlightClass)
	if !ok {
		return miningBlightDataInvalid(corr, "classProfile", "class not found in blight-model config")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.sites[cmd.Key]; exists {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{"key": cmd.Key, "rule": "site key already in use"})
	}
	if _, exists := b.objects[cmd.Key]; exists {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{"key": cmd.Key, "rule": "blight object key already in use"})
	}

	site := &extractionSite{
		key:             cmd.Key,
		typeKey:         cmd.TypeKey,
		tile:            cmd.Tile,
		local:           cmd.Local,
		outputCommodity: params.OutputCommodity,
		outputRate:      params.OutputRate,
		capacity:        capacity,
		jobs:            params.Jobs,
	}
	// Deep coal: spoil tip + subsidence (AC-3).
	spoilTip, subsidenceRadius := params.Subsidence()
	site.spoilTipFootprint = spoilTip
	site.subsidenceRadiusM = subsidenceRadius

	b.sites[cmd.Key] = site
	b.objects[cmd.Key] = &blightingObject{
		key:             cmd.Key,
		class:           params.BlightClass,
		tile:            cmd.Tile,
		local:           cmd.Local,
		hasLoc:          true,
		noiseRadiusM:    prof.NoiseRadiusM,
		visualHeightM:   prof.VisualHeightM,
		visualMagnitude: prof.Magnitude,
	}

	// The spoil tip is itself a minor blighting object (AC-3): registered
	// against BlightAPI like any other, with the data-sourced spoil-tip
	// profile, distinct from the mine's own contour.
	if spoilTip > 0 {
		site.spoilTipKey = cmd.Key + ":spoil"
		b.objects[site.spoilTipKey] = &blightingObject{
			key:             site.spoilTipKey,
			class:           b.cfg.SpoilTip.Class,
			tile:            cmd.Tile,
			local:           cmd.Local,
			hasLoc:          true,
			noiseRadiusM:    b.cfg.SpoilTip.NoiseRadiusM,
			visualHeightM:   b.cfg.SpoilTip.HeightM,
			visualMagnitude: b.cfg.ClassProfile[b.cfg.SpoilTip.Class].Magnitude,
		}
	}
	return nil
}

// resolveType resolves a mine-type key against the wired catalogue
// (feat.minetypes). An unwired catalogue rejects every siting attempt loudly
// rather than resolving against a zero catalogue.
func (b *BlightAPI) resolveType(typeKey, corr string) (MineTypeParams, error) {
	b.mu.RLock()
	c := b.catalogue
	b.mu.RUnlock()
	if c.Len() == 0 {
		return MineTypeParams{}, errs.New(ErrSitingNotPermitted, corr, map[string]any{
			"typeKey": typeKey, "rule": "mine-type catalogue not wired",
		})
	}
	return c.Resolve(typeKey)
}

// checkGeologyGate enforces the §32/§34 geology gate (AC-2/AC-11): the tile
// must be prospected (revealed geology), and the type's geology class must
// match. "chalk" matches the always-present baseline (chalk everywhere);
// clay/gravel/deep_coal must match the revealed pocket.
func (b *BlightAPI) checkGeologyGate(params MineTypeParams, tile world.TileCoord, corr string) error {
	prospected, err := b.world.IsProspected(tile)
	if err != nil {
		return err
	}
	if !prospected {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{"tile": tile, "rule": "unprospected land"})
	}
	gate := params.GeologyClass
	if gate == "" {
		// Deposit-backed-only types would be gated here by FEAT-051; the six
		// §32 catalogue types all declare a geology gate (validated at load).
		return nil
	}
	if gate == "chalk" {
		// §32: chalk is the baseline formation, common knowledge everywhere —
		// a chalk quarry is siteable on any prospected tile.
		return nil
	}
	pocket, err := b.world.PocketGeology(tile, corr)
	if err != nil {
		return err
	}
	if pocket.String() != gate {
		return errs.New(ErrSitingNotPermitted, corr, map[string]any{
			"tile": tile, "rule": "geology gate", "want": gate, "got": pocket.String(),
		})
	}
	return nil
}

// Extract produces up to tonnes from a site, exhausting it when cumulative
// extraction reaches its data-sourced capacity (AC-9). A further Extract on an
// exhausted or reclaimed site is rejected (never a silent no-op).
func (b *BlightAPI) Extract(siteKey string, tonnes float64, correlationID string) (float64, error) {
	if err := b.checkNotCopied("Extract"); err != nil {
		return 0, err
	}
	if tonnes <= 0 || math.IsNaN(tonnes) || math.IsInf(tonnes, 0) {
		return 0, errs.New(ErrExtractionInvalid, correlationID, map[string]any{"tonnes": tonnes, "rule": "must be finite and positive"})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sites[siteKey]
	if !ok {
		return 0, errs.New(ErrUnknownBlightKey, correlationID, map[string]any{"key": siteKey})
	}
	if s.exhaustedAndReclaimed() || s.closed {
		var rule string
		if s.exhaustedAndReclaimed() {
			rule = "exhausted or reclaimed"
		} else {
			rule = "closed"
		}
		return 0, miningSiteExhaustedInvalid(correlationID, siteKey, rule)
	}
	remaining := s.capacity - s.extracted
	take := tonnes
	if take > remaining {
		take = remaining
	}
	s.extracted += take
	if s.extracted >= s.capacity {
		s.exhausted = true
	}
	return take, nil
}

// CloseSite triggers deep-mine closure (AC-10): the site stops producing, and
// the workforce-at-risk figure remains queryable via [BlightAPI.WorkforceAtRisk]
// — the §32 "scripted-by-you Detroit test" consequence made checkable, not a
// narrative aside.
func (b *BlightAPI) CloseSite(siteKey string, correlationID string) error {
	if err := b.checkNotCopied("CloseSite"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sites[siteKey]
	if !ok {
		return errs.New(ErrUnknownBlightKey, correlationID, map[string]any{"key": siteKey})
	}
	if s.closed {
		return errs.New(ErrSiteExhausted, correlationID, map[string]any{"key": siteKey, "rule": "already closed"})
	}
	s.closed = true
	return nil
}

// SiteInfo returns the read-only snapshot of one sited extraction facility.
func (b *BlightAPI) SiteInfo(siteKey string, correlationID string) (ExtractionSite, error) {
	if err := b.checkNotCopied("SiteInfo"); err != nil {
		return ExtractionSite{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.sites[siteKey]
	if !ok {
		return ExtractionSite{}, errs.New(ErrUnknownBlightKey, correlationID, map[string]any{"key": siteKey})
	}
	return s.snapshot(), nil
}
