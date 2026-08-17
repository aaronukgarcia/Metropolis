package news

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
)

// NewsAPI is code.json's "engine.news" inbound contract (GUID
// 129592d7-2873-4857-b733-e21f175b2ec2, "event bus consumer; salience
// scoring; searchable archive"): the ticker-ingestion path, the archive
// query surface, and the wiring point for the engine.roads naming seam and
// the optional LLM soft-layer. The four generation layers themselves are
// the pure functions [Bulletin], [AnnualReview] and [Epilogue], which take
// the [History] this API owns.
//
// The zero value is not usable; construct via [New]. A *NewsAPI is safe for
// concurrent use (AC-12): the weights are immutable after New, the log is
// self-synchronized, and the namer/rewriter fields are guarded by mu, with
// checkNotCopied rejecting a method call on a struct-copied value
// (SEC-020-class, mirroring engine.attract/engine.wellbeing).
type NewsAPI struct {
	correlationID string
	weights       map[Category]float64
	history       *History

	mu       sync.RWMutex
	namer    RoadNamer
	rewriter ProseRewriter

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[NewsAPI]
}

// New constructs a NewsAPI, loading the salience weight table from the
// embedded salience.json (GR#15). correlationID is attached to every error
// this call (and the returned API's methods) construct (GR#1); an empty
// value is minted via errs.NewCorrelationID. An invalid weight file is
// rejected with [ErrSalienceDataInvalid] — never a silently-defaulted
// weight table.
func New(correlationID string) (*NewsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	weights, err := loadSalienceWeights(correlationID)
	if err != nil {
		return nil, err
	}
	// Load (and validate) the fact-word list once, here, so a bad
	// news-facts.json fails construction (GR#15) rather than surfacing
	// later as a fail-closed FactLock.
	loadFactWordsOnce(correlationID)
	if factWordsErr != nil {
		return nil, factWordsErr
	}
	n := &NewsAPI{
		correlationID: correlationID,
		weights:       weights,
		history:       NewHistory(),
	}
	n.self.Store(n)
	return n, nil
}

