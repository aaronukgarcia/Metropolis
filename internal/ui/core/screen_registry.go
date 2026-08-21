package core

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// ScreenID names one of the F-key-addressable screens (or a non-F-key
// surface a future navigation trigger — e.g. ui.dash's drill-through —
// can still jump to; FEAT-211 design §7(e)). A plain string type, not an
// int enum: every real ID today is a short, stable, human-legible
// mnemonic ("map", "finance", "services") a boot.go call site names
// literally, and a string compares/logs cleanly without a lookup table.
type ScreenID string

// ScreenEntry is what ScreenRegistry needs from one registered screen: a
// Draw closure (exactly DrawFunc's shape — mapDrawFunc, financeDrawFunc,
// servicesDrawFunc in cmd/metropolis/boot.go all close a real screen's
// own render call into this signature, the same pattern mapDrawFunc
// already established before this design) and an optional KeyGrammar to
// feed while this screen is active. Grammar may be nil — a screen with
// no registered actions of its own still switches to and draws
// correctly; input routing for it is chrome-globals-only (FEAT-211
// design §7(b)). Draw, by contrast, is REQUIRED — Register rejects a nil
// Draw with MET-U007 rather than letting a mis-wired screen render blank
// (GR#1).
type ScreenEntry struct {
	ID      ScreenID
	Draw    DrawFunc
	Grammar *keys.KeyGrammar
}

// noopDraw is what ActiveDraw returns before any screen has ever been
// registered or activated — a real, callable DrawFunc that touches
// nothing, so a caller (RenderLoop) never needs a nil check on the
// registry's own accessor return value, matching stubDraw's own
// "substitute a real no-op, never a nil func value" idiom (render.go).
func noopDraw(_ *Buffer, _ *ViewModels) {}

// ScreenRegistry is FEAT-211 increment 1's ActiveScreen state owner
// (design doc E:\git\metropolis-status\active-screen-design.md §7(a),
// living in ui.core for the same reason RenderLoop/InputLoop/ViewStore
// do: it is the one package every screen and the composition root
// already depend on, and it owns exactly the "single goroutine touches
// the screen" invariant that ActiveScreen switching must not violate).
//
// # Registration order (GR#21)
//
// entries is a slice, appended to in the exact order Register is called
// — never a map iteration — so any future audit/introspection (a
// screen-picker UI, an AllScreens()-style listing) is deterministic run
// over run, mirroring skeletonModuleKeys' own "never Go map iteration
// order" discipline (cmd/metropolis/boot.go). index is a ScreenID ->
// slice-position lookup ONLY — it is never ranged over for anything
// order-sensitive, so its use as a plain Go map does not reintroduce the
// property entries' own slice shape exists to avoid.
//
// # Concurrency
//
// Register is boot-time only, called before either goroutine below
// exists — no locking is required for correctness there, but Register
// still takes mu (cheap, and defensive against a future caller that
// registers a screen after boot). Activate is the one runtime mutation
// (FEAT-211 design §7(a): "called from the same goroutine InputLoop's
// OnDelivered runs on") and ActiveID/ActiveDraw/ActiveGrammar are read
// from RenderLoop's own goroutine every tick — two independent
// goroutines touching r.active concurrently, exactly the shape mu
// exists to serialize. Activate never touches a tcell.Screen (a pointer
// swap plus, at most, the caller's own TriggerRender() call afterward)
// — the pointer-swap property the <30ms budget (UI-SPEC §5, "pre-built
// layouts, cached data") is trivially met by (BUG-291/292's own "count
// work, not wall-clock" rule: this is structural, not benchmarked
// against a clock — see screen_registry_test.go's allocation-count
// proof).
//
// # Copy safety
//
// Mirrors this codebase's standard SEC-020-class idiom (RenderLoop.self,
// KeyGrammar.self, InProcTransport.self): self is an atomic.Pointer
// storing the address NewScreenRegistry returned, set exactly once. A
// struct-copied ScreenRegistry (`r2 := *r`) gets its own, independent mu
// and self — checkNotCopied rejects every exported method call on such a
// copy (MET-U006) before it touches entries/index/active, rather than
// letting two independent registries silently disagree about which
// screen is active.
type ScreenRegistry struct {
	mu sync.Mutex

	entries []ScreenEntry    // registration order (GR#21) — never ranged for order
	index   map[ScreenID]int // ScreenID -> entries[] position; lookup only, never ranged
	active  int              // index into entries, or -1 if nothing registered/activated yet

	correlationID string

	self atomic.Pointer[ScreenRegistry]
}

