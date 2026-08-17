package defence

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PendingMandates is the mandate-event accessor (AC-1): it returns the
// data-driven mandate events whose population threshold the given population
// has reached and which have not yet been accepted or refused. The thresholds
// are read from data/defence.json — there is no hardcoded population
// if/switch ladder in this package (AC-1/AC-4). The returned slice is in the
// data file's fixed order (GR#21 — never map iteration order).
func (d *DefenceAPI) PendingMandates(population int64) []Mandate {
	if err := d.checkNotCopied("PendingMandates"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var out []Mandate
	for _, m := range d.cfg.Mandates {
		if population < m.PopulationThreshold {
			continue
		}
		if _, responded := d.responses[m.ID]; responded {
			continue
		}
		out = append(out, mandateFromConfig(m))
	}
	return out
}

// RespondToMandate records the player's decision on a pending mandate
// (AC-5/AC-6): refuse (priced — no build, reputation penalty, grant access
// revoked), or accept one of the mandate's compliant choices, siting it via
// engine.build, crediting the compensation via engine.finance, and settling
// its personnel as real citizen records via engine.citizens. A second
// response to an already-responded mandate is rejected with
// [ErrMandateAlreadyResponded] (AC-12) — the first response is never
// silently overwritten.
func (d *DefenceAPI) RespondToMandate(req MandateResponse) (MandateResult, error) {
	if err := d.checkNotCopied("RespondToMandate"); err != nil {
		return MandateResult{}, err
	}
	mcfg, ok := d.mandateByID(req.MandateID)
	if !ok {
		return MandateResult{}, errs.New(ErrUnknownMandate, d.correlationID, map[string]any{"mandate": req.MandateID})
	}

	// The whole response is atomic under d.mu. The wired dependencies
	// (build/finance/citizens) never call back into this package, so the lock
	// order is always defence → dependency, never reversed.
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, responded := d.responses[req.MandateID]; responded {
		return MandateResult{}, errs.New(ErrMandateAlreadyResponded, d.correlationID, map[string]any{"mandate": req.MandateID})
	}

	// AC-6: refusal is a legal, priced command — it records the refusal,
	// applies the reputation penalty, and does NOT build anything.
	if req.Refuse {
		d.refusedMandates[req.MandateID] = true
		d.reputationPenalty += d.cfg.Reputation.RefusalPenaltyPoints
		d.responses[req.MandateID] = MandateResult{MandateID: req.MandateID, Refused: true}
		return MandateResult{MandateID: req.MandateID, Refused: true}, nil
	}

	// Accept path — the choice must be one of the mandate's compliant choices
	// (AC-5), and the site must be in bounds (AC-11).
	choice, ok := d.choiceByID(mcfg, req.Choice)
	if !ok {
		return MandateResult{}, errs.New(ErrInvalidChoice, d.correlationID, map[string]any{"mandate": req.MandateID, "choice": req.Choice})
	}
	if !req.Site.Tile.InExtent() || !req.Site.Local.InBounds() {
		return MandateResult{}, errs.New(ErrIneligibleSite, d.correlationID, map[string]any{"site": req.Site.String()})
	}
	fc, ok := d.cfg.Facilities[choice.FacilityType]
	if !ok {
		// Unreachable with valid data (Validate enforces the reference), but
		// fail closed rather than index a missing config.
		return MandateResult{}, errs.New(ErrDefenceDataInvalid, d.correlationID, map[string]any{"facilityType": choice.FacilityType})
	}

	// SEC-215: pre-flight the entire dependency surface BEFORE any mutation.
	// A mandate response is all-or-nothing — if build, finance or citizens is
	// unwired the attempt must fail with [ErrDependencyMissing] up front, so a
	// failed response can never leave a half-applied state (an enqueued build
	// order, a credited compensation grant, or a recorded facility) that a
	// retry after wiring the missing dependency would then duplicate — money
	// creation and duplicate construction. The round-1 attack drove exactly
	// that: with build+finance wired but citizens not, the old code returned
	// ErrDependencyMissing only after build and finance had already mutated.
	b, f, c, err := d.requireAllLocked("RespondToMandate")
	if err != nil {
		return MandateResult{}, err
	}

	// Site the chosen option through engine.build (the registered edge).
	buildCmd := build.BuildCommand{
		Tile:    req.Site.Tile,
		Local:   req.Site.Local,
		OwnerID: req.OwnerID,
		Zone:    build.ZoneType(fc.BuildZone),
		Month:   req.Month,
	}
	if _, err := b.SubmitBuildCommand(buildCmd); err != nil {
		// build's ownership/out-of-bounds/unknown-zone rejections all mean the
		// site is ineligible for a defence facility: surface AC-11's
		// defence-specific code rather than leaking build's internal code.
		return MandateResult{}, errs.Wrap(ErrIneligibleSite, d.correlationID, err, map[string]any{
			"site":  req.Site.String(),
			"cause": err.Error(),
		})
	}

	// Credit the documented compensation grant through engine.finance.
	compensation := finance.Money(mcfg.CompensationMicropounds)
	if compensation > 0 {
		if err := postGrant(f, req.Month, compensation, "mandate compensation: "+mcfg.ID); err != nil {
			return MandateResult{}, err
		}
	}

	// Record the built facility.
	id := d.nextFacilityID
	d.nextFacilityID++
	fac := &facility{
		id:              id,
		typ:             FacilityType(choice.FacilityType),
		site:            req.Site,
		mandateID:       mcfg.ID,
		choiceID:        choice.ID,
		nominalPayroll:  finance.Money(fc.PayrollMicropounds),
		payrollFloor:    finance.Money(fc.PayrollFloorMicropounds),
		personnel:       fc.PersonnelCount,
		marriedQuarters: fc.MarriedQuarters,
		schoolPlaces:    fc.MarriedQuarters * fc.ChildrenPerQuarter,
		procurement:     finance.Money(fc.ProcurementMicropounds),
	}
	d.facilities[id] = fac

	// Settle personnel as real citizen records (AC-8).
	if err := d.settlePersonnelLocked(c, id, fac); err != nil {
		return MandateResult{}, err
	}

	res := MandateResult{MandateID: mcfg.ID, FacilityID: id, Compensation: compensation}
	d.responses[mcfg.ID] = res
	return res, nil
}

// ReputationPenalty returns the accumulated refusal reputation penalty
// (AC-6): zero until a mandate is refused, then the data-sourced per-refusal
// points. Queryable so the refusal cost is inspectable, not buried.
func (d *DefenceAPI) ReputationPenalty() int64 {
	if err := d.checkNotCopied("ReputationPenalty"); err != nil {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.reputationPenalty
}

// mandateByID returns a mandate config by id (caller may hold d.mu or not —
// it reads only the immutable cfg).
func (d *DefenceAPI) mandateByID(id string) (MandateConfig, bool) {
	for _, m := range d.cfg.Mandates {
		if m.ID == id {
			return m, true
		}
	}
	return MandateConfig{}, false
}

// choiceByID returns a mandate's compliant choice by id.
func (d *DefenceAPI) choiceByID(m MandateConfig, id string) (MandateChoiceConfig, bool) {
	for _, ch := range m.Choices {
		if ch.ID == id {
			return ch, true
		}
	}
	return MandateChoiceConfig{}, false
}

// mandateFromConfig converts a MandateConfig into the exported Mandate view.
func mandateFromConfig(m MandateConfig) Mandate {
	choices := make([]MandateChoice, 0, len(m.Choices))
	for _, ch := range m.Choices {
		choices = append(choices, MandateChoice{ID: ch.ID, FacilityType: FacilityType(ch.FacilityType), Description: ch.Description})
	}
	return Mandate{
		ID:                  m.ID,
		FacilityType:        FacilityType(m.FacilityType),
		PopulationThreshold: m.PopulationThreshold,
		Compensation:        finance.Money(m.CompensationMicropounds),
		Choices:             choices,
	}
}

// requireBuildLocked/requireFinanceLocked/requireCitizensLocked are the
// already-under-lock variants of the require* helpers (the caller holds
// d.mu; re-acquiring RLock would deadlock).
func (d *DefenceAPI) requireBuildLocked(op string) (*build.BuildAPI, error) {
	if d.build == nil {
		return nil, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"operation": op, "dependency": "engine.build"})
	}
	return d.build, nil
}

