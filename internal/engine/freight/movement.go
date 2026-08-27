package freight

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Movement is the queryable view of one freight movement (AC-7): a bulk
// quantity of a commodity moving by road/rail/sea, bounded by that mode's
// documented per-movement cap, with a deterministic departure and arrival
// tick. An import/export is a movement to/from the off-map world; a Ship is
// a city-internal movement between storage sites (still en route until its
// ArrivalTick — AC-10's InTransitDelta term).
type Movement struct {
	ID            uint64
	Commodity     Commodity
	Tonnes        int64
	Mode          Mode
	From          SiteType
	To            SiteType
	DepartureTick int64
	ArrivalTick   int64
}

// MovementResult is the outcome of a movement command: the movement that
// was accepted and the tonnes actually moved (a capacity-bounded import may
// move less than requested; a movement whose tonnage was clamped to the
// mode cap never is — over-cap tonnage is rejected outright, AC-13).
type MovementResult struct {
	Movement Movement
	Moved    int64
}

// portDistrict is the district name freight passes to engine.logistics for
// import/export capacity resolution. Freight does not model districts (out
// of scope — engine.logistics's throughput is commodity-global at stub
// depth), so the port's movements use this single constant district.
const portDistrict = "port"

// ModalCap is one transport mode's documented per-movement bulk limits
// (§33/§8, AC-7/AC-13): the maximum per-movement tonnage, the minimum
// (sea's 3kt coaster floor; zero for road/rail), and the coarse lead time.
type ModalCap struct {
	Mode                 Mode
	MaxTonnesPerMovement int64
	MinTonnesPerMovement int64
	LeadTimeTicks        int64
}

