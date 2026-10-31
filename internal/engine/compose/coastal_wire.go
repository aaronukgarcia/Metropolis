package compose

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/coastal"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-2326609833: engine.coastal (MOD-044) wired into the composition root.
//
// Before this file engine.coastal had zero non-test importers: no boat ever
// arrived in the running game. The wiring is:
//
//   - a MONTHLY PhasePopulation hook ("coastal", registrationOrder): RunShard
//     reads the month from the clock; ApplyEffect runs coastal.Advance(month)
//     (GR#21: month from the clock in RunShard, effects in ApplyEffect);
//   - a ShoreSource adapter over engine.world (worldShoreSource: land tiles
//     with a sea 4-neighbour, sorted);
//   - granted cases mint citizens through the COMPOSE citizen id allocator
//     (st.nextCitizenID), never a raw case id -- so ids are unique across the
//     whole population and residentIDs()' closed [1,nextCitizenID) range
//     already includes them;
//   - the population gained is measured from citizens.TotalPopulation across
//     the Advance call and folded into peopleDelta (the people-conservation
//     ledger) in the same tick;
//   - the month's hotel / departure / processing / integration costs are
//     posted to the ONE-MONEY ledger as balanced treasury -> external
//     transactions (categories coastal.hotels, coastal.departure,
//     coastal.processing, coastal.integration); a rejected post stays owed and
//     is retried next month (never dropped);
//   - the read-only "f4.coastal" protocol view (coastal_publish.go);
//   - a save.Participant ("coastal") carrying the module snapshot, the posted
//     cumulative cost counters, the last-processed month and the citizen id
//     allocator position.
//
// GR#25 edges needed (registered in the master plan by the lead at port):
//
//	feat.compositionroot -> engine.coastal
//	engine.attract       -> engine.coastal   (lead ruling (a); see MigrationInflow)
//	(the ledger posting consumer reuses the existing compose -> engine.finance edge)

// Ledger categories for coastal cost postings (finance.Category is an open
// string).
const (
	finCatCoastalHotels      = finance.Category("coastal.hotels")
	finCatCoastalDeparture   = finance.Category("coastal.departure")
	finCatCoastalProcessing  = finance.Category("coastal.processing")
	finCatCoastalIntegration = finance.Category("coastal.integration")
)

// coastalCostLines is the FIXED posting order (never a map range, GR#21).
var coastalCostLines = []struct {
	cat   finance.Category
	label string
	get   func(*coastal.CoastalAPI) int64
}{
	{finCatCoastalHotels, "hotels", (*coastal.CoastalAPI).HotelCost},
	{finCatCoastalDeparture, "departure", (*coastal.CoastalAPI).DepartureCost},
	{finCatCoastalProcessing, "processing", (*coastal.CoastalAPI).ProcessingOpex},
	{finCatCoastalIntegration, "integration", (*coastal.CoastalAPI).IntegrationOpex},
}

// worldShoreSource adapts engine.world's land/sea classifier to
// coastal.ShoreSource. Shore cells are the land tiles with at least one
// in-extent 4-neighbour that is sea, in (X, Y) order. The set is a pure
// function of the (static) coastline model, so it is computed once.
type worldShoreSource struct {
	once  sync.Once
	cells []coastal.CellCoord
	set   map[coastal.CellCoord]bool
}

func (s *worldShoreSource) build() {
	s.once.Do(func() {
		s.set = make(map[coastal.CellCoord]bool)
		for x := 0; x < world.TilesPerSide; x++ {
			for y := 0; y < world.TilesPerSide; y++ {
				if !world.IsLandTile(world.TileCoord{X: x, Y: y}) {
					continue
				}
				shore := false
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					n := world.TileCoord{X: x + d[0], Y: y + d[1]}
					if n.InExtent() && !world.IsLandTile(n) {
						shore = true
						break
					}
				}
				if shore {
					c := coastal.CellCoord{X: x, Y: y}
					s.cells = append(s.cells, c)
					s.set[c] = true
				}
			}
		}
		sort.Slice(s.cells, func(i, j int) bool {
			if s.cells[i].X != s.cells[j].X {
				return s.cells[i].X < s.cells[j].X
			}
			return s.cells[i].Y < s.cells[j].Y
		})
	})
}

