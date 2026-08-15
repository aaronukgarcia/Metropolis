package menu

import (
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// SendCommandFunc issues one protocol.Command toward the engine. Screen
// never holds a protocol.Transport itself — mirrors internal/ui/screens/
// map's and demo's SendCommandFunc convention exactly (SF-1/SF-4): the
// caller owns the transport and the CorrelationID-to-SubscriptionID
// bookkeeping, and hands the resulting SubscriptionID back to this screen
// via BindSubscription.
type SendCommandFunc func(protocol.Command) error

// BundleLister enumerates the save bundle directory paths to browse,
// given the configured save root (SF-1's serializer seam — see doc.go's
// "save-root enumeration seam" note and saves.go). It returns paths only;
// the header read is this package's own call into serialize.
type BundleLister func(root string) ([]string, error)

// LayoutEditorFunc is the F10 → layouts entry point into ui.dash's
// (MOD-038) layout editor (MEN-4). This screen hosts the entry point; the
// editor's mechanics are MOD-038's. A nil-wired editor (the default)
// makes OpenLayoutEditor report "unavailable" (MEN-7).
type LayoutEditorFunc func(profile *LayoutProfile) error

// Screen is F10, the Menu & saves screen: see doc.go for the full package
// contract. The zero Screen is not ready to use; construct with New.
//
// Concurrency: every exported method locks mu, so ApplyDelta (called from
// the delta-applying goroutine) and the render/accessor methods (called
// from the render goroutine) may run concurrently (SF-9).
//
// Copy safety (SEC-020): mu is a sync.Mutex VALUE — a struct copy `s2 :=
// *s` gets its own, independent lock — while subs/stale/saveEntries/
// settings/selectedKeymap/saveListErrors are reference types a copy
// ALIASES. self plus checkNotCopied (copyguard.go) reject every exported
// method call made on such a copy before mu is ever touched, mirroring
// MapScreen.self/debug.Screen.self/demo.Screen.self exactly (GR#3).
type Screen struct {
	mu sync.Mutex

	// self holds the address New gave this Screen at construction
	// (self.Store(s), set once, at the end of New, never stored to again).
	// See copyguard.go's checkNotCopied for the full rationale.
	self atomic.Pointer[Screen]

	correlationID string

	// subs maps a bound SubscriptionID to the view name it was bound to
	// (BindSubscription) — the lookup ApplyDelta uses to route (and to
	// reject an unknown/stale SubscriptionID per SF-7).
	subs map[protocol.SubscriptionID]string

	// stale mirrors ui.core's per-subscription staleness flag, keyed by
	// view name (SetStale).
	stale map[string]bool

	// haveSession/session is the f10.session view's last-known-good state
	// (SF-7: a malformed patch leaves it untouched).
	haveSession bool
	session     Session

	// saveRoot is the directory the save browser lists bundles under
	// (WithSaveRoot).
	saveRoot string
	// bundleLister enumerates bundle directories (WithBundleLister); nil
	// means defaultBundleLister.
	bundleLister BundleLister

	// saveEntries/saveListErrs are the last Refresh() result. saveListErrs
	// are the per-entry header-read failures (one bad slot must not hide
	// the rest — see saves.go), not a fatal listing failure. listFailed is
	// non-empty when enumeration itself failed (render "unavailable").
	saveEntries  []SaveEntry
	saveListErrs []error
	listFailed   string

	// settings/settingValues are the data-driven settings panel state
	// (MEN-2). haveSettings is false until a schema is SetSettingsSchema.
	haveSettings  bool
	settings      []SettingSpec
	settingValues map[string]string

	// selectedKeymap is the keymap profile last loaded/selected (MEN-3);
	// nil until a load/select succeeds.
	selectedKeymap *keys.Keymap

	// layoutEditor is the ui.dash entry point (MEN-4); nil = unavailable.
	layoutEditor LayoutEditorFunc
	// selectedLayout is the last layout profile loaded/selected.
	selectedLayout *LayoutProfile

	// newGameReq is the new-game form's last submitted request (MEN-5).
	haveNewGame bool
	newGameReq  NewGameRequest
}

// Option customizes a Screen at construction time.
type Option func(*Screen)

// WithSaveRoot sets the directory the save browser lists bundles under.
// Unset (the default) means the save browser renders "unavailable"
// (MEN-7) rather than listing an empty or wrong directory.
func WithSaveRoot(root string) Option {
	return func(s *Screen) { s.saveRoot = root }
}

// WithBundleLister overrides the bundle-directory enumeration (defaults to
// defaultBundleLister — see doc.go's "save-root enumeration seam" note).
func WithBundleLister(fn BundleLister) Option {
	return func(s *Screen) { s.bundleLister = fn }
}

// WithLayoutEditor wires the F10 → layouts entry point to ui.dash's
// (MOD-038) layout editor (MEN-4). Unset (the default) makes
// OpenLayoutEditor report "unavailable" (MEN-7).
func WithLayoutEditor(fn LayoutEditorFunc) Option {
	return func(s *Screen) { s.layoutEditor = fn }
}

// New constructs an empty Screen (no data applied yet). correlationID is
// used for this screen's own registry-sourced log entries (malformed
// patches, unknown subscriptions — GR#1) and as the CorrelationID on every
// Subscribe command Subscribe sends; pass errs.NewCorrelationID() if the
// caller has no more specific ID to thread through.
func New(correlationID string, opts ...Option) *Screen {
	s := &Screen{
		correlationID: correlationID,
		subs:          make(map[protocol.SubscriptionID]string),
		stale:         make(map[string]bool),
		settingValues: make(map[string]string),
	}
	for _, opt := range opts {
		opt(s)
	}
	// Stored exactly once, here, before s is returned to any caller — no
	// goroutine can have a reference to s to race this Store against
	// (SEC-016; see copyguard.go's self doc comment).
	s.self.Store(s)
	return s
}

// Subscribe sends a Subscribe command for view via send (SF-1). view must
// be ViewSession — any other value is rejected with MET-U603 and no
// command is sent.
func (s *Screen) Subscribe(view string, send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	if !knownViews[view] {
		return errs.New(ErrUnrecognisedView, s.correlationID, map[string]any{"view": view})
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(s.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: view},
	}
	return send(cmd)
}

// SubscribeSession issues Subscribe for this screen's one view (ViewSession).
func (s *Screen) SubscribeSession(send SendCommandFunc) error {
	// SEC-020: checked here too, not only inside the Subscribe call it
	// makes — an entry point exported directly to callers (a copy could be
	// called on SubscribeSession itself) needs its own guard, mirrored from
	// demo.Screen.SubscribeAll's identical reasoning.
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SubscribeSession"}); err != nil {
		return err
	}
	return s.Subscribe(ViewSession, send)
}

