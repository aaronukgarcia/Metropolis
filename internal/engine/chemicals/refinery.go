package chemicals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is feat.refinery: the oil refinery + integrated chemical works as
// two DISTINCT, data-sourced facilities (AC-1/AC-2), plus the facility-level
// make-vs-buy decision (AC-3), the crude-tanker → chain → fuel-flow wiring
// (AC-4/AC-5/AC-6), the top-blight/hazmat-fire risk wiring (AC-7), and the
// permit/decommission inheritance (AC-8). It shares this package with
// engine.chemicals and feat.commoditymarket — see doc.go for the full
// package narrative, the spec citations, and the ASM scope split.
//
// # Seams (dependency-inversion over the registered edges)
//
// Each outbound dependency below is a consumer-side interface seam the owning
// module implements when it lands — the same shape the freight package uses
// for engine.firms / engine.rail / feat.facilitypermits / feat.decommission.
// This feature consumes these seams, never a local reimplementation (GR#3).

// FreightAPI is the seam for the registered engine.chemicals → engine.freight
// edge (AC-4): crude oil arrives by tanker as ordinary freight tonnage, never
// a refinery-local port model.
type FreightAPI interface {
	// CrudeLanding delivers up to tonnes of crude oil as an ordinary tanker
	// landing (t/day), returning the tonnes actually landed (bounded by the
	// port/tanker edge's own capacity). tonnes must be non-negative: Operate
	// rejects a negative request before this seam is called, and an implementer
	// must hold the same rule rather than return a negative landing (SEC-165).
	// Operate additionally defends the consumer side: a negative landing is
	// rejected rather than recorded (SEC-166).
	CrudeLanding(tonnes int64) (int64, error)
}

// FuelAPI is the seam for feat.refinery's registered engine.fuel edge (AC-6):
// the refinery's fuel output is the upstream supply the fuel system consumes
// (tanker truck → pump → tax). This feature feeds the supply surface; it does
// not reimplement fuel distribution, duty, or the EV transition.
type FuelAPI interface {
	// Supply records the refinery's fuel output (t/day) as the fuel system's
	// upstream supply.
	Supply(tonnesPerDay int64) error
	// SupplyTonnes returns the fuel system's own supply figure, read back
	// through the fuel edge, so a caller can observe a degradation when the
	// refinery's crude input is constrained.
	SupplyTonnes() int64
}

// DispatchAPI is the seam for the registered engine.chemicals → engine.dispatch
// edge (AC-7): the refinery's hazmat-fire category feeds the unified incident
// queue. The blight-class half of AC-7 stays single-sourced in
// data/buildings.json and its registration is blocked — see doc.go.
type DispatchAPI interface {
	// ReportIncident files an incident category into the dispatch queue.
	ReportIncident(category string, severity int) error
}

// PermitAuthority is the seam for feat.facilitypermits (open): the refinery
// build is permit-gated through it (AC-8), never a refinery-local permit state.
type PermitAuthority interface {
	// PermitGranted reports whether building facilityKey is permitted at the
	// caller's current milestone.
	PermitGranted(facilityKey string, milestone int) (bool, error)
}

// DecommissionRegistrar is the seam for feat.decommission (open): the refinery
// carries a day-one decommission liability through it (AC-8), never a
// refinery-local liability ledger.
type DecommissionRegistrar interface {
	// RegisterLiability records a facility's day-one decommission liability
	// (micro-pounds).
	RegisterLiability(facilityKey string, costMicropounds int64) error
}

// Chain-stage and facility names, matching data/refinery.json — string
// taxonomy, not balance figures.
const (
	commodityFuel           = "fuel"
	commodityFeedstock      = "feedstock"
	commodityCrudeOil       = "crude_oil"
	commodityPlastics       = "plastics"
	commodityRefinedProduct = "refined_product"

	hazmatFireCategory = "hazmat_fire"
	hazmatPurpose      = "refinery.hazmat"

	facilityRefinery = "refinery"
)

// fileRefinery is data/refinery.json's filename, relative to the resolved data
// directory (see the data package's ResolveDataDir).
const fileRefinery = "refinery.json"