// ShoreCells implements coastal.ShoreSource.
func (s *worldShoreSource) ShoreCells() []coastal.CellCoord {
	s.build()
	return append([]coastal.CellCoord(nil), s.cells...)
}

// IsShore implements coastal.ShoreSource.
func (s *worldShoreSource) IsShore(c coastal.CellCoord) bool {
	s.build()
	return s.set[c]
}

// coastalWiring is the composition's coastal state (everything compose owns
// around the module). mu guards the fields the read-only view reads while the
// tick mutates them.
type coastalWiring struct {
	api *coastal.CoastalAPI
	svc *services.ServicesAPI
	// baseline are the data/services.json baselineServices, re-ensured (idempotent
	// register) every coastal month so a Load that replaced the services
	// instance table cannot leave the rescue ids unregistered.
	baseline []services.ServiceSpec

	mu sync.RWMutex
	// self holds the address newCoastalWiring gave this value (stored once,
	// before the pointer is shared). A struct copy of *coastalWiring would get
	// its own independent mu while still aliasing api/svc, so every
	// lock-taking entry point rejects a copy BEFORE touching mu (SEC-020
	// family, same shape as errs.Logger; see checkNotCopied).
	self atomic.Pointer[coastalWiring]
	// lastMonth is the last month Advance ran for (-1 = never): the replay guard
	// that stops a load-then-advance or a re-fired hook posting a month twice.
	lastMonth int64
	// posted are the cumulative micropounds already posted to the ledger per
	// coastalCostLines entry; the amount still owed is api total - posted.
	posted [4]int64
	// lastGranted is the number of coastal citizens minted in the last month
	// processed (the coastal term of the month's migration inflow).
	lastGranted int64
	// unregistered are the rescue ids the last month found absent.
	unregistered []string
	// failedMonths counts months whose Advance or ledger posting errored.
	failedMonths int64
}

// checkNotCopied reports whether the receiver is the value newCoastalWiring
// constructed rather than a struct copy (SEC-020 family; mirrors
// errs.Logger.checkNotCopied). Lock-free (one atomic.Pointer.Load) so it is
// safe to run BEFORE mu is touched -- a copy taken while mu was held would
// capture it "locked" and could park forever (SEC-016). A bare literal that
// never went through newCoastalWiring has a nil self and is rejected the same
// way.
func (w *coastalWiring) checkNotCopied() bool {
	return w.self.Load() == w
}

// errCoastalCopied is the registry-sourced rejection for a copied receiver.
func errCoastalCopied(cid, entry string) error {
	return errs.New(ErrCoastalStateInvalid, cid, map[string]any{"cause": "struct-copied coastalWiring", "entry": entry})
}

// newCoastalWiring builds the module (the injected instance, or one loaded
// from data/coastal.json) and registers the data/services.json baseline
// services. It wires the services and shore deps; the citizen deps are bound
// by bind() once simState exists.
func newCoastalWiring(injected *coastal.CoastalAPI, seed uint64, cid string, svc *services.ServicesAPI) (*coastalWiring, error) {
	api := injected
	if api == nil {
		var err error
		api, err = coastal.LoadDefault(seed, cid)
		if err != nil {
			return nil, err
		}
	}
	baseline, err := services.LoadBaselineServiceSpecs(cid)
	if err != nil {
		return nil, err
	}
	w := &coastalWiring{api: api, svc: svc, baseline: baseline, lastMonth: -1}
	w.self.Store(w) // once, before w is shared with any other goroutine
	if err := w.ensureBaselineServices(); err != nil {
		return nil, err
	}
	if err := api.SetServices(svc); err != nil {
		return nil, err
	}
	if err := api.SetShore(&worldShoreSource{}); err != nil {
		return nil, err
	}
	return w, nil
}

// ensureBaselineServices registers every baseline service not yet present.
// ErrDuplicateService is a no-op (RegisterService only inserts, so restored
// funding/upgrade state is never overwritten).
func (w *coastalWiring) ensureBaselineServices() error {
	if !w.checkNotCopied() {
		return errCoastalCopied("", "ensureBaselineServices")
	}
	for _, spec := range w.baseline {
		if err := w.svc.RegisterService(spec); err != nil {
			var e *errs.E
			if errors.As(err, &e) && e.Code == services.ErrDuplicateService {
				continue
			}
			return err
		}
	}
	return nil
}

