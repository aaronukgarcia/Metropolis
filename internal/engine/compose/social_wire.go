package compose

import (
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/social"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-2326609834: engine.social (MOD-053) wired into the composition root.
//
// Before this file engine.social had zero non-test importers: in the running
// game nothing was ever caught by the net. The wiring is:
//
//   - a MONTHLY PhasePopulation hook ("social", registrationOrder) placed after
//     wellbeing (so the wellbeing drivers are fresh) and before coastal (coastal's
//     new arrivals are not social's concern this month). RunShard reads the
//     month from the clock and the citizen employment census; ApplyEffect runs
//     the step (GR#21: inputs in RunShard, effects in ApplyEffect);
//   - the five caseload DriverInputs come from real state: UnemploymentMonths
//     from the live unemployment rate, Crowding/FinancialStress from
//     engine.wellbeing's registered drivers through social.WellbeingFamilyStress,
//     Deprivation/NightlifeDensity from data/social_wire.json (documented
//     placeholders, GR#15);
//   - cases close after a funding-dependent duration (data constants), so a
//     funded net makes the open caseload shallower;
//   - the month's support (open cases x per-case amount x funding level) is
//     posted to the ONE-MONEY ledger as a balanced treasury -> households
//     transfer under category "social.support" (money-neutral);
//   - engine.projections is the instance buildPolicies constructed (REUSED,
//     never a second one); social registers its caseload curve provider on it;
//   - a plain SetFunding command on a social service id that would CUT funding
//     is refused (a Slow-Fuse decision cannot be laundered through the raw
//     services seam); raises pass through social's own validated path;
//   - the read-only "f4.social" view and a save.Participant ("social").
//
// BUG-1141: ledger lines posted outside PhaseFinance are cleared from the
// per-tick log by the next BeginMonth; tests therefore sample social.support
// lines at the start of the following PhaseLandValueDecay, as coastal does.
//
// GR#25 edges needed (registered in the master plan by the lead at port):
//
//	feat.compositionroot -> engine.social
//	(the ledger posting consumer reuses the existing compose -> engine.finance edge)

// finCatSocialSupport is the social support ledger category (finance.Category
// is an open string).
const finCatSocialSupport = finance.Category("social.support")

const (
	socialWireFile = "social_wire.json"

	kindComposeSocial = "social"
	recComposeSocial  = "social.state"

	// socialServicePrefix prefixes every social service id (social.categoryServiceID).
	socialServicePrefix = "social."

	socialViewSubscriptionName = "f4.social"
	socialWireSchemaVersion    = 1
)

// socialSupportPerCaseCapMicropounds is the sanity ceiling on the per-case
// support constant: 100x the wage-regime value (GBP 1/case/month = 1000000),
// i.e. GBP 100/case. A value above it is out of the money regime (BUG-355
// class; the pre-rescale 200000000 drained the treasury) and is refused.
const socialSupportPerCaseCapMicropounds = int64(100000000)

// socialWireData is data/social_wire.json (GR#15).
type socialWireData struct {
	Deprivation                   float64 `json:"deprivation"`
	NightlifeDensity              float64 `json:"nightlifeDensity"`
	CitywidePersonsPerRoom        float64 `json:"citywidePersonsPerRoom"`
	CitywideRentBurden            float64 `json:"citywideRentBurden"`
	UnemploymentScaleMonths       float64 `json:"unemploymentScaleMonths"`
	CaseMonthsFunded              float64 `json:"caseMonthsFunded"`
	CaseMonthsUnfunded            float64 `json:"caseMonthsUnfunded"`
	InitialFunding                float64 `json:"initialFunding"`
	SupportPerOpenCaseMicropounds int64   `json:"supportPerOpenCaseMicropounds"`
}

func loadSocialWireData(cid string) (socialWireData, error) {
	dir, err := data.ResolveDataDir(cid)
	if err != nil {
		return socialWireData{}, err
	}
	path := filepath.Join(dir, socialWireFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return socialWireData{}, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "social_wire_data", "path": path})
	}
	var d socialWireData
	if err := json.Unmarshal(b, &d); err != nil {
		return socialWireData{}, moduleFailedWrapped(cid, "social_wire_data", err)
	}
	unit := func(name string, v float64) error {
		if !num.IsFinite(v) || v < 0 || v > 1 {
			return moduleDataInvalid(cid, "social_wire_data", name, v)
		}
		return nil
	}
	for _, c := range []struct {
		n string
		v float64
	}{{"deprivation", d.Deprivation}, {"nightlifeDensity", d.NightlifeDensity}} {
		if err := unit(c.n, c.v); err != nil {
			return socialWireData{}, err
		}
	}
	if !num.IsFinite(d.CitywidePersonsPerRoom) || d.CitywidePersonsPerRoom < 0 {
		return socialWireData{}, moduleDataInvalid(cid, "social_wire_data", "citywidePersonsPerRoom", d.CitywidePersonsPerRoom)
	}
	if !num.IsFinite(d.CitywideRentBurden) || d.CitywideRentBurden < 0 {
		return socialWireData{}, moduleDataInvalid(cid, "social_wire_data", "citywideRentBurden", d.CitywideRentBurden)
	}
	if !num.IsFinite(d.UnemploymentScaleMonths) || d.UnemploymentScaleMonths < 0 {
		return socialWireData{}, moduleDataInvalid(cid, "social_wire_data", "unemploymentScaleMonths", d.UnemploymentScaleMonths)
	}
	if !num.IsFinite(d.CaseMonthsFunded) || d.CaseMonthsFunded < 1 ||
		!num.IsFinite(d.CaseMonthsUnfunded) || d.CaseMonthsUnfunded < d.CaseMonthsFunded {
		return socialWireData{}, moduleDataInvalid(cid, "social_wire_data", "caseDurationMonths", fmt.Sprintf("%v/%v", d.CaseMonthsFunded, d.CaseMonthsUnfunded))
	}
	if err := unit("initialFunding", d.InitialFunding); err != nil {
		return socialWireData{}, err
	}
	if d.SupportPerOpenCaseMicropounds < 0 || d.SupportPerOpenCaseMicropounds > socialSupportPerCaseCapMicropounds {
		return socialWireData{}, moduleDataInvalid(cid, "social_wire_data", "supportPerOpenCaseMicropounds", d.SupportPerOpenCaseMicropounds)
	}
	return d, nil
}

