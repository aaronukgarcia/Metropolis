package destination

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/engine/parking"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// DestinationAPI is code.json's "engine.destination" inbound contract (GUID
// 3d6b9b6a-d30c-49ea-a32d-0dfc70ae4be1, "DestinationAPI", "regional-draw
// machinery shared with TourismAPI"): the two §48 buildable destination
// archetypes (forest resort, mega-mall), the mega-mall's reclamation-site
// eligibility and viewshed screening (through engine.mining's general blight
// model), and its colossal parking demand (through engine.parking), all
// behind this single API.
//
// The zero value is not usable; construct via [New], [Load], or
// [LoadDefault]. A *DestinationAPI is safe for concurrent use (AC-14):
// every mutable field is guarded by mu, and checkNotCopied rejects a method
// call on a struct-copied value (SEC-020 family, mirroring engine.crime).
type DestinationAPI struct {
	correlationID string
	seed          uint64
	cfg           config

	mu sync.RWMutex

	tourism TourismDraw
	mining  MiningBlight
	parking ParkingSink

	destinations map[DestinationID]*destinationState
	nextID       DestinationID

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[DestAPI]
}

// DestAPI is the conventional short name for [DestinationAPI] (the
// code.json inbound name). Kept as an alias so the self-copy-guard field
// reads as `atomic.Pointer[DestAPI]` while the contract name resolves in
// `go doc`.
type DestAPI = DestinationAPI

// destinationState is one placed destination's record.
type destinationState struct {
	id       DestinationID
	kind     ArchetypeKind
	tile     world.TileCoord
	local    world.CellLocal
	screened bool

	// blightKey names the registered engine.mining blighting object; empty
	// when the archetype is not a blight source.
	blightKey string

	// parkingSpaces is the demand figure pushed to engine.parking at
	// placement (AC-9).
	parkingSpaces int64
}

// New constructs a DestAPI from a validated config. correlationID is
// attached to every error the returned API constructs (GR#1); an empty one
// mints a fresh ID. An invalid config is a registry-sourced
// ErrMalformedConfig, never a silent default (AC-11).
func New(seed uint64, cfg config, correlationID string) (*DestinationAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrMalformedConfig, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	d := &DestinationAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		destinations:  make(map[DestinationID]*destinationState),
		nextID:        1,
	}
	// Armed exactly once, before d is returned to any caller (SEC-020).
	d.self.Store(d)
	return d, nil
}