// bind wires the citizens dependency and the compose citizen id allocator.
func (w *coastalWiring) bind(st *simState) error {
	if !w.checkNotCopied() {
		return errCoastalCopied(st.cid, "bind")
	}
	if err := w.api.SetCitizens(st.citizens); err != nil {
		return err
	}
	return w.api.SetCitizenIDAllocator(st.allocateCoastalCitizenID)
}

// allocateCoastalCitizenID is the compose citizen id seam for granted cases
// (lead ruling 2: the COMPOSE allocator, not a reserved range). It draws from
// the same counter spawnCitizens uses, under the same three-way namespace
// guard, and additionally refuses to enter the externally-seeded resident id
// range (Deps.SeedResidentIDBase/Count) so a coastal citizen can never be
// handed an id another store already owns.
func (st *simState) allocateCoastalCitizenID() (uint64, error) {
	id := st.nextCitizenID
	if id >= attract.MigrantIDBase {
		return 0, errs.New(ErrCoastalCitizenIDCollision, st.cid, map[string]any{"id": id, "limit": attract.MigrantIDBase})
	}
	if st.seedResidentIDCount > 0 && id > st.seedResidentIDBase && id <= st.seedResidentIDBase+uint64(st.seedResidentIDCount) {
		return 0, errs.New(ErrCoastalCitizenIDCollision, st.cid, map[string]any{"id": id, "limit": st.seedResidentIDBase})
	}
	st.nextCitizenID++
	return id, nil
}

// coastalEffect carries the month RunShard read from the clock.
type coastalEffect struct {
	month int64
}

// coastalHook is the monthly engine.coastal hook (PhasePopulation).
type coastalHook struct {
	st *simState
}

func (h *coastalHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	clock, err := h.st.e.Clock()
	if err != nil {
		return nil, err
	}
	return []core.Effect{{Sequence: 0, Payload: coastalEffect{month: clock.Month()}}}, nil
}

func (h *coastalHook) ApplyEffect(eff core.Effect) {
	p, ok := eff.Payload.(coastalEffect)
	if !ok {
		return
	}
	h.st.runCoastal(p.month)
}

// SingleShard implements core.SingleShardHook.
func (h *coastalHook) SingleShard() bool { return true }

// seasonIndexForMonth maps a simulation month to the §27 season index used by
// coastal's multiplier table (0 winter .. 3 autumn; month 0 = January).
func seasonIndexForMonth(month int64) int {
	m := month % 12
	if m < 0 {
		m += 12
	}
	return int((m + 1) % 12 / 3)
}

// runCoastal is one coastal month. Nil-wiring is a documented no-op (a bare
// simState in a test). Errors never abort the tick: they are recorded under
// registry codes and the month is not retried (lastMonth advances) except for
// owed ledger amounts, which carry forward.
func (st *simState) runCoastal(month int64) {
	w := st.coastalW
	if w == nil || st.citizens == nil {
		return
	}
	if !st.coastalW.checkNotCopied() { // pre-lock copy guard (fail closed: no month is run)
		_ = errCoastalCopied(st.cid, "runCoastal")
		return
	}
	w.mu.RLock()
	last := w.lastMonth
	w.mu.RUnlock()
	if month == last {
		return // replay guard: this month already ran (re-fire); equality, like attract, so a Load onto a restarted clock still advances
	}

	if err := w.ensureBaselineServices(); err != nil {
		_ = errs.New(ErrCoastalAdvanceFailed, st.cid, map[string]any{"month": month, "cause": err.Error()})
	}
	if err := w.api.SetSeasonIndex(seasonIndexForMonth(month)); err != nil {
		_ = errs.New(ErrCoastalAdvanceFailed, st.cid, map[string]any{"month": month, "cause": err.Error()})
	}

	popBefore := int64(st.citizens.TotalPopulation(st.cid))
	res, err := w.api.Advance(month)
	popAfter := int64(st.citizens.TotalPopulation(st.cid))
	gained := num.SatSub(popAfter, popBefore)
	// People conservation: every citizen minted by granted cases is a birth-
	// like inflow this tick; fold the MEASURED change (robust to a partial
	// Advance failure) into peopleDelta before the invariant observes it.
	st.peopleDelta = num.SatAdd(st.peopleDelta, gained)

	failed := false
	if err != nil {
		failed = true
		_ = errs.New(ErrCoastalAdvanceFailed, st.cid, map[string]any{"month": month, "cause": err.Error()})
	}
	for _, id := range res.RescueUnregistered {
		_ = errs.New(ErrCoastalRescueServiceUnregistered, st.cid, map[string]any{"serviceID": id})
	}
	if !st.postCoastalCosts(month) {
		failed = true
	}

	w.mu.Lock()
	w.lastMonth = month
	w.lastGranted = gained
	w.unregistered = append([]string(nil), res.RescueUnregistered...)
	if failed {
		w.failedMonths++
	}
	w.mu.Unlock()
}