// NewScreenRegistry constructs a ready-to-use, empty ScreenRegistry.
// correlationID is used for every registry-sourced error this instance
// raises (GR#1).
func NewScreenRegistry(correlationID string) *ScreenRegistry {
	r := &ScreenRegistry{
		index:         make(map[ScreenID]int),
		active:        -1,
		correlationID: correlationID,
	}
	// Stored exactly once, here, before r is returned to any caller — no
	// goroutine can have a reference to r to race this Store against
	// (mirrors RenderLoop.self/KeyGrammar.self — see the struct's own
	// copy-safety doc comment).
	r.self.Store(r)
	return r
}

// checkNotCopied mirrors RenderLoop.checkNotCopied / KeyGrammar.checkNotCopied
// exactly — see ScreenRegistry's own doc comment for the shared
// rationale. Deliberately lock-free (a single atomic.Pointer.Load), so
// it is safe and correct to call BEFORE mu is ever touched.
func (r *ScreenRegistry) checkNotCopied(correlationID string, ctx map[string]any) error {
	if r.self.Load() != r {
		return errs.New(ErrScreenRegistryCopied, correlationID, ctx)
	}
	return nil
}

// Register adds e to the registry (boot-time only — see the struct's own
// concurrency doc comment). A duplicate ScreenID (e.ID already
// registered) is rejected (MET-U004) rather than silently overwritten —
// two boot.go call sites registering the same ID by mistake is a
// programming error that must surface loudly, not the second Register
// call silently winning. A nil e.Draw is rejected the same way
// (MET-U007, GR#1): every registered screen must have a real DrawFunc,
// because the alternative — accepting nil and letting ActiveDraw
// substitute noopDraw — turns a nil-adapter wiring mistake into a screen
// that switches, reports itself active, and renders an entirely blank
// terminal with no error anywhere. That is precisely the silent failure
// GR#1 exists to forbid, and it is the exact same class of boot-time
// programming error a duplicate ID already fails loudly on. (Grammar,
// by contrast, is legitimately optional — see ScreenEntry's own doc
// comment: nil there means "chrome globals only", a supported
// configuration, not a mistake.) The first screen ever registered becomes the
// initially active one (matching this binary's pre-FEAT-211 baseline
// behaviour, where mapScreen — the one screen registered first in every
// design increment-1 boot order — was always what rendered); a caller
// that wants a different initial screen calls Activate explicitly after
// registering everything.
func (r *ScreenRegistry) Register(e ScreenEntry) error {
	correlationID := errs.NewCorrelationID()
	if err := r.checkNotCopied(correlationID, map[string]any{"method": "Register"}); err != nil {
		return err
	}
	// Checked before mu is taken and before any duplicate-ID check: a nil
	// Draw is a property of the CALLER's argument alone, needing none of
	// this registry's state to adjudicate.
	if e.Draw == nil {
		return errs.New(ErrScreenNilDraw, r.correlationID, map[string]any{"id": string(e.ID)})
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.index[e.ID]; exists {
		return errs.New(ErrScreenAlreadyRegistered, r.correlationID, map[string]any{"id": string(e.ID)})
	}

	r.index[e.ID] = len(r.entries)
	r.entries = append(r.entries, e)
	if r.active < 0 {
		r.active = 0
	}
	return nil
}

// Activate switches the active screen to id — the single switch
// primitive (FEAT-211 design §7(a)/§7(d)): a pointer swap only. It does
// NOT touch any subscription, any transport state, or the tcell.Screen —
// router.BindSubscription already keeps every bound screen's delta
// stream live regardless of which one is active (proven from real boot
// wiring, design §4), so switching never re-subscribes, re-fetches, or
// reconstructs anything. Rejects an unregistered id (MET-U005) rather
// than silently no-op-ing or falling back to whatever was active before
// — a caller (e.g. an F-key global action) passing a typo'd or
// never-registered ScreenID is a programming error that must be visible,
// not swallowed.
//
// # Aborting the outgoing screen's pending grammar
//
// A switch that actually changes screens calls Abort() on the OUTGOING
// screen's KeyGrammar, if it registered one — keys.KeyGrammar.Abort's own
// doc comment names this exact caller ("a caller that wants to trigger it
// without synthesizing a key event (e.g. a screen switch)"). Without it a
// half-typed leader prefix survives the switch and, because no keystroke
// on the way back re-enters the trie from root, a single subsequent key
// silently COMPLETES the abandoned sequence and dispatches a real action
// the player never asked for on this visit (found by FEAT-211
// increment 1's independent destructive round: F4, "s", "f", F1, F4, "+"
// sent a live funding-adjust command to the engine).
//
// This lives here, in Activate, rather than in cmd/metropolis's F-key
// global actions, because Activate is by construction THE single switch
// primitive (design §7(a)/§7(d), and §7(e) explicitly earmarks it for a
// future non-F-key trigger — ui.dash's drill-through). Putting the abort
// in the F-key handler would leave every other present or future path
// into Activate — drill-through, a screen-picker UI, a scripted/demo
// switch, a test — reintroducing the identical defect, and nothing would
// catch it. Here it cannot be bypassed: there is no way to change the
// active screen that does not go through this function.
//
// Re-activating the ALREADY-active screen ALSO aborts (overturned
// 2026-08-21, r2's finding F1 on FEAT-211 increment 1's independent
// destructive round). The original carve-out reasoned that pressing F4
// while already on services is a harmless no-op the player may well hit
// mid-sequence, so cancelling their half-typed mnemonic for it would be a
// second, smaller surprise of the same kind — but r2 proved that reasoning
// wrong on two counts: an F-key is a navigation intent regardless of
// whether it lands on a different screen, and the carve-out made Activate
// internally inconsistent (F1->F4 cleared the pending prefix, F4->F4 did
// not), which is worse than either policy applied uniformly. Concretely:
// F4, "s", "f", F4, "+" fired a real funding-adjust command the player
// never asked for on that visit, from a keystroke ("+") that is not bound
// to anything on its own. A keystroke bound to nothing must never move the
// city's money, so the outgoing grammar is now captured whenever a screen
// is already active (r.active >= 0), with no exception for id == the
// current screen.
//
// Abort is called AFTER mu is released. It takes the grammar's own,
// unrelated mutex, so there is no lock-ordering hazard today, but holding
// two independent components' locks at once is exactly how one gets
// created later; and Abort runs no caller-supplied Action, so there is
// nothing re-entrant to serialize against here.
func (r *ScreenRegistry) Activate(id ScreenID) error {
	correlationID := errs.NewCorrelationID()
	if err := r.checkNotCopied(correlationID, map[string]any{"method": "Activate"}); err != nil {
		return err
	}

	var outgoing *keys.KeyGrammar
	r.mu.Lock()
	idx, ok := r.index[id]
	if !ok {
		r.mu.Unlock()
		return errs.New(ErrScreenUnknown, r.correlationID, map[string]any{"id": string(id)})
	}
	if r.active >= 0 {
		outgoing = r.entries[r.active].Grammar
	}
	r.active = idx
	r.mu.Unlock()

	if outgoing != nil {
		outgoing.Abort()
	}
	return nil
}

// ActiveID returns the currently active screen's ScreenID, or "" if
// nothing has ever been registered.
func (r *ScreenRegistry) ActiveID() ScreenID {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ActiveID"}); err != nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active < 0 {
		return ""
	}
	return r.entries[r.active].ID
}