// BindSubscription records that id (the SubscriptionID the engine allocated
// in response to a prior Subscribe(view, ...) call) belongs to view.
// ApplyDelta uses this binding to route/validate incoming Deltas. Rebinding
// an id to a different view simply overwrites the previous binding.
func (s *Screen) BindSubscription(view string, id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "BindSubscription"}); err != nil {
		return
	}
	s.subs[id] = view
}

// UnbindSubscription forgets id (e.g. after Unsubscribe) so a
// subsequently-arriving stale Delta for it is treated as unknown (SF-7)
// rather than accidentally still routed.
func (s *Screen) UnbindSubscription(id protocol.SubscriptionID) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "UnbindSubscription"}); err != nil {
		return
	}
	delete(s.subs, id)
}

// ApplyDelta routes delta to the view its SubscriptionID is bound to and
// applies its Patch. A Delta for an unbound (unknown/stale) SubscriptionID
// is dropped and logged via MET-U602 — never applied, never a panic (SF-7).
func (s *Screen) ApplyDelta(delta protocol.Delta) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		return
	}
	s.mu.Lock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "ApplyDelta"}); err != nil {
		s.mu.Unlock()
		return
	}
	view, ok := s.subs[delta.SubscriptionID]
	s.mu.Unlock()
	if !ok {
		s.logUnknownSubscription(delta.SubscriptionID)
		return
	}
	switch view {
	case ViewSession:
		s.applySession(delta.Patch)
	}
}

// SetStale surfaces ui.core's per-subscription staleness flag for view
// (UI-SPEC §1's "staleness dot").
func (s *Screen) SetStale(view string, stale bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SetStale"}); err != nil {
		return
	}
	s.stale[view] = stale
}

// Stale reports whether view is currently marked stale.
func (s *Screen) Stale(view string) bool {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Stale"}); err != nil {
		return false
	}
	return s.stale[view]
}

// Session returns the current f10.session summary. have is false until the
// first valid patch has been applied (SF-7: the "unavailable" state).
func (s *Screen) Session() (session Session, have bool) {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Session"}); err != nil {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Session"}); err != nil {
		return Session{}, false
	}
	return s.session, s.haveSession
}

func (s *Screen) applySession(raw json.RawMessage) {
	var p wireSessionPatch
	if err := decodeWirePatch(raw, &p); err != nil {
		s.logMalformed(ViewSession, err)
		return
	}
	s.mu.Lock()
	s.session = Session{
		WorldSeed: p.WorldSeed,
		Tick:      p.Tick,
		GameMonth: p.GameMonth,
		Paused:    p.Paused,
		Speed:     p.Speed,
	}
	s.haveSession = true
	s.mu.Unlock()
}

// sortSaveEntries orders entries by Name ascending so render order is
// deterministic regardless of directory walk order (GR#21). Never mutates
// its input's backing array beyond the returned copy.
func sortSaveEntries(entries []SaveEntry) []SaveEntry {
	out := make([]SaveEntry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// --- error trapping (GR#1/GR#7) ---------------------------------------

func (s *Screen) logMalformed(view string, cause error) {
	_ = errs.New(ErrMalformedPatch, s.correlationID, map[string]any{
		"view":  view,
		"cause": cause.Error(),
	})
}

func (s *Screen) logUnknownSubscription(id protocol.SubscriptionID) {
	_ = errs.New(ErrUnknownSubscription, s.correlationID, map[string]any{
		"subscriptionId": string(id),
	})
}
