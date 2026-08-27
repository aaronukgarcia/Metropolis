package keys

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DefaultIdleTimeout is NewKeyGrammar's default leader-sequence inactivity
// timeout (AC-2d, ASM-117) when the caller passes 0. UI-SPEC §3/§6 name no
// mandated duration; this is an implementer default in the same spirit as
// DefaultDimAfterUses below (GR#15: documented, not silently hardcoded).
// Logged as ASM-117 in the BOW (see this item's dispatch report).
const DefaultIdleTimeout = 2 * time.Second

// DefaultDimAfterUses is NewKeyGrammar's default which-key HUD dim
// threshold (AC-4) when the caller passes 0 — after this many completed
// dispatches of the SAME mnemonic path, ShouldDimHUD(path) starts
// returning true. Implementer default (GR#15), same status as
// DefaultIdleTimeout above.
const DefaultDimAfterUses = 5

// reservedTokens are the built-in tokens Feed intercepts unconditionally,
// before any registered path or global binding is consulted (see Feed's
// doc comment). Register and RegisterGlobal both reject these tokens at
// the top level (MET-U306) so a registered action can never be silently
// unreachable dead code.
var reservedTokens = map[string]bool{
	"<Esc>": true,
	".":     true,
	"u":     true,
	"U":     true,
}

// trieNode is one node of the mnemonic-path trie Register builds and Feed
// walks. A node is terminal (action != nil) XOR has children — see
// Register's doc comment for why that invariant is enforced structurally
// (AC-14b) rather than merely documented.
type trieNode struct {
	children map[string]*trieNode
	action   *registeredAction
}

func newTrieNode() *trieNode { return &trieNode{children: map[string]*trieNode{}} }

type registeredAction struct {
	path   []string
	action Action
}

// KeyGrammar is ui.keys' leader-key state machine (AC-1): every action is
// registered with a mnemonic path (Register), fed keys advance a pending
// prefix (Feed), and the which-key HUD's data is read back via
// Continuations. The zero value is NOT ready for use (SEC-020-class);
// construct with NewKeyGrammar.
type KeyGrammar struct {
	mu sync.Mutex

	root         *trieNode
	node         *trieNode // current traversal position; root when idle
	pendingPath  []string  // tokens fed so far in the current sequence
	pendingSince time.Time
	countDigits  string

	globals map[string]Action

	usage        map[string]int // joined path -> completed-dispatch count
	lastDispatch *Dispatch

	undoFn func() bool
	redoFn func() bool

	marks map[string]any

	searchMatches []string
	searchPos     int

	keymap *Keymap

	clock       Clock
	idleTimeout time.Duration
	dimAfter    int

	correlationID string

	// self mirrors this codebase's standard SEC-020 copy-safety pattern
	// (internal/harness/uitest.Harness, internal/protocol.InProcTransport,
	// internal/ui/screens/*.Screen): an atomic.Pointer identity check runs
	// before mu is ever touched, so a struct-copied KeyGrammar fails
	// closed (MET-U308) instead of racing the original over the same
	// trie/maps via an independent, unlinked mutex.
	self atomic.Pointer[KeyGrammar]
}

// NewKeyGrammar constructs a ready-to-use KeyGrammar. clock may be nil
// (defaults to the real wall clock, AC-16's one sanctioned time.Now
// site); idleTimeout <= 0 uses DefaultIdleTimeout; dimAfterUses <= 0 uses
// DefaultDimAfterUses.
func NewKeyGrammar(clock Clock, idleTimeout time.Duration, dimAfterUses int, correlationID string) *KeyGrammar {
	if clock == nil {
		clock = systemClock
	}
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	if dimAfterUses <= 0 {
		dimAfterUses = DefaultDimAfterUses
	}
	root := newTrieNode()
	g := &KeyGrammar{
		root:          root,
		node:          root,
		globals:       map[string]Action{},
		usage:         map[string]int{},
		marks:         map[string]any{},
		clock:         clock,
		idleTimeout:   idleTimeout,
		dimAfter:      dimAfterUses,
		correlationID: correlationID,
	}
	// Stored exactly once, here, before g is returned to any caller — no
	// goroutine can have a reference to g to race this Store against
	// (mirrors NewHarness/NewInProcTransport — see self's doc comment).
	g.self.Store(g)
	return g
}

