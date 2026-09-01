package checkpoint

import (
	"os"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// Manager orchestrates checkpoint creation, revert-as-fork, and load
// against one checkpoint root and one registered [save.Participant] list.
// It delegates the whole-state save/load mechanics to a feat.saveux
// [save.Manager] it owns (GR#3/GR#20 — never reimplements them) and adds
// this package's lineage vocabulary on top. The zero value is not usable —
// construct via NewManager.
//
// A *Manager is safe for concurrent use: mu serialises CreateCheckpoint/
// Revert (a second call arriving while one is in flight is rejected with
// ErrCheckpointInProgress, mirroring feat.saveux's single-save-in-flight
// guard). Load/CurrentID/Lineage do not take mu — Load is protected by
// feat.saveux's own atomic-promotion design, and the others only read.
type Manager struct {
	mu            sync.Mutex
	root          string
	saveMgr       *save.Manager
	correlationID string

	// maxRetainedForks bounds the abandoned-branch retention (AC-6);
	// default MaxRetainedForks, overridable via SetMaxRetainedForks.
	maxRetainedForks int

	// lastPruneErr holds the most recent auto-prune failure, or nil after a
	// successful prune (or before any has run). Prune failures are non-fatal
	// to CreateCheckpoint/Revert — the checkpoint is already promoted by the
	// time pruning runs — so they are surfaced via LastPruneError rather than
	// returned alongside the created Checkpoint, where an error would be
	// ambiguous with "not created" (SEC-190). Guarded by mu.
	lastPruneErr error

	// self holds the address NewManager gave this Manager, so
	// checkNotCopied can reject a struct copy (SEC-020-class — mu is a
	// sync.Mutex VALUE while saveMgr is an aliased pointer, exactly the
	// two-locks-one-referent shape). atomic.Pointer for the pre-lock
	// ordering guarantee (SEC-016).
	self atomic.Pointer[Manager]

	// expectedWorldSeed is this Manager's OWN composition's world seed
	// (BUG-485), given at construction — mirroring compose.Composition's
	// c.state.seed field exactly (compose.go/save_wire.go), since a
	// *Manager is a single-composition-lifetime object just like a
	// *Composition, unlike the generic, seed-agnostic save.Manager it
	// wraps. Every restore this package performs into LIVE participants
	// (Load, Revert's restore-then-fork, recoverAfterLoad's undo-reload)
	// passes save.WithExpectedWorldSeed(expectedWorldSeed) to the owned
	// saveMgr, so a differently-seeded bundle is refused with
	// save.ErrSaveSeedMismatch instead of silently diverging every
	// seed-derived stateless draw from the composition's real trajectory
	// — closing the gap BUG-479 deliberately left open at this layer
	// (see save/options.go's loadOptions comment).
	expectedWorldSeed int64
}

// NewManager constructs a *Manager rooted at root, saving/loading the
// given participants via an owned feat.saveux Manager. correlationID is
// attached to every registry-sourced error this package constructs (GR#1)
// and is forwarded to the owned save.Manager. worldSeed is THIS Manager's
// own composition's world seed (BUG-485) — the same value the caller
// would pass as save.Context.WorldSeed to CreateCheckpoint/Revert — and
// is checked against every bundle's header on every restore into live
// participants (Load/Revert/recoverAfterLoad), refusing a mismatch with
// save.ErrSaveSeedMismatch. Pass the composition's real seed even in
// tests that never intend to exercise a mismatch: 0 is a legitimate seed
// value, not an "unchecked" sentinel — there is no way to construct a
// Manager that skips the check, by design (the pre-BUG-479 opt-in-only
// gap this closes).
func NewManager(root string, participants []save.Participant, correlationID string, worldSeed int64) *Manager {
	m := &Manager{
		root:              root,
		saveMgr:           save.NewManager(root, participants, correlationID),
		correlationID:     correlationID,
		maxRetainedForks:  MaxRetainedForks,
		expectedWorldSeed: worldSeed,
	}
	m.self.Store(m)
	return m
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Manager value (SEC-020-class), mirroring save.Manager.checkNotCopied
// / Engine.checkNotCopied. Deliberately lock-free (a single atomic.Pointer
// Load) so it is safe and correct to call BEFORE m.mu is ever touched.
func (m *Manager) checkNotCopied(ctx map[string]any) error {
	if m.self.Load() != m {
		return errs.New(ErrCheckpointCopied, m.correlationID, ctx)
	}
	return nil
}

// SetMaxRetainedForks overrides the abandoned-branch retention bound
// (AC-6). n must be non-negative; 0 retains no abandoned branches (only the
// active head and its ancestors survive pruning).
func (m *Manager) SetMaxRetainedForks(n int) error {
	if err := m.checkNotCopied(map[string]any{"method": "SetMaxRetainedForks", "value": n}); err != nil {
		return err
	}
	if n < 0 {
		return errs.New(ErrInvalidForkConfig, m.correlationID, map[string]any{"method": "SetMaxRetainedForks", "value": n})
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxRetainedForks = n
	return nil
}

// Root returns this Manager's checkpoint root directory.
func (m *Manager) Root() string {
	if err := m.checkNotCopied(map[string]any{"method": "Root"}); err != nil {
		return ""
	}
	return m.root
}

// LastPruneError returns the most recent auto-prune failure, or nil if the
// last prune succeeded (or none has run yet). Prune failures are non-fatal
// to CreateCheckpoint/Revert — the checkpoint is already promoted by the
// time pruning runs — so they are surfaced here rather than returned
// alongside the created Checkpoint, where a non-nil error would be
// ambiguous with "not created" (SEC-190).
func (m *Manager) LastPruneError() error {
	if err := m.checkNotCopied(map[string]any{"method": "LastPruneError"}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastPruneErr
}

// CreateCheckpoint writes a whole-state checkpoint named name whose
// immediate parent is parent, and makes it the active head (AC-1/AC-4).
// The checkpoint is a complete bundle via feat.saveux's SaveManual — never
// a delta — plus this package's checkpoint-meta.json lineage sidecar.
// parent may be empty to create a root checkpoint; a non-empty parent must
// name an existing checkpoint (ErrParentNotFound otherwise).
//
// On any failure of the checkpoint write itself — SaveManual, the lineage
// sidecar, or the head-pointer write — the just-created bundle is rolled
// back and the active head is left unchanged, so no half-recorded
// checkpoint is left discoverable. Auto-prune failure is the one non-fatal
// exception: the checkpoint is already promoted and active by then, so a
// prune failure is surfaced via [Manager.LastPruneError] rather than
// returned as an error (SEC-190 — a promoted checkpoint must never be
// returned alongside a non-nil error).
func (m *Manager) CreateCheckpoint(ctx save.Context, name string, parent ID) (Checkpoint, error) {
	if err := m.checkNotCopied(map[string]any{"method": "CreateCheckpoint", "name": name}); err != nil {
		return Checkpoint{}, err
	}
	if !m.mu.TryLock() {
		return Checkpoint{}, errs.New(ErrCheckpointInProgress, m.correlationID, map[string]any{"method": "CreateCheckpoint"})
	}
	defer m.mu.Unlock()

	id := ID(name)
	if err := validateCheckpointID(id); err != nil {
		return Checkpoint{}, errs.Wrap(ErrInvalidCheckpointID, m.correlationID, err, map[string]any{"id": name, "cause": err.Error()})
	}
	// SEC-189: bound the name length so the create-at and revert-at domains
	// agree (see maxCheckpointNameLen). A longer name would create fine but
	// derive a fork name Revert's SaveManual rejects — permanently
	// unrevertible. Reject it here, before any save.
	if len(name) > maxCheckpointNameLen {
		return Checkpoint{}, errs.New(ErrCheckpointNameTooLong, m.correlationID, map[string]any{"name": name, "length": len(name), "max": maxCheckpointNameLen})
	}
	// SEC-174/SEC-188: reject a name that already occupies the manual/
	// namespace — a checkpoint OR a same-named manual save (feat.saveux
	// shares this namespace; see bundleExists) — BEFORE any save.
	// Re-creating an existing name would silently save-over the prior
	// bundle (destroying player data) and, for a checkpoint, re-parent it —
	// a lineage cycle when the new parent is a descendant of the existing
	// checkpoint. Lineage is fixed at creation (AC-4), so an existing name
	// can never be re-parented.
	if bundleExists(m.root, id) {
		return Checkpoint{}, errs.New(ErrNameOccupied, m.correlationID, map[string]any{"name": name})
	}
	if parent != "" {
		if err := validateCheckpointID(parent); err != nil {
			return Checkpoint{}, errs.Wrap(ErrInvalidCheckpointID, m.correlationID, err, map[string]any{"parent": string(parent), "id": string(parent), "cause": err.Error()})
		}
		if !isCheckpoint(m.root, parent) {
			return Checkpoint{}, errs.New(ErrParentNotFound, m.correlationID, map[string]any{"parent": string(parent)})
		}
	}

	if err := m.saveBundle(ctx, name, parent); err != nil {
		return Checkpoint{}, err
	}
	if err := m.setHead(id); err != nil {
		_ = os.RemoveAll(checkpointDir(m.root, id))
		return Checkpoint{}, err
	}

	cp := Checkpoint{
		ID:            id,
		ParentID:      parent,
		CreatedAtTick: ctx.CreatedAtTick,
		GameMonth:     ctx.GameMonth,
		DisplayName:   name,
		Active:        true,
	}
	// Auto-prune abandoned branches beyond MaxRetainedForks. The checkpoint
	// is already promoted and active; a prune failure is non-fatal and is
	// surfaced via LastPruneError rather than returned alongside cp
	// (SEC-190 — see CreateCheckpoint's doc contract).
	m.lastPruneErr = m.prune()
	return cp, nil
}

// Revert reverts play to the checkpoint named target, forking the
// timeline (AC-3): it loads target's whole-state back into the live
// participants (reusing feat.saveux's Load) and then creates a NEW
// checkpoint — a fresh branch head whose parent is target — capturing the
// just-reverted state. The branch that was active before the revert is
// left fully intact and independently loadable; nothing is deleted,
// truncated, or rebased by the act of reverting itself. As with
// CreateCheckpoint, an auto-prune failure is non-fatal and surfaced via
// [Manager.LastPruneError] rather than returned alongside the fork
// (SEC-190).
func (m *Manager) Revert(ctx save.Context, target ID) (Checkpoint, error) {
	if err := m.checkNotCopied(map[string]any{"method": "Revert", "target": string(target)}); err != nil {
		return Checkpoint{}, err
	}
	if !m.mu.TryLock() {
		return Checkpoint{}, errs.New(ErrCheckpointInProgress, m.correlationID, map[string]any{"method": "Revert"})
	}
	defer m.mu.Unlock()

	if err := validateCheckpointID(target); err != nil {
		return Checkpoint{}, errs.Wrap(ErrInvalidCheckpointID, m.correlationID, err, map[string]any{"target": string(target), "id": string(target), "cause": err.Error()})
	}
	if !isCheckpoint(m.root, target) {
		return Checkpoint{}, errs.New(ErrNotACheckpoint, m.correlationID, map[string]any{"target": string(target)})
	}

	// Read the head pointer and derive the fork name BEFORE mutating live
	// state, so the prior active head is captured for SEC-176's recovery and
	// any input/fork-name failure returns with live state untouched. Revert
	// holds mu, so this head is stable for the whole operation (Load never
	// mutates the head pointer; only setHead/writeHead do).
	head, err := readHead(m.root)
	if err != nil {
		return Checkpoint{}, errs.Wrap(ErrHeadReadFailed, m.correlationID, err, map[string]any{"cause": err.Error()})
	}
	priorActiveID := head.ActiveID

	// SEC-175: derive a free fork name BEFORE the Load. forkName is
	// deterministic (<target>.fork<seq>, GR#15), but a player checkpoint may
	// already occupy that name; skip forward past any collision so a revert
	// can never silently save-over an existing checkpoint (GR#16: the
	// sequence is advanced with saturating arithmetic, so it can never wrap).
	forkID, forkSeq, err := m.nextFreeForkName(target, head.ForkSeq)
	if err != nil {
		return Checkpoint{}, err
	}

	// Restore target's state into the live participants. This is the one
	// side effect of revert outside this package's own on-disk tree; it is
	// delegated to feat.saveux's Load, which reconstructs each participant
	// via its Handler. BUG-485: passes save.WithExpectedWorldSeed so a
	// target checkpoint whose bundle seed does not match this Manager's
	// own expectedWorldSeed is refused with save.ErrSaveSeedMismatch
	// before any participant is touched, instead of silently reverting
	// live state into a differently-seeded trajectory.
	if _, _, err := m.saveMgr.Load(checkpointDir(m.root, target), save.WithExpectedWorldSeed(m.expectedWorldSeed)); err != nil {
		return Checkpoint{}, err
	}

	// From here on, any failure has already mutated live state, so undo it
	// by reloading the prior active head (SEC-176) before returning — the
	// caller must never observe live state reverted while CurrentID still
	// reports the pre-revert head.
	if err := m.saveBundle(ctx, forkID, target); err != nil {
		return Checkpoint{}, m.recoverAfterLoad(priorActiveID, err)
	}
	head.ActiveID = ID(forkID)
	head.ForkSeq = forkSeq
	if err := writeHead(m.root, head); err != nil {
		_ = os.RemoveAll(checkpointDir(m.root, ID(forkID)))
		return Checkpoint{}, m.recoverAfterLoad(priorActiveID, errs.Wrap(ErrHeadWriteFailed, m.correlationID, err, map[string]any{"cause": err.Error()}))
	}

	cp := Checkpoint{
		ID:            ID(forkID),
		ParentID:      target,
		CreatedAtTick: ctx.CreatedAtTick,
		GameMonth:     ctx.GameMonth,
		DisplayName:   forkID,
		Active:        true,
	}
	// Auto-prune abandoned branches; non-fatal, surfaced via LastPruneError
	// (SEC-190 — the same contract as CreateCheckpoint).
	m.lastPruneErr = m.prune()
	return cp, nil
}

// Load reconstructs every registered participant's state from the
// checkpoint bundle named id (AC-2, GR#12), delegating to feat.saveux's
// Load — which runs serialize.ValidateBundle first, then streams each
// shard to its participant's Handler. Returns the bundle's Header and
// feat.saveux's Meta. BUG-485: refuses a bundle whose header WorldSeed
// does not equal this Manager's own expectedWorldSeed (given at
// NewManager construction) with save.ErrSaveSeedMismatch, BEFORE any
// participant Handler runs — mirroring compose.Composition.Load's own
// BUG-479 check at this package's layer, since this method restores
// straight into the caller's live participants exactly as that one does.
func (m *Manager) Load(id ID) (serialize.Header, save.Meta, error) {
	if err := m.checkNotCopied(map[string]any{"method": "Load", "id": string(id)}); err != nil {
		return serialize.Header{}, save.Meta{}, err
	}
	if err := validateCheckpointID(id); err != nil {
		return serialize.Header{}, save.Meta{}, errs.Wrap(ErrInvalidCheckpointID, m.correlationID, err, map[string]any{"id": string(id), "cause": err.Error()})
	}
	return m.saveMgr.Load(checkpointDir(m.root, id), save.WithExpectedWorldSeed(m.expectedWorldSeed))
}

// CurrentID returns the identifier of the currently-active checkpoint/
// branch head without mutating checkpoint state (AC-16). It returns an
// empty ID with no error when no checkpoint exists yet (a fresh root).
func (m *Manager) CurrentID() (ID, error) {
	if err := m.checkNotCopied(map[string]any{"method": "CurrentID"}); err != nil {
		return "", err
	}
	head, err := readHead(m.root)
	if err != nil {
		return "", errs.Wrap(ErrHeadReadFailed, m.correlationID, err, map[string]any{"cause": err.Error()})
	}
	return head.ActiveID, nil
}

// saveBundle writes a whole-state checkpoint bundle named name with parent
// parent: feat.saveux's SaveManual first (atomic promotion), then this
// package's checkpoint-meta.json lineage sidecar. On a sidecar failure the
// just-created bundle is removed, so a checkpoint is either fully recorded
// or absent — never half-recorded.
func (m *Manager) saveBundle(ctx save.Context, name string, parent ID) error {
	if err := m.saveMgr.SaveManual(ctx, name); err != nil {
		return err
	}
	dir := checkpointDir(m.root, ID(name))
	if err := writeCheckpointMeta(dir, parent); err != nil {
		_ = os.RemoveAll(dir)
		return errs.Wrap(ErrCheckpointMetaWriteFailed, m.correlationID, err, map[string]any{"dir": dir, "cause": err.Error()})
	}
	return nil
}

// nextFreeForkName returns the deterministic fork name for a branch off
// target and the fork sequence consumed to reach it, starting from startSeq.
// forkName derives "<target>.fork<seq>"; a player checkpoint OR a same-named
// manual save may already occupy that name (SEC-175/SEC-188), in which case
// the sequence is advanced until a name that does not already occupy the
// manual/ namespace is found — so a revert can never silently save-over an
// existing bundle. A derived name that would exceed maxSaveNameLen (save's
// manual-name limit) is rejected with ErrForkNameTooLong rather than
// returned (SEC-196), so a fork-of-fork chain can never produce an
// over-length — and therefore unrevertible — fork. The sequence is advanced
// with saturating arithmetic (GR#16), so it can never wrap; saturation is
// reported as ErrForkSeqExhausted. The result is deterministic given the
// on-disk tree (GR#21): same tree, same startSeq, same name.
func (m *Manager) nextFreeForkName(target ID, startSeq int64) (string, int64, error) {
	seq := startSeq
	for {
		next, saturated := num.SatAddChecked(seq, 1)
		if saturated {
			return "", 0, errs.New(ErrForkSeqExhausted, m.correlationID, map[string]any{"target": string(target)})
		}
		name := forkName(target, next)
		// SEC-196: bound the derived fork name to save's manual-name limit
		// (maxSaveNameLen). forkName appends ".fork<seq>" to target, and a
		// fork checkpoint is itself a valid future revert target, so a
		// fork-of-fork chain grows the name by len(forkNamePrefix)+digits(seq)
		// per level. Reject here — BEFORE any state mutation (Revert has not
		// loaded target yet) — the moment the derived name would exceed
		// maxSaveNameLen, so Revert never silently creates a fork whose own
		// fork name would be over-length and therefore unrevertible. Because
		// digits(seq) is monotonically non-decreasing, once a name is
		// over-length every later sequence is too, so rejecting immediately
		// is correct and deterministic (GR#21).
		if len(name) > maxSaveNameLen {
			return "", 0, errs.New(ErrForkNameTooLong, m.correlationID, map[string]any{
				"target":   string(target),
				"forkName": name,
				"length":   len(name),
				"max":      maxSaveNameLen,
			})
		}
		if !bundleExists(m.root, ID(name)) {
			return name, next, nil
		}
		seq = next
	}
}

// recoverAfterLoad undoes Revert's Load of target after a later step (the
// fork-save or head-write) failed, restoring the prior active head into the
// live participants so the caller never observes a half-applied revert
// (SEC-176). It is best-effort: if priorActiveID is empty (no prior head
// existed) or the reload itself fails, the caller's original error is
// returned wrapped with ErrRevertRestoreFailed so the live state's
// reverted-but-unrecovered condition is surfaced, never silent. BUG-485:
// this reload also passes save.WithExpectedWorldSeed(m.expectedWorldSeed)
// — priorActiveID names a bundle this same Manager wrote, so a mismatch
// here would itself indicate corruption/tampering worth surfacing rather
// than restoring silently.
func (m *Manager) recoverAfterLoad(priorActiveID ID, cause error) error {
	if priorActiveID == "" {
		return cause
	}
	if _, _, err := m.saveMgr.Load(checkpointDir(m.root, priorActiveID), save.WithExpectedWorldSeed(m.expectedWorldSeed)); err != nil {
		return errs.Wrap(ErrRevertRestoreFailed, m.correlationID, err, map[string]any{
			"priorHead":     string(priorActiveID),
			"restoreCause":  err.Error(),
			"originalCause": cause.Error(),
		})
	}
	return cause
}

// setHead advances the active-head pointer to id, preserving the fork
// sequence. It reads the current head, sets ActiveID, and writes the
// pointer atomically (writeHead).
func (m *Manager) setHead(id ID) error {
	head, err := readHead(m.root)
	if err != nil {
		return errs.Wrap(ErrHeadReadFailed, m.correlationID, err, map[string]any{"cause": err.Error()})
	}
	head.ActiveID = id
	if err := writeHead(m.root, head); err != nil {
		return errs.Wrap(ErrHeadWriteFailed, m.correlationID, err, map[string]any{"cause": err.Error()})
	}
	return nil
}