// socialWiring is the composition's social state. mu guards the fields the
// read-only view reads while the tick mutates them.
type socialWiring struct {
	api  *social.SocialAPI
	data socialWireData

	mu sync.RWMutex
	// self holds the address newSocialWiring gave this value (stored once,
	// before the pointer is shared). A struct copy of *socialWiring would get
	// its own independent mu while still aliasing api, so every lock-taking
	// entry point rejects a copy BEFORE touching mu (SEC-020 family, same shape
	// as coastalWiring and errs.Logger; see checkNotCopied).
	self atomic.Pointer[socialWiring]
	// lastMonth is the last month the step ran for (-1 = never): the replay guard.
	lastMonth int64
	// posted is the cumulative support posted to the ledger (micropounds).
	posted int64
	// lastSupport is the last month's posted support.
	lastSupport int64
	// failedMonths counts months whose step or posting errored.
	failedMonths int64
}

// checkNotCopied reports whether the receiver is the value newSocialWiring
// constructed rather than a struct copy (SEC-020 family). Lock-free (one
// atomic.Pointer.Load) so it is safe to run BEFORE mu is touched -- a copy
// taken while mu was held would capture it "locked" and could park forever
// (SEC-016). A bare literal that never went through newSocialWiring has a nil
// self and is rejected the same way.
func (w *socialWiring) checkNotCopied() bool {
	return w.self.Load() == w
}

// errSocialCopied is the registry-sourced rejection for a copied receiver.
func errSocialCopied(cid, entry string) error {
	return errs.New(ErrSocialStateInvalid, cid, map[string]any{"cause": "struct-copied socialWiring", "entry": entry})
}