// ActiveDraw returns the currently active screen's Draw closure —
// RenderLoop's own draw loop (render.go) never needs a nil check on this
// return value, the same "always a real, callable value" contract
// stubDraw already establishes for the below-minimum-terminal case.
//
// It returns the real no-op DrawFunc (noopDraw) in exactly two cases,
// both of which mean "there is no active screen to draw", never "the
// active screen has no Draw":
//
//   - nothing has ever been registered (active < 0); or
//   - this is a struct copy of the registry (MET-U006 — the copy-guard
//     rejects the call, and a caller that already holds a DrawFunc
//     variable must still get something callable back).
//
// A registered entry can no longer HAVE a nil Draw: Register rejects that
// at boot with MET-U007 (see its doc comment). The nil check below is
// therefore defence in depth against a future field-assignment path that
// bypasses Register, not a supported configuration — and it is
// deliberately not silent about being unreachable, because the whole
// point of MET-U007 is that a blank screen must never be the first
// symptom.
func (r *ScreenRegistry) ActiveDraw() DrawFunc {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ActiveDraw"}); err != nil {
		return noopDraw
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active < 0 {
		return noopDraw
	}
	d := r.entries[r.active].Draw
	if d == nil {
		return noopDraw
	}
	return d
}

// RegisteredIDs returns every registered ScreenID in REGISTRATION ORDER
// (GR#21 — a defensive copy of the slice entries was built from, never a
// map range), for a future screen-picker UI or AllActions()-style
// introspection (FEAT-211 design §7(a)). Not consulted by Activate/
// ActiveID/ActiveDraw/ActiveGrammar themselves — those resolve by ID
// through index — this exists purely for a caller that needs the whole
// registered set, in order.
func (r *ScreenRegistry) RegisteredIDs() []ScreenID {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "RegisteredIDs"}); err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ScreenID, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.ID
	}
	return out
}

// ActiveGrammar returns the currently active screen's KeyGrammar, or nil
// if nothing has ever been registered OR the active screen registered a
// nil Grammar (both legal — see ScreenEntry's own doc comment: nil means
// "chrome globals only" for that screen). Callers (run.go's input
// routing) must treat a nil return as "there is no screen-scoped grammar
// to feed this keystroke to," never as an error.
func (r *ScreenRegistry) ActiveGrammar() *keys.KeyGrammar {
	if err := r.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ActiveGrammar"}); err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active < 0 {
		return nil
	}
	return r.entries[r.active].Grammar
}
