package consumption

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// UtilityAPI is code.json's "engine.consumption" inbound interface
// (GUID 3300a878-d938-4236-b138-dbb5e081674a): the coefficient-driven
// demand model and the per-network daily-tick solve, "coefficients from
// Store" (data/consumption.json via foundation.data). Construct via [Load]
// or [LoadDefault]; the zero value is not usable.
//
// Demand-query methods ([ResidentialDemand], [ClassDemand],
// [ClassCoefficients], [ResidentialBaseline], [WastewaterOutput]) are pure
// after construction — same inputs, same outputs, no side effects. The
// solve entry point [SolveDailyTick] mutates the passed [Network]'s
// last-solve state (and any aquifer's degraded yield), which is the tick's
// consumption, not hidden API state. A *UtilityAPI is safe for concurrent
// use across independent networks: its loaded coefficient/season/market
// maps are populated once at Load and never mutated afterward (AC-17).
type UtilityAPI struct {
	consumption   data.Consumption
	season        *season.SeasonAPI
	market        *market.MarketAPI
	correlationID string
}

// Load reads data/consumption.json (this module's Store — via
// foundation/data.LoadConsumption), data/seasonal.json (via
// engine.season.Load, for the AC-11 seasonal layer), and data/market.json
// (via engine.market.Load, for the AC-20 billing prices) from dir, and
// returns a ready-to-query *UtilityAPI. correlationID is attached to every
// error this call (and the returned API's methods) construct (GR#1).
// Every failure is a registry-sourced *errs.E — never a silent default
// substitution, never a panic.
func Load(dir, correlationID string) (*UtilityAPI, error) {
	cons, err := data.LoadConsumption(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrConsumptionDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	seasonAPI, err := season.Load(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrSeasonDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	marketAPI, err := market.Load(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrMarketDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	return &UtilityAPI{
		consumption:   cons,
		season:        seasonAPI,
		market:        marketAPI,
		correlationID: correlationID,
	}, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*UtilityAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// ResidentialBaseline returns the loaded §17.1 per-person-per-day baseline
// (water 145 L, electricity 3.5 kWh, gas 13 kWh, food staples/fresh,
// household waste 1.1 kg, wastewater fraction 0.95) — the raw Store figures
// AC-2's transcription test asserts.
func (a *UtilityAPI) ResidentialBaseline() data.ResidentialBaseline {
	return a.consumption.Residential
}

// WastewaterFraction returns the §17 "≈95% of water drawn" fraction from
// the loaded baseline (data/consumption.json's wastewaterFractionOfWater).
func (a *UtilityAPI) WastewaterFraction() float64 {
	return a.consumption.Residential.WastewaterFractionOfWater
}

// WastewaterOutput returns the §17 wastewater output for a given water
// draw: fraction × water (AC-5). For the §17.1 baseline figures this is
// 0.95 × 145 ≈ 138 L, matching the spec's "~138 L" note.
func (a *UtilityAPI) WastewaterOutput(waterDraw float64) float64 {
	return a.WastewaterFraction() * waterDraw
}

// ClassCoefficients resolves a consumptionRef key against the loaded
// §17.2 classes map (AC-3/AC-4). Returns ErrUnresolvedConsumptionRef for a
// key that does not resolve — never a silent zero row (AC-13).
func (a *UtilityAPI) ClassCoefficients(ref string) (data.ClassCoefficients, error) {
	coef, ok := a.consumption.Classes[ref]
	if !ok {
		return data.ClassCoefficients{}, errs.New(ErrUnresolvedConsumptionRef, a.correlationID, map[string]any{
			"ref": ref,
		})
	}
	return coef, nil
}

// ClassDemand computes one building-class demand for one tick:
// coefficient × occupancy, then the seasonal layer, then the all-electric
// reroute, then wastewater derivation (AC-4/AC-5/AC-10/AC-11). occupancy
// is the class's §17.2 unit (pupil, bed, desk, worker, m³, ...).
func (a *UtilityAPI) ClassDemand(ref string, occupancy float64, opts DemandOptions) (Demand, error) {
	coef, err := a.ClassCoefficients(ref)
	if err != nil {
		return Demand{}, err
	}
	if !isFinite(occupancy) || occupancy < 0 {
		return Demand{}, errs.New(ErrInvalidOccupancy, a.correlationID, map[string]any{
			"ref":       ref,
			"occupancy": occupancy,
		})
	}
	base := Demand{
		Water: coef.WaterL * occupancy,
		Power: coef.ElecKWh * occupancy,
		Gas:   coef.GasKWh * occupancy,
		Waste: coef.WasteKg * occupancy,
	}
	d, err := a.applyModifiers(base, opts)
	if err != nil {
		return Demand{}, err
	}
	if err := validateDemand(d, ref, a.correlationID); err != nil {
		return Demand{}, err
	}
	return d, nil
}

// ResidentialDemand computes one household's demand for one tick from the
// §17.1 per-person baseline × population, then the same
// seasonal/all-electric/wastewater layers as [ClassDemand].
func (a *UtilityAPI) ResidentialDemand(population float64, opts DemandOptions) (Demand, error) {
	if !isFinite(population) || population < 0 {
		return Demand{}, errs.New(ErrInvalidOccupancy, a.correlationID, map[string]any{
			"ref":       "residential",
			"occupancy": population,
		})
	}
	r := a.consumption.Residential
	base := Demand{
		Water: r.WaterLitresPerPersonPerDay * population,
		Power: r.ElectricityKWhPerPersonPerDay * population,
		Gas:   r.GasKWhPerPersonPerDay * population,
		Waste: r.HouseholdWasteKgPerPersonPerDay * population,
	}
	d, err := a.applyModifiers(base, opts)
	if err != nil {
		return Demand{}, err
	}
	if err := validateDemand(d, "residential", a.correlationID); err != nil {
		return Demand{}, err
	}
	return d, nil
}

// validateDemand rejects a computed demand whose any field overflowed to a
// non-finite value (GR#1/GR#16): coefficient × occupancy (and the seasonal
// layer) must never propagate +Inf/NaN out of a public demand query.
func validateDemand(d Demand, ref, correlationID string) error {
	if !isFinite(d.Water) || !isFinite(d.Power) || !isFinite(d.Gas) ||
		!isFinite(d.Wastewater) || !isFinite(d.Waste) {
		return errs.New(ErrDemandOverflow, correlationID, map[string]any{"ref": ref})
	}
	return nil
}

// DemandForEntity resolves one [DemandEntity] into a [Demand]: a class
// reference when ClassRef is set, else the residential baseline.
func (a *UtilityAPI) DemandForEntity(e DemandEntity, opts DemandOptions) (Demand, error) {
	if e.ClassRef != "" {
		return a.ClassDemand(e.ClassRef, e.Occupancy, opts)
	}
	return a.ResidentialDemand(e.Population, opts)
}

// applyModifiers applies the two multiplicative layers that sit ON TOP of
// coefficient-driven base demand (AC-11/AC-10): engine.season's seasonal
// multipliers (never a locally re-implemented curve), then the all-electric
// gas→power reroute, then the wastewater derivation.
func (a *UtilityAPI) applyModifiers(base Demand, opts DemandOptions) (Demand, error) {
	waterMult, err := a.season.WaterDemandMultiplier(opts.MonthIndex)
	if err != nil {
		return Demand{}, err
	}
	powerMult, err := a.season.PowerDemandMultiplier(opts.MonthIndex)
	if err != nil {
		return Demand{}, err
	}
	gasMult, err := a.season.GasDemandMultiplier(opts.MonthIndex)
	if err != nil {
		return Demand{}, err
	}

	base.Water *= waterMult
	base.Power *= powerMult
	base.Gas *= gasMult

	if !opts.GasNetworkPresent {
		base.Power += base.Gas
		base.Gas = 0
	}

	base.Wastewater = a.WastewaterOutput(base.Water)
	return base, nil
}

// scalarFor extracts the utility field a network of the given kind solves
// against from a full [Demand].
func scalarFor(kind Utility, d Demand) float64 {
	switch kind {
	case UtilityWater:
		return d.Water
	case UtilityWastewater:
		return d.Wastewater
	case UtilityPower:
		return d.Power
	case UtilityGas:
		return d.Gas
	default:
		return 0
	}
}

// SolveDailyTick is the daily-tick network-solve entry point (AC-1): it
// resolves each entity's coefficient reference (or residential baseline)
// into a per-utility [Demand], extracts the passed network's utility, and
// runs that network's conserved solve (AC-6). Seasonal modifiers and the
// all-electric reroute are applied during demand computation, so the solve
// itself stays a pure conserved allocation.
func (a *UtilityAPI) SolveDailyTick(network *Network, entities []DemandEntity, opts DemandOptions) (SolveResult, error) {
	consumers := make([]Consumer, 0, len(entities))
	for _, e := range entities {
		d, err := a.DemandForEntity(e, opts)
		if err != nil {
			return SolveResult{}, err
		}
		consumers = append(consumers, Consumer{
			EntityRef: e.EntityRef,
			Demand:    scalarFor(network.Kind(), d),
		})
	}
	return network.Solve(consumers)
}

// BilledAmount computes the money value (micro-pounds, M0-ENG §1.2) of the
// delivered utility quantities against engine.market's per-unit prices for
// water, electricity, and gas (AC-20). It returns a money figure, distinct
// from the raw physical quantities — never a relabelled Demand. Wastewater
// has no Market commodity and solid waste/food bill through other
// consumers, so only the three networked, billable utilities appear. This
// is the input engine.finance's household-spend stage consumes; this
// package never posts to the ledger itself.
func (a *UtilityAPI) BilledAmount(delivered DeliveredByCommodity) (BilledAmount, error) {
	waterPrice, err := a.market.Price(market.Water)
	if err != nil {
		return BilledAmount{}, err
	}
	powerPrice, err := a.market.Price(market.Power)
	if err != nil {
		return BilledAmount{}, err
	}
	gasPrice, err := a.market.Price(market.Gas)
	if err != nil {
		return BilledAmount{}, err
	}
	return BilledAmount{
		WaterMicropounds: delivered.Water * float64(waterPrice),
		PowerMicropounds: delivered.Power * float64(powerPrice),
		GasMicropounds:   delivered.Gas * float64(gasPrice),
	}, nil
}
