package fuel

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FleetEra is one milestone era's fleet composition (AC-1/AC-2): the
// EV-share fraction per segment in [0,1]. It is the value
// [FuelAPI.FleetComposition] returns — the fleet ICE/EV split engine.traffic's
// future mode-share consumer reads (see doc.go's "engine.traffic feed" note).
type FleetEra struct {
	Era          string
	CarEVShare   float64
	VanEVShare   float64
	TruckEVShare float64
}

// ReplenishmentCommodity is the closed set of fuel-transported replenishment
// commodities whose logistics delivery [FuelAPI.ReplenishmentDelivery] gates
// on fuel availability (§49: a fuel shortage strands the logistics that fix
// shortages — the trucks that replenish these commodities burn fuel).
type ReplenishmentCommodity string

const (
	ReplenishFoodStaples           ReplenishmentCommodity = commodityFoodStaples
	ReplenishFoodFresh             ReplenishmentCommodity = commodityFoodFresh
	ReplenishConstructionMaterials ReplenishmentCommodity = commodityConstructionMaterials
	ReplenishConsumerGoods         ReplenishmentCommodity = commodityConsumerGoods
)

// Untyped string constants for the four truck-delivered commodities, declared
// so ReplenishmentDelivery can pass them to engine.logistics'
// market.CommodityType parameter WITHOUT this package importing engine.market
// (the blocked BUG-058 edge — GR#20, code.json). The values duplicate
// engine.market's constants across the module boundary; that duplication is
// deliberate and held in lockstep by TestReplenishmentCommoditiesMatchMarket
// (weakness pattern #2, mirroring engine.comms' parcelCommodity precedent).
const (
	commodityFoodStaples           = "foodStaples"
	commodityFoodFresh             = "foodFresh"
	commodityConstructionMaterials = "constructionMaterials"
	commodityConsumerGoods         = "consumerGoods"
)

// FuelAPI is code.json's "engine.fuel" inbound interface (FuelAPI, GUID
// 7ecc7077-8700-4f45-aa5f-9fe056c4ca73): "fleet composition per era; charging
// load into UtilityAPI". It owns the §49 fleet/EV-transition model — the
// data-driven EV-share-by-era curve, fuel demand, hour-of-day charging load,
// strategic-reserve sizing, fuel-duty posting through engine.tax, forecourt
// coverage, and the JIT fuel-shortage fragility that gates engine.logistics
// replenishment deliveries.
//
// The zero value is not usable; construct via [Load] or [LoadDefault]. A
// *FuelAPI is safe for concurrent use: every mutable field is guarded by mu,
// loaded data is immutable after Load, and checkNotCopied rejects a method
// call on a struct-copied value (SEC-020-class).
type FuelAPI struct {
	mu            sync.RWMutex
	correlationID string

	// Immutable after Load (the validated data/fuel.json surface).
	eraOrder        []string
	eraShares       map[string]FleetEra
	carDemand       float64
	vanDemand       float64
	truckDemand     float64
	logisticsDemand float64

	chargingBaseKWh   float64
	chargingWeight    [24]float64
	chargingWeightSum float64

	daysOfCover     float64
	dutyRatePence   float64
	dutyInstrument  string
	forecourtTarget float64

	// Mutable runtime state (the injectable §49 shortage surface).
	tankerThroughput float64
	strategicReserve float64

	// Wired cross-module dependencies (GR#20), via Set*.
	taxAPI       *tax.TaxAPI
	logisticsAPI *logistics.LogisticsAPI

	self atomic.Pointer[FuelAPI]
}