// refineryFacilityKeys is the canonical ordered manifest of the two modelled
// facilities. The keys are the SAME ids data/buildings.json uses, so the
// catalogue extends (never forks) the existing rows (GR#3, AC-1).
var refineryFacilityKeys = []string{"refinery", "petrochemical_works"}

func isRefineryFacilityKey(key string) bool {
	for _, k := range refineryFacilityKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ChainOutput is one chain-stage output: a commodity and its production rate.
type ChainOutput struct {
	Commodity    string
	TonnesPerDay int64
}

// FacilityProfile is one refinery-catalogue facility's resolved, data-sourced
// behavioural parameter set (AC-1): footprint, throughput, jobs, utility draw,
// capex/opex, the chain input/output pair, and the hazmat-fire risk. blight,
// unlock and section are deliberately ABSENT — they stay single-sourced in
// data/buildings.json (GR#3).
type FacilityProfile struct {
	Key                    string
	Name                   string
	FootprintCells         int64
	ThroughputTonnesPerDay int64
	Jobs                   int64
	PowerKWhPerDay         int64
	WaterLitresPerDay      int64
	CapexMicropounds       int64
	OpexMicropoundsPerDay  int64
	CapexAmortisationDays  int64
	InputCommodity         string
	Outputs                []ChainOutput
	HazmatSeverity         int
	HazmatFirePeriodDays   int64
}

// Output returns the profile's declared output rate for commodity, and whether
// it is declared (AC-5's chain output lookup).
func (p FacilityProfile) Output(commodity string) (int64, bool) {
	for _, o := range p.Outputs {
		if o.Commodity == commodity {
			return o.TonnesPerDay, true
		}
	}
	return 0, false
}

// OperateResult is the outcome of one refinery operate tick (AC-4/AC-6/AC-7):
// the crude actually landed, the fuel and feedstock actually produced, and
// whether a hazmat-fire incident was reported this tick.
type OperateResult struct {
	CrudeLanded     int64
	FuelOutput      int64
	FeedstockOutput int64
	HazmatReported  bool
	HazmatSeverity  int
}

// Refinery is the facility surface: the refinery + petrochemical works
// catalogue, the make-vs-buy decision, and the crude→chain→fuel→risk wiring.
// The zero value is not usable; construct via LoadRefinery. A *Refinery is
// safe for concurrent use (mu guards every mutable field; checkNotCopied
// rejects a struct-copied value).
type Refinery struct {
	mu sync.RWMutex
	// buildMu serializes Build end-to-end so exactly one build is ever in
	// flight (SEC-170): it is held across the permit/decommission seams to
	// preserve the original build-once ordering (permit → liability → built),
	// but it is NEVER acquired by any read method (Built, Facility, Facilities,
	// Operate), so a seam that re-enters one of those cannot deadlock on it.
	buildMu       sync.Mutex
	correlationID string
	worldSeed     uint64

	order []string
	byKey map[string]FacilityProfile

	chem *ChemAPI

	freight  FreightAPI
	fuel     FuelAPI
	dispatch DispatchAPI
	permit   PermitAuthority
	decom    DecommissionRegistrar

	built bool

	self atomic.Pointer[Refinery]
}

// rawRefineryData is data/refinery.json's JSON wire shape, decoded only to be
// validated and folded into the profile catalogue.
type rawRefineryData struct {
	Version    int                    `json:"version"`
	Facilities map[string]rawFacility `json:"facilities"`
	Import     rawImport              `json:"import"`
}

type rawFacility struct {
	Name                   string           `json:"name"`
	FootprintCells         int64            `json:"footprintCells"`
	ThroughputTonnesPerDay int64            `json:"throughputTonnesPerDay"`
	Jobs                   int64            `json:"jobs"`
	PowerKWhPerDay         int64            `json:"powerKWhPerDay"`
	WaterLitresPerDay      int64            `json:"waterLitresPerDay"`
	CapexMicropounds       int64            `json:"capexMicropounds"`
	OpexMicropoundsPerDay  int64            `json:"opexMicropoundsPerDay"`
	CapexAmortisationDays  int64            `json:"capexAmortisationDays"`
	InputCommodity         string           `json:"inputCommodity"`
	Outputs                []rawChainOutput `json:"outputs"`
	HazmatSeverity         int              `json:"hazmatSeverity"`
	HazmatFirePeriodDays   int64            `json:"hazmatFirePeriodDays"`
	Disclosure             string           `json:"disclosure"`
}

type rawChainOutput struct {
	Commodity    string `json:"commodity"`
	TonnesPerDay int64  `json:"tonnesPerDay"`
}

type rawImport struct {
	Commodity                 string `json:"commodity"`
	MarginMicropoundsPerTonne int64  `json:"marginMicropoundsPerTonne"`
	Disclosure                string `json:"disclosure"`
}

// refineryData is the validated, folded form of data/refinery.json.
type refineryData struct {
	order           []string
	byKey           map[string]FacilityProfile
	importCommodity string
	importMargin    int64
}

// LoadRefinery reads data/refinery.json from dir, validates it, registers the
// two stages against a fresh ChemAPI, and returns a ready *Refinery. The seams
// start unset — wire them with the Wire* methods before Build/Operate.
func LoadRefinery(dir, correlationID string, worldSeed uint64) (*Refinery, error) {
	data, err := loadRefineryData(filepath.Join(dir, fileRefinery), correlationID)
	if err != nil {
		return nil, err
	}

	chem := NewChemAPI(correlationID)
	for _, key := range data.order {
		p := data.byKey[key]
		inputs := map[string]int64{p.InputCommodity: p.ThroughputTonnesPerDay}
		if err := chem.RegisterStage(key, inputs, outputsToMap(p.Outputs)); err != nil {
			return nil, err
		}
	}
	if err := chem.SetImportMargin(data.importCommodity, data.importMargin); err != nil {
		return nil, err
	}

	r := &Refinery{
		correlationID: correlationID,
		worldSeed:     worldSeed,
		order:         data.order,
		byKey:         data.byKey,
		chem:          chem,
	}
	r.self.Store(r) // armed exactly once, before r escapes (SEC-020)
	return r, nil
}

func outputsToMap(outputs []ChainOutput) map[string]int64 {
	m := make(map[string]int64, len(outputs))
	for _, o := range outputs {
		m[o.Commodity] = o.TonnesPerDay
	}
	return m
}

// checkNotCopied rejects a method call on a struct-copied *Refinery. Lock-free
// (a single atomic.Pointer.Load), so it is safe to run before mu is touched.
func (r *Refinery) checkNotCopied(method string) error {
	if r.self.Load() != r {
		return errs.New(ErrRefineryCopied, r.correlationID, map[string]any{"method": method})
	}
	return nil
}

// Chem returns the ChemAPI this facility's stages are registered against
// (AC-5's chain surface). The returned *ChemAPI is the live, shared chain — its
// own methods are individually copy-guarded, but a caller holding a
// struct-copied *Refinery must never be handed the ORIGINAL's chain pointer
// (mutations through it would hit the original's registered stages and import
// margins). This accessor is therefore itself copy-guarded: a struct-copied
// value is rejected with ErrRefineryCopied rather than leaking the original's
// live chain (SEC-164).
func (r *Refinery) Chem() (*ChemAPI, error) {
	if err := r.checkNotCopied("Chem"); err != nil {
		return nil, err
	}
	return r.chem, nil
}

// Facility resolves one facility to its distinct profile. An unknown key is
// ErrUnknownRefineryFacility — never a silently-created default profile.
func (r *Refinery) Facility(key string) (FacilityProfile, error) {
	if err := r.checkNotCopied("Facility"); err != nil {
		return FacilityProfile{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byKey[key]
	if !ok {
		return FacilityProfile{}, errs.New(ErrUnknownRefineryFacility, r.correlationID, map[string]any{"facility": key})
	}
	return snapshotProfile(p), nil
}

// Facilities returns every facility in manifest order (deterministic, GR#21),
// each a deep copy so callers cannot mutate the catalogue. A struct-copied
// value is rejected with ErrRefineryCopied rather than returning a plausible
// empty slice (SEC-136 sentinel class).
func (r *Refinery) Facilities() ([]FacilityProfile, error) {
	if err := r.checkNotCopied("Facilities"); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]FacilityProfile, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, snapshotProfile(r.byKey[key]))
	}
	return out, nil
}

