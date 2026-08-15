package unlocks

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DebugGateFunc authorizes a debug-only operation. It returns nil when
// the operation is authorized, and a registry-sourced error otherwise.
// The composition root wires feat.debugmode's gate here (a closure over
// debug.State.IsOn, or debug.State.InvokeCheat's own requireOn); a nil
// DebugGateFunc means "debug off" and every debug-gated call is denied
// (AC-11).
type DebugGateFunc func(correlationID string) error

// nodeInfo is one tree node's resolved record, indexed by node id at Load
// time. Immutable after Load, so safe for lock-free concurrent reads.
type nodeInfo struct {
	ID         string
	Name       string
	Kind       string // "unlock" or "none"
	Tier       int    // §4 tier this node belongs to (1..13)
	PrereqTier int    // milestone tier required to spend this node (1..13)
	DPCost     int    // Development-Point cost to unlock (kind "unlock")
	TreeName   string // the category display name this node lives under
}

// UnlocksAPI is code.json's "engine.unlocks" inbound contract
// (UnlocksAPI, "gate checks data-driven from unlock_trees.json
// (GR#15)"). It owns the XP counter, the current milestone tier
// (higher-water-mark — never downgraded), the Development-Point balance
// and spent-node set, the expansion-permit allowance, and the off-map
// capacity tranches purchased via the Buy path.
//
// The zero value is not usable; construct via [Load] or [LoadDefault].
// A *UnlocksAPI is safe for concurrent use (AC-16): every mutable field
// is guarded by mu, the node/category indexes are immutable after Load,
// and checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class).
type UnlocksAPI struct {
	mu            sync.RWMutex
	correlationID string

	// Load-time immutable state (no lock needed after construction).
	categories []string            // the twelve category display names, in file order
	nodes      map[string]nodeInfo // node id -> record

	// Runtime state. tier is an atomic.Int32 rather than a plain int so
	// MilestoneReached can read it lock-free: engine.finance.Borrow calls
	// the injected MilestoneGate while HOLDING finance's own lock, so a
	// MilestoneReached that took u.mu here would create a finance.mu ->
	// unlocks.mu edge that deadlocks against unlocks.mu -> finance.mu (the
	// cash-award / Buy post path) — SEC-083. Everything else stays under
	// mu.
	xp               int64
	population       int64
	tier             atomic.Int32 // current milestone tier (0 = none)
	dp               int64        // unspent Development-Point balance
	dpSpent          int64        // total DP ever spent (the "any DP" gate)
	unlockedNodes    map[string]bool
	expansionPermits int64
	capacity         map[OffMapKind]int64
	debugTouched     bool

	// Injected dependencies (wired via the Set* methods).
	finance    *finance.FinanceAPI
	debugGate  DebugGateFunc
	debugTouch func() error

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller.
	self atomic.Pointer[UnlocksAPI]
}