// Load reads and schema-validates data/fuel.json from dir (via
// foundation/data's generic Load — GR#3/GR#15, no fuel figure is a Go
// literal) and returns a ready-to-query *FuelAPI. correlationID is attached
// to every error this call (and the returned API's methods) construct (GR#1).
// Every failure is a registry-sourced *errs.E — never a silent default, never
// a panic.
func Load(dir, correlationID string) (*FuelAPI, error) {
	fd, err := loadFuelData(dir, correlationID)
	if err != nil {
		return nil, err
	}

	f := &FuelAPI{
		correlationID:    correlationID,
		eraShares:        make(map[string]FleetEra, len(fd.Eras)),
		carDemand:        fd.FuelDemand.CarLitresPerTick,
		vanDemand:        fd.FuelDemand.VanLitresPerTick,
		truckDemand:      fd.FuelDemand.TruckLitresPerTick,
		logisticsDemand:  fd.FuelDemand.LogisticsFleetLitresPerTick,
		chargingBaseKWh:  fd.ChargingProfile.BaseKWhPerTick,
		daysOfCover:      fd.StrategicReserve.DaysOfCover,
		dutyRatePence:    fd.Duty.RatePencePerLitre,
		dutyInstrument:   fd.Duty.TaxInstrument,
		forecourtTarget:  fd.Forecourt.TargetForecourtsPerThousandPopulation,
		tankerThroughput: fd.Tanker.PortThroughputLitresPerTick,
	}
	f.chargingWeight = fd.ChargingProfile.HourlyWeight
	for _, w := range f.chargingWeight {
		f.chargingWeightSum += w
	}

	for i := range fd.Eras {
		e := fd.Eras[i]
		f.eraShares[e.Era] = FleetEra{
			Era:          e.Era,
			CarEVShare:   *e.CarEVShare,
			VanEVShare:   *e.VanEVShare,
			TruckEVShare: *e.TruckEVShare,
		}
		// eraOrder preserves the data file's array order — the CHRONOLOGICAL
		// milestone order (early → mid → late → transitioned), which the
		// EV-share-by-era curve (AC-2) is defined against. Alphabetical sort
		// would scramble the curve and is deliberately NOT used here.
		f.eraOrder = append(f.eraOrder, e.Era)
	}

	f.self.Store(f)
	return f, nil
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*FuelAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *FuelAPI (SEC-020
// family, mirroring engine.tax/engine.roads). Lock-free — a single
// atomic.Pointer.Load — and therefore safe to run before mu is ever touched.
func (f *FuelAPI) checkNotCopied(method string) error {
	if f.self.Load() != f {
		return errs.New(ErrCopiedValue, f.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetTax wires the engine.tax dependency used by [PostFuelDuty] (the
// registered engine.fuel → engine.tax edge, GR#20). A nil tax leaves
// [PostFuelDuty] failing with ErrTaxNotWired rather than silently no-op'ing
// (GR#17).
func (f *FuelAPI) SetTax(t *tax.TaxAPI) error {
	if err := f.checkNotCopied("SetTax"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taxAPI = t
	return nil
}

// SetLogistics wires the engine.logistics dependency used by
// [ReplenishmentDelivery] (the registered engine.fuel → engine.logistics
// edge, GR#20). A nil logistics leaves that operation failing with
// ErrLogisticsNotWired rather than fabricating a delivery (GR#17).
func (f *FuelAPI) SetLogistics(l *logistics.LogisticsAPI) error {
	if err := f.checkNotCopied("SetLogistics"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logisticsAPI = l
	return nil
}

// SetTankerThroughput sets the current port tank farm / refinery tanker supply
// throughput in litres per tick (§49/§50). This is the injectable shortage
// surface for AC-3: cutting it below liquid-fuel demand strands the logistics
// that fix shortages. A negative or non-finite value is rejected (ErrInvalidInput).
func (f *FuelAPI) SetTankerThroughput(litresPerTick float64) error {
	if err := f.checkNotCopied("SetTankerThroughput"); err != nil {
		return err
	}
	if !num.IsFinite(litresPerTick) || litresPerTick < 0 {
		return errs.New(ErrInvalidInput, f.correlationID, map[string]any{
			"field": "tankerThroughput", "value": litresPerTick,
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tankerThroughput = litresPerTick
	return nil
}

// SetStrategicReserve sets the strategic-reserve level in litres (§49's
// "strategic reserve is a buildable"). It is the mitigant AC-3 tests: a
// stocked reserve covers the shortfall and prevents (or, in the multi-tick
// reading, delays) the logistics degradation. A negative or non-finite value
// is rejected (ErrInvalidInput).
func (f *FuelAPI) SetStrategicReserve(litres float64) error {
	if err := f.checkNotCopied("SetStrategicReserve"); err != nil {
		return err
	}
	if !num.IsFinite(litres) || litres < 0 {
		return errs.New(ErrInvalidInput, f.correlationID, map[string]any{
			"field": "strategicReserve", "value": litres,
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strategicReserve = litres
	return nil
}

// StrategicReserveSize returns the full strategic-reserve level in litres for
// the given era: the data-driven days-of-cover sizing placeholder (ASM-307)
// × that era's liquid-fuel demand. It is the "how big is a full reserve"
// figure; [SetStrategicReserve] sets the actual runtime level.
func (f *FuelAPI) StrategicReserveSize(era string) (float64, error) {
	if err := f.checkNotCopied("StrategicReserveSize"); err != nil {
		return 0, err
	}
	demand, err := f.ICELitres(era)
	if err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.daysOfCover * (demand + f.logisticsDemand), nil
}

// Eras returns the loaded era keys in ascending order (GR#21 — never Go
// map-iteration order on a path whose result matters). The returned slice is
// owned by the caller.
func (f *FuelAPI) Eras() []string {
	if err := f.checkNotCopied("Eras"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]string, len(f.eraOrder))
	copy(out, f.eraOrder)
	return out
}

// FleetComposition returns the fleet EV-vs-ICE split for one milestone era
// (AC-1/AC-2). It returns ErrUnknownEra for an era key not present in
// data/fuel.json — never a zero-value fleet composition silently treated as
// valid.
func (f *FuelAPI) FleetComposition(era string) (FleetEra, error) {
	if err := f.checkNotCopied("FleetComposition"); err != nil {
		return FleetEra{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	fe, ok := f.eraShares[era]
	if !ok {
		return FleetEra{}, errs.New(ErrUnknownEra, f.correlationID, map[string]any{"era": era})
	}
	return fe, nil
}

// ICELitres returns the liquid-fuel burn (litres per tick) of the ICE fleet in
// the given era: each segment's 0%-EV demand scaled by (1 − EV share). It is
// the taxable base the fuel-duty erosion (AC-4) and the JIT-fragility demand
// (AC-3) are functions of.
func (f *FuelAPI) ICELitres(era string) (float64, error) {
	if err := f.checkNotCopied("ICELitres"); err != nil {
		return 0, err
	}
	fe, err := f.FleetComposition(era)
	if err != nil {
		return 0, err
	}
	return f.carDemand*(1-fe.CarEVShare) +
		f.vanDemand*(1-fe.VanEVShare) +
		f.truckDemand*(1-fe.TruckEVShare), nil
}

// FleetEVShare returns the fleet-wide EV share for the given era, weighted by
// each segment's fuel demand (the demand-weighted substitution fraction the
// duty base erodes by). The logistics fleet is not part of this figure — it is
// assumed 0%-EV at this depth (a documented placeholder).
func (f *FuelAPI) FleetEVShare(era string) (float64, error) {
	if err := f.checkNotCopied("FleetEVShare"); err != nil {
		return 0, err
	}
	fe, err := f.FleetComposition(era)
	if err != nil {
		return 0, err
	}
	total := f.carDemand + f.vanDemand + f.truckDemand
	if total <= 0 {
		return 0, nil
	}
	return (f.carDemand*fe.CarEVShare + f.vanDemand*fe.VanEVShare + f.truckDemand*fe.TruckEVShare) / total, nil
}

// PostFuelDuty posts fuel-duty revenue through engine.tax's registered edge
// (AC-4): it sets the 0%-EV duty base and the fleet EV-share erosion input on
// the data-declared tax instrument, then reads the eroding revenue back. It
// never touches the instrument's rate — the erosion is a consequence of the
// shrinking taxable base (EV substitution), not a rate cut. Requires SetTax
// (ErrTaxNotWired otherwise). The returned finance.Money is the posted revenue.
func (f *FuelAPI) PostFuelDuty(era string) (finance.Money, error) {
	if err := f.checkNotCopied("PostFuelDuty"); err != nil {
		return 0, err
	}
	evShare, err := f.FleetEVShare(era)
	if err != nil {
		return 0, err
	}

	f.mu.RLock()
	t := f.taxAPI
	base := f.fullDutyBaseLocked()
	instrument := f.dutyInstrument
	f.mu.RUnlock()

	if t == nil {
		return 0, errs.New(ErrTaxNotWired, f.correlationID, map[string]any{"operation": "PostFuelDuty"})
	}
	if err := t.SetBase(instrument, base); err != nil {
		return 0, err
	}
	if err := t.SetEVShare(instrument, evShare); err != nil {
		return 0, err
	}
	return t.Revenue(instrument)
}

// fullDutyBaseLocked returns the 0%-EV fuel-duty taxable base in micro-pounds:
// total road-fuel demand at 0% EV (car + van + truck + logistics fleet) × the
// duty rate (pence per litre) × 10,000 micro-pounds per pence. Caller holds at
// least a read lock.
func (f *FuelAPI) fullDutyBaseLocked() finance.Money {
	litres := f.carDemand + f.vanDemand + f.truckDemand + f.logisticsDemand
	pence := litres * f.dutyRatePence
	return finance.Money(num.ClampInt64FromFloat(pence * 10000))
}

// ChargingLoad returns the EV charging load as a consumption.Demand (power
// only, kWh) for one hour of day (AC-5): the data-driven hour-of-day profile
// (an evening-peak concentration, not a flat daily average) scaled to the
// loaded daily charging base. It is the shape the composition root sums into
// engine.consumption's UtilityAPI power solve alongside every other load.
// hour is in [0, 24); anything else is ErrInvalidHour.
func (f *FuelAPI) ChargingLoad(hour int) (consumption.Demand, error) {
	if err := f.checkNotCopied("ChargingLoad"); err != nil {
		return consumption.Demand{}, err
	}
	if hour < 0 || hour >= 24 {
		return consumption.Demand{}, errs.New(ErrInvalidHour, f.correlationID, map[string]any{"hour": hour})
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	power := f.chargingBaseKWh * f.chargingWeight[hour] / f.chargingWeightSum
	return consumption.Demand{Power: power}, nil
}

// ChargingLoadProfile returns the full 24-hour charging-load weight profile
// (a copy, caller-owned) so a consumer can find the evening peak without
// re-deriving it from 24 separate ChargingLoad calls.
func (f *FuelAPI) ChargingLoadProfile() [24]float64 {
	if err := f.checkNotCopied("ChargingLoadProfile"); err != nil {
		return [24]float64{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out [24]float64
	copy(out[:], f.chargingWeight[:])
	return out
}

// ForecourtCoverageAdequacy returns the forecourt-network coverage-adequacy
// ratio for a given forecourt count and served population (AC-6):
//
//	adequacy = forecourts / ((population/1000) × targetForecourtsPerThousandPopulation)
//
// 1.0 means exactly at target; above 1.0 surplus, below 1.0 deficit. Holding
// forecourts fixed, a growing population strictly degrades adequacy — "a
// growing city needs forecourts like it needs substations". A negative
// forecourt count or population is ErrInvalidInput.
func (f *FuelAPI) ForecourtCoverageAdequacy(forecourts int, population float64) (float64, error) {
	if err := f.checkNotCopied("ForecourtCoverageAdequacy"); err != nil {
		return 0, err
	}
	if forecourts < 0 || !num.IsFinite(population) || population < 0 {
		return 0, errs.New(ErrInvalidInput, f.correlationID, map[string]any{
			"forecourts": forecourts, "population": population,
		})
	}
	f.mu.RLock()
	target := f.forecourtTarget
	f.mu.RUnlock()
	if target <= 0 {
		return 0, errs.New(ErrFuelDataInvalid, f.correlationID, map[string]any{"field": "forecourt.targetForecourtsPerThousandPopulation"})
	}
	required := (population / 1000) * target
	if required <= 0 {
		return 0, nil // zero population needs zero forecourts; adequacy is defined as 0
	}
	return float64(forecourts) / required, nil
}

// FuelAvailabilityFactor returns the fuel-availability fraction in [0,1] for
// the given era: the fraction of liquid-fuel demand that the current tanker
// throughput plus strategic reserve can cover. 1.0 = fully fuelled (logistics
// deliveries run at full throughput); < 1.0 = a §49 fuel shortage strands some
// of the logistics fleet. Pure query — no mutation.
func (f *FuelAPI) FuelAvailabilityFactor(era string) (float64, error) {
	if err := f.checkNotCopied("FuelAvailabilityFactor"); err != nil {
		return 0, err
	}
	demand, err := f.ICELitres(era)
	if err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	demand += f.logisticsDemand
	if demand <= 0 {
		return 1, nil
	}
	supply := f.tankerThroughput + f.strategicReserve
	if supply >= demand {
		return 1, nil
	}
	return supply / demand, nil
}

// ReplenishmentDelivery computes the fuel-gated replenishment delivery of a
// truck-delivered commodity through engine.logistics (AC-3): it reads
// logistics' own [logistics.LogisticsAPI.Deliverable] delivery/queue state for
// the commodity, then scales it by the current fuel-availability factor. A
// fuel shortage (reduced tanker throughput with no strategic reserve to cover
// it) therefore degrades the delivery of OTHER commodities whose replenishment
// trucks are fuel-dependent, and returns the registry-sourced ErrFuelShortage
// (AC-7) — never a silent stall. A stocked strategic reserve covers the
// shortfall and prevents the degradation. Requires SetLogistics
// (ErrLogisticsNotWired) and a registered commodity (ErrUnknownCommodity).
func (f *FuelAPI) ReplenishmentDelivery(era, district string, commodity ReplenishmentCommodity, requested int64) (logistics.Delivery, error) {
	if err := f.checkNotCopied("ReplenishmentDelivery"); err != nil {
		return logistics.Delivery{}, err
	}
	if requested < 0 {
		return logistics.Delivery{}, errs.New(ErrInvalidInput, f.correlationID, map[string]any{"field": "requested", "value": requested})
	}

	factor, err := f.FuelAvailabilityFactor(era)
	if err != nil {
		return logistics.Delivery{}, err
	}

	f.mu.RLock()
	l := f.logisticsAPI
	f.mu.RUnlock()
	if l == nil {
		return logistics.Delivery{}, errs.New(ErrLogisticsNotWired, f.correlationID, map[string]any{"operation": "ReplenishmentDelivery"})
	}

	// Each commodity's literal is an untyped string constant at the call site,
	// so it converts to logistics' market.CommodityType parameter without this
	// package importing engine.market (the blocked BUG-058 edge — GR#20). The
	// values duplicate engine.market's constants across the boundary and are
	// held in lockstep by TestReplenishmentCommoditiesMatchMarket.
	var d logistics.Delivery
	switch commodity {
	case ReplenishFoodStaples:
		d, err = l.Deliverable(district, commodityFoodStaples, requested)
	case ReplenishFoodFresh:
		d, err = l.Deliverable(district, commodityFoodFresh, requested)
	case ReplenishConstructionMaterials:
		d, err = l.Deliverable(district, commodityConstructionMaterials, requested)
	case ReplenishConsumerGoods:
		d, err = l.Deliverable(district, commodityConsumerGoods, requested)
	default:
		return logistics.Delivery{}, errs.New(ErrUnknownCommodity, f.correlationID, map[string]any{"commodity": string(commodity)})
	}
	if err != nil {
		return logistics.Delivery{}, err
	}

	d = applyFuelGate(d, factor)
	if factor < 1 {
		return d, errs.New(ErrFuelShortage, f.correlationID, map[string]any{
			"era": era, "district": district, "commodity": string(commodity),
			"factor": factor,
		})
	}
	return d, nil
}

// applyFuelGate scales a logistics delivery's throughput (and therefore its
// delivered quantity) by the fuel-availability factor, recomputing the
// shortfall. It is a pure function of the delivery and the factor — no
// LogisticsAPI/FuelAPI state touched.
func applyFuelGate(d logistics.Delivery, factor float64) logistics.Delivery {
	if factor >= 1 {
		return d
	}
	if factor < 0 {
		factor = 0
	}
	gated := int64(math.Floor(float64(d.Throughput) * factor))
	if gated < 0 {
		gated = 0
	}
	delivered := d.Requested
	if delivered > gated {
		delivered = gated
	}
	return logistics.Delivery{
		Requested:  d.Requested,
		Throughput: gated,
		Delivered:  delivered,
		Shortfall:  d.Requested - delivered,
	}
}