// newSocialWiring constructs the module, wires citizens / services / wellbeing /
// the SHARED projections instance (st.pol.proj, built by buildPolicies), and
// registers the five service categories and the caseload curve provider. Runs
// inside Wire BEFORE the first hook registration.
func newSocialWiring(cid string, st *simState, quiet bool) (*socialWiring, error) {
	d, err := loadSocialWireData(cid)
	if err != nil {
		return nil, err
	}
	if quiet {
		d.Deprivation, d.NightlifeDensity = 0, 0
		d.CitywidePersonsPerRoom, d.CitywideRentBurden = 0, 0
		d.UnemploymentScaleMonths = 0
		d.InitialFunding = 0
		d.SupportPerOpenCaseMicropounds = 0
	}
	dir, err := data.ResolveDataDir(cid)
	if err != nil {
		return nil, err
	}
	api, err := social.Load(dir, cid)
	if err != nil {
		return nil, err
	}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"SetCitizens", func() error { return api.SetCitizens(st.citizens) }},
		{"SetServices", func() error { return api.SetServices(st.services) }},
		{"SetFamilyStressSource", func() error {
			return api.SetFamilyStressSource(social.WellbeingFamilyStress{Wellbeing: st.wellbeingAPI})
		}},
		{"SetProjections", func() error { return api.SetProjections(st.pol.proj) }},
		{"RegisterServices", api.RegisterServices},
		{"RegisterProjectionProvider", api.RegisterProjectionProvider},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "social", "step": s.name})
		}
	}
	// A new city starts every category at the data-declared funding (services
	// register unfunded); a Load restores the saved level over it.
	for _, cat := range socialCategories {
		if err := st.services.SetFunding(services.ServiceID(socialServicePrefix+cat.String()), d.InitialFunding); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "social", "step": "initialFunding", "category": cat.String()})
		}
	}
	w := &socialWiring{api: api, data: d, lastMonth: -1}
	w.self.Store(w) // once, before w is shared with any other goroutine
	return w, nil
}

// socialEffect carries the inputs RunShard read before any hook mutated state.
type socialEffect struct {
	month      int64
	unemployed int64
	labour     int64
}

// socialHook is the monthly engine.social hook (PhasePopulation).
type socialHook struct {
	st *simState
}

func (h *socialHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	eff := socialEffect{month: clock.Month()}
	if h.st.citizens != nil {
		h.st.citizens.GatherInShardOrder(h.st.liveResidentIDs(), h.st.cid, func(_ uint64, cit citizens.Citizen, ok bool) {
			if !ok {
				return
			}
			switch cit.Employment.State {
			case citizens.EmploymentEmployed:
				eff.labour++
			case citizens.EmploymentUnemployed:
				eff.labour++
				eff.unemployed++
			}
		})
	}
	return []core.Effect{{Sequence: 0, Payload: eff}}, nil
}

func (h *socialHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(socialEffect)
	if !ok {
		return
	}
	h.st.runSocial(p)
}

// SingleShard implements core.SingleShardHook.
func (h *socialHook) SingleShard() bool { return true }

// unemploymentMonths turns the live unemployment rate into the UnemploymentMonths
// driver (rate x data constant, rounded). Zero labour force yields 0.
func (w *socialWiring) unemploymentMonths(unemployed, labour int64) int64 {
	if !w.checkNotCopied() {
		_ = errSocialCopied("", "unemploymentMonths")
		return 0 // fail closed: no unemployment pressure from a copy
	}
	if labour <= 0 || unemployed <= 0 {
		return 0
	}
	rate := float64(unemployed) / float64(labour)
	return num.ClampInt64FromFloat(math.Round(rate * w.data.UnemploymentScaleMonths))
}

// caseDuration is the number of months a case in a category stays open at the
// given funding level (>= 1).
func (w *socialWiring) caseDuration(funding float64) int64 {
	if !w.checkNotCopied() {
		_ = errSocialCopied("", "caseDuration")
		return 1 // fail closed: the minimum duration, so a copy closes cases rather than holding them open
	}
	if !num.IsFinite(funding) {
		funding = 0
	}
	funding = math.Max(0, math.Min(1, funding))
	d := w.data.CaseMonthsUnfunded + (w.data.CaseMonthsFunded-w.data.CaseMonthsUnfunded)*funding
	n := num.ClampInt64FromFloat(math.Round(d))
	if n < 1 {
		n = 1
	}
	return n
}