// ModalCap returns a transport mode's documented bulk limits from
// data/freight.json. Errors with ErrModalCapExceeded for an unknown mode
// (never a silently-returned zero cap).
func (f *FreightAPI) ModalCap(mode Mode) (ModalCap, error) {
	if err := f.checkNotCopied("ModalCap"); err != nil {
		return ModalCap{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	cap, ok := f.cfg.ModalCaps[mode]
	if !ok {
		return ModalCap{}, modalCapUnknownError(f.correlationID, f.cfg.ModalCaps, mode, 0)
	}
	return ModalCap{
		Mode:                 mode,
		MaxTonnesPerMovement: cap.MaxTonnesPerMovement,
		MinTonnesPerMovement: cap.MinTonnesPerMovement,
		LeadTimeTicks:        cap.LeadTimeTicks,
	}, nil
}

// validateModalCap rejects a movement whose declared tonnage exceeds the
// mode's documented cap, or falls below the sea minimum bulk size (AC-13).
// It returns ErrModalCapExceeded or ErrNegativeTonnage — never a silent clamp to the cap.
func (f *FreightAPI) validateModalCap(commodity Commodity, mode Mode, tonnes int64) error {
	cap, ok := f.cfg.ModalCaps[mode]
	if !ok {
		return modalCapUnknownError(f.correlationID, f.cfg.ModalCaps, mode, tonnes)
	}
	if tonnes <= 0 {
		return negativeTonnageError(f.correlationID, commodity, tonnes)
	}
	if tonnes > cap.MaxTonnesPerMovement {
		return modalCapError(f.correlationID, mode, tonnes, cap.MaxTonnesPerMovement)
	}
	if tonnes < cap.MinTonnesPerMovement {
		return modalCapError(f.correlationID, mode, tonnes, cap.MinTonnesPerMovement)
	}
	return nil
}

// Import brings commodity tonnage into the city through the port, bounded
// by the mode's cap, then by engine.market's capacity ceiling and
// engine.logistics's deliverable capacity (the registered outbound edges,
// AC-7/AC-8) — freight genuinely contends for the capacity model
// engine.logistics exposes, never a freight-owned parallel capacity number.
// It records the imported tonnage (AC-9's import accessor) and the customs
// demand (AC-3). It returns the movement and the tonnes actually imported
// (≤ requested — a capacity-bounded partial import).
func (f *FreightAPI) Import(commodity Commodity, tonnes int64, mode Mode) (MovementResult, error) {
	if err := f.checkNotCopied("Import"); err != nil {
		return MovementResult{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return MovementResult{}, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	// Validate the DECLARED tonnage against the mode cap first (AC-13: a
	// declared >cap movement is rejected, never silently bounded to what the
	// market/logistics capacity happens to deliver).
	if err := f.validateModalCap(commodity, mode, tonnes); err != nil {
		return MovementResult{}, err
	}

	// Market ceiling (AC-8): the commodity's configured import-capacity
	// ceiling, in market units.
	requestedUnits := safeMulTonnes(tonnes, cc.UnitsPerTonne)
	avail, err := f.market.Availability(cc.Market, requestedUnits)
	if err != nil {
		return MovementResult{}, err
	}

	// Logistics capacity (AC-7): the actual deliverable this tick, read
	// through the registered engine.logistics edge.
	delivery, err := f.logistics.Deliverable(portDistrict, cc.Market, avail.Available)
	if err != nil {
		return MovementResult{}, err
	}

	importedTonnes := delivery.Delivered / cc.UnitsPerTonne
	if importedTonnes < 0 {
		importedTonnes = 0
	}

	site := f.cfg.canonicalSite[cc.StorageClass]
	// The imported tonnage enters the city through the port and is IN
	// TRANSIT to its storage site for the mode's lead time (AC-7's
	// port→estate movement) — it is recorded in the import ledger and the
	// in-transit delta now, and lands in the site's stock on arrival.
	f.imported[commodity] = num.SatAdd(f.imported[commodity], importedTonnes)
	f.inTransitDelta[commodity] = num.SatAdd(f.inTransitDelta[commodity], importedTonnes)
	f.customsDemanded = num.SatAdd(f.customsDemanded, importedTonnes)

	id := f.nextMovementID
	f.nextMovementID++
	lead := f.cfg.ModalCaps[mode].LeadTimeTicks
	f.movements = append(f.movements, movement{
		ID:            id,
		Commodity:     commodity,
		Tonnes:        importedTonnes,
		From:          "",
		To:            site,
		Mode:          mode,
		DepartureTick: f.tick,
		ArrivalTick:   f.tick + lead,
	})

	return MovementResult{
		Movement: Movement{
			ID:            id,
			Commodity:     commodity,
			Tonnes:        importedTonnes,
			Mode:          mode,
			From:          "",
			To:            site,
			DepartureTick: f.tick,
			ArrivalTick:   f.tick + lead,
		},
		Moved: importedTonnes,
	}, nil
}

// Export records commodity tonnage departing the city through the port
// (AC-9's export/departure ledger), bounded by the mode's cap. It removes
// the tonnes from the commodity's canonical storage site, records them in
// the export ledger and customs demand, and returns the movement. A
// departure is recorded at command time (the tonnes left the city's books);
// sea/rail transit itself is out of scope (deferred to the full logistics
// movement model).
func (f *FreightAPI) Export(commodity Commodity, tonnes int64, mode Mode) (MovementResult, error) {
	if err := f.checkNotCopied("Export"); err != nil {
		return MovementResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return MovementResult{}, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	if err := f.validateModalCap(commodity, mode, tonnes); err != nil {
		return MovementResult{}, err
	}
	site := f.cfg.canonicalSite[cc.StorageClass]
	current := f.sites[site].stock[commodity]
	if tonnes > current {
		tonnes = current // a departure cannot exceed the stock held
	}
	if tonnes <= 0 {
		return MovementResult{}, negativeTonnageError(f.correlationID, commodity, tonnes)
	}

	f.sites[site].stock[commodity] = num.SatSub(current, tonnes)
	f.exported[commodity] = num.SatAdd(f.exported[commodity], tonnes)
	f.customsDemanded = num.SatAdd(f.customsDemanded, tonnes)

	id := f.nextMovementID
	f.nextMovementID++
	return MovementResult{
		Movement: Movement{
			ID:            id,
			Commodity:     commodity,
			Tonnes:        tonnes,
			Mode:          mode,
			From:          site,
			To:            "",
			DepartureTick: f.tick,
			ArrivalTick:   f.tick,
		},
		Moved: tonnes,
	}, nil
}

// Ship moves commodity tonnage between storage sites inside the city,
// bounded by the mode's cap, creating an in-transit movement that resolves
// after the mode's lead time (AC-7). The destination site's class must
// match the commodity's storage class (AC-6), and the source is the
// commodity's canonical site. The tonnes leave the source's stock now and
// arrive at the destination on the movement's ArrivalTick.
func (f *FreightAPI) Ship(commodity Commodity, tonnes int64, to SiteType, mode Mode) (MovementResult, error) {
	if err := f.checkNotCopied("Ship"); err != nil {
		return MovementResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	cc, ok := f.cfg.commodities[commodity]
	if !ok {
		return MovementResult{}, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{
			"commodity": string(commodity),
		})
	}
	dest, ok := f.sites[to]
	if !ok {
		return MovementResult{}, errs.New(ErrUnknownStorageSite, f.correlationID, map[string]any{
			"site": string(to),
		})
	}
	if cc.StorageClass != dest.cfg.CommodityClass {
		return MovementResult{}, errs.New(ErrStorageTypeMismatch, f.correlationID, map[string]any{
			"commodity":      string(commodity),
			"commodityClass": string(cc.StorageClass),
			"site":           string(to),
			"siteClass":      string(dest.cfg.CommodityClass),
		})
	}
	if err := f.validateModalCap(commodity, mode, tonnes); err != nil {
		return MovementResult{}, err
	}

	source := f.cfg.canonicalSite[cc.StorageClass]
	current := f.sites[source].stock[commodity]
	cap, _ := f.cfg.ModalCaps[mode]
	if tonnes > current {
		return MovementResult{}, insufficientStockError(f.correlationID, commodity, tonnes, cap.MaxTonnesPerMovement)
	}

	f.sites[source].stock[commodity] = num.SatSub(current, tonnes)
	f.inTransitDelta[commodity] = num.SatAdd(f.inTransitDelta[commodity], tonnes)

	id := f.nextMovementID
	f.nextMovementID++
	lead := f.cfg.ModalCaps[mode].LeadTimeTicks
	m := movement{
		ID:            id,
		Commodity:     commodity,
		Tonnes:        tonnes,
		From:          source,
		To:            to,
		Mode:          mode,
		DepartureTick: f.tick,
		ArrivalTick:   f.tick + lead,
	}
	f.movements = append(f.movements, m)

	return MovementResult{
		Movement: Movement{
			ID:            m.ID,
			Commodity:     m.Commodity,
			Tonnes:        m.Tonnes,
			Mode:          m.Mode,
			From:          m.From,
			To:            m.To,
			DepartureTick: m.DepartureTick,
			ArrivalTick:   m.ArrivalTick,
		},
		Moved: tonnes,
	}, nil
}

// Movements returns the current in-transit movements (AC-7's movement
// ledger — the source of AC-10's InTransitDelta term), in ascending ID
// order.
func (f *FreightAPI) Movements() []Movement {
	if err := f.checkNotCopied("Movements"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Movement, 0, len(f.movements))
	for _, m := range f.movements {
		out = append(out, Movement{
			ID:            m.ID,
			Commodity:     m.Commodity,
			Tonnes:        m.Tonnes,
			Mode:          m.Mode,
			From:          m.From,
			To:            m.To,
			DepartureTick: m.DepartureTick,
			ArrivalTick:   m.ArrivalTick,
		})
	}
	return out
}

// safeMulTonnes multiplies two non-negative int64s with saturation (GR#16).
func safeMulTonnes(a, b int64) int64 {
	v, _ := num.SafeMul(a, b)
	if v < 0 {
		return 0
	}
	return v
}
