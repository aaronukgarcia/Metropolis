package defence

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// DefenceAPI is code.json's "engine.defence" inbound contract (GUID
// 38f3829b-91df-43da-89b3-8cedd390054c, DefenceAPI): central-government
// grants (competitive + formula support), population-threshold mandates with
// choice-within-compliance and a priced refusal path, and the integrated
// facilities (anti-cyclical payroll, personnel-as-citizens, procurement
// value, closure-as-shock output). It consumes engine.build, engine.finance
// and engine.citizens through their registered inbound contracts alone
// (GR#20), wired via SetBuild/SetFinance/SetCitizens.
//
// The zero value is not usable; construct via [New] or [Load]. A *DefenceAPI
// is safe for concurrent use (AC-15): every mutable field is guarded by mu,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class, mirroring engine.finance/engine.fiscal).
type DefenceAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           Config
	worldSeed     uint64

	// Wired dependencies, read under mu. The three registered outbound
	// edges (engine.build / engine.finance / engine.citizens). engine.world
	// is imported only for its coordinate value types (see types.go), never
	// for a WorldAPI.
	build    *build.BuildAPI
	finance  *finance.FinanceAPI
	citizens *citizens.CitizensAPI

	// Pushed inputs (documented state fields, not running totals).
	planningQuality float64 // §54 planning quality, 0..1 (AC-2 pushed input)
	wageBillFactor  float64 // recession wage-bill factor, (0,1] (AC-7)

	// Mandate/refusal state.
	responses         map[string]MandateResult
	refusedMandates   map[string]bool
	reputationPenalty int64

	// Facility state.
	facilities     map[FacilityID]*facility
	nextFacilityID FacilityID

	// bidSeq is the monotonic grant-bid counter that seeds each bid's
	// deterministic draw (det.NewStream(worldSeed, bidSeq, month, purpose)).
	bidSeq uint64

	// self is the SEC-020 copy guard (atomic.Pointer), stored exactly once in
	// New before the value is returned to any caller.
	self atomic.Pointer[DefenceAPI]
}

// facility is the internal, mutable record of one built facility. Unexported
// — the only read surface is [DefenceAPI.Facility]'s exported FacilityInfo
// snapshot.
type facility struct {
	id              FacilityID
	typ             FacilityType
	site            SiteRef
	mandateID       string
	choiceID        string
	nominalPayroll  finance.Money
	payrollFloor    finance.Money
	personnel       int64
	marriedQuarters int64
	schoolPlaces    int64
	procurement     finance.Money
	closed          bool
}

// New constructs a ready-to-wire DefenceAPI from a validated Config.
// worldSeed keys the deterministic grant-draw hash stream (AC-13).
// correlationID is attached to every error the returned API constructs
// (GR#1); an empty one mints a fresh ID. Dependencies are wired later via
// SetBuild/SetFinance/SetCitizens.
func New(cfg Config, worldSeed uint64, correlationID string) (*DefenceAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrDefenceDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}
	d := &DefenceAPI{
		correlationID:   correlationID,
		cfg:             cfg,
		worldSeed:       worldSeed,
		planningQuality: 0,
		wageBillFactor:  1,
		responses:       make(map[string]MandateResult),
		refusedMandates: make(map[string]bool),
		facilities:      make(map[FacilityID]*facility),
		nextFacilityID:  1,
	}
	// Stored exactly once, before d is returned to any caller.
	d.self.Store(d)
	return d, nil
}

// checkNotCopied rejects a method call on a struct-copied *DefenceAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (d *DefenceAPI) checkNotCopied(method string) error {
	if d.self.Load() != d {
		return errs.New(ErrCopiedValue, d.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetBuild wires the engine.build dependency used by facility siting (the
// registered engine.defence → engine.build edge, GR#20).
func (d *DefenceAPI) SetBuild(b *build.BuildAPI) error {
	if err := d.checkNotCopied("SetBuild"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.build = b
	return nil
}

// SetFinance wires the engine.finance dependency used by compensation
// posting and the double-entry grant/payroll lines (the registered
// engine.defence → engine.finance edge, GR#20).
func (d *DefenceAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := d.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.finance = f
	return nil
}

// SetCitizens wires the engine.citizens dependency used to settle facility
// personnel as real citizen records and forces-families households (the
// registered engine.defence → engine.citizens edge, GR#20).
func (d *DefenceAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := d.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.citizens = c
	return nil
}

// SetPlanningQuality sets the §54 planning-quality input to the grant win
// rate (AC-2's pushed-input surface, mirroring engine.attract's TermInputs
// pattern). It is accepted as a pushed value rather than a live
// engine.fiscal call because no engine.defence → engine.fiscal edge is
// registered (BUG-058). quality must be finite and in [0,1]; out-of-domain
// values are rejected, never clamped.
func (d *DefenceAPI) SetPlanningQuality(quality float64) error {
	if err := d.checkNotCopied("SetPlanningQuality"); err != nil {
		return err
	}
	if !num.IsFinite(quality) || quality < 0 || quality > 1 {
		return errs.New(ErrInvalidInput, d.correlationID, map[string]any{"field": "planningQuality", "value": quality})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.planningQuality = quality
	return nil
}
