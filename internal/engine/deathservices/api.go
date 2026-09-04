package deathservices

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// api.go is MOD-083 increment 1's DeathServicesAPI: the GR#20 inbound
// interface (code.json guid ba1fe880-21b7-46c0-bd56-257c0f6c223d) driving
// body intake directly off FEAT-087's RealisedDeath queue handoff (AC-1)
// and exposing disposal commands (bury/cremate/dispense) with no directly
// writable exported field (GR#20 contract). See doc.go for the full module
// narrative and the six-term conservation identity (AC-14).

// BodyState is one body's current position in the disposal pipeline
// (AC-14/AC-15): exactly one of five states at any time, transitioning
// forward only -- a body never returns to Awaiting once it reaches a
// terminal state (Buried/Cremated/Dispensed), and En Route is the only
// non-terminal state besides Awaiting.
type BodyState string

const (
	// BodyAwaiting: intake has produced the body record but no disposal
	// method has yet claimed it.
	BodyAwaiting BodyState = "awaiting"
	// BodyEnRoute: a hearse trip has been scheduled for this body (normal
	// burial-transport path, AC-7/AC-8) but the trip has not yet completed.
	BodyEnRoute BodyState = "enRoute"
	// BodyBuried: terminal -- the body occupies a graveyard plot (AC-2).
	BodyBuried BodyState = "buried"
	// BodyCremated: terminal -- the body was disposed of by cremation,
	// consuming no plot (AC-5).
	BodyCremated BodyState = "cremated"
	// BodyDispensed: terminal -- the body was handled through emergency
	// dispensation (AC-11), a route distinct from ordinary burial/cremation
	// so the conservation identity's six terms never overlap.
	BodyDispensed BodyState = "dispensed"
)

// Body is one intake record (AC-1). Every exported field is a read-only
// snapshot returned by value from an accessor -- there is no exported
// pointer into the live map, so no other package can mutate a Body's state
// by direct field assignment (GR#20's "no directly-writable exported
// fields" contract, mechanically checked by AC-1's grep).
type Body struct {
	CitizenID     uint64
	DeathMonth    int64
	EmergencyFlag bool
	State         BodyState
	CemeteryID    string // set once State == BodyBuried
	CrematoriumID string // set once State == BodyCremated
}

// bodyRecord is the unexported, mutable internal record.
type bodyRecord struct {
	citizenID     uint64
	deathMonth    int64
	emergencyFlag bool
	state         BodyState
	cemeteryID    string
	crematoriumID string
}

func snapshotBody(b *bodyRecord) Body {
	return Body{
		CitizenID:     b.citizenID,
		DeathMonth:    b.deathMonth,
		EmergencyFlag: b.emergencyFlag,
		State:         b.state,
		CemeteryID:    b.cemeteryID,
		CrematoriumID: b.crematoriumID,
	}
}

// DeathServicesAPI is code.json's "engine.deathservices" inbound interface.
// The zero value is not usable; construct via [NewDeathServicesAPI] or
// [Load]/[LoadDefault]. A *DeathServicesAPI is safe for concurrent use: all
// mutable state is guarded by mu, and checkNotCopied rejects a method call
// on a struct-copied value (SEC-020 family, mirroring engine.citizens'
// CitizensAPI/DeathQueue and engine.logistics' LogisticsAPI).
type DeathServicesAPI struct {
	mu            sync.Mutex
	correlationID string
	cfg           Config

	bodies map[uint64]*bodyRecord // citizenID -> body record; one per death (AC-1)

	cemeteries   map[string]*cemeteryState
	crematoria   map[string]*crematoriumState
	hearse       hearseState
	dispensation dispensationState

	// releasedTotal is AC-14's BodiesReleased term: the cumulative count of
	// bodies Intake has ever produced. Tracked as its own counter (never
	// derived from len(bodies), which would still be correct today but
	// would tie the identity's LHS to the same map the RHS terms read,
	// making a future accidental double-count invisible -- AC-14's
	// false-pass-risk note requires every term independently sourced).
	releasedTotal int64

	// Optional injected dependencies (mirrors CitizensAPI.SetSeason /
	// SetDeathDrainCapacity's post-construction wiring precedent -- no
	// constructor argument, so a DeathServicesAPI is usable stand-alone in
	// every existing/future test that never calls Wire, per GR#20's
	// "stub-forever" contract). A DeathServicesAPI never wired via Wire
	// still processes intake/burial/reuse/dispensation normally; only
	// cremation-cost posting (AC-6) and hearse/logistics congestion (AC-8)
	// degrade to a documented local-only accounting when unwired.
	servicesAPI  *services.ServicesAPI
	logisticsAPI *logistics.LogisticsAPI

	// negativeBudgetWarned (M2, mirrors citizens.DeathQueue.
	// negativeDrainWarned): true once this API has logged
	// [ErrNegativeBudget] at least once. A negative remaining budget
	// (usedThisMonth exceeding the data-sourced budget) is always a
	// programming error, not a normal condition -- logged once, not once
	// per call, so a stuck-negative accounting bug does not flood the log.
	negativeBudgetWarned bool

	// self is the SEC-020 copyguard (atomic.Pointer, mirroring
	// citizens.DeathQueue.self / logistics.LogisticsAPI.self): stored
	// exactly once, at the end of construction, before the value is
	// returned to any caller.
	self atomic.Pointer[DeathServicesAPI]
}