func snapshotProfile(p FacilityProfile) FacilityProfile {
	p.Outputs = append([]ChainOutput(nil), p.Outputs...)
	return p
}

// WireFreight wires the engine.freight crude-landing seam (AC-4). Call before
// Operate; nil rejects every operate as not-wired. A struct-copied value is
// rejected with ErrRefineryCopied rather than silently wiring a dead copy
// (SEC-160).
func (r *Refinery) WireFreight(f FreightAPI) error {
	if err := r.checkNotCopied("WireFreight"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freight = f
	return nil
}

// WireFuel wires the engine.fuel supply seam (AC-6). Call before Operate; nil
// rejects every operate as not-wired. A struct-copied value is rejected with
// ErrRefineryCopied rather than silently wiring a dead copy (SEC-160).
func (r *Refinery) WireFuel(f FuelAPI) error {
	if err := r.checkNotCopied("WireFuel"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fuel = f
	return nil
}

// WireDispatch wires the engine.dispatch incident seam (AC-7). Call before
// Operate; nil leaves the hazmat-fire report unwired (skipped, not crashed).
// A struct-copied value is rejected with ErrRefineryCopied rather than
// silently wiring a dead copy (SEC-160).
func (r *Refinery) WireDispatch(d DispatchAPI) error {
	if err := r.checkNotCopied("WireDispatch"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dispatch = d
	return nil
}

// WirePermit wires the feat.facilitypermits permit-authority seam (AC-8).
// Call before Build; nil rejects every build as not-wired. A struct-copied
// value is rejected with ErrRefineryCopied rather than silently wiring a dead
// copy (SEC-160).
func (r *Refinery) WirePermit(p PermitAuthority) error {
	if err := r.checkNotCopied("WirePermit"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.permit = p
	return nil
}

// WireDecommission wires the feat.decommission liability-registrar seam
// (AC-8). Call before Build; nil rejects every build as not-wired. A
// struct-copied value is rejected with ErrRefineryCopied rather than silently
// wiring a dead copy (SEC-160).
func (r *Refinery) WireDecommission(d DecommissionRegistrar) error {
	if err := r.checkNotCopied("WireDecommission"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decom = d
	return nil
}

// Build provisions the refinery (AC-8): permit-gated through the PermitAuthority
// seam and carrying a day-one decommission liability through the
// DecommissionRegistrar seam — neither obligation is reimplemented locally, and
// no permit-state or liability-provision field lives on this struct. The
// liability figure is the profile's capex (a data-driven placeholder proxy).
//
// Concurrency (SEC-170): Build must not hold r.mu across a seam call. The
// permit/decommission seams are external modules that may call back into any
// Refinery read method (Built, Facility, Facilities, Operate) — all of which
// take r.mu — so holding r.mu across the seam would deadlock a re-entrant seam
// permanently, wedging the whole facility surface. Build therefore snapshots
// what it needs under RLock, releases r.mu, and only then invokes the seams,
// mirroring Operate. Build itself is serialized by the dedicated buildMu
// (never acquired by any read method), which preserves the original end-to-end
// build-once semantics: exactly one Build is in flight, the liability is
// registered exactly once, and built is set exactly once, in the order permit
// check → register liability → set built, so a seam failure leaves no partial
// state (SEC-162 class).
func (r *Refinery) Build(milestone int) error {
	if err := r.checkNotCopied("Build"); err != nil {
		return err
	}
	r.buildMu.Lock()
	defer r.buildMu.Unlock()

	// Snapshot under RLock, release before any seam call (SEC-170, FEAT-135).
	r.mu.RLock()
	built := r.built
	p, ok := r.byKey[facilityRefinery]
	permit := r.permit
	decom := r.decom
	r.mu.RUnlock()

	if built {
		return errs.New(ErrRefineryBuildRejected, r.correlationID, map[string]any{"reason": "already built"})
	}
	if !ok {
		return errs.New(ErrUnknownRefineryFacility, r.correlationID, map[string]any{"facility": facilityRefinery})
	}
	if permit == nil {
		return errs.New(ErrRefineryNotWired, r.correlationID, map[string]any{"edge": "permit"})
	}
	granted, err := permit.PermitGranted(facilityRefinery, milestone)
	if err != nil {
		return err
	}
	if !granted {
		return errs.New(ErrRefineryBuildRejected, r.correlationID, map[string]any{"reason": "permit not granted"})
	}
	if decom == nil {
		return errs.New(ErrRefineryNotWired, r.correlationID, map[string]any{"edge": "decommission"})
	}
	if err := decom.RegisterLiability(facilityRefinery, p.CapexMicropounds); err != nil {
		return err
	}

	// buildMu serializes Build, so this is the sole writer of built; r.mu guards
	// the field for the readers (Built, Operate, DomesticUnitCost).
	r.mu.Lock()
	r.built = true
	r.mu.Unlock()
	return nil
}

// Built reports whether the refinery has been built. A struct-copied value is
// rejected with ErrRefineryCopied rather than returning a plausible false
// (SEC-136 sentinel class).
func (r *Refinery) Built() (bool, error) {
	if err := r.checkNotCopied("Built"); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.built, nil
}

// Operate runs one refinery tick (AC-4/AC-6/AC-7): it draws crude through the
// freight seam, scales fuel and feedstock output proportionally with the crude
// actually landed, feeds the fuel output to the fuel system, and reports a
// hazmat-fire incident to dispatch when the deterministic risk draw fires. A
// not-built refinery is ErrRefineryNotBuilt and emits nothing.
func (r *Refinery) Operate(tick int64, crudeTonnes int64) (OperateResult, error) {
	if err := r.checkNotCopied("Operate"); err != nil {
		return OperateResult{}, err
	}
	r.mu.RLock()
	built := r.built
	freight := r.freight
	fuel := r.fuel
	dispatch := r.dispatch
	p, ok := r.byKey[facilityRefinery]
	r.mu.RUnlock()

	if !built {
		return OperateResult{}, errs.New(ErrRefineryNotBuilt, r.correlationID, nil)
	}
	if !ok {
		return OperateResult{}, errs.New(ErrUnknownRefineryFacility, r.correlationID, map[string]any{"facility": facilityRefinery})
	}
	if freight == nil {
		return OperateResult{}, errs.New(ErrRefineryNotWired, r.correlationID, map[string]any{"edge": "freight"})
	}
	if fuel == nil {
		return OperateResult{}, errs.New(ErrRefineryNotWired, r.correlationID, map[string]any{"edge": "fuel"})
	}
	// A negative crude request is rejected at the boundary rather than passed to
	// the freight seam, which would report a nonsensical negative landing a
	// downstream accounting consumer would record (SEC-165). Zero is a valid
	// "no crude this tick" request, mirroring ImportRefined's tonnes < 0 rule.
	if crudeTonnes < 0 {
		return OperateResult{}, refineryDataInvalid(r.correlationID, fmt.Sprintf("negative crude tonnes %d", crudeTonnes))
	}

	landed, err := freight.CrudeLanding(crudeTonnes)
	if err != nil {
		return OperateResult{}, err
	}
	// The freight seam's contract forbids a negative landing (SEC-165), but the
	// seam is a trust boundary: a negative landing is rejected rather than
	// recorded, so CrudeLanded always lies within 0..throughput (SEC-166).
	if landed < 0 {
		return OperateResult{}, refineryDataInvalid(r.correlationID, fmt.Sprintf("negative crude landed %d", landed))
	}
	// Throughput is the facility's processing capacity cap, not merely a
	// scaling denominator: whatever the freight seam lands, a single refinery
	// can never emit beyond its rated output (SEC-134). CrudeLanded reports the
	// tonnage actually processed, not the port's raw landing.
	if landed > p.ThroughputTonnesPerDay {
		landed = p.ThroughputTonnesPerDay
	}

	// buildFacilityProfile guarantees the refinery declares both structural
	// outputs (SEC-168); this second check is defense-in-depth at the point of
	// use, mirroring SEC-132's loader-plus-setter same-domain precedent — a
	// profile lacking either is rejected, never silently fed as zero.
	fuelRate, fuelOK := p.Output(commodityFuel)
	feedRate, feedOK := p.Output(commodityFeedstock)
	if !fuelOK || !feedOK {
		return OperateResult{}, errs.New(ErrRefineryDataInvalid, r.correlationID, map[string]any{
			"facility": facilityRefinery, "cause": "structural output missing",
		})
	}
	fuelOut, fuelOverflow := scaleTonnes(landed, fuelRate, p.ThroughputTonnesPerDay)
	feedOut, feedOverflow := scaleTonnes(landed, feedRate, p.ThroughputTonnesPerDay)
	if fuelOverflow || feedOverflow {
		return OperateResult{}, errs.New(ErrRefineryDataInvalid, r.correlationID, map[string]any{
			"facility": facilityRefinery, "cause": "scaled output overflow",
		})
	}
	res := OperateResult{
		CrudeLanded:     landed,
		FuelOutput:      fuelOut,
		FeedstockOutput: feedOut,
	}

	if err := fuel.Supply(res.FuelOutput); err != nil {
		return OperateResult{}, err
	}

	if dispatch != nil && p.HazmatFirePeriodDays > 0 && r.HazmatRisk(tick)%p.HazmatFirePeriodDays == 0 {
		if err := dispatch.ReportIncident(hazmatFireCategory, p.HazmatSeverity); err != nil {
			return OperateResult{}, err
		}
		res.HazmatReported = true
		res.HazmatSeverity = p.HazmatSeverity
	}
	return res, nil
}

// HazmatRisk returns the refinery's deterministic hazmat-fire risk draw for a
// tick: a value that is a pure function of (worldSeed, tick) — never the wall
// clock and never a shared/global RNG source (GR#21). Operate compares it
// against the data-driven fire period to decide whether an incident is
// reported this tick.
//
// This is a deliberately UNGUARDED exported method (the SEC-164 enumeration's
// documented pure-read exception): it reads only worldSeed — an immutable value
// field set once in LoadRefinery and never written again — plus its tick
// argument and a package constant, and exposes no shared pointer, map, slice or
// mutex state. A struct-copied Refinery's HazmatRisk therefore returns the same
// value as the original's with no mutation channel, so there is no shared-state
// exposure to guard against.
func (r *Refinery) HazmatRisk(tick int64) int64 {
	s := det.NewStream(r.worldSeed, 0, tick, hazmatPurpose)
	return s.Int63()
}

// scaleTonnes scales an output rate by the proportion of crude actually
// landed: landed * rate / throughput, using the project's saturating multiply
// (GR#16). A zero/negative input yields zero output (no output without input).
// The returned bool reports whether the intermediate product overflowed int64;
// a caller must reject it rather than emit a saturated output as if it were
// exact (SEC-137 class).
func scaleTonnes(landed, rate, throughput int64) (int64, bool) {
	if landed <= 0 || rate <= 0 || throughput <= 0 {
		return 0, false
	}
	prod, overflow := num.SafeMul(landed, rate)
	return prod / throughput, overflow
}

// DomesticUnitCost returns the refinery's per-tonne cost of domestic refined
// product once built: (capex amortised per day + opex per day) / throughput.
// It is the "build" leg of the make-vs-buy comparison (AC-3) — a pure function
// of the data file, never a pinned literal.
func (r *Refinery) DomesticUnitCost(throughput int64) (int64, error) {
	if err := r.checkNotCopied("DomesticUnitCost"); err != nil {
		return 0, err
	}
	r.mu.RLock()
	built := r.built
	p, ok := r.byKey[facilityRefinery]
	r.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrRefineryNotBuilt, r.correlationID, nil)
	}
	if !ok {
		return 0, errs.New(ErrUnknownRefineryFacility, r.correlationID, map[string]any{"facility": facilityRefinery})
	}
	if throughput <= 0 {
		return 0, refineryDataInvalid(r.correlationID, fmt.Sprintf("non-positive throughput %d", throughput))
	}
	capexPerDay := p.CapexMicropounds / p.CapexAmortisationDays
	totalPerDay := num.SatAdd(capexPerDay, p.OpexMicropoundsPerDay)
	return totalPerDay / throughput, nil
}

// ImportUnitCost returns the import-at-margin unit cost of refined product,
// read through the registered ChemAPI import surface (MOD-063's path, not a
// refinery-local import-price table) — the "import" leg of the make-vs-buy
// comparison (AC-3). Available with no refinery built.
func (r *Refinery) ImportUnitCost() (int64, error) {
	if err := r.checkNotCopied("ImportUnitCost"); err != nil {
		return 0, err
	}
	margin, ok, err := r.chem.ImportMargin(commodityRefinedProduct)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, refineryDataInvalid(r.correlationID, fmt.Sprintf("unknown commodity %q", commodityRefinedProduct))
	}
	return margin, nil
}

// ImportRefined returns the cost (micro-pounds) to import tonnes of a refined
// commodity at the documented margin — the always-available make-vs-buy import
// path, usable with no refinery built (AC-3). It delegates to the ChemAPI
// import surface, never a refinery-local price table.
func (r *Refinery) ImportRefined(commodity string, tonnes int64) (int64, error) {
	if err := r.checkNotCopied("ImportRefined"); err != nil {
		return 0, err
	}
	return r.chem.ImportRefined(commodity, tonnes)
}

// loadRefineryData reads, decodes and validates data/refinery.json from path.
// Every failure is a registry-sourced *errs.E — never a panic, never a silent
// default substitution, never a partially-populated catalogue (AC-9).
func loadRefineryData(path, correlationID string) (refineryData, error) {
	var zero refineryData
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrRefineryDataInvalid, correlationID, err, map[string]any{
			"path": path, "cause": err.Error(),
		})
	}
	var raw rawRefineryData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrRefineryDataInvalid, correlationID, err, map[string]any{
			"path": path, "cause": err.Error(),
		})
	}
	return buildRefineryData(raw, path, correlationID)
}