// Load reads and validates data/unlock_trees.json from dir (via
// foundation/data.LoadUnlockTrees — the validated-load path AC-13
// requires) and builds a ready-to-query *UnlocksAPI. correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every load failure is a registry-sourced *errs.E —
// never a silent default substitution, never a panic.
func Load(dir, correlationID string) (*UnlocksAPI, error) {
	trees, err := data.LoadUnlockTrees(dir, correlationID)
	if err != nil {
		// foundation/data.LoadUnlockTrees already returns a
		// registry-sourced *errs.E (MET-F6xx) for a missing file,
		// malformed JSON, or any schema/cycle violation. Re-wrap it under
		// this package's own ErrDataInvalid so every load failure callers
		// see carries one consistent engine.unlocks code, matching
		// engine.market/engine.season's Load wrap. MET-G900's registered
		// template has a "{cause}" placeholder (BUG-099) — populate it
		// from the wrapped error's own text.
		return nil, errs.Wrap(ErrDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	u := &UnlocksAPI{
		correlationID: correlationID,
		categories:    make([]string, 0, len(trees.Trees)),
		nodes:         make(map[string]nodeInfo),
		unlockedNodes: make(map[string]bool),
		capacity:      make(map[OffMapKind]int64),
	}

	// Build the immutable node/category indexes in file order (JSON
	// arrays decode preserving order, so this is deterministic — GR#21).
	for _, tree := range trees.Trees {
		u.categories = append(u.categories, tree.Name)
		for _, n := range tree.Nodes {
			u.nodes[n.ID] = nodeInfo{
				ID:         n.ID,
				Name:       n.Name,
				Kind:       n.Kind,
				Tier:       n.Tier,
				PrereqTier: n.PrereqTier,
				DPCost:     n.DPCost,
				TreeName:   tree.Name,
			}
		}
	}

	// Stored exactly once, before u is returned to any caller (SEC-016 —
	// no goroutine can have a reference to race this Store against).
	u.self.Store(u)
	return u, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*UnlocksAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *UnlocksAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load, safe to run
// before mu is ever touched.
func (u *UnlocksAPI) checkNotCopied(method string) error {
	if u.self.Load() != u {
		return errs.New(ErrCopiedValue, u.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetFinance wires the engine.finance dependency used by milestone cash
// awards and the Buy path (US-7, AC-4, AC-9). Mirrors engine.attract's
// SetFinance wiring convention. A nil finance leaves those operations
// failing with ErrFinanceNotWired rather than silently no-op'ing
// (GR#17).
func (u *UnlocksAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := u.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.finance = f
	return nil
}

// SetDebugGate wires the debug authorizer consulted by ForceUnlock. A nil
// gate means "debug off" and every ForceUnlock is denied (AC-11).
func (u *UnlocksAPI) SetDebugGate(gate DebugGateFunc) error {
	if err := u.checkNotCopied("SetDebugGate"); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.debugGate = gate
	return nil
}

// SetDebugTouch wires the sticky-flag callback ForceUnlock invokes on
// success. The composition root wires this to feat.debugmode's
// serialize.Header.DebugTouched write (M0-ENG §3). A nil callback means
// the unlock still succeeds but no debug-touched flag is written — the
// composition root always wires one in a real build.
func (u *UnlocksAPI) SetDebugTouch(mark func() error) error {
	if err := u.checkNotCopied("SetDebugTouch"); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.debugTouch = mark
	return nil
}

// XP returns the total XP accrued from all four per-source award
// functions.
func (u *UnlocksAPI) XP() int64 {
	if err := u.checkNotCopied("XP"); err != nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.xp
}

// CurrentPopulation returns the last population value passed to
// AdvancePopulation (0 before the first call).
func (u *UnlocksAPI) CurrentPopulation() int64 {
	if err := u.checkNotCopied("CurrentPopulation"); err != nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.population
}

// CurrentTier returns the current (higher-water-mark) milestone tier:
// 0 before any milestone has been crossed, then 1..13.
func (u *UnlocksAPI) CurrentTier() int {
	if err := u.checkNotCopied("CurrentTier"); err != nil {
		return 0
	}
	return int(u.tier.Load())
}

// DevelopmentPoints returns the current unspent Development-Point
// balance.
func (u *UnlocksAPI) DevelopmentPoints() int64 {
	if err := u.checkNotCopied("DevelopmentPoints"); err != nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.dp
}

// ExpansionPermits returns the accumulated expansion-permit allowance.
func (u *UnlocksAPI) ExpansionPermits() int64 {
	if err := u.checkNotCopied("ExpansionPermits"); err != nil {
		return 0
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.expansionPermits
}

// DebugTouched reports whether a debug force-unlock has been applied on
// this API. The sticky-forever durability lives in the composition
// root's debugTouch callback (serialize.Header.DebugTouched); this flag
// mirrors it for queryability (AC-11).
func (u *UnlocksAPI) DebugTouched() bool {
	if err := u.checkNotCopied("DebugTouched"); err != nil {
		return false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.debugTouched
}

// Categories returns a defensive copy of the twelve category display
// names, in data/unlock_trees.json order (AC-6). The returned slice is
// owned by the caller — never an alias of the internal index (weakness
// pattern #1).
func (u *UnlocksAPI) Categories() []string {
	if err := u.checkNotCopied("Categories"); err != nil {
		return nil
	}
	out := make([]string, len(u.categories))
	copy(out, u.categories)
	return out
}
