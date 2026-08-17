package coastal

import "github.com/aaronukgarcia/Metropolis/internal/foundation/num"

// This file owns the three §30 policy sliders (AC-11) and the outcome
// metrics they move. Each slider is a finite value in [0,1], settable only
// through a validating command (never a direct field write — AC-1/AC-13),
// and each moves at least two outcome metrics in opposite directions, so
// "real trade-offs, no right answer" is a structural property, not prose.

// SetProcessingFunding sets the processing-funding slider (AC-11). Domain
// [0,1]: higher funding raises the caseworker throughput (lower backlog) but
// raises processing opex (higher cost). A non-finite value is ErrNonFinite;
// outside [0,1] is ErrInvalidPolicyRange — never silently clamped.
func (c *CoastalAPI) SetProcessingFunding(v float64) error {
	if err := c.checkNotCopied("SetProcessingFunding"); err != nil {
		return err
	}
	if !num.IsFinite(v) {
		return c.coastalErr(ErrNonFinite, map[string]any{"field": "processingFunding"})
	}
	if v < 0 || v > 1 {
		return c.coastalErr(ErrInvalidPolicyRange, map[string]any{"field": "processingFunding", "value": v})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.processingFunding = v
	return nil
}

// ProcessingFunding returns the current processing-funding slider value.
func (c *CoastalAPI) ProcessingFunding() float64 {
	if err := c.checkNotCopied("ProcessingFunding"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.processingFunding
}

// SetHousingApproach sets the housing-approach slider (AC-11): 0 = dispersal,
// 1 = concentrated centres. Domain [0,1]. Higher approach (centres) lowers the
// hotel-requisition cost per case (economies of scale) but raises the local
// satisfaction friction and slows integration — three metrics, opposite
// directions. Never silently clamped.
func (c *CoastalAPI) SetHousingApproach(v float64) error {
	if err := c.checkNotCopied("SetHousingApproach"); err != nil {
		return err
	}
	if !num.IsFinite(v) {
		return c.coastalErr(ErrNonFinite, map[string]any{"field": "housingApproach"})
	}
	if v < 0 || v > 1 {
		return c.coastalErr(ErrInvalidPolicyRange, map[string]any{"field": "housingApproach", "value": v})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.housingApproach = v
	return nil
}

// HousingApproach returns the current housing-approach slider value.
func (c *CoastalAPI) HousingApproach() float64 {
	if err := c.checkNotCopied("HousingApproach"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.housingApproach
}

// SetIntegrationInvestment sets the integration-investment slider (AC-11).
// Domain [0,1]: higher investment raises integration speed (shorter pipeline
// durations) but raises integration opex. Never silently clamped.
func (c *CoastalAPI) SetIntegrationInvestment(v float64) error {
	if err := c.checkNotCopied("SetIntegrationInvestment"); err != nil {
		return err
	}
	if !num.IsFinite(v) {
		return c.coastalErr(ErrNonFinite, map[string]any{"field": "integrationInvestment"})
	}
	if v < 0 || v > 1 {
		return c.coastalErr(ErrInvalidPolicyRange, map[string]any{"field": "integrationInvestment", "value": v})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.integrationInvestment = v
	return nil
}

// IntegrationInvestment returns the current integration-investment slider value.
func (c *CoastalAPI) IntegrationInvestment() float64 {
	if err := c.checkNotCopied("IntegrationInvestment"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.integrationInvestment
}

// IntegrationSpeed returns the derived integration-speed coefficient in [0,1]
// (AC-11/US-4's metric): integration investment raises it, the concentrated-
// centres housing approach lowers it. It is the value that shortens granted-
// case pipeline durations.
func (c *CoastalAPI) IntegrationSpeed() float64 {
	if err := c.checkNotCopied("IntegrationSpeed"); err != nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return clamp01(
		c.integrationInvestment*c.cfg.Policy.IntegrationInvestmentGainPerUnit -
			c.housingApproach*c.cfg.Policy.HousingApproachIntegrationPenaltyPerUnit,
	)
}

// throughput returns the caseworker throughput (cases assignable this month)
// for the given processing-funding level: the data ceiling scaled up by the
// funding slider (AC-5's finite caseworker-throughput ceiling). It is a pure
// function of the immutable Config and the funding input.
func throughput(cfg Config, funding float64) float64 {
	return cfg.Reception.CaseworkerThroughputPerMonth *
		(1 + funding*cfg.Policy.ProcessingFundingThroughputGainPerUnit)
}

// effectiveHotelCost returns the hotel-requisition cost per overflow case per
// month, adjusted by the housing-approach slider (centres are cheaper — a
// negative data adjustment). Saturates at 0 (never a negative cost).
func effectiveHotelCost(cfg Config, approach float64) int64 {
	adj := num.ClampInt64FromFloat(approach * float64(cfg.Policy.HousingApproachCostPerUnitPerMonth))
	cost, overflow := num.SatAddChecked(cfg.Reception.HotelCostPerCase, adj)
	if overflow || cost < 0 {
		return 0
	}
	return cost
}

// effectiveFriction returns the satisfaction friction per overflow case,
// scaled up by the concentrated-centres approach (centres concentrate local
// friction).
func effectiveFriction(cfg Config, approach float64) float64 {
	return cfg.Reception.SatisfactionFrictionPerCase *
		(1 + approach*cfg.Policy.HousingApproachFrictionIncreasePerUnit)
}

// clamp01 clamps v into [0,1] (GR#16: a non-finite value collapses to 0,
// never leaks +Inf/NaN — mirroring engine.services/engine.comms).
func clamp01(v float64) float64 {
	if !num.IsFinite(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
