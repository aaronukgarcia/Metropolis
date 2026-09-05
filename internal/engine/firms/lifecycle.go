package firms

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// stageConfigFor returns the loaded config for a stage (the caller holds
// f.mu when it matters; the config is immutable after Load so the read is
// safe either way).
func (f *FirmsAPI) stageConfigFor(st Stage) stageConfig {
	for _, sc := range f.cfg.Stages {
		if sc.Stage == st {
			return sc
		}
	}
	return stageConfig{}
}

// GrantPremises records that a firm has secured premises of the given build
// zone class (right zone, right size — AC-7). The composition root calls
// this after engine.build zones a cell; this module records the premises
// state and lets growth proceed.
func (f *FirmsAPI) GrantPremises(id FirmID, zone string) error {
	if err := f.checkNotCopied("GrantPremises"); err != nil {
		return err
	}
	if zone == "" {
		return errs.New(ErrNoPremises, f.correlationID, map[string]any{"firm": uint64(id), "zone": zone})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fs, ok := f.firms[id]
	if !ok {
		return errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	fs.firm.Premises = Premises{Secured: true, ZoneClass: zone}
	fs.firm.Stalled = false
	return nil
}

// HireStaff adds real CitizenIDs to a firm's staff roster (AC-4): each
// hire must resolve through CitizensAPI (AC-15), and the roster grows by
// real IDs, never a counter. A zero-length hire list is rejected with
// ErrInvalidStaffCount (AC-16).
func (f *FirmsAPI) HireStaff(id FirmID, hireIDs []uint64) error {
	if err := f.checkNotCopied("HireStaff"); err != nil {
		return err
	}
	if len(hireIDs) == 0 {
		return errs.New(ErrInvalidStaffCount, f.correlationID, map[string]any{"value": 0})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.citizens == nil {
		return errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
			"operation": "HireStaff", "dependency": "citizens",
		})
	}
	fs, ok := f.firms[id]
	if !ok {
		return errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	return f.addHiresLocked(fs, hireIDs)
}

// validateHiresLocked checks each hire resolves through CitizensAPI (AC-15)
// and is not already on the roster nor duplicated within the batch — the
// roster is a SET (SEC-101). It does NOT mutate the roster; callers that
// need an atomic validate-then-commit (Grow) use this and append only after
// every other gate passes. The caller holds f.mu.
func (f *FirmsAPI) validateHiresLocked(fs *firmState, hireIDs []uint64) error {
	onRoster := make(map[uint64]bool, len(fs.firm.Staff)+len(hireIDs))
	for _, cid := range fs.firm.Staff {
		onRoster[cid] = true
	}
	for _, cid := range hireIDs {
		if _, ok := f.citizens.CitizenAt(cid, f.correlationID); !ok {
			return errs.New(ErrUnknownCitizen, f.correlationID, map[string]any{"citizen": cid})
		}
		if onRoster[cid] {
			return errs.New(ErrDuplicateStaff, f.correlationID, map[string]any{
				"citizen": cid, "firm": uint64(fs.firm.ID),
			})
		}
		onRoster[cid] = true
	}
	return nil
}

// addHiresLocked validates and appends a batch of real CitizenIDs to a
// firm's roster (AC-4) — the immediate-commit form used by HireStaff. A
// duplicate is rejected with ErrDuplicateStaff (SEC-101). The caller holds
// f.mu.
func (f *FirmsAPI) addHiresLocked(fs *firmState, hireIDs []uint64) error {
	if err := f.validateHiresLocked(fs, hireIDs); err != nil {
		return err
	}
	fs.firm.Staff = append(fs.firm.Staff, hireIDs...)
	return nil
}

// Grow advances a firm to the next lifecycle stage (AC-4/AC-6/AC-7):
//  1. the firm must not already be at Enterprise (ErrAlreadyEnterprise);
//  2. its roster (after adding the supplied real hires) must reach the
//     target stage's staff-count floor (else ErrGrowthBlocked — AC-4);
//  3. it must secure premises of the target stage's zone class, queried
//     through engine.build (else it enters the stalled/exit state and
//     growth returns ErrNoPremises — AC-7).
//
// A firm can never skip a stage (AC-6): the target is always the
// immediately next stage.
func (f *FirmsAPI) Grow(id FirmID, hireIDs []uint64) error {
	if err := f.checkNotCopied("Grow"); err != nil {
		return err
	}
	if len(hireIDs) == 0 {
		return errs.New(ErrInvalidStaffCount, f.correlationID, map[string]any{"value": 0})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.growLocked(id, hireIDs)
}

func (f *FirmsAPI) growLocked(id FirmID, hireIDs []uint64) error {
	if f.citizens == nil {
		return errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
			"operation": "Grow", "dependency": "citizens",
		})
	}
	fs, ok := f.firms[id]
	if !ok {
		return errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	if fs.firm.Stage == StageEnterprise {
		return errs.New(ErrAlreadyEnterprise, f.correlationID, map[string]any{"firm": uint64(id)})
	}

	// Validate the hires WITHOUT mutating the roster (resolve + dedup,
	// SEC-101) — Grow commits atomically only after every gate passes.
	if err := f.validateHiresLocked(fs, hireIDs); err != nil {
		return err
	}

	target, _ := nextStage(fs.firm.Stage)

	// Staff floor gate (AC-4): the projected roster (after the hires) must
	// reach the target stage's floor with DISTINCT real hires.
	floor := f.stageConfigFor(target).MinStaff
	if int64(len(fs.firm.Staff)+len(hireIDs)) < floor {
		return errs.New(ErrGrowthBlocked, f.correlationID, map[string]any{
			"firm": uint64(id), "reason": "staff roster below the target stage floor",
			"staff": len(fs.firm.Staff) + len(hireIDs), "floor": floor,
		})
	}

	// Premises gate (AC-7/SEC-103): the target stage's zone class must be
	// present in engine.build's catalogue AND the firm's SECURED zone class
	// must match the target stage's required class — right zone, right size,
	// never "any secured premises counts". Without it the firm enters the
	// stalled/exit state and the secured zone is not silently overwritten.
	premiseClass := f.stageConfigFor(target).PremiseClass
	zoneKnown := false
	if f.build != nil {
		_, zoneKnown = f.build.ZoneTypeByID(premiseClass)
	}
	if !zoneKnown || !fs.firm.Premises.Secured || fs.firm.Premises.ZoneClass != premiseClass {
		fs.firm.Stalled = true
		return errs.New(ErrNoPremises, f.correlationID, map[string]any{
			"firm": uint64(id), "zone": premiseClass, "secured": fs.firm.Premises.ZoneClass,
		})
	}

	// All gates passed: commit atomically.
	fs.firm.Staff = append(fs.firm.Staff, hireIDs...)
	fs.firm.Stage = target
	f.emitLocked(LifecycleEvent{Kind: EventGrown, FirmID: id, Month: f.month})
	return nil
}

// Fail closes a firm by insolvency/closure (AC-5/AC-9): every CitizenID in
// its staff roster has its employmentState set to unemployed through
// CitizensAPI, and the firm is removed from the registry. Citizens NOT on
// the roster are provably unaffected (only the roster IDs are touched).
// It returns the distinct Insolvency outcome.
func (f *FirmsAPI) Fail(id FirmID) (Insolvency, error) {
	if err := f.checkNotCopied("Fail"); err != nil {
		return Insolvency{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failLocked(id)
}

func (f *FirmsAPI) failLocked(id FirmID) (Insolvency, error) {
	fs, ok := f.firms[id]
	if !ok {
		return Insolvency{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	unemployed := append([]uint64(nil), fs.firm.Staff...)
	if f.citizens != nil {
		for _, cid := range unemployed {
			_ = f.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
				CorrelationID: f.correlationID,
				Kind:          citizens.LifeEventEmployment,
				CitizenID:     cid,
				Employment:    citizens.EmploymentUnemployed,
				Sector:        citizens.SectorNone,
			})
		}
	}
	// Retire the failed firm's outstanding credit from the aggregate
	// (SEC-100): written-off credit frees deposit-backed capacity.
	f.totalCreditOutstanding -= fs.firm.Financial.CreditOutstanding
	if f.totalCreditOutstanding < 0 {
		f.totalCreditOutstanding = 0
	}
	delete(f.firms, id)
	f.failedCount++
	f.emitLocked(LifecycleEvent{Kind: EventFailed, FirmID: id, Month: f.month})
	return Insolvency{FirmID: id, Unemployed: unemployed}, nil
}

// Acquire absorbs a target firm into an acquirer (AC-9): the target's
// staff roster TRANSFERS to the acquirer (no employment-state change), the
// target is removed, and the target's founder is logged as a successful
// exit (AC-12's angel-boost ledger). It returns the distinct Acquisition
// outcome. The acquirer and target must be distinct, registered firms.
func (f *FirmsAPI) Acquire(acquirerID, targetID FirmID) (Acquisition, error) {
	if err := f.checkNotCopied("Acquire"); err != nil {
		return Acquisition{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if acquirerID == targetID {
		return Acquisition{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(targetID)})
	}
	acq, ok := f.firms[acquirerID]
	if !ok {
		return Acquisition{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(acquirerID)})
	}
	tgt, ok := f.firms[targetID]
	if !ok {
		return Acquisition{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(targetID)})
	}
	transferred := append([]uint64(nil), tgt.firm.Staff...)
	acq.firm.Staff = append(acq.firm.Staff, transferred...)
	// The target's outstanding credit transfers to the acquirer — net-zero on
	// the aggregate total, but per-firm correct for ResolveMonth (SEC-100).
	acq.firm.Financial.CreditOutstanding = num.SatAdd(acq.firm.Financial.CreditOutstanding, tgt.firm.Financial.CreditOutstanding)
	// The target's founder logged a successful exit (AC-12).
	if rec := f.founderHistory[tgt.firm.FounderCitizenID]; rec != nil {
		rec.exited = true
	} else {
		f.founderHistory[tgt.firm.FounderCitizenID] = &founderRecord{exited: true}
	}
	delete(f.firms, targetID)
	f.emitLocked(LifecycleEvent{Kind: EventAcquired, FirmID: targetID, Month: f.month})
	return Acquisition{AcquirerID: acquirerID, TargetID: targetID, Transferred: transferred}, nil
}

// RecordExit logs a successful exit for a firm's founder (AC-12's
// "grown to Enterprise and later exited" half): the founder's subsequent
// founding/angel probability rises via the angel boost.
func (f *FirmsAPI) RecordExit(id FirmID) error {
	if err := f.checkNotCopied("RecordExit"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fs, ok := f.firms[id]
	if !ok {
		return errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	if rec := f.founderHistory[fs.firm.FounderCitizenID]; rec != nil {
		rec.exited = true
	} else {
		f.founderHistory[fs.firm.FounderCitizenID] = &founderRecord{exited: true}
	}
	return nil
}

// ResolveMonth runs one monthly failure/churn resolution (AC-9/AC-14):
// it applies the market input-availability scale (AC-8) to each firm's
// output, then fails any credit-dependent (Startup/Small) firm whose
// monthly borrowing cost — priced from the off-map base-rate cycle — now
// exceeds its (scaled) monthly cash flow. Deterministic: firms are
// resolved in ascending FirmID order (GR#21). Returns no partial state on
// a dependency error.
func (f *FirmsAPI) ResolveMonth(month int64) error {
	if err := f.checkNotCopied("ResolveMonth"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.month = month

	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		fs := f.firms[id]
		f.applyInputScalingLocked(fs)
		if fs.firm.Stage != StageStartup && fs.firm.Stage != StageSmall {
			continue
		}
		if fs.firm.Financial.CreditOutstanding <= 0 {
			continue
		}
		rateBp := f.borrowingRateLocked(month, fs.firm.Stage)
		cost := monthlyInterest(fs.firm.Financial.CreditOutstanding, rateBp)
		effective := fs.firm.Financial.MonthlyCashFlow * fs.firm.Financial.OutputScale / 1000
		if cost > effective {
			if _, err := f.failLocked(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyInputScalingLocked reduces a firm's output scale by its §33-chain
// input shortfall (AC-8): the available input (engine.market's
// capacity-bounded availability) over the required input, in per-mille,
// then folds in MOD-034's injected wellbeing ProductivityModifier
// multiplicatively (1.0 at perfect health, falling as the cohort's
// wellbeing worsens — wellbeing.WellbeingAPI.ProductivityModifier's own
// doc comment), so a declining-wellbeing workforce produces less output
// at the same input-availability shape. The caller holds f.mu.
func (f *FirmsAPI) applyInputScalingLocked(fs *firmState) {
	var scale int64
	if f.market == nil || fs.firm.InputRequired <= 0 {
		scale = 1000
	} else {
		avail, err := f.market.Availability(fs.firm.InputCommodity, fs.firm.InputRequired)
		if err != nil {
			// An unknown commodity is a config error, not a shortfall; leave
			// the input-scarcity term at full and let the caller's own error
			// surface carry it.
			scale = 1000
		} else {
			scale = satMul(avail.Available, 1000) / fs.firm.InputRequired
		}
	}

	if f.productivityModifier != nil {
		mod := f.productivityModifier()
		scale = int64(float64(scale) * mod)
	}
	fs.firm.Financial.OutputScale = clampPerMille(scale)
}

// monthlyInterest is the monthly interest charge on an outstanding credit
// balance at an annual rate in basis points: outstanding × rateBp / 10000
// / 12, in saturating fixed point (GR#16).
func monthlyInterest(outstanding, rateBp int64) int64 {
	annual := satMul(outstanding, rateBp) / 10000
	return annual / 12
}