var socialCategories = []social.Category{
	social.CategoryFamilySupport, social.CategoryHomelessness, social.CategoryDisabilityCarers,
	social.CategoryFostering, social.CategoryAddiction,
}

// runSocial is one social month. Nil wiring is a documented no-op. Errors never
// abort the tick: they are logged under registry codes.
func (st *simState) runSocial(p socialEffect) {
	w := st.socialW
	if w == nil || st.citizens == nil {
		return
	}
	if !st.socialW.checkNotCopied() { // pre-lock copy guard (fail closed: no month is run)
		_ = errSocialCopied(st.cid, "runSocial")
		return
	}
	month := p.month
	w.mu.RLock()
	last := w.lastMonth
	w.mu.RUnlock()
	if month == last {
		return // replay guard
	}
	failed := false
	fail := func(cause error) {
		failed = true
		_ = errs.New(ErrSocialAdvanceFailed, st.cid, map[string]any{"month": month, "cause": cause.Error()})
	}

	in := social.DriverInputs{
		Deprivation:        w.data.Deprivation,
		NightlifeDensity:   w.data.NightlifeDensity,
		UnemploymentMonths: w.unemploymentMonths(p.unemployed, p.labour),
	}
	fs, err := w.api.FamilyStressInput(social.FamilyStressQuery{
		Month: month, PersonsPerRoom: w.data.CitywidePersonsPerRoom, RentBurden: w.data.CitywideRentBurden,
	})
	if err != nil {
		fail(err)
	} else {
		in.CrowdingStress, in.FinancialStress = fs.Crowding, fs.FinancialStress
	}
	if err := w.api.AdvanceMonth(month, in); err != nil {
		fail(err)
	}

	// Close cases that have run their funding-dependent duration (ascending id
	// order, deterministic), then size the support from what is still open.
	var support float64
	for _, cat := range socialCategories {
		funding, ferr := w.api.FundingLevel(cat)
		if ferr != nil {
			fail(ferr)
			continue
		}
		dur := w.caseDuration(funding)
		for _, id := range w.api.OpenCaseIDs(cat) {
			cs, cerr := w.api.Case(id)
			if cerr != nil {
				fail(cerr)
				continue
			}
			if month-cs.OpenedMonth >= dur {
				if rerr := w.api.ResolveCase(id, month, "closed-after-duration"); rerr != nil {
					fail(rerr)
				}
			}
		}
		open, oerr := w.api.Caseload(cat, month)
		if oerr != nil {
			fail(oerr)
			continue
		}
		support += float64(open) * float64(w.data.SupportPerOpenCaseMicropounds) * funding
	}

	posted := st.postSocialSupport(month, num.ClampInt64FromFloat(math.Round(support)))
	if !posted {
		failed = true
	}
	w.mu.Lock()
	w.lastMonth = month
	if failed {
		w.failedMonths++
	}
	w.mu.Unlock()
}

// postSocialSupport posts one balanced treasury -> households transfer under
// social.support, tracking the money-stock change from the measured ledger
// delta (zero for an internal transfer). Reports success; an amount <= 0 is a
// successful no-op.
func (st *simState) postSocialSupport(month, amount int64) bool {
	w := st.socialW
	if !st.socialW.checkNotCopied() { // pre-lock copy guard (fail closed: nothing posted, month reported failed)
		_ = errSocialCopied(st.cid, "postSocialSupport")
		return false
	}
	w.mu.Lock()
	w.lastSupport = 0
	w.mu.Unlock()
	if st.finance == nil || amount <= 0 {
		return true
	}
	err := st.withLedgerDelta(func() error {
		_, perr := st.finance.Post(finance.Transaction{
			Month:       month,
			Description: "social support",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(amount), Category: finCatSocialSupport},
				{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: finance.Money(amount), Category: finCatSocialSupport},
			},
		})
		return perr
	})
	if err != nil {
		_ = errs.New(ErrSocialSupportUnposted, st.cid, map[string]any{"amountMicropounds": amount, "month": month, "cause": err.Error()})
		return false
	}
	w.mu.Lock()
	w.posted = num.SatAdd(w.posted, amount)
	w.lastSupport = amount
	w.mu.Unlock()
	return true
}