// NewDeathServicesAPI constructs an empty DeathServicesAPI from an
// already-loaded Config (AC-16: a caller with no data directory handy in a
// test can still construct one directly).
func NewDeathServicesAPI(cfg Config, correlationID string) *DeathServicesAPI {
	d := &DeathServicesAPI{
		correlationID: correlationID,
		cfg:           cfg,
		bodies:        make(map[uint64]*bodyRecord),
		cemeteries:    make(map[string]*cemeteryState),
		crematoria:    make(map[string]*crematoriumState),
	}
	d.self.Store(d)
	return d
}

// Load reads and validates data/deathservices.json (via [LoadConfig]) and
// returns a ready *DeathServicesAPI.
func Load(dir, correlationID string) (*DeathServicesAPI, error) {
	cfg, err := LoadConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return NewDeathServicesAPI(cfg, correlationID), nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it.
func LoadDefault(correlationID string) (*DeathServicesAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct copy of the
// *DeathServicesAPI [NewDeathServicesAPI]/[Load] returned.
func (d *DeathServicesAPI) checkNotCopied(correlationID, method string) error {
	if d.self.Load() != d {
		return errs.New(ErrDeathServicesCopied, correlationID, map[string]any{"method": method})
	}
	return nil
}

// Wire injects the optional engine.services / engine.logistics dependencies
// (the registered code.json outbound edges) after construction, mirroring
// CitizensAPI.SetSeason's post-construction wiring precedent -- no
// constructor argument, so every existing/future NewDeathServicesAPI/Load
// call site is unaffected. Either argument may be nil to leave that
// dependency unwired (see the struct's field doc for what degrades).
func (d *DeathServicesAPI) Wire(sv *services.ServicesAPI, lg *logistics.LogisticsAPI, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "Wire"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.servicesAPI = sv
	d.logisticsAPI = lg
	return nil
}

// Config returns the loaded configuration (read-only value copy).
func (d *DeathServicesAPI) Config(correlationID string) (Config, error) {
	if err := d.checkNotCopied(correlationID, "Config"); err != nil {
		return Config{}, err
	}
	return d.cfg, nil
}

// Intake drains FEAT-087's RealisedDeath handoff (AC-1): every entry
// produces exactly one Awaiting body record, in the order given.
//
// H4 fix (round-2, AC-1/AC-17): the WHOLE input batch is scanned for
// duplicates BEFORE anything is applied. A duplicate citizenID -- one
// repeated within THIS batch, or one that already has a body record from
// a prior Intake call -- is documented POLICY (b): the duplicate entry
// itself is skipped, but every OTHER (non-duplicate) entry in the batch is
// still applied. Intake still returns a non-nil [ErrDuplicateDeath] when
// any duplicate was found, so a caller knows something was wrong, but that
// error is a WARNING about a skipped entry, never a signal that "nothing
// in this call was applied" -- callers must not blindly retry the whole
// batch on error (retrying would just re-hit the same already-applied
// ids' now-real duplicates). The prior revision aborted entirely on the
// FIRST duplicate encountered mid-batch, silently DROPPING every
// not-yet-processed entry after it forever, with no retry path that could
// recover them (attack_round_test.go's
// TestAttackIntakePartialCommitOnDuplicate: citizen 3, arriving after a
// mid-stream duplicate of citizen 1, was never intaken at all under the
// old policy).
//
// AC-10/H5: this call also updates the module's dispensation signal from
// the SAME EmergencyFlag FEAT-087 already stamped on each RealisedDeath --
// never a local weather recalculation (GR#3). An applied entry carrying
// EmergencyFlag=true RAISES active to true. Intake NEVER lowers active on
// its own: an ordinary (EmergencyFlag=false) batch arriving while a
// composition-root-declared event is still live must not silently clear
// it (attack_round_test.go's
// TestAttackOrdinaryIntakeClearsActiveDispensation) -- lowering active
// requires the EXTERNAL signal itself to report the event has ended,
// which reaches this module through [DeathServicesAPI.SetDispensationActive]
// (or, in the live composition, a composition-root read of FEAT-087's own
// weather-event state calling that same setter). Most deaths, even DURING
// a declared weather emergency, are ordinary (non-emergency) deaths --
// AC-10 requires deactivation on THE EVENT ending, not on an unrelated
// batch's contents.
//
// Returns the citizenIDs actually applied (excludes any skipped
// duplicate), in the order intake received them.
func (d *DeathServicesAPI) Intake(deaths []citizens.RealisedDeath, correlationID string) ([]uint64, error) {
	if err := d.checkNotCopied(correlationID, "Intake"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	seenInBatch := make(map[uint64]bool, len(deaths))
	out := make([]uint64, 0, len(deaths))
	anyEmergency := false
	var dupErr error

	for _, rd := range deaths {
		if seenInBatch[rd.CitizenID] {
			if dupErr == nil {
				dupErr = errs.New(ErrDuplicateDeath, correlationID, map[string]any{
					"citizenId": rd.CitizenID,
					"cause":     "duplicate within this Intake batch",
				})
			}
			continue // H4 policy (b): skip only the duplicate entry
		}
		seenInBatch[rd.CitizenID] = true
		if _, exists := d.bodies[rd.CitizenID]; exists {
			if dupErr == nil {
				dupErr = errs.New(ErrDuplicateDeath, correlationID, map[string]any{
					"citizenId": rd.CitizenID,
					"cause":     "already intaken by a prior Intake call",
				})
			}
			continue
		}
		d.bodies[rd.CitizenID] = &bodyRecord{
			citizenID:     rd.CitizenID,
			deathMonth:    rd.DeathMonth,
			emergencyFlag: rd.EmergencyFlag,
			state:         BodyAwaiting,
		}
		d.releasedTotal++
		out = append(out, rd.CitizenID)
		if rd.EmergencyFlag {
			anyEmergency = true
		}
	}
	if anyEmergency {
		// H5: raise only -- never lower active here (see doc above).
		d.dispensation.active = true
	}
	return out, dupErr
}

// Body returns a read-only snapshot of one body record. Unknown citizenID
// returns [ErrUnknownBody].
func (d *DeathServicesAPI) Body(citizenID uint64, correlationID string) (Body, error) {
	if err := d.checkNotCopied(correlationID, "Body"); err != nil {
		return Body{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.bodies[citizenID]
	if !ok {
		return Body{}, errs.New(ErrUnknownBody, correlationID, map[string]any{"bodyId": citizenID})
	}
	return snapshotBody(b), nil
}

// AwaitingSorted returns the current Awaiting-state citizenIDs in
// deterministic FIFO order (DeathMonth, then CitizenID) -- the same
// (selectionMonth, id) tie-break convention citizens.DeathQueue's
// realiseLocked uses for its release order (GR#21: never map-iteration
// order). This is the composition-root-facing accessor a real
// RunHearseTransport/Cremate caller uses to build its own bodyIDs batch
// from the CURRENT backlog rather than blindly resubmitting an original
// list (see crematory.go's Cremate doc for why that matters once some ids
// in a batch have already reached a terminal state).
func (d *DeathServicesAPI) AwaitingSorted(correlationID string) ([]uint64, error) {
	if err := d.checkNotCopied(correlationID, "AwaitingSorted"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.awaitingSortedLocked(correlationID), nil
}

// awaitingSortedLocked is AwaitingSorted's lock-held implementation.
// Caller must hold d.mu. The checkNotCopied call here is REDUNDANT with
// AwaitingSorted's own guard (the only call site) -- kept anyway, matching
// this package's (and citizens.DeathQueue's indexInsert/indexRemove)
// established double-check convention: astgate's syntactic, no-call-graph
// scan cannot see that an unexported helper is reached only through an
// already-guarded entry point.
func (d *DeathServicesAPI) awaitingSortedLocked(correlationID string) []uint64 {
	_ = d.checkNotCopied(correlationID, "awaitingSortedLocked")
	ids := make([]uint64, 0, len(d.bodies))
	for id, b := range d.bodies {
		if b.state == BodyAwaiting {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		bi, bj := d.bodies[ids[i]], d.bodies[ids[j]]
		if bi.deathMonth != bj.deathMonth {
			return bi.deathMonth < bj.deathMonth
		}
		return ids[i] < ids[j]
	})
	return ids
}

// AwaitingBacklog returns the current count of Awaiting-state bodies (the
// unhandled-body backlog AC-7 requires be queryable).
func (d *DeathServicesAPI) AwaitingBacklog(correlationID string) (int, error) {
	if err := d.checkNotCopied(correlationID, "AwaitingBacklog"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, b := range d.bodies {
		if b.state == BodyAwaiting {
			n++
		}
	}
	return n, nil
}

// DispensationActive reports whether emergency dispensation is currently
// active (AC-10/AC-12).
func (d *DeathServicesAPI) DispensationActive(correlationID string) (bool, error) {
	if err := d.checkNotCopied(correlationID, "DispensationActive"); err != nil {
		return false, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dispensation.active, nil
}

// SetDispensationActive is an explicit external signal setter for a
// composition-root-level wire (or a test) that already has FEAT-087's
// weather-event state in hand and wants to set it directly, rather than
// waiting for the next Intake batch to carry an EmergencyFlag. This is
// still reading FEAT-087's own signal (the caller obtained `active` from
// [citizens.IsWeatherEmergency] or an equivalent live read) -- it is NOT a
// local weather recalculation (GR#3, AC-10).
func (d *DeathServicesAPI) SetDispensationActive(active bool, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "SetDispensationActive"); err != nil {
		return err
	}
	d.mu.Lock()
	d.dispensation.active = active
	d.mu.Unlock()
	return nil
}
