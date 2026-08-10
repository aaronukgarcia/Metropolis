package keys

// ActionArgs carries what a dispatched Action receives: the count prefix
// (AC-5, default 1 when no digits were fed) and the completed mnemonic
// path that resolved to it (useful for an Action that wants to know its
// own identity, e.g. for logging).
type ActionArgs struct {
	Count int
	Path  []string
}

// Action is what a mnemonic path (or a global binding) resolves to. Run
// is invoked synchronously, on the caller's own Feed goroutine, exactly
// once per completed dispatch — it is this package's contract that Run
// is where a caller's own translation into a protocol.Command (or
// whatever downstream effect) happens; this package never inspects or
// constructs one itself (doc.go's "standing rule").
type Action struct {
	// Name is a short human-readable label — the palette (AC-9) renders
	// it beside the action's mnemonic path.
	Name string
	Run  func(ActionArgs)
}

// Continuation is one entry of what Continuations() returns after a
// prefix keystroke — the which-key HUD's data model (AC-3). Key is the
// next token's canonical Token() form; IsLeaf reports whether feeding Key
// next completes a registered action (vs. only extending the prefix
// further).
type Continuation struct {
	Key    string
	Path   []string
	IsLeaf bool
	Name   string // Action.Name, populated only when IsLeaf
}

// FeedStatus discriminates FeedResult — see doc comments below for what
// each status guarantees (AC-2, AC-2b, AC-2c, AC-2d).
type FeedStatus int

const (
	// Pending: the fed key extended a valid prefix; no action fired.
	// Continuations() now reflects the new pending state.
	Pending FeedStatus = iota
	// Dispatched: the fed key completed a registered mnemonic path; its
	// Action.Run was invoked exactly once, synchronously, before Feed
	// returned. Dispatch is populated.
	Dispatched
	// Aborted: an explicit Esc (AC-2c) or CheckIdleTimeout (AC-2d)
	// cancelled a pending sequence back to idle. No action fired.
	// Distinguishable from NoSuchSequence: Aborted means "the player (or
	// the clock) chose to back out," not "that combination doesn't
	// exist."
	Aborted
	// NoSuchSequence: the fed key does not extend the current pending
	// prefix to any registered continuation (AC-2b). The grammar resets
	// to idle; no action fired, and — critically — dispatch never falls
	// through to "the nearest registered prefix" or any other guess.
	NoSuchSequence
	// GlobalDispatched: a registered global fired (AC-10). Pending
	// leader-sequence state, if any, is left exactly as it was — a
	// global does not consume or reset an in-progress mnemonic prefix
	// (doc.go documents this as the chosen interruption rule).
	GlobalDispatched
	// NoOp: the fed key was deliberately inert — Esc at idle (a second
	// Esc with nothing pending, AC-2c's documented no-op), or '.'/'u'/'U'
	// when no last-action/undo/redo hook is available to invoke. Not an
	// error and not a sequence outcome; the caller should treat it as
	// "nothing happened, by design."
	NoOp
)

// Dispatch describes a completed dispatch (FeedResult.Dispatch, when
// Status is Dispatched or GlobalDispatched).
type Dispatch struct {
	Path   []string
	Count  int
	Action Action
}

// FeedResult is Feed's return value.
type FeedResult struct {
	Status   FeedStatus
	Dispatch *Dispatch
}