// handleSocialFunding is the SetFunding command seam for a social service id.
// A cut is a Slow-Fuse decision whose projected consequence this plain
// command cannot carry, so it is refused (never forwarded to the raw services
// seam, which would launder it past the gate); a raise or no-change goes
// through social's own validated SetFunding.
func (st *simState) handleSocialFunding(serviceID string, level float64) error {
	w := st.socialW
	if !st.socialW.checkNotCopied() { // pre-lock copy guard (fail closed: the command is refused)
		return errSocialCopied(st.cid, "handleSocialFunding")
	}
	cat := social.Category(255)
	for _, c := range socialCategories {
		if socialServicePrefix+c.String() == serviceID {
			cat = c
		}
	}
	if !cat.Valid() {
		return errs.New(ErrSocialFundingCutRefused, st.cid, map[string]any{"serviceId": serviceID, "cause": "unknown social category"})
	}
	cur, err := w.api.FundingLevel(cat)
	if err != nil {
		return err
	}
	if num.IsFinite(level) && level < cur {
		return errs.New(ErrSocialFundingCutRefused, st.cid, map[string]any{"serviceId": serviceID, "cause": fmt.Sprintf("cut from %v to %v needs a projected consequence", cur, level)})
	}
	clock, cerr := st.e.Clock()
	if cerr != nil {
		return cerr
	}
	return w.api.SetFunding(social.FundingCommand{Category: cat, Level: level, Month: clock.Month()})
}

// isSocialService reports whether a service id belongs to engine.social.
func isSocialService(id string) bool { return strings.HasPrefix(id, socialServicePrefix) }

// Social returns the composition's engine.social instance (read-mostly
// accessors for headless drivers and tests).
func (c *Composition) Social() *social.SocialAPI {
	if c.state.socialW == nil {
		return nil
	}
	if !c.state.socialW.checkNotCopied() {
		_ = errSocialCopied(c.state.cid, "Social")
		return nil
	}
	return c.state.socialW.api
}

// SocialPostedMicropounds returns the cumulative support posted to the ledger.
func (c *Composition) SocialPostedMicropounds() int64 {
	w := c.state.socialW
	if w == nil {
		return 0
	}
	if !w.checkNotCopied() {
		_ = errSocialCopied(c.state.cid, "SocialPostedMicropounds")
		return 0
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.posted
}

// --- view ---

type socialCategoryView struct {
	Category  string  `json:"category"`
	ServiceID string  `json:"serviceId"`
	Open      int64   `json:"open"`
	Funding   float64 `json:"funding"`
}

type socialWirePatch struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Month         int64                `json:"month"`
	Categories    []socialCategoryView `json:"categories"`
	PostedTotal   int64                `json:"postedTotal"`
	LastSupport   int64                `json:"lastSupport"`
	FailedMonths  int64                `json:"failedMonths"`
}