func (d *DefenceAPI) requireFinanceLocked(op string) (*finance.FinanceAPI, error) {
	if d.finance == nil {
		return nil, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"operation": op, "dependency": "engine.finance"})
	}
	return d.finance, nil
}

func (d *DefenceAPI) requireCitizensLocked(op string) (*citizens.CitizensAPI, error) {
	if d.citizens == nil {
		return nil, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{"operation": op, "dependency": "engine.citizens"})
	}
	return d.citizens, nil
}

// requireAllLocked pre-flights all three wired dependencies (the caller holds
// d.mu). It returns the first missing dependency as [ErrDependencyMissing]
// WITHOUT touching any dependency, so RespondToMandate can validate its whole
// dependency surface before committing any side effect (SEC-215 — a mandate
// response must not be half-applied because a later dependency was unwired).
// Lock order is defence → dependency, never reversed, since the returned
// dependencies never call back into this package.
func (d *DefenceAPI) requireAllLocked(op string) (*build.BuildAPI, *finance.FinanceAPI, *citizens.CitizensAPI, error) {
	b, err := d.requireBuildLocked(op)
	if err != nil {
		return nil, nil, nil, err
	}
	f, err := d.requireFinanceLocked(op)
	if err != nil {
		return nil, nil, nil, err
	}
	c, err := d.requireCitizensLocked(op)
	if err != nil {
		return nil, nil, nil, err
	}
	return b, f, c, nil
}
