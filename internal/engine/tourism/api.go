package tourism

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// TourismAPI is code.json's "engine.tourism" inbound contract
// (GUID 1082096b-c263-4415-b592-29d1e1e0ea63, "visitor streams parallel to
// citizens; portfolio score decomposed"): the §44 holiday-tourism visitor
// economy — the day-tripper/staying-visitor parallel population stream, the
// decomposed attraction-portfolio draw score, and the accommodation-stock
// capacity enforcement.
//
// The zero value is not usable; construct via [New] or [Load]. A
// *TourismAPI is safe for concurrent use (AC-19): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.attract).
//
// Dependencies are consumed through the narrow seams in seam.go (GR#20) and
// wired via SetAttract/SetLeisure/SetSeason/SetNews. The three blocked
// edges (engine.households, engine.tax, engine.cafe) are never imported
// (AC-17) — their mechanics (AC-7/AC-8/AC-9) remain pending BUG-058.
type TourismAPI struct {
	correlationID string
	seed          uint64
	cfg           Config
	month         int64

	// Dependencies, wired via Set* and read under mu.
	attract AttractAPI
	leisure LeisureAPI
	season  SeasonAPI
	news    NewsAPI

	// Portfolio state. attractions is insertion-ordered so term summation is
	// deterministic (GR#21: never range over a map on a results-affecting
	// path). accessTier is the current §44 access rung.
	attractions []Attraction
	accessTier  AccessTier

	// Accommodation state. accommodation is insertion-ordered for the same
	// determinism reason (capacity sums are exact int64 anyway, but the
	// ordering rule is applied uniformly).
	accommodation []Accommodation

	// Visitor ledger (§5.2 conservation doctrine mirrored for visitors,
	// AC-13a): admitted = departed + present must hold at all times.
	presentStaying  int64
	admittedStaying int64
	departedStaying int64
	admittedDayTrip int64
	departedDayTrip int64

	// queue is the accommodation waitlist (AC-13c): would-be staying
	// visitors held over capacity, drained when beds free.
	queue int64

	// admissions[i] is the staying-visitor count admitted in month i — the
	// departure schedule (a visitor admitted in month i departs in month
	// i + cfg.StayingVisitorStayMonths). repHistory[i] is the reputation
	// reading observed in month i — the AC-10 lag buffer.
	admissions []int64
	repHistory []float64

	// Current-month records, refreshed by AdvanceMonth.
	dayTrip        DayTripper
	stayingVisitor StayingVisitor
	load           VisitorLoad

	mu   sync.RWMutex
	self atomic.Pointer[TourismAPI]
}

// New constructs a TourismAPI from a validated Config and a world seed
// (reserved for future stochastic draws — the current model is a pure
// deterministic function of state and data, AC-18). correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted gate or scale. Dependencies are wired
// later via Set*.
func New(cfg Config, seed uint64, correlationID string) (*TourismAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	t := &TourismAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		accessTier:    AccessDomestic,
	}
	// Seed the per-kind accommodation facilities from the configured default
	// bed stock so AC-6's capacity pool is queryable immediately.
	for kind := AccommodationKind(0); int(kind) < numAccommodationKinds; kind++ {
		if cfg.Accommodation[kind] != 0 {
			t.accommodation = append(t.accommodation, Accommodation{
				ID:   uint64(kind) + 1,
				Kind: kind,
				Beds: cfg.Accommodation[kind],
			})
		}
	}
	t.self.Store(t)
	return t, nil
}