// postCoastalCosts posts every cost line's outstanding amount (module
// cumulative total minus already-posted) as a balanced treasury -> external
// transaction, tracking the money conservation delta from the measured ledger
// change. Reports whether every line posted.
func (st *simState) postCoastalCosts(month int64) bool {
	w := st.coastalW
	if st.finance == nil {
		return true
	}
	if !st.coastalW.checkNotCopied() { // pre-lock copy guard (fail closed: nothing posted, month reported failed)
		_ = errCoastalCopied(st.cid, "postCoastalCosts")
		return false
	}
	ok := true
	for i, line := range coastalCostLines {
		total := line.get(w.api)
		w.mu.RLock()
		posted := w.posted[i]
		w.mu.RUnlock()
		owed := num.SatSub(total, posted)
		if owed <= 0 {
			continue
		}
		preMoney := num.SatAdd(st.treasury, st.citizenWealth)
		_, err := st.finance.Post(finance.Transaction{
			Month:       month,
			Description: "coastal " + line.label,
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(owed), Category: line.cat},
				{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(owed), Category: line.cat},
			},
		})
		if err != nil {
			ok = false
			_ = errs.New(ErrCoastalCostUnposted, st.cid, map[string]any{
				"category": string(line.cat), "amountMicropounds": owed, "month": month, "cause": err.Error(),
			})
			continue
		}
		w.mu.Lock()
		w.posted[i] = total
		w.mu.Unlock()
		st.syncMoneyFromLedger()
		postMoney := num.SatAdd(st.treasury, st.citizenWealth)
		st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(postMoney, preMoney))
	}
	return ok
}

// MigrationInflow is the month's total population inflow as seen by the
// migration consumers: engine.attract's applied inflow plus the coastal
// arrivals admitted (granted citizens minted) the same month, each counted
// exactly ONCE (AC-7). attractInflow is attract's own MigrationResult.Inflow.
func (st *simState) migrationInflow(attractInflow int64) int64 {
	if st.coastalW == nil {
		return attractInflow
	}
	if !st.coastalW.checkNotCopied() { // pre-lock copy guard (fail closed: attract's inflow alone)
		_ = errCoastalCopied(st.cid, "migrationInflow")
		return attractInflow
	}
	st.coastalW.mu.RLock()
	defer st.coastalW.mu.RUnlock()
	return num.SatAdd(attractInflow, st.coastalW.lastGranted)
}

// Coastal returns the composition's engine.coastal instance (read-mostly
// accessors for headless drivers and tests; never mutate through it outside
// a tick -- policy changes belong in the command journal).
func (c *Composition) Coastal() *coastal.CoastalAPI {
	if c.state.coastalW == nil {
		return nil
	}
	return c.state.coastalW.api
}