// checkNotCopied rejects a method call on a struct-copied *NewsAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and
// therefore safe to run before mu is ever touched.
func (n *NewsAPI) checkNotCopied(method string) error {
	if n.self.Load() != n {
		return errs.New(ErrCopiedValue, n.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetRoadNamer wires the engine.roads naming seam (§20/AC-3). Until it is
// wired, an event whose entity reference is non-empty errors at
// generation time (AC-8) rather than fabricating a name.
func (n *NewsAPI) SetRoadNamer(nm RoadNamer) error {
	if err := n.checkNotCopied("SetRoadNamer"); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.namer = nm
	return nil
}

// SetRewriter wires the optional LLM soft-layer (AC-6/AC-7). A nil
// rewriter disables it — the v1 default. The rewriter only ever affects
// prose, and only through the fact-locked [RewriteBulletin]/[RewriteEpilogue]
// stage, never the deterministic generation functions.
func (n *NewsAPI) SetRewriter(rw ProseRewriter) error {
	if err := n.checkNotCopied("SetRewriter"); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.rewriter = rw
	return nil
}

// Ingest accepts one real sim event, emits its ticker item (AC-1), and
// records it in the log. The returned [Story] carries the source reference
// (EventID, EntityID, Tick, Month) back to the originating record. It is
// safe for concurrent use from multiple shard workers (AC-12).
//
// An invalid event is rejected with [ErrInvalidEvent] and is NOT recorded;
// an event whose entity reference does not resolve is rejected with
// [ErrUnresolvedEntity] and is NOT recorded (AC-8) — never a silently
// dropped story and never a fabricated placeholder name.
func (n *NewsAPI) Ingest(ev Event) (Story, error) {
	if err := n.checkNotCopied("Ingest"); err != nil {
		return Story{}, err
	}
	if err := validateEvent(ev, n.weights, n.correlationID); err != nil {
		return Story{}, err
	}
	n.mu.RLock()
	namer := n.namer
	n.mu.RUnlock()

	st, err := materializeStory(ev, Config{Weights: n.weights, Namer: namer}, n.correlationID)
	if err != nil {
		return Story{}, err
	}
	// Persist the resolved name with the event: the archive and generation
	// layers read it back, so a later namer change can never drop or alter an
	// already-accepted story (SEC-110, AC-8/AC-9).
	n.history.append(ev, st.Name)
	return st, nil
}

// History returns the persisted event log this API owns — the input the
// pure generation functions take. The returned *History is read-only from
// outside this package: its only exported methods are Len / Snapshot /
// SnapshotWhere (the write path is unexported, reachable only via Ingest).
func (n *NewsAPI) History() *History {
	if err := n.checkNotCopied("History"); err != nil {
		return nil
	}
	return n.history
}

// Config returns the static generation configuration (the loaded weights
// plus the wired namer) for callers that drive the pure generation
// functions directly. The weights are returned as a defensive copy, so a
// caller mutating the result cannot corrupt the shared state (SEC-111).
func (n *NewsAPI) Config() Config {
	if err := n.checkNotCopied("Config"); err != nil {
		return Config{}
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return Config{Weights: registry.CloneMap(n.weights), Namer: n.namer}
}

// Rewriter returns the currently wired LLM soft-layer (nil = disabled).
func (n *NewsAPI) Rewriter() ProseRewriter {
	if err := n.checkNotCopied("Rewriter"); err != nil {
		return nil
	}
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.rewriter
}

// Archive returns a snapshot of every recorded event's story, in ingest
// order — the full searchable history (§29.4). Because the resolved name is
// persisted at ingest, every event that was accepted into the log appears
// here, so "didn't make the front page" is always distinguishable from
// "never happened" (AC-9).
func (n *NewsAPI) Archive() []Story {
	if err := n.checkNotCopied("Archive"); err != nil {
		return nil
	}
	return n.Query(func(Story) bool { return true })
}

// Query returns, in ingest order, the stories whose source event matches
// the predicate — the searchable-archive query path (AC-9). Stories are
// rebuilt from the log using the names persisted at ingest, never
// re-resolved against the current namer, so a recorded event is never
// silently skipped or able to fail the whole query after a namer change
// (SEC-110).
func (n *NewsAPI) Query(match func(Story) bool) []Story {
	if err := n.checkNotCopied("Query"); err != nil {
		return nil
	}
	records := n.history.Snapshot()
	out := make([]Story, 0, len(records))
	for _, r := range records {
		st := buildStory(r.ev, r.name)
		if match(st) {
			out = append(out, st)
		}
	}
	return out
}

// validateEvent rejects an event that cannot become a well-formed story:
// empty ID after trimming (TIK-5's "no hallucinated news" precondition),
// negative tick, negative magnitude, an unknown category, or empty prose
// after trimming. Every rejection is [ErrInvalidEvent] (GR#1/GR#16) —
// never silently accepted into the log.
func validateEvent(ev Event, weights map[Category]float64, correlationID string) error {
	fail := func(field, rule string) error {
		return errs.New(ErrInvalidEvent, correlationID, map[string]any{
			"field": field,
			"rule":  rule,
		})
	}
	if strings.TrimSpace(ev.ID) == "" {
		return fail("id", "non-empty after trimming")
	}
	if ev.Tick < 0 {
		return fail("tick", "non-negative")
	}
	if !ValidCategory(ev.Category) {
		return fail("category", "one of death|first|record|crisis|milestone (§29)")
	}
	if _, ok := weights[ev.Category]; !ok {
		return fail("category", "present in the salience weight table")
	}
	if ev.Magnitude < 0 {
		return fail("magnitude", "non-negative")
	}
	if strings.TrimSpace(ev.Text) == "" {
		return fail("text", "non-empty after trimming")
	}
	return nil
}