// checkNotCopied rejects a method call on a struct-copied *TourismAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load.
func (t *TourismAPI) checkNotCopied(method string) error {
	if t.self.Load() != t {
		return errs.New(ErrCopiedValue, t.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetAttract wires the engine.attract reputation source (AC-3).
func (t *TourismAPI) SetAttract(a AttractAPI) error {
	if err := t.checkNotCopied("SetAttract"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attract = a
	return nil
}

// SetLeisure wires the engine.leisure venue-mix source (AC-2's venues term).
func (t *TourismAPI) SetLeisure(l LeisureAPI) error {
	if err := t.checkNotCopied("SetLeisure"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.leisure = l
	return nil
}

// SetSeason wires the engine.season seasonal-curve source (AC-4).
func (t *TourismAPI) SetSeason(s SeasonAPI) error {
	if err := t.checkNotCopied("SetSeason"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.season = s
	return nil
}

// SetNews wires the engine.news event sink (the supply-the-event half of
// the registered edge; ticker rendering is engine.news's own job).
func (t *TourismAPI) SetNews(n NewsAPI) error {
	if err := t.checkNotCopied("SetNews"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.news = n
	return nil
}

// SetAccessTier sets the current §44 access rung (domestic → continental →
// global), whose reach multiplier is data-derived.
func (t *TourismAPI) SetAccessTier(tier AccessTier) error {
	if err := t.checkNotCopied("SetAccessTier"); err != nil {
		return err
	}
	if int(tier) >= numAccessTiers {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{
			"field":  "accessTier",
			"value":  int(tier),
			"reason": "outside the domestic/continental/global ladder",
		})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.accessTier = tier
	return nil
}

// AddAttraction registers one portfolio contribution. A duplicate ID, an
// out-of-range term, or a non-finite/negative score is rejected (AC-15's
// companion write path).
func (t *TourismAPI) AddAttraction(a Attraction) error {
	if err := t.checkNotCopied("AddAttraction"); err != nil {
		return err
	}
	if a.ID == 0 {
		return errs.New(ErrInvalidAttraction, t.correlationID, map[string]any{"id": a.ID, "reason": "zero ID"})
	}
	if int(a.Term) >= numPortfolioTerms {
		return errs.New(ErrInvalidAttraction, t.correlationID, map[string]any{"id": a.ID, "reason": "term outside the active portfolio terms"})
	}
	if !num.IsFinite(a.Score) || a.Score < 0 {
		return errs.New(ErrInvalidAttraction, t.correlationID, map[string]any{"id": a.ID, "reason": "score must be finite and non-negative"})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, existing := range t.attractions {
		if existing.ID == a.ID {
			return errs.New(ErrInvalidAttraction, t.correlationID, map[string]any{"id": a.ID, "reason": "duplicate ID"})
		}
	}
	t.attractions = append(t.attractions, a)
	return nil
}

// RemoveAttraction de-lists a registered attraction. Removing an unknown ID
// is a no-op by design (idempotent de-list), matching a cancellation of a
// scheduled event.
func (t *TourismAPI) RemoveAttraction(id uint64) error {
	if err := t.checkNotCopied("RemoveAttraction"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, a := range t.attractions {
		if a.ID == id {
			t.attractions = append(t.attractions[:i], t.attractions[i+1:]...)
			return nil
		}
	}
	return nil
}

// AttractionScore returns a single registered attraction's score; an
// unregistered ID is rejected with a registry-sourced error (AC-15), never
// a silent zero.
func (t *TourismAPI) AttractionScore(id uint64) (float64, error) {
	if err := t.checkNotCopied("AttractionScore"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, a := range t.attractions {
		if a.ID == id {
			return a.Score, nil
		}
	}
	return 0, errs.New(ErrUnknownAttraction, t.correlationID, map[string]any{"id": id})
}

// AddAccommodation registers one accommodation facility.
func (t *TourismAPI) AddAccommodation(a Accommodation) error {
	if err := t.checkNotCopied("AddAccommodation"); err != nil {
		return err
	}
	if a.ID == 0 {
		return errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": a.ID, "reason": "zero ID"})
	}
	if int(a.Kind) >= numAccommodationKinds {
		return errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": a.ID, "reason": "kind outside the four accommodation categories"})
	}
	if a.Beds < 0 {
		return errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": a.ID, "reason": "negative bed count"})
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, existing := range t.accommodation {
		if existing.ID == a.ID {
			// Upsert by ID so SetAccommodationCapacity can re-seed a kind.
			t.accommodation[i] = a
			return nil
		}
	}
	t.accommodation = append(t.accommodation, a)
	return nil
}

// SetAccommodationCapacity sets the configured bed stock for one
// accommodation kind (upserting the kind's canonical facility), the
// runtime path engine.build uses to push capacity into AC-6's pool.
func (t *TourismAPI) SetAccommodationCapacity(kind AccommodationKind, beds int64) error {
	if err := t.checkNotCopied("SetAccommodationCapacity"); err != nil {
		return err
	}
	if int(kind) >= numAccommodationKinds {
		return errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": 0, "reason": "kind outside the four accommodation categories"})
	}
	if beds < 0 {
		return errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": 0, "reason": "negative bed count"})
	}
	return t.AddAccommodation(Accommodation{ID: uint64(kind) + 1, Kind: kind, Beds: beds})
}

// AccommodationBeds returns a registered facility's bed count; an
// unregistered ID is rejected with a registry-sourced error (AC-15).
func (t *TourismAPI) AccommodationBeds(id uint64) (int64, error) {
	if err := t.checkNotCopied("AccommodationBeds"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, a := range t.accommodation {
		if a.ID == id {
			return a.Beds, nil
		}
	}
	return 0, errs.New(ErrUnknownAccommodation, t.correlationID, map[string]any{"id": id})
}

// AccommodationCapacity returns the configured bed stock for one kind
// (hotels + B&Bs + campsite/caravan + holiday lets).
func (t *TourismAPI) AccommodationCapacity(kind AccommodationKind) (int64, error) {
	if err := t.checkNotCopied("AccommodationCapacity"); err != nil {
		return 0, err
	}
	if int(kind) >= numAccommodationKinds {
		return 0, errs.New(ErrInvalidAccommodation, t.correlationID, map[string]any{"id": 0, "reason": "kind outside the four accommodation categories"})
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, a := range t.accommodation {
		if a.Kind == kind {
			total = num.SatAdd(total, a.Beds)
		}
	}
	return total, nil
}

// TotalAccommodationCapacity returns the summed configured bed stock —
// AC-6's hard cap.
func (t *TourismAPI) TotalAccommodationCapacity() int64 {
	_ = t.checkNotCopied("TotalAccommodationCapacity")
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, a := range t.accommodation {
		total = num.SatAdd(total, a.Beds)
	}
	return total
}

// TermScore returns the decomposed score of one portfolio term. The venues
// term is sourced live from engine.leisure (the registered edge); the other
// terms are the sum of their registered attractions. An out-of-range term
// or a missing leisure dependency errors.
func (t *TourismAPI) TermScore(term TermKind) (float64, error) {
	if err := t.checkNotCopied("TermScore"); err != nil {
		return 0, err
	}
	if int(term) >= numPortfolioTerms {
		return 0, errs.New(ErrInvalidInput, t.correlationID, map[string]any{
			"field":  "term",
			"value":  int(term),
			"reason": "outside the active portfolio terms",
		})
	}
	if term == TermVenues {
		t.mu.RLock()
		leisure := t.leisure
		t.mu.RUnlock()
		if leisure == nil {
			return 0, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{
				"dependency": "leisure",
				"operation":  "TermScore(venues)",
			})
		}
		mix, err := leisure.VenueMix(0, t.correlationID)
		if err != nil {
			return 0, err
		}
		return sumVenueMix(mix), nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return sumTermAttractions(t.attractions, term), nil
}

// BeachPromenadePier returns the beach/promenade/pier term (AC-2).
func (t *TourismAPI) BeachPromenadePier() float64 {
	_ = t.checkNotCopied("BeachPromenadePier")
	v, _ := t.TermScore(TermBeach)
	return v
}

// Venues returns the venues term, sourced live from engine.leisure (AC-2).
func (t *TourismAPI) Venues() (float64, error) {
	if err := t.checkNotCopied("Venues"); err != nil {
		return 0, err
	}
	return t.TermScore(TermVenues)
}

// EventsTerm returns the events term (AC-2).
func (t *TourismAPI) EventsTerm() float64 {
	_ = t.checkNotCopied("EventsTerm")
	v, _ := t.TermScore(TermEvents)
	return v
}

// LandmarksHeritage returns the landmarks/heritage term (AC-2).
func (t *TourismAPI) LandmarksHeritage() float64 {
	_ = t.checkNotCopied("LandmarksHeritage")
	v, _ := t.TermScore(TermLandmarks)
	return v
}

// CountrysideBDI returns the countryside/BDI term (AC-2).
func (t *TourismAPI) CountrysideBDI() float64 {
	_ = t.checkNotCopied("CountrysideBDI")
	v, _ := t.TermScore(TermCountryside)
	return v
}

// PortfolioScore returns the weighted sum of the decomposed terms (AC-1's
// "portfolio score decomposed" — every term independently queryable, the
// composite a weighted fold, never one opaque blended float).
func (t *TourismAPI) PortfolioScore() (float64, error) {
	if err := t.checkNotCopied("PortfolioScore"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	leisure := t.leisure
	weights := t.cfg.PortfolioWeights
	attractions := append([]Attraction(nil), t.attractions...)
	t.mu.RUnlock()

	return portfolioScore(attractions, leisure, weights, t.correlationID)
}

// DrawScore returns the composite draw score for the current month:
// portfolio score × reputation × access × season (§44). It is a pure read:
// it mutates nothing. It errors (ErrDependencyMissing) if attract or season
// is not yet wired.
func (t *TourismAPI) DrawScore() (float64, error) {
	if err := t.checkNotCopied("DrawScore"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	month := t.month
	t.mu.RUnlock()
	return t.drawScore(month)
}

// ProjectDraw returns the §44 draw projection for a future month — the
// seasonal multiplier and resulting draw score queryable ahead of the month
// it happens (AC-12, the anti-ambush contract). Reputation/access/portfolio
// use the current state; only the seasonal multiplier varies with the
// projected month (a pure function of the month index).
func (t *TourismAPI) ProjectDraw(month int64) (DrawProjection, error) {
	if err := t.checkNotCopied("ProjectDraw"); err != nil {
		return DrawProjection{}, err
	}
	if month < 0 {
		return DrawProjection{}, errs.New(ErrInvalidInput, t.correlationID, map[string]any{
			"field":  "month",
			"value":  month,
			"reason": "negative month index",
		})
	}
	t.mu.RLock()
	season := t.season
	attract := t.attract
	leisure := t.leisure
	weights := t.cfg.PortfolioWeights
	accessMult := t.cfg.AccessTierReach[t.accessTier]
	repScale := t.cfg.ReputationScale
	lagRep := t.laggedReputationLocked(t.month)
	attractions := append([]Attraction(nil), t.attractions...)
	t.mu.RUnlock()

	if season == nil {
		return DrawProjection{}, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "season", "operation": "ProjectDraw"})
	}
	if attract == nil {
		return DrawProjection{}, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "attract", "operation": "ProjectDraw"})
	}
	seasonal, err := t.seasonalMultiplier(season, month)
	if err != nil {
		return DrawProjection{}, err
	}
	portfolio, err := portfolioScore(attractions, leisure, weights, t.correlationID)
	if err != nil {
		return DrawProjection{}, err
	}
	repMult := reputationMultiplier(lagRep, repScale)
	draw := portfolio * repMult * accessMult * seasonal
	return DrawProjection{
		Month:                month,
		SeasonalMultiplier:   seasonal,
		ReputationMultiplier: repMult,
		AccessMultiplier:     accessMult,
		DrawScore:            draw,
	}, nil
}

// DayTrippers returns the current month's day-tripper stream record.
func (t *TourismAPI) DayTrippers() DayTripper {
	_ = t.checkNotCopied("DayTrippers")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dayTrip
}

// StayingVisitors returns the current month's staying-visitor stream record.
func (t *TourismAPI) StayingVisitors() StayingVisitor {
	_ = t.checkNotCopied("StayingVisitors")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.stayingVisitor
}

// VisitorLoad returns the current month's volume-responsive transport/waste/
// policing load signal (AC-11).
func (t *TourismAPI) VisitorLoad() VisitorLoad {
	_ = t.checkNotCopied("VisitorLoad")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.load
}

// Month returns the current simulated month index (0 = genesis).
func (t *TourismAPI) Month() int64 {
	_ = t.checkNotCopied("Month")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.month
}

// StayingPresent returns the realised staying-visitor count currently
// occupying accommodation beds (AC-6's realised count).
func (t *TourismAPI) StayingPresent() int64 {
	_ = t.checkNotCopied("StayingPresent")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.presentStaying
}

// StayingAdmitted returns the cumulative staying-visitor admissions.
func (t *TourismAPI) StayingAdmitted() int64 {
	_ = t.checkNotCopied("StayingAdmitted")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.admittedStaying
}

// StayingDeparted returns the cumulative staying-visitor departures.
func (t *TourismAPI) StayingDeparted() int64 {
	_ = t.checkNotCopied("StayingDeparted")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.departedStaying
}

// DayTripperAdmitted returns the cumulative day-tripper admissions.
func (t *TourismAPI) DayTripperAdmitted() int64 {
	_ = t.checkNotCopied("DayTripperAdmitted")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.admittedDayTrip
}

// DayTripperDeparted returns the cumulative day-tripper departures.
func (t *TourismAPI) DayTripperDeparted() int64 {
	_ = t.checkNotCopied("DayTripperDeparted")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.departedDayTrip
}

// QueueLength returns the current accommodation-waitlist length (AC-13c).
func (t *TourismAPI) QueueLength() int64 {
	_ = t.checkNotCopied("QueueLength")
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.queue
}

// ReportEvent supplies one tourism event through the registered
// engine.tourism→engine.news edge (the supply-the-event half; ticker copy
// is engine.news's job). Errors if news is not wired.
func (t *TourismAPI) ReportEvent(ev news.Event) (news.Story, error) {
	if err := t.checkNotCopied("ReportEvent"); err != nil {
		return news.Story{}, err
	}
	t.mu.RLock()
	newsSink := t.news
	t.mu.RUnlock()
	if newsSink == nil {
		return news.Story{}, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{
			"dependency": "news",
			"operation":  "ReportEvent",
		})
	}
	return newsSink.Ingest(ev)
}

// --- internal computation (pure, deterministic) ---

// maxDrawFloat is the float64 saturation ceiling for the portfolio/draw
// chain: float64(math.MaxInt64) (exactly 2^63), the value at which
// num.ClampInt64FromFloat saturates to math.MaxInt64. Every portfolio term
// and the running SUM are clamped to it, so the SUM stays finite and no
// +Inf/NaN can ever reach the float64→int64 cast (GR#16).
const maxDrawFloat = float64(math.MaxInt64)

// satTermFloat clamps a portfolio contribution into the non-negative draw
// domain [0, maxDrawFloat]. NaN and negative values collapse to 0; any
// value at or past maxDrawFloat saturates to maxDrawFloat (the downstream
// num.ClampInt64FromFloat saturation point — beyond it the visitor count is
// already saturated, so nothing is lost).
func satTermFloat(x float64) float64 {
	if math.IsNaN(x) || x <= 0 {
		return 0
	}
	if x >= maxDrawFloat {
		return maxDrawFloat
	}
	return x
}

// satFloatAdd adds b onto a, saturating the total at maxDrawFloat. Both
// operands are assumed already clamped to [0, maxDrawFloat], so a+b is at
// most 2·maxDrawFloat — finite — and the guard only bounds the total.
func satFloatAdd(a, b float64) float64 {
	s := a + b
	if math.IsNaN(s) || s >= maxDrawFloat {
		return maxDrawFloat
	}
	return s
}

// sumVenueMix folds a citywide venue-mix array into a single venue term
// (index order — deterministic). The home category carries no venue
// capacity, so it contributes zero in practice.
func sumVenueMix(mix [leisure.NumCategories]float64) float64 {
	var sum float64
	for i := 0; i < len(mix); i++ {
		sum = satFloatAdd(sum, satTermFloat(mix[i]))
	}
	return sum
}

// sumTermAttractions sums the scores of the attractions of one term in
// insertion order (deterministic — GR#21). Each score and the running sum
// are saturated so a term can never overflow to +Inf (GR#16).
func sumTermAttractions(attractions []Attraction, term TermKind) float64 {
	var sum float64
	for _, a := range attractions {
		if a.Term == term {
			sum = satFloatAdd(sum, satTermFloat(a.Score))
		}
	}
	return sum
}

// reputationMultiplier maps engine.attract's signed reputation momentum to
// a non-negative draw multiplier: 1 + reputation/scale, floored at 0 (a
// severe reputation collapse drives draw toward zero — §44 "a trap as a
// first one" — never negative). A non-finite reading contributes neutral.
func reputationMultiplier(rep, scale float64) float64 {
	if !num.IsFinite(rep) {
		return 1
	}
	m := 1 + rep/scale
	if !num.IsFinite(m) || m < 0 {
		return 0
	}
	return m
}

// seasonalMultiplier reads the §44 seaside seasonal curve for month from
// the wired engine.season dependency (the beach half of LeisureMix — the
// summer-peaking, data-derived draw multiplier; never a hardcoded ×3).
func (t *TourismAPI) seasonalMultiplier(season SeasonAPI, month int64) (float64, error) {
	w, err := season.LeisureMix(month)
	if err != nil {
		return 0, err
	}
	return w.Beach, nil
}

// laggedReputationLocked returns the reputation currently in effect for the
// AC-10 lag: the reading recorded cfg.ReputationLagMonths months before
// `month`, clamped to the first recorded reading before the lag window has
// filled (caller holds mu).
func (t *TourismAPI) laggedReputationLocked(month int64) float64 {
	lag := int64(t.cfg.ReputationLagMonths)
	idx := month - lag
	if idx < 0 {
		idx = 0
	}
	if int(idx) >= len(t.repHistory) {
		idx = int64(len(t.repHistory) - 1)
	}
	if idx < 0 {
		return 0
	}
	return t.repHistory[idx]
}

// drawScore computes the draw score for month (caller must not hold mu
// across the dependency calls; it re-acquires only for reads).
func (t *TourismAPI) drawScore(month int64) (float64, error) {
	t.mu.RLock()
	attract := t.attract
	leisure := t.leisure
	season := t.season
	weights := t.cfg.PortfolioWeights
	accessMult := t.cfg.AccessTierReach[t.accessTier]
	repScale := t.cfg.ReputationScale
	lagRep := t.laggedReputationLocked(month)
	attractions := append([]Attraction(nil), t.attractions...)
	t.mu.RUnlock()

	if attract == nil {
		return 0, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "attract", "operation": "DrawScore"})
	}
	if season == nil {
		return 0, errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "season", "operation": "DrawScore"})
	}
	seasonal, err := t.seasonalMultiplier(season, month)
	if err != nil {
		return 0, err
	}
	portfolio, err := portfolioScore(attractions, leisure, weights, t.correlationID)
	if err != nil {
		return 0, err
	}
	repMult := reputationMultiplier(lagRep, repScale)
	return portfolio * repMult * accessMult * seasonal, nil
}

// portfolioScore folds the decomposed terms into the weighted portfolio
// score. Callers pass the already-snapshotted leisure/attractions so this
// helper never touches t.mu.
func portfolioScore(attractions []Attraction, leisure LeisureAPI, weights [numPortfolioTerms]float64, correlationID string) (float64, error) {
	var sum float64
	for term := TermKind(0); int(term) < numPortfolioTerms; term++ {
		var v float64
		if term == TermVenues {
			if leisure == nil {
				return 0, errs.New(ErrDependencyMissing, correlationID, map[string]any{
					"dependency": "leisure",
					"operation":  "portfolioScore",
				})
			}
			mix, err := leisure.VenueMix(0, correlationID)
			if err != nil {
				return 0, err
			}
			v = sumVenueMix(mix)
		} else {
			v = sumTermAttractions(attractions, term)
		}
		sum = satFloatAdd(sum, satTermFloat(weights[term]*v))
	}
	return sum, nil
}

// AdvanceMonth advances one simulated month: records the reputation reading,
// computes the draw, departs visitors whose stay has elapsed, admits
// staying visitors up to the accommodation cap (queueing the overflow), and
// refreshes the current-month records. Wall-clock time is never read
// (GR#21).
func (t *TourismAPI) AdvanceMonth() error {
	if err := t.checkNotCopied("AdvanceMonth"); err != nil {
		return err
	}

	// Snapshot the wired seams and the immutable current-month inputs under a
	// read lock, then RELEASE before calling into them. The seams are external
	// composition-root calls that may take their own locks; holding this
	// module's write lock across them is the BUG-298 deadlock hazard (a seam
	// that ever calls back into a TourismAPI read — e.g. Month — would block
	// forever). Mirrors the snapshot-then-call pattern of DrawScore/ProjectDraw/
	// PortfolioScore/TermScore. cfg and correlationID are immutable after New,
	// so they are read lock-free below (as the read methods already do).
	t.mu.RLock()
	attract := t.attract
	season := t.season
	leisure := t.leisure
	attractions := append([]Attraction(nil), t.attractions...)
	m := t.month
	accessMult := t.cfg.AccessTierReach[t.accessTier]
	t.mu.RUnlock()

	if attract == nil {
		return errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "attract", "operation": "AdvanceMonth"})
	}
	if season == nil {
		return errs.New(ErrDependencyMissing, t.correlationID, map[string]any{"dependency": "season", "operation": "AdvanceMonth"})
	}

	rep := attract.Reputation()
	if !num.IsFinite(rep) {
		rep = 0
	}
	seasonal, err := t.seasonalMultiplier(season, m)
	if err != nil {
		return err
	}
	portfolio, err := portfolioScore(attractions, leisure, t.cfg.PortfolioWeights, t.correlationID)
	if err != nil {
		return err
	}

	// Mutation phase under the write lock — no seam call below, they all
	// completed lock-free above.
	t.mu.Lock()
	defer t.mu.Unlock()
	t.repHistory = append(t.repHistory, rep)

	// lagRep/draw/desired are local math, computed under the lock to preserve
	// the original append-then-read ordering: laggedReputationLocked reads the
	// just-appended reading when the lag window is still unfilled (month 0).
	lagRep := t.laggedReputationLocked(m)
	repMult := reputationMultiplier(lagRep, t.cfg.ReputationScale)
	draw := portfolio * repMult * accessMult * seasonal

	desiredStaying := num.ClampInt64FromFloat(draw * t.cfg.StayingVisitorRate)
	desiredDayTrip := num.ClampInt64FromFloat(draw * t.cfg.DayTripRate)

	// 1. Departures: visitors admitted stayMonths ago leave, freeing beds.
	stay := int(t.cfg.StayingVisitorStayMonths)
	if len(t.admissions) >= stay {
		departing := t.admissions[len(t.admissions)-stay]
		t.presentStaying = num.SatSub(t.presentStaying, departing)
		t.departedStaying = num.SatAdd(t.departedStaying, departing)
	}

	// 2. Admission: waitlist first, then new demand, up to free beds.
	capacity := t.totalCapacityLocked()
	free := capacity - t.presentStaying
	if free < 0 {
		free = 0
	}
	fromQueue := int64(0)
	if t.queue > 0 && free > 0 {
		fromQueue = minInt64(t.queue, free)
		t.queue = num.SatSub(t.queue, fromQueue)
		free -= fromQueue
	}
	fromNew := int64(0)
	if desiredStaying > 0 && free > 0 {
		fromNew = minInt64(desiredStaying, free)
	}
	if desiredStaying > fromNew {
		t.queue = num.SatAdd(t.queue, desiredStaying-fromNew)
	}
	admitted := num.SatAdd(fromQueue, fromNew)
	t.presentStaying = num.SatAdd(t.presentStaying, admitted)
	t.admittedStaying = num.SatAdd(t.admittedStaying, admitted)
	t.admissions = append(t.admissions, admitted)

	// 3. Day-trippers: arrive and depart within the same month (transient).
	t.admittedDayTrip = num.SatAdd(t.admittedDayTrip, desiredDayTrip)
	t.departedDayTrip = num.SatAdd(t.departedDayTrip, desiredDayTrip)

	// 4. Refresh current-month records. Spend/nights are int64 visitor
	// quantities, so they route through SafeMul (GR#16) — a saturated
	// visitor count times a large spend coefficient must saturate at
	// math.MaxInt64, never wrap negative.
	dayTripCount := desiredDayTrip
	stayingCount := admitted
	dayTripSpend, _ := num.SafeMul(dayTripCount, t.cfg.Spend.DayTripMicroPounds)
	stayNights, _ := num.SafeMul(stayingCount, int64(stay))
	stayingSpend, _ := num.SafeMul(stayNights, t.cfg.Spend.StayingPerNightMicroPounds)
	t.dayTrip = DayTripper{
		Count:            dayTripCount,
		Hours:            float64(dayTripCount) * t.cfg.Spend.DayTripHours,
		SpendMicroPounds: dayTripSpend,
		TransportLoad:    float64(dayTripCount) * t.cfg.Load.DayTripperTransport,
	}
	t.stayingVisitor = StayingVisitor{
		Count:            stayingCount,
		Nights:           stayNights,
		SpendMicroPounds: stayingSpend,
		TransportLoad:    float64(stayingCount) * t.cfg.Load.StayingTransport,
	}
	t.load = VisitorLoad{
		Transport: float64(dayTripCount)*t.cfg.Load.DayTripperTransport +
			float64(stayingCount)*t.cfg.Load.StayingTransport,
		Waste: float64(dayTripCount)*t.cfg.Load.DayTripperWaste +
			float64(stayingCount)*t.cfg.Load.StayingWaste,
		Policing: float64(dayTripCount)*t.cfg.Load.DayTripperPolicing +
			float64(stayingCount)*t.cfg.Load.StayingPolicing,
	}

	t.month++
	return nil
}

// totalCapacityLocked sums the configured bed stock (caller holds mu).
func (t *TourismAPI) totalCapacityLocked() int64 {
	var total int64
	for _, a := range t.accommodation {
		total = num.SatAdd(total, a.Beds)
	}
	return total
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