// buildRefineryData folds the decoded raw data into an ordered, validated
// catalogue. The canonical order is the manifest order (not a Go map), so
// Facilities is deterministic (GR#21). Validation is all-or-nothing (AC-9).
func buildRefineryData(raw rawRefineryData, path, correlationID string) (refineryData, error) {
	fail := func(field, rule string) (refineryData, error) {
		return refineryData{}, errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
			"path": path, "field": field, "rule": rule, "cause": field + ": " + rule,
		})
	}
	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if len(raw.Facilities) != len(refineryFacilityKeys) {
		return fail("facilities", "must contain exactly the two modelled facilities (no missing, no extra)")
	}
	for _, key := range refineryFacilityKeys {
		if _, ok := raw.Facilities[key]; !ok {
			return fail("facilities."+key, "missing modelled facility")
		}
	}
	for key := range raw.Facilities {
		if !isRefineryFacilityKey(key) {
			return fail("facilities."+key, "unrecognised facility key")
		}
	}

	byKey := make(map[string]FacilityProfile, len(refineryFacilityKeys))
	for _, key := range refineryFacilityKeys {
		p, err := buildFacilityProfile(key, raw.Facilities[key], path, correlationID)
		if err != nil {
			return refineryData{}, err
		}
		byKey[key] = p
	}

	if raw.Import.Commodity == "" {
		return fail("import.commodity", "required, must name the refined commodity")
	}
	if raw.Import.MarginMicropoundsPerTonne <= 0 {
		return fail("import.marginMicropoundsPerTonne", "must be a positive unit cost")
	}
	if raw.Import.Disclosure == "" {
		return fail("import.disclosure", "required, non-empty disclosure")
	}

	return refineryData{
		order:           append([]string(nil), refineryFacilityKeys...),
		byKey:           byKey,
		importCommodity: raw.Import.Commodity,
		importMargin:    raw.Import.MarginMicropoundsPerTonne,
	}, nil
}