// CoastalPostedMicropounds returns the cumulative micropounds posted to the
// ledger under each coastal category, keyed by category label.
func (c *Composition) CoastalPostedMicropounds() map[string]int64 {
	out := map[string]int64{}
	w := c.state.coastalW
	if w == nil {
		return out
	}
	if !w.checkNotCopied() {
		_ = errCoastalCopied(c.state.cid, "CoastalPostedMicropounds")
		return out
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	for i, line := range coastalCostLines {
		out[line.label] = w.posted[i]
	}
	return out
}

// CoastalLastGranted returns the number of coastal citizens admitted in the
// last processed month.
func (c *Composition) CoastalLastGranted() int64 {
	w := c.state.coastalW
	if w == nil {
		return 0
	}
	if !w.checkNotCopied() {
		_ = errCoastalCopied(c.state.cid, "CoastalLastGranted")
		return 0
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastGranted
}

// --- save participant ---

const (
	kindComposeCoastal = "coastal"
	recComposeCoastal  = "coastal.state"
)

// coastalWire is the durable coastal record.
type coastalWire struct {
	Snapshot      coastal.Snapshot `json:"snapshot"`
	LastMonth     int64            `json:"lastMonth"`
	Posted        [4]int64         `json:"posted"`
	LastGranted   int64            `json:"lastGranted"`
	NextCitizenID uint64           `json:"nextCitizenID"`
}

type coastalParticipant struct {
	st *simState
}

func newCoastalParticipant(st *simState) *coastalParticipant { return &coastalParticipant{st: st} }

func (p *coastalParticipant) Kind() string { return kindComposeCoastal }

func (p *coastalParticipant) Source() serialize.RecordSource {
	w := p.st.coastalW
	var wire coastalWire
	if w != nil && !w.checkNotCopied() {
		_ = errCoastalCopied(p.st.cid, "Source")
		w = nil // fail closed: emit no record rather than a copy's state
	}
	if w != nil {
		w.mu.RLock()
		wire = coastalWire{
			Snapshot: w.api.Snapshot(), LastMonth: w.lastMonth, Posted: w.posted,
			LastGranted: w.lastGranted, NextCitizenID: p.st.nextCitizenID,
		}
		w.mu.RUnlock()
	}
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted || w == nil {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(wire)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recComposeCoastal, err)
		}
		emitted = true
		return serialize.Record{Kind: recComposeCoastal, Data: data}, true, nil
	}
}

func (p *coastalParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recComposeCoastal {
			return fmt.Errorf("compose: unknown coastal save record kind %q", rec.Kind)
		}
		w := p.st.coastalW
		if w == nil {
			return nil
		}
		if !w.checkNotCopied() {
			return errCoastalCopied(p.st.cid, "Handler")
		}
		var wire coastalWire
		if err := json.Unmarshal(rec.Data, &wire); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		for _, v := range wire.Posted {
			if v < 0 {
				return errs.New(ErrCoastalStateInvalid, p.st.cid, map[string]any{"cause": "negative posted counter"})
			}
		}
		if wire.NextCitizenID == 0 || wire.NextCitizenID >= attract.MigrantIDBase {
			return errs.New(ErrCoastalStateInvalid, p.st.cid, map[string]any{"cause": "citizen id allocator position out of range"})
		}
		if err := w.api.Restore(wire.Snapshot); err != nil {
			return errs.Wrap(ErrCoastalStateInvalid, p.st.cid, err, map[string]any{})
		}
		w.mu.Lock()
		w.lastMonth = wire.LastMonth
		w.posted = wire.Posted
		w.lastGranted = wire.LastGranted
		w.mu.Unlock()
		p.st.nextCitizenID = wire.NextCitizenID
		return nil
	}
}

// digestInto folds the coastal observables into the composition state digest.
func (w *coastalWiring) digestInto(h hash.Hash) {
	if !w.checkNotCopied() {
		_ = errCoastalCopied("", "digestInto")
		return
	}
	s := w.api.Snapshot()
	w.mu.RLock()
	defer w.mu.RUnlock()
	digestInt64(h, int64(s.EraTier))
	digestInt64(h, int64(s.SeasonIndex))
	digestFloat(h, s.WorldConditions)
	digestFloat(h, s.ProcessingFunding)
	digestFloat(h, s.HousingApproach)
	digestFloat(h, s.IntegrationInvestment)
	digestInt64(h, int64(len(s.Arrivals)))
	for _, ev := range s.Arrivals {
		digestInt64(h, int64(ev.ID))
		digestInt64(h, ev.Month)
		digestInt64(h, ev.Size)
	}
	digestInt64(h, int64(len(s.Cases)))
	for _, k := range s.Cases {
		digestInt64(h, int64(k.ID))
		digestInt64(h, int64(k.Stage))
		digestInt64(h, k.ResolveMonth)
		digestInt64(h, int64(k.CitizenID))
	}
	digestInt64(h, int64(len(s.Backlog)))
	digestInt64(h, s.ProcessingOpex)
	digestInt64(h, s.IntegrationOpex)
	digestInt64(h, s.HotelCost)
	digestInt64(h, s.DepartureCost)
	digestFloat(h, s.Friction)
	digestInt64(h, w.lastMonth)
	for _, v := range w.posted {
		digestInt64(h, v)
	}
}

// compile-time proof the id-allocator signature matches the module seam.
var _ func() (uint64, error) = (*simState)(nil).allocateCoastalCitizenID