// Load reads and validates data/destination.json from dir and returns a
// ready *DestinationAPI (GR#15). Every failure is a registry-sourced *errs.E —
// never a panic or a silent default.
func Load(dir, correlationID string) (*DestinationAPI, error) {
	cfg, err := loadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(0, cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*DestinationAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *DestinationAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (d *DestinationAPI) checkNotCopied(method string) error {
	if d.self.Load() != d {
		return errs.New(ErrCopiedValue, d.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetTourism wires the registered engine.destination → engine.tourism seam
// (AC-1). Read by RegionalDraw.
func (d *DestinationAPI) SetTourism(t TourismDraw) error {
	if err := d.checkNotCopied("SetTourism"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tourism = t
	return nil
}

// SetMining wires the registered engine.destination → engine.mining seam
// (AC-3/AC-4). Read by Place and ViewshedBlightAt.
func (d *DestinationAPI) SetMining(m MiningBlight) error {
	if err := d.checkNotCopied("SetMining"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.mining = m
	return nil
}

// SetParking wires the registered engine.destination → engine.parking seam
// (AC-9). Read by Place for the mega-mall.
func (d *DestinationAPI) SetParking(p ParkingSink) error {
	if err := d.checkNotCopied("SetParking"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.parking = p
	return nil
}

// archetype returns the data-loaded archetype config for a kind. cfg is
// immutable after New, so no lock is needed. Callers must have validated
// the kind.
func (d *DestinationAPI) archetype(k ArchetypeKind) archetypeConfig {
	return d.cfg.Archetypes[k.String()]
}

// Archetype returns the exported, data-loaded characteristic set for a kind
// (AC-2). An out-of-range kind is rejected.
func (d *DestinationAPI) Archetype(k ArchetypeKind) (Archetype, error) {
	if err := d.checkNotCopied("Archetype"); err != nil {
		return Archetype{}, err
	}
	if int(k) >= int(numArchetypes) {
		return Archetype{}, errs.New(ErrInvalidSite, d.correlationID, map[string]any{"field": "archetype", "value": int(k)})
	}
	ar := d.archetype(k)
	return Archetype{
		Kind:              k,
		Name:              ar.Name,
		Jobs:              ar.Jobs,
		MinFootprintHa:    ar.MinFootprintHa,
		MinShopFloorspace: ar.MinShopFloorspace,
		YearRoundStaying:  ar.YearRoundStaying,
		ParkingSpaces:     ar.ParkingSpaces,
	}, nil
}

// Place records a destination placement after validating it against the
// archetype's minimum footprint and, for the mega-mall, the registered
// engine.mining reclamation gate (AC-3), the viewshed blight registration
// plus optional screening wall (AC-4), and the engine.parking demand push
// (AC-9). On any failure nothing is recorded locally (AC-10) — never a
// silently-applied no-op placement.
func (d *DestinationAPI) Place(req PlacementRequest) (DestinationID, error) {
	if err := d.checkNotCopied("Place"); err != nil {
		return 0, err
	}
	if int(req.Kind) >= int(numArchetypes) {
		return 0, errs.New(ErrInvalidSite, d.correlationID, map[string]any{"field": "archetype", "value": int(req.Kind)})
	}
	ar := d.archetype(req.Kind)
	if !num.IsFinite(req.FootprintHa) || req.FootprintHa < ar.MinFootprintHa {
		return 0, errs.New(ErrInvalidSite, d.correlationID, map[string]any{
			"field": "footprintHa", "value": req.FootprintHa, "min": ar.MinFootprintHa,
		})
	}

	// Resolve the seams before any mutation (each may be a composition-root
	// call with its own locking).
	d.mu.RLock()
	miningSeam := d.mining
	parkingSeam := d.parking
	d.mu.RUnlock()

	var class mining.BlightClass
	if req.Kind == ArchetypeMegaMall {
		if req.SiteKey == "" {
			return 0, errs.New(ErrInvalidSite, d.correlationID, map[string]any{"field": "siteKey", "value": ""})
		}
		if miningSeam == nil {
			return 0, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"dependency": "mining", "operation": "Place(mega-mall)"})
		}
		// AC-3: the mega-mall is a pit-reclamation option (§32) — legal only
		// on a site the blight model reports as an exhausted, not-yet-reclaimed
		// extraction pit.
		site, err := miningSeam.SiteInfo(req.SiteKey, d.correlationID)
		if err != nil {
			return 0, err
		}
		if !site.Exhausted || site.Reclaimed != nil {
			return 0, errs.New(ErrNotReclamationSite, d.correlationID, map[string]any{
				"siteKey": req.SiteKey, "exhausted": site.Exhausted, "reclaimed": site.Reclaimed != nil,
			})
		}
		if parkingSeam == nil {
			return 0, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"dependency": "parking", "operation": "Place(mega-mall)"})
		}
		// Resolve the blight class before any id is burned or seam mutated: a
		// data-file blightClass the mining model doesn't recognise is a
		// placement-wide validation failure (AC-10), never an id-consuming or
		// seam-mutating one.
		var ok bool
		class, ok = blightClass(ar.BlightClass)
		if !ok {
			return 0, errs.New(ErrInvalidSite, d.correlationID, map[string]any{"field": "blightClass", "value": ar.BlightClass})
		}
	}

	// AC-4: screening is a data-height wall bund inserted into mining's own
	// viewshed line-of-sight occluder model — never a destination-local
	// reduction coefficient (§32). It needs no id, so it runs before the id is
	// burned: a failed screening bund can never consume an id.
	if req.Kind == ArchetypeMegaMall && req.Screened {
		if err := miningSeam.AddBund(req.Tile, req.Local, ar.ScreenWallHeightM, d.correlationID); err != nil {
			return 0, err
		}
	}

	// Allocate the id under the write lock; the id doubles as the blight
	// object key and the parking facility id. Everything locally-validatable
	// (including the mega-mall's blight-class resolution) has already been
	// rejected above, before the id is burned.
	d.mu.Lock()
	id := d.nextID
	d.nextID++
	d.mu.Unlock()

	// The resort is a non-blighting archetype: it leaves blightKey empty so
	// [ViewshedBlightAt] reports zero for it (AC-4).
	var blightKey string
	if req.Kind == ArchetypeMegaMall {
		blightKey = fmt.Sprintf("destination-%d", id)

		// AC-9: push the data-driven parking demand through engine.parking.
		if err := parkingSeam.RegisterFacility(uint64(id), req.Tile, req.Local, int(ar.ParkingSpaces), parking.MultiStorey, req.District); err != nil {
			return 0, err
		}

		// Mining blight registration — the LAST seam call, immediately before
		// the local commit, so a failed bund or parking registration above can
		// never leave an orphaned blight object behind (the mining seam offers
		// no remove).
		if err := miningSeam.PlaceBlightingObject(mining.BlightingObjectSpec{
			Key:             blightKey,
			Class:           class,
			Tile:            req.Tile,
			Local:           req.Local,
			NoiseRadiusM:    ar.NoiseRadiusM,
			VisualHeightM:   ar.VisualHeightM,
			VisualMagnitude: ar.VisualMagnitude,
		}); err != nil {
			return 0, err
		}
	}

	d.mu.Lock()
	d.destinations[id] = &destinationState{
		id:            id,
		kind:          req.Kind,
		tile:          req.Tile,
		local:         req.Local,
		screened:      req.Screened,
		blightKey:     blightKey,
		parkingSpaces: ar.ParkingSpaces,
	}
	d.mu.Unlock()

	return id, nil
}

// blightClass resolves a data/destination.json blight-class name to the
// engine.mining ordinal it names (GR#3: no local name table — derive from
// mining.BlightClass.String()). It fails for "none" (a non-blight source,
// which never reaches this path).
func blightClass(name string) (mining.BlightClass, bool) {
	for b := mining.BlightLow; b <= mining.BlightSevere; b++ {
		if b.String() == name {
			return b, true
		}
	}
	return 0, false
}

// RegionalDraw returns the destination's regional draw (AC-1): the tourism
// portfolio score (the shared decomposed machinery, via the registered
// engine.destination → engine.tourism edge) multiplied by the archetype's
// data-driven draw factor. For the forest resort the factor additionally
// carries the §48 BDI synergy (AC-5), a concave function of the surrounding
// Biodiversity Index. bdi is a [0,1] fixture input (the surrounding region's
// BDI — no live BDI edge is registered, see the AC-5 escalation).
func (d *DestinationAPI) RegionalDraw(id DestinationID, bdi float64) (float64, error) {
	if err := d.checkNotCopied("RegionalDraw"); err != nil {
		return 0, err
	}
	if !num.IsFinite(bdi) || bdi < 0 || bdi > 1 {
		return 0, errs.New(ErrInvalidSite, d.correlationID, map[string]any{"field": "bdi", "value": bdi})
	}
	d.mu.RLock()
	tourismSeam := d.tourism
	st, ok := d.destinations[id]
	d.mu.RUnlock()
	if !ok {
		return 0, errs.New(ErrUnknownDestination, d.correlationID, map[string]any{"destination": id})
	}
	if tourismSeam == nil {
		return 0, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"dependency": "tourism", "operation": "RegionalDraw"})
	}
	portfolio, err := tourismSeam.PortfolioScore()
	if err != nil {
		return 0, err
	}
	return portfolio * d.drawFactor(st, bdi), nil
}

// drawFactor returns the archetype's regional-draw multiplier. The resort's
// factor carries the BDI synergy term; the mall's is flat (the mall draws
// retail spend, not biodiversity).
func (d *DestinationAPI) drawFactor(st *destinationState, bdi float64) float64 {
	ar := d.archetype(st.kind)
	switch st.kind {
	case ArchetypeForestResort:
		return ar.BaseDrawFactor * (1 + bdiSynergy(bdi, ar.BDIHalfSaturation, ar.BDIMaxBoost))
	default:
		return ar.BaseDrawFactor
	}
}

// bdiSynergy is the concave (diminishing-return) BDI boost the resort's draw
// factor carries (AC-5): maxBoost · BDI/(BDI + halfSaturation). Pure and
// deterministic.
func bdiSynergy(bdi, halfSaturation, maxBoost float64) float64 {
	if halfSaturation <= 0 {
		return 0
	}
	return maxBoost * (bdi / (bdi + halfSaturation))
}

// ViewshedBlightAt returns the destination's viewshed-blight (SEEN)
// contribution at a neighbouring cell (AC-4), read from the registered
// engine.mining blight model's EffectAt — never a destination-local
// formula. A non-blighting destination (the resort) contributes zero.
func (d *DestinationAPI) ViewshedBlightAt(id DestinationID, tile world.TileCoord, local world.CellLocal, year int64) (float64, error) {
	if err := d.checkNotCopied("ViewshedBlightAt"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	miningSeam := d.mining
	st, ok := d.destinations[id]
	d.mu.RUnlock()
	if !ok {
		return 0, errs.New(ErrUnknownDestination, d.correlationID, map[string]any{"destination": id})
	}
	if st.blightKey == "" {
		return 0, nil
	}
	if miningSeam == nil {
		return 0, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"dependency": "mining", "operation": "ViewshedBlightAt"})
	}
	eff, err := miningSeam.EffectAt(tile, local, year, d.correlationID)
	if err != nil {
		return 0, err
	}
	return eff.Seen, nil
}

// ParkingDemand returns the parking-space demand figure pushed for a
// destination at placement (AC-9) — queryable/consumable by engine.parking.
func (d *DestinationAPI) ParkingDemand(id DestinationID) (int64, error) {
	if err := d.checkNotCopied("ParkingDemand"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	st, ok := d.destinations[id]
	d.mu.RUnlock()
	if !ok {
		return 0, errs.New(ErrUnknownDestination, d.correlationID, map[string]any{"destination": id})
	}
	return st.parkingSpaces, nil
}

// DestinationIDs returns the ids of every placed destination, ascending
// (deterministic — never a map range in observable order, GR#21).
func (d *DestinationAPI) DestinationIDs() []DestinationID {
	if err := d.checkNotCopied("DestinationIDs"); err != nil {
		return nil
	}
	d.mu.RLock()
	ids := make([]DestinationID, 0, len(d.destinations))
	for id := range d.destinations {
		ids = append(ids, id)
	}
	d.mu.RUnlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