// buildFacilityProfile validates and folds one facility entry (AC-9: a missing
// throughput figure, a negative utility draw, an unrecognised stage name, or a
// missing disclosure is rejected — never default-substituted).
func buildFacilityProfile(key string, rf rawFacility, path, correlationID string) (FacilityProfile, error) {
	fail := func(field, rule string) (FacilityProfile, error) {
		return FacilityProfile{}, errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
			"path": path, "field": field, "rule": rule, "cause": field + ": " + rule,
		})
	}
	if rf.Name == "" {
		return fail("facilities."+key+".name", "required, non-empty name")
	}
	if rf.FootprintCells <= 0 {
		return fail("facilities."+key+".footprintCells", "must be a positive cell count")
	}
	if rf.ThroughputTonnesPerDay <= 0 {
		return fail("facilities."+key+".throughputTonnesPerDay", "must be a positive t/day rate (missing or zero throughput)")
	}
	if rf.Jobs < 0 {
		return fail("facilities."+key+".jobs", "must be >= 0")
	}
	if rf.PowerKWhPerDay < 0 {
		return fail("facilities."+key+".powerKWhPerDay", "must be >= 0 (negative utility draw)")
	}
	if rf.WaterLitresPerDay < 0 {
		return fail("facilities."+key+".waterLitresPerDay", "must be >= 0 (negative utility draw)")
	}
	if rf.CapexMicropounds <= 0 {
		return fail("facilities."+key+".capexMicropounds", "must be a positive build cost")
	}
	if rf.OpexMicropoundsPerDay < 0 {
		return fail("facilities."+key+".opexMicropoundsPerDay", "must be >= 0")
	}
	if rf.CapexAmortisationDays <= 0 {
		return fail("facilities."+key+".capexAmortisationDays", "must be a positive payback horizon")
	}
	if rf.InputCommodity == "" {
		return fail("facilities."+key+".inputCommodity", "required, non-empty input commodity")
	}
	if len(rf.Outputs) == 0 {
		return fail("facilities."+key+".outputs", "must have at least one output")
	}
	seen := make(map[string]bool, len(rf.Outputs))
	for _, o := range rf.Outputs {
		if o.Commodity == "" {
			return fail("facilities."+key+".outputs.commodity", "must be a non-empty commodity")
		}
		if o.TonnesPerDay <= 0 {
			return fail("facilities."+key+".outputs."+o.Commodity+".tonnesPerDay", "must be a positive t/day rate")
		}
		if seen[o.Commodity] {
			return fail("facilities."+key+".outputs."+o.Commodity, "duplicate output commodity (each commodity may be declared once)")
		}
		seen[o.Commodity] = true
	}
	// The refinery facility's two structural outputs — fuel (feeds the fuel
	// system, AC-6) and feedstock (feeds the petrochemical works, AC-5) — are
	// required, not optional: Operate reads both, and a facility that omits one
	// would silently emit zero for it (SEC-168). Rejected at load, never
	// default-substituted (AC-9, FEAT-135).
	if key == facilityRefinery {
		for _, required := range []string{commodityFuel, commodityFeedstock} {
			if !seen[required] {
				return fail("facilities."+key+".outputs."+required, "required structural output commodity missing (the refinery must declare its fuel and feedstock outputs)")
			}
		}
	}
	if rf.HazmatSeverity < 0 {
		return fail("facilities."+key+".hazmatSeverity", "must be >= 0")
	}
	if rf.HazmatFirePeriodDays <= 0 {
		return fail("facilities."+key+".hazmatFirePeriodDays", "must be a positive fire period")
	}
	if rf.Disclosure == "" {
		return fail("facilities."+key+".disclosure", "required, non-empty placeholder disclosure naming it pending the balance pass")
	}

	outputs := make([]ChainOutput, 0, len(rf.Outputs))
	for _, o := range rf.Outputs {
		outputs = append(outputs, ChainOutput(o))
	}

	return FacilityProfile{
		Key:                    key,
		Name:                   rf.Name,
		FootprintCells:         rf.FootprintCells,
		ThroughputTonnesPerDay: rf.ThroughputTonnesPerDay,
		Jobs:                   rf.Jobs,
		PowerKWhPerDay:         rf.PowerKWhPerDay,
		WaterLitresPerDay:      rf.WaterLitresPerDay,
		CapexMicropounds:       rf.CapexMicropounds,
		OpexMicropoundsPerDay:  rf.OpexMicropoundsPerDay,
		CapexAmortisationDays:  rf.CapexAmortisationDays,
		InputCommodity:         rf.InputCommodity,
		Outputs:                outputs,
		HazmatSeverity:         rf.HazmatSeverity,
		HazmatFirePeriodDays:   rf.HazmatFirePeriodDays,
	}, nil
}