// buildSocialPatch serves the f4.social view: five categories with open
// counts and funding levels. A nil wiring yields the honest empty patch.
func (st *simState) buildSocialPatch() (json.RawMessage, error) {
	p := socialWirePatch{SchemaVersion: socialWireSchemaVersion, Categories: []socialCategoryView{}}
	if w := st.socialW; w != nil && !st.socialW.checkNotCopied() {
		_ = errSocialCopied(st.cid, "buildSocialPatch") // fail closed: honest empty patch
	} else if w != nil {
		w.mu.RLock()
		p.Month, p.PostedTotal, p.LastSupport, p.FailedMonths = w.lastMonth, w.posted, w.lastSupport, w.failedMonths
		w.mu.RUnlock()
		month := p.Month
		if month < 0 {
			month = 0
		}
		for _, cat := range socialCategories {
			open, _ := w.api.Caseload(cat, month)
			funding, _ := w.api.FundingLevel(cat)
			p.Categories = append(p.Categories, socialCategoryView{
				Category: cat.String(), ServiceID: "social." + cat.String(), Open: open, Funding: funding,
			})
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "social", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// --- save participant ---

type socialRecord struct {
	State       social.State `json:"state"`
	LastMonth   int64        `json:"lastMonth"`
	Posted      int64        `json:"posted"`
	LastSupport int64        `json:"lastSupport"`
}

type socialParticipant struct {
	st *simState
}

func newSocialParticipant(st *simState) *socialParticipant { return &socialParticipant{st: st} }

func (p *socialParticipant) Kind() string { return kindComposeSocial }

func (p *socialParticipant) Source() serialize.RecordSource {
	w := p.st.socialW
	var rec socialRecord
	if w != nil && !w.checkNotCopied() {
		_ = errSocialCopied(p.st.cid, "Source")
		w = nil // fail closed: emit no record rather than a copy's state
	}
	if w != nil {
		w.mu.RLock()
		rec = socialRecord{State: w.api.ExportState(), LastMonth: w.lastMonth, Posted: w.posted, LastSupport: w.lastSupport}
		w.mu.RUnlock()
	}
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted || w == nil {
			return serialize.Record{}, false, nil
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recComposeSocial, err)
		}
		emitted = true
		return serialize.Record{Kind: recComposeSocial, Data: b}, true, nil
	}
}

func (p *socialParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recComposeSocial {
			return fmt.Errorf("compose: unknown social save record kind %q", rec.Kind)
		}
		w := p.st.socialW
		if w == nil {
			return nil
		}
		if !w.checkNotCopied() {
			return errSocialCopied(p.st.cid, "Handler")
		}
		var r socialRecord
		if err := json.Unmarshal(rec.Data, &r); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		if r.Posted < 0 || r.LastSupport < 0 {
			return errs.New(ErrSocialStateInvalid, p.st.cid, map[string]any{"cause": "negative posted counter"})
		}
		if err := w.api.ImportState(r.State); err != nil {
			return errs.Wrap(ErrSocialStateInvalid, p.st.cid, err, map[string]any{"cause": err.Error()})
		}
		w.mu.Lock()
		w.lastMonth, w.posted, w.lastSupport = r.LastMonth, r.Posted, r.LastSupport
		w.mu.Unlock()
		return nil
	}
}

// resetSocialIfNoShard returns social to clean state when a loaded bundle
// carries no social shard (every save taken before this wiring).
func (st *simState) resetSocialIfNoShard(shardKinds []string) error {
	if st.socialW == nil {
		return nil
	}
	if !st.socialW.checkNotCopied() {
		return errSocialCopied(st.cid, "resetSocialIfNoShard")
	}
	for _, k := range shardKinds {
		if k == kindComposeSocial {
			return nil
		}
	}
	if err := st.socialW.api.ImportState(social.State{LastHostelMonth: -1, LastFosterMonth: -1}); err != nil {
		return err
	}
	st.socialW.mu.Lock()
	st.socialW.lastMonth, st.socialW.posted, st.socialW.lastSupport = -1, 0, 0
	st.socialW.mu.Unlock()
	return nil
}

// digestInto folds the social observables into the composition state digest.
func (w *socialWiring) digestInto(h hash.Hash) {
	if !w.checkNotCopied() {
		_ = errSocialCopied("", "digestInto")
		return
	}
	s := w.api.ExportState()
	w.mu.RLock()
	defer w.mu.RUnlock()
	digestInt64(h, int64(len(s.Cases)))
	for _, c := range s.Cases {
		digestInt64(h, int64(c.ID))
		digestInt64(h, int64(c.Category))
		digestInt64(h, c.OpenedMonth)
		digestInt64(h, c.ClosedMonth)
		digestInt64(h, int64(c.Status))
	}
	digestInt64(h, int64(s.NextCaseID))
	digestInt64(h, w.lastMonth)
	digestInt64(h, w.posted)
}