// checkNotCopied mirrors Harness.checkNotCopied / InProcTransport's
// identity guard exactly (see KeyGrammar.self's doc comment).
func (g *KeyGrammar) checkNotCopied() error {
	if g.self.Load() != g {
		return errs.New(codeGrammarCopied, g.correlationID, map[string]any{
			"cause": "struct copy detected",
		})
	}
	return nil
}

func joinPath(path []string) string { return strings.Join(path, "\x1f") }

// Register adds action at mnemonic path (AC-1). path must be non-empty
// and its first token must not be a reserved built-in (Esc/./u/U,
// MET-U306). Two structural conflicts are rejected here, at Register
// time, rather than left for Feed to guess about at runtime:
//
//   - AC-14: path is already registered (exact duplicate) -> MET-U300.
//   - AC-14b (ASM-118): path is a strict prefix of an already-registered
//     longer path, OR an already-registered shorter path is a strict
//     prefix of path -> MET-U301, naming both conflicting paths. Without
//     this rule Feed could not tell, on completing the shorter path,
//     whether to dispatch immediately or wait for a possible longer
//     continuation — UI-SPEC §3 names no such tiebreak, so the ambiguity
//     is avoided structurally instead of resolved arbitrarily.
func (g *KeyGrammar) Register(path []string, action Action) error {
	if err := g.checkNotCopied(); err != nil {
		return err
	}
	if len(path) == 0 {
		return errs.New(codeRegisterPrefixConflict, g.correlationID, map[string]any{
			"path": path, "cause": "empty mnemonic path", "conflictsWith": nil,
		})
	}
	if reservedTokens[path[0]] {
		return errs.New(codeReservedToken, g.correlationID, map[string]any{"token": path[0], "path": path})
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	cur := g.root
	for _, tok := range path {
		if cur.action != nil {
			return errs.New(codeRegisterPrefixConflict, g.correlationID, map[string]any{
				"path": path, "conflictsWith": cur.action.path,
				"cause": "an already-registered shorter path is a prefix of this one",
			})
		}
		child, ok := cur.children[tok]
		if !ok {
			child = newTrieNode()
			cur.children[tok] = child
		}
		cur = child
	}

	if cur.action != nil {
		return errs.New(codeRegisterDuplicate, g.correlationID, map[string]any{"path": path})
	}
	if len(cur.children) > 0 {
		example := make([]string, 0, 1)
		for tok := range cur.children {
			example = append(example, tok)
			break
		}
		return errs.New(codeRegisterPrefixConflict, g.correlationID, map[string]any{
			"path": path, "conflictsWith": append(append([]string{}, path...), example...),
			"cause": "this path is a prefix of an already-registered longer path",
		})
	}
	cur.action = &registeredAction{path: append([]string{}, path...), action: action}
	return nil
}

// RegisterGlobal registers action under a single key, resolved and fired
// independently of the leader-sequence tree (AC-10): a global fires even
// mid-sequence, without disturbing whatever leader prefix is currently
// pending. k's token must not be a reserved built-in (MET-U306); a
// duplicate global token is rejected the same way Register rejects a
// duplicate mnemonic path (MET-U300).
func (g *KeyGrammar) RegisterGlobal(k Key, action Action) error {
	if err := g.checkNotCopied(); err != nil {
		return err
	}
	tok := k.Token()
	if reservedTokens[tok] {
		return errs.New(codeReservedToken, g.correlationID, map[string]any{"token": tok})
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.globals[tok]; exists {
		return errs.New(codeRegisterDuplicate, g.correlationID, map[string]any{"path": []string{tok}, "cause": "global already registered"})
	}
	g.globals[tok] = action
	return nil
}

// RegisterUndo/RegisterRedo wire the hooks 'u'/'U' invoke (AC-6). The
// grammar calls through to these; it implements no undo semantics itself
// — "where the engine permits" is the caller's decision, expressed by
// whether it registers a hook at all and what that hook's bool return
// means (true: something was undone/redone; false: nothing to do).
func (g *KeyGrammar) RegisterUndo(fn func() bool) error {
	if err := g.checkNotCopied(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.undoFn = fn
	return nil
}

func (g *KeyGrammar) RegisterRedo(fn func() bool) error {
	if err := g.checkNotCopied(); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.redoFn = fn
	return nil
}

// pendingAction is what Feed computed under lock but must run AFTER
// releasing it — never call a caller-supplied Action.Run (or undo/redo
// hook) while holding mu, so a re-entrant call back into this KeyGrammar
// from inside that closure (e.g. Continuations() for logging) cannot
// deadlock against itself.
type pendingAction struct {
	run    func() bool // returns whether anything actually happened (for undo/redo)
	result FeedResult
}

// Feed advances the state machine by one key (AC-1, AC-2). Interception
// order, all unconditional (checked before the leader tree or global
// bindings, and BEFORE any keymap substitution is even consulted for
// these four literal tokens — see doc.go's keymap section):
//
//  1. Esc: aborts a pending sequence (AC-2c) or is a no-op at idle.
//  2. '.': repeats the last dispatched action (AC-6).
//  3. 'u' / 'U': invoke the registered undo/redo hook, if any (AC-6).
//
// After that: keymap substitution (if a profile is loaded, AC-11)
// resolves k's physical token to a mnemonic PATH. A multi-segment
// resolution (a remapped shortcut key, e.g. "ctrl+p" -> "b r s") is a
// direct one-keystroke dispatch of that already-validated action, from
// root, without disturbing whatever leader prefix is currently pending —
// same non-disturbance contract as a global. A one-segment resolution
// (the common case, including "no keymap loaded" identity) proceeds as
// before: a registered global (AC-10) fires regardless of pending state,
// without disturbing it; otherwise a digit at idle accumulates into the
// count prefix (AC-5); otherwise the token either extends the pending
// prefix (Pending), completes a registered path (Dispatched, Action.Run
// invoked exactly once), or matches nothing (NoSuchSequence, AC-2b) —
// which resets to idle without ever falling through to the nearest
// registered prefix.
func (g *KeyGrammar) Feed(k Key) FeedResult {
	if err := g.checkNotCopied(); err != nil {
		return FeedResult{Status: NoOp}
	}
	pending := g.computeFeed(k)
	if pending.run != nil {
		pending.run()
	}
	return pending.result
}

// FeedTcellEvent is Feed's real-terminal entry point (AC-20): converts a
// genuine *tcell.EventKey via FromTcellEvent and feeds it. This — not any
// site in cmd/metropolis or internal/ui/core — is the ONE place a raw
// tcell key event becomes a dispatch decision (AC-21).
func (g *KeyGrammar) FeedTcellEvent(ev *tcell.EventKey) FeedResult {
	return g.Feed(FromTcellEvent(ev))
}

func (g *KeyGrammar) computeFeed(k Key) pendingAction {
	g.mu.Lock()

	tok := k.Token()

	// 1. Esc — unconditional abort/no-op.
	if tok == "<Esc>" {
		wasPending := len(g.pendingPath) > 0 || g.countDigits != ""
		g.resetLocked()
		g.mu.Unlock()
		if wasPending {
			return pendingAction{result: FeedResult{Status: Aborted}}
		}
		return pendingAction{result: FeedResult{Status: NoOp}}
	}

	// 2. '.' — repeat last dispatch.
	if tok == "." {
		last := g.lastDispatch
		g.mu.Unlock()
		if last == nil {
			return pendingAction{result: FeedResult{Status: NoOp}}
		}
		d := *last
		return pendingAction{
			result: FeedResult{Status: Dispatched, Dispatch: &d},
			run:    func() bool { d.Action.Run(ActionArgs{Count: d.Count, Path: d.Path}); return true },
		}
	}

	// 3. 'u' / 'U' — undo/redo hooks.
	if tok == "u" || tok == "U" {
		fn := g.undoFn
		if tok == "U" {
			fn = g.redoFn
		}
		g.mu.Unlock()
		if fn == nil {
			return pendingAction{result: FeedResult{Status: NoOp}}
		}
		var ranOK bool
		return pendingAction{
			run: func() bool { ranOK = fn(); return ranOK },
			result: FeedResult{Status: Dispatched, Dispatch: &Dispatch{
				Path: []string{tok}, Count: 1, Action: Action{Name: undoRedoName(tok)},
			}},
		}
	}

	// Keymap substitution (AC-11): only applied past this point — Esc/./
	// u/U are matched on the PHYSICAL token, never remappable, so a
	// hostile or malformed profile can never hijack the abort/repeat/
	// undo/redo keys (see doc.go's keymap section). A keymap binding
	// resolves to a full mnemonic PATH (Bill's ruling on ASM-165, AC-11b):
	// a one-segment resolution is the common case and behaves exactly
	// like the un-keymapped path below (step 4 onward, single token); a
	// multi-segment resolution is a dedicated shortcut key for one
	// specific, already-validated (ApplyKeymap) action, dispatched
	// directly from root without disturbing whatever leader prefix is
	// currently pending — same non-disturbance contract as a global.
	resolvedPath := []string{tok}
	if g.keymap != nil {
		if mapped, ok := g.keymap.resolve(tok); ok {
			resolvedPath = mapped
		}
	}

	if len(resolvedPath) > 1 {
		reg, found := g.resolveActionLocked(resolvedPath)
		if !found {
			// Defensive only: ApplyKeymap validates every loaded binding's
			// target path resolves to a real action before installing it,
			// so this should be unreachable in practice. If it somehow
			// isn't (e.g. the registry changed shape after the keymap was
			// applied), fail the same way an ordinary unmatched sequence
			// does — never guess, never partially apply.
			g.mu.Unlock()
			return pendingAction{result: FeedResult{Status: NoSuchSequence}}
		}
		count := g.consumeCountLocked()
		path := append([]string{}, reg.path...)
		g.usage[joinPath(path)]++
		dispatch := &Dispatch{Path: path, Count: count, Action: reg.action}
		g.lastDispatch = dispatch
		g.mu.Unlock()
		return pendingAction{
			run:    func() bool { reg.action.Run(ActionArgs{Count: count, Path: path}); return true },
			result: FeedResult{Status: Dispatched, Dispatch: dispatch},
		}
	}
	resolved := resolvedPath[0]

	// 4. Registered global — fires regardless of pending state, does not
	// consume or reset it.
	if action, ok := g.globals[resolved]; ok {
		g.mu.Unlock()
		return pendingAction{
			run: func() bool { action.Run(ActionArgs{Count: 1, Path: []string{resolved}}); return true },
			result: FeedResult{Status: GlobalDispatched, Dispatch: &Dispatch{
				Path: []string{resolved}, Count: 1, Action: action,
			}},
		}
	}

	// 5. Digit count prefix — only while fully idle (AC-5).
	if len(g.pendingPath) == 0 && k.IsDigit() {
		g.countDigits += string(k.Rune)
		g.pendingSince = g.clock.Now()
		g.mu.Unlock()
		return pendingAction{result: FeedResult{Status: Pending}}
	}

	// 6. Leader-tree traversal.
	child, ok := g.node.children[resolved]
	if !ok {
		g.resetLocked()
		g.mu.Unlock()
		return pendingAction{result: FeedResult{Status: NoSuchSequence}}
	}
	g.pendingPath = append(g.pendingPath, resolved)
	g.node = child
	g.pendingSince = g.clock.Now()

	if child.action == nil {
		g.mu.Unlock()
		return pendingAction{result: FeedResult{Status: Pending}}
	}

	count := g.consumeCountLocked()
	reg := child.action
	path := append([]string{}, reg.path...)
	g.usage[joinPath(path)]++
	dispatch := &Dispatch{Path: path, Count: count, Action: reg.action}
	g.lastDispatch = dispatch
	g.resetLocked()
	g.mu.Unlock()

	return pendingAction{
		run:    func() bool { reg.action.Run(ActionArgs{Count: count, Path: path}); return true },
		result: FeedResult{Status: Dispatched, Dispatch: dispatch},
	}
}

func undoRedoName(tok string) string {
	if tok == "U" {
		return "redo"
	}
	return "undo"
}

// consumeCountLocked parses and clears the accumulated digit-count
// prefix, defaulting to 1 (AC-5). Caller must hold mu.
func (g *KeyGrammar) consumeCountLocked() int {
	if g.countDigits == "" {
		return 1
	}
	n := 0
	for _, r := range g.countDigits {
		n = n*10 + int(r-'0')
	}
	g.countDigits = ""
	if n == 0 {
		return 1
	}
	return n
}

// resetLocked returns the state machine to idle: no pending prefix, no
// accumulated count, traversal position back at root. Caller must hold
// mu.
func (g *KeyGrammar) resetLocked() {
	g.pendingPath = nil
	g.countDigits = ""
	g.node = g.root
}

// Abort programmatically cancels a pending sequence exactly as an
// explicit Esc would (AC-2c), for a caller that wants to trigger it
// without synthesizing a key event (e.g. a screen switch). Returns
// whether anything was actually pending to abort.
func (g *KeyGrammar) Abort() bool {
	if err := g.checkNotCopied(); err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	wasPending := len(g.pendingPath) > 0 || g.countDigits != ""
	g.resetLocked()
	return wasPending
}

// CheckIdleTimeout polls whether a pending leader sequence has exceeded
// its inactivity timeout (AC-2d, ASM-117) using the grammar's injected
// Clock — never a blocking sleep or its own timer goroutine (T-INPUT's
// non-blocking contract). A caller drives this from whatever tick it
// already has (a render loop, a poll interval); a test drives it by
// advancing a fake Clock and calling this directly. Returns true if a
// pending sequence was aborted.
func (g *KeyGrammar) CheckIdleTimeout() bool {
	if err := g.checkNotCopied(); err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	pending := len(g.pendingPath) > 0 || g.countDigits != ""
	if !pending {
		return false
	}
	if g.clock.Now().Sub(g.pendingSince) < g.idleTimeout {
		return false
	}
	g.resetLocked()
	return true
}

// Continuations returns the valid next tokens from the current pending
// state (root, if idle), for the which-key HUD (AC-3). Populated
// synchronously — no async delay — and returned in a stable, sorted
// order (AC-15: determinism, no map-iteration-order leakage).
func (g *KeyGrammar) Continuations() []Continuation {
	if err := g.checkNotCopied(); err != nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	toks := make([]string, 0, len(g.node.children))
	for tok := range g.node.children {
		toks = append(toks, tok)
	}
	sort.Strings(toks)

	out := make([]Continuation, 0, len(toks))
	for _, tok := range toks {
		child := g.node.children[tok]
		c := Continuation{
			Key:    tok,
			Path:   append(append([]string{}, g.pendingPath...), tok),
			IsLeaf: child.action != nil,
		}
		if child.action != nil {
			c.Name = child.action.action.Name
		}
		out = append(out, c)
	}
	return out
}

// ShouldDimHUD reports whether path has been dispatched at least
// dimAfterUses times (NewKeyGrammar's constructor parameter, AC-4) — the
// which-key HUD's cue to stop showing an entry the player has clearly
// learned.
func (g *KeyGrammar) ShouldDimHUD(path []string) bool {
	if err := g.checkNotCopied(); err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usage[joinPath(path)] >= g.dimAfter
}

// AllActions returns every registered (path, action) leaf, sorted by
// joined path for deterministic ordering (AC-15) — the palette's listing
// source (AC-9) and useful for tests/diagnostics generally.
func (g *KeyGrammar) AllActions() []Dispatch {
	if err := g.checkNotCopied(); err != nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Dispatch
	var walk func(n *trieNode)
	walk = func(n *trieNode) {
		if n.action != nil {
			out = append(out, Dispatch{Path: append([]string{}, n.action.path...), Count: 1, Action: n.action.action})
		}
		toks := make([]string, 0, len(n.children))
		for tok := range n.children {
			toks = append(toks, tok)
		}
		sort.Strings(toks)
		for _, tok := range toks {
			walk(n.children[tok])
		}
	}
	walk(g.root)
	sort.Slice(out, func(i, j int) bool { return joinPath(out[i].Path) < joinPath(out[j].Path) })
	return out
}

// IsPending reports whether a leader sequence or count prefix is
// currently in progress.
func (g *KeyGrammar) IsPending() bool {
	if err := g.checkNotCopied(); err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pendingPath) > 0 || g.countDigits != ""
}
