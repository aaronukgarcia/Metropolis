package mining

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GENERAL blight model (MOD-046 core, AC-1..AC-8): the
// BlightAPI registration surface + the EffectAt query, computed from engine
// world's real elevation (viewshed) and a genuine distance-based dBA-falloff
// (noise). It is the "any module registers blighting objects; viewshed
// computed from WorldAPI elevation" half of code.json's engine.mining inbound
// contract.
//
// # The two mechanically-distinct components (AC-4/AC-5)
//
//   - HEARD (noise): a dBA-falloff-shaped power curve of distance only. It
//     never reads elevation — two home cells at identical distance from the
//     same source hear identically, no matter what high ground lies between.
//   - SEEN (viewshed): a genuine line-of-sight test against WorldAPI.CellAt's
//     real per-cell Elevation along the straight path from the blighting
//     object to the home cell. Intervening higher ground occludes the object,
//     continuously (occlusion rises with how far the ground pokes above the
//     sight-line), so two equidistant home cells can receive genuinely
//     different seen outcomes — exactly the fixture AC-4 is written against.
//
// Both stack (AC-6): EffectAt returns Heard and Seen separately, and the
// combined satisfaction penalty is their sum, never the larger of the two.

// BlightEffect is EffectAt's answer for one home cell: the two stacked
// components. Heard is the mental-health/satisfaction hit from noise; Seen is
// the land-value + satisfaction hit from visual exposure.
type BlightEffect struct {
	Heard float64
	Seen  float64
}

// Combined returns the stacked satisfaction penalty — Heard + Seen, never
// max(Heard, Seen) (AC-6: "both stack").
func (e BlightEffect) Combined() float64 { return e.Heard + e.Seen }

// ReclaimOption is the reclamation outcome for an exhausted site (AC-9).
// The landfill-void option is deliberately ABSENT — the engine.refuse edge it
// would hand off to does not exist (BUG-058 closed with engine.refuse↔
// engine.farming instead; the mining↔refuse pair is now governed by the
// collaborations gate). See doc.go.
type ReclaimOption uint8

const (
	ReclaimLake ReclaimOption = iota
	ReclaimPark
)

// String returns the canonical reclamation option name.
func (r ReclaimOption) String() string {
	switch r {
	case ReclaimLake:
		return "lake"
	case ReclaimPark:
		return "park"
	default:
		return "unrecognised"
	}
}

// blightingObject is one registered blighting object's mutable state. Every
// field is a value (no slices/maps), so a struct copy under lock is a safe
// snapshot.
type blightingObject struct {
	key             string
	class           BlightClass
	tile            world.TileCoord
	local           world.CellLocal
	hasLoc          bool
	noiseRadiusM    int64
	visualHeightM   float64
	visualMagnitude float64
	enclosure       bool
	nightBan        bool
}

// occluderKind distinguishes the two real-earthwork mitigations (AC-7).
type occluderKind uint8

const (
	occluderBund occluderKind = iota
	occluderTreeBelt
)

// occluder is a real earthwork inserted into the SAME line-of-sight path the
// ridge exercises (AC-7): a bund occludes immediately, a tree belt grows in
// over data/mining.json's grow-in delay.
type occluder struct {
	kind        occluderKind
	heightM     float64
	plantedYear int64 // tree belts only (bunds are always mature)
}

// occluderKey identifies the cell an occluder sits on.
type occluderKey struct {
	tile  world.TileCoord
	local world.CellLocal
}

// BlightAPI is code.json's "engine.mining" inbound contract: the general
// blight model plus geology-gated extraction siting (site.go). The zero value
// is not usable; construct via [NewBlightAPI]. A *BlightAPI is safe for
// concurrent use (AC-14): all mutable state is guarded by mu, and
// checkNotCopied rejects a method call on a struct-copied value (SEC-020
// family, mirroring DepositMap).
type BlightAPI struct {
	correlationID string
	world         *world.WorldAPI
	cfg           BlightConfig

	mu        sync.RWMutex
	catalogue Catalogue // feat.minetypes' catalogue, wired via SetCatalogue
	objects   map[string]*blightingObject
	occluders map[occluderKey]occluder
	sites     map[string]*extractionSite

	self atomic.Pointer[BlightAPI]
}

// NewBlightAPI constructs a BlightAPI over w and a validated BlightConfig
// (from LoadBlightConfig). w is the same *world.WorldAPI whose cells the
// viewshed reads via CellAt — there is no mining-local terrain copy (GR#3).
func NewBlightAPI(w *world.WorldAPI, cfg BlightConfig, correlationID string) (*BlightAPI, error) {
	if w == nil {
		return nil, errs.New(ErrBlightDataInvalid, correlationID, map[string]any{"cause": "nil world"})
	}
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := validateBlightConfig(cfg, correlationID); err != nil {
		return nil, err
	}
	b := &BlightAPI{
		correlationID: correlationID,
		world:         w,
		cfg:           cfg,
		objects:       make(map[string]*blightingObject),
		occluders:     make(map[occluderKey]occluder),
		sites:         make(map[string]*extractionSite),
	}
	b.self.Store(b)
	return b, nil
}

// checkNotCopied rejects a method call on a struct-copied *BlightAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to call before mu is ever touched (mirroring DepositMap.checkNotCopied).
func (b *BlightAPI) checkNotCopied(method string) error {
	if b.self.Load() != b {
		return errs.New(ErrBlightCopied, b.correlationID, map[string]any{"method": method})
	}
	return nil
}

// validClass reports whether class is one of the four recognised ordinals.
func validClass(class BlightClass) bool { return class >= BlightLow && class <= BlightSevere }

// SetCatalogue wires feat.minetypes' mine-type catalogue into the siting
// surface (AC-2). The catalogue is immutable after LoadMineTypes, so it is
// stored by value and read under mu for the same reason as every other field.
func (b *BlightAPI) SetCatalogue(c Catalogue) error {
	if err := b.checkNotCopied("SetCatalogue"); err != nil {
		return err
	}
	b.mu.Lock()
	b.catalogue = c
	b.mu.Unlock()
	return nil
}

// RegisterBlightingObject registers (or re-registers) a blighting object by
// key with its blight class and noise contour radius — the exact
// engine.airport BlightRegistrar seam (AC-1): an idempotent UPSERT, so a
// re-register of an existing key atomically REPLACES that object's profile,
// never a duplicate-key error, never a stacked second contour. The visual
// height/magnitude are derived from the class's data-sourced profile; the
// object has no location yet, so it contributes nothing to EffectAt until
// [SetBlightLocation] attaches one.
func (b *BlightAPI) RegisterBlightingObject(objectKey string, class BlightClass, contourRadiusM int64) error {
	if err := b.checkNotCopied("RegisterBlightingObject"); err != nil {
		return err
	}
	if objectKey == "" {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{"field": "objectKey", "rule": "must be non-empty"})
	}
	if !validClass(class) {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{"field": "class", "value": class})
	}
	if contourRadiusM <= 0 {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "contourRadiusM", "value": contourRadiusM, "rule": "must be positive",
		})
	}
	prof, ok := b.cfg.classProfile(class)
	if !ok {
		return errs.New(ErrBlightDataInvalid, b.correlationID, map[string]any{"class": class})
	}
	obj := &blightingObject{
		key:             objectKey,
		class:           class,
		noiseRadiusM:    contourRadiusM,
		visualHeightM:   prof.VisualHeightM,
		visualMagnitude: prof.Magnitude,
	}
	b.mu.Lock()
	b.objects[objectKey] = obj
	b.mu.Unlock()
	return nil
}

// BlightingObjectSpec is the full-profile registration (AC-1's "location +
// noise/visual profile"): every field is explicit — no class-derived default
// hides behind a zero value.
type BlightingObjectSpec struct {
	Key             string
	Class           BlightClass
	Tile            world.TileCoord
	Local           world.CellLocal
	NoiseRadiusM    int64
	VisualHeightM   float64
	VisualMagnitude float64
}

// PlaceBlightingObject registers a blighting object with an explicit location
// and noise/visual profile (AC-1). It is the registration path the mining
// siting command uses for objects that have a known cell from the start; the
// airport uses [RegisterBlightingObject] + [SetBlightLocation] because its
// seam predates location. A re-register of an existing key replaces that
// object atomically.
func (b *BlightAPI) PlaceBlightingObject(spec BlightingObjectSpec) error {
	if err := b.checkNotCopied("PlaceBlightingObject"); err != nil {
		return err
	}
	if spec.Key == "" {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{"field": "key", "rule": "must be non-empty"})
	}
	if !validClass(spec.Class) {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{"field": "class", "value": spec.Class})
	}
	if !spec.Tile.InExtent() || !spec.Local.InBounds() {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "tile/local", "tile": spec.Tile, "local": spec.Local, "rule": "out of extent",
		})
	}
	if spec.NoiseRadiusM <= 0 {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "noiseRadiusM", "value": spec.NoiseRadiusM, "rule": "must be positive",
		})
	}
	if spec.VisualHeightM < 0 || math.IsNaN(spec.VisualHeightM) || math.IsInf(spec.VisualHeightM, 0) {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "visualHeightM", "value": spec.VisualHeightM, "rule": "must be finite and non-negative",
		})
	}
	if spec.VisualMagnitude <= 0 || spec.VisualMagnitude > 1 || math.IsNaN(spec.VisualMagnitude) || math.IsInf(spec.VisualMagnitude, 0) {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "visualMagnitude", "value": spec.VisualMagnitude, "rule": "must be in (0,1]",
		})
	}
	obj := &blightingObject{
		key:             spec.Key,
		class:           spec.Class,
		tile:            spec.Tile,
		local:           spec.Local,
		hasLoc:          true,
		noiseRadiusM:    spec.NoiseRadiusM,
		visualHeightM:   spec.VisualHeightM,
		visualMagnitude: spec.VisualMagnitude,
	}
	b.mu.Lock()
	b.objects[spec.Key] = obj
	b.mu.Unlock()
	return nil
}

// SetBlightLocation attaches (or moves) a location onto an already-registered
// object — the airport's composition-root wiring calls this after the airport
// is placed. The object contributes to EffectAt only once it has a location.
func (b *BlightAPI) SetBlightLocation(objectKey string, tile world.TileCoord, local world.CellLocal) error {
	if err := b.checkNotCopied("SetBlightLocation"); err != nil {
		return err
	}
	if !tile.InExtent() || !local.InBounds() {
		return errs.New(ErrBlightProfileInvalid, b.correlationID, map[string]any{
			"field": "tile/local", "tile": tile, "local": local, "rule": "out of extent",
		})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	obj, ok := b.objects[objectKey]
	if !ok {
		return errs.New(ErrUnknownBlightKey, b.correlationID, map[string]any{"key": objectKey})
	}
	obj.tile = tile
	obj.local = local
	obj.hasLoc = true
	return nil
}

// AddBund places a screening bund — a real earthwork at a cell with the given
// full height — into the SAME line-of-sight computation the escarpment uses
// (AC-7). It is NOT a percentage multiplier on the final blight number: it
// raises the effective ground elevation at that cell for every viewshed pass.
func (b *BlightAPI) AddBund(tile world.TileCoord, local world.CellLocal, heightM float64, correlationID string) error {
	if err := b.checkNotCopied("AddBund"); err != nil {
		return err
	}
	if !tile.InExtent() || !local.InBounds() {
		return errs.New(ErrBlightQueryOutOfBounds, correlationID, map[string]any{"tile": tile, "local": local})
	}
	if heightM <= 0 || math.IsNaN(heightM) || math.IsInf(heightM, 0) {
		return errs.New(ErrBlightProfileInvalid, correlationID, map[string]any{"field": "heightM", "value": heightM, "rule": "must be finite and positive"})
	}
	b.mu.Lock()
	b.occluders[occluderKey{tile: tile, local: local}] = occluder{kind: occluderBund, heightM: heightM}
	b.mu.Unlock()
	return nil
}

// AddTreeBelt places a tree-belt occluder with a documented grow-in delay:
// plantedYear is the simulated year it was planted, and its effective height
// ramps linearly from 0 to heightM over data/mining.json's grow-in window
// (AC-7's "5-year grow-in"). The occlusion flows through the same viewshed
// path as any bund or natural ridge.
func (b *BlightAPI) AddTreeBelt(tile world.TileCoord, local world.CellLocal, heightM float64, plantedYear int64, correlationID string) error {
	if err := b.checkNotCopied("AddTreeBelt"); err != nil {
		return err
	}
	if !tile.InExtent() || !local.InBounds() {
		return errs.New(ErrBlightQueryOutOfBounds, correlationID, map[string]any{"tile": tile, "local": local})
	}
	if heightM <= 0 || math.IsNaN(heightM) || math.IsInf(heightM, 0) {
		return errs.New(ErrBlightProfileInvalid, correlationID, map[string]any{"field": "heightM", "value": heightM, "rule": "must be finite and positive"})
	}
	b.mu.Lock()
	b.occluders[occluderKey{tile: tile, local: local}] = occluder{kind: occluderTreeBelt, heightM: heightM, plantedYear: plantedYear}
	b.mu.Unlock()
	return nil
}

// SetEnclosure toggles the enclosure-building mitigation (AC-8): it reduces
// the HEARD component specifically and leaves the viewshed untouched.
func (b *BlightAPI) SetEnclosure(objectKey string, enclosed bool) error {
	if err := b.checkNotCopied("SetEnclosure"); err != nil {
		return err
	}
	return b.setNoiseMitigation("SetEnclosure", objectKey, func(o *blightingObject) { o.enclosure = enclosed })
}

// SetNightBan toggles the night-working-ban mitigation (AC-8): it reduces the
// HEARD component specifically and leaves the viewshed untouched.
func (b *BlightAPI) SetNightBan(objectKey string, banned bool) error {
	if err := b.checkNotCopied("SetNightBan"); err != nil {
		return err
	}
	return b.setNoiseMitigation("SetNightBan", objectKey, func(o *blightingObject) { o.nightBan = banned })
}

func (b *BlightAPI) setNoiseMitigation(method, objectKey string, apply func(*blightingObject)) error {
	if err := b.checkNotCopied(method); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	obj, ok := b.objects[objectKey]
	if !ok {
		return errs.New(ErrUnknownBlightKey, b.correlationID, map[string]any{"key": objectKey})
	}
	apply(obj)
	return nil
}

// EffectAt returns the stacked blight effect at a home cell from every
// located blighting object (AC-1/AC-4/AC-5/AC-6). It is a pure, deterministic
// read (AC-12): objects are summed in sorted key order, never map-iteration
// order, so repeated runs are byte-identical. year drives tree-belt grow-in.
func (b *BlightAPI) EffectAt(tile world.TileCoord, local world.CellLocal, year int64, correlationID string) (BlightEffect, error) {
	if err := b.checkNotCopied("EffectAt"); err != nil {
		return BlightEffect{}, err
	}
	if !tile.InExtent() || !local.InBounds() {
		return BlightEffect{}, errs.New(ErrBlightQueryOutOfBounds, correlationID, map[string]any{"tile": tile, "local": local})
	}

	// Snapshot value copies + a copy of the occluder map under the lock, then
	// read elevations (world.CellAt) outside it — the same snapshot-then-call
	// lock discipline engine.wellbeing uses for its seams.
	type snap struct {
		key string
		obj blightingObject
	}
	b.mu.RLock()
	snaps := make([]snap, 0, len(b.objects))
	for k, o := range b.objects {
		snaps = append(snaps, snap{key: k, obj: *o})
	}
	occluders := make(map[occluderKey]occluder, len(b.occluders))
	for k, v := range b.occluders {
		occluders[k] = v
	}
	b.mu.RUnlock()
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].key < snaps[j].key })

	hx, hy := globalCell(tile, local)
	homeElev, err := b.elevationAt(hx, hy, correlationID)
	if err != nil {
		return BlightEffect{}, err
	}

	var eff BlightEffect
	for _, s := range snaps {
		if !s.obj.hasLoc {
			continue // an unplaced object has no distance/LOS, hence no effect
		}
		ox, oy := globalCell(s.obj.tile, s.obj.local)
		d := distanceM(ox, oy, hx, hy)
		eff.Heard += b.heardPenalty(s.obj, d)
		seen, err := b.seenPenalty(s.obj, ox, oy, hx, hy, homeElev, year, occluders, correlationID)
		if err != nil {
			return BlightEffect{}, err
		}
		eff.Seen += seen
	}
	return eff, nil
}

// heardPenalty is the distance-only noise component (AC-5). It is a
// data-sourced power falloff, monotonically non-increasing in distance, and it
// never reads elevation — the two mechanically-distinct halves stay distinct.
func (b *BlightAPI) heardPenalty(o blightingObject, distanceM float64) float64 {
	prof := b.cfg.ClassProfile[o.class]
	d := distanceM
	if d < b.cfg.Noise.MinDistanceM {
		d = b.cfg.Noise.MinDistanceM
	}
	radius := float64(o.noiseRadiusM)
	var heard float64
	if d <= radius {
		heard = prof.Magnitude
	} else {
		heard = prof.Magnitude * math.Pow(radius/d, b.cfg.Noise.FalloffExponent)
	}
	if o.enclosure {
		heard *= 1 - b.cfg.Noise.EnclosureReduction
	}
	if o.nightBan {
		heard *= 1 - b.cfg.Noise.NightBanReduction
	}
	return heard
}

// seenPenalty is the elevation-based viewshed component (AC-4): a genuine
// line-of-sight test against real CellAt elevation along the object→home path,
// with a continuous occlusion factor and a monotonic distance falloff.
func (b *BlightAPI) seenPenalty(o blightingObject, ox, oy, hx, hy int, homeElev float64, year int64, occluders map[occluderKey]occluder, correlationID string) (float64, error) {
	objElev, err := b.elevationAt(ox, oy, correlationID)
	if err != nil {
		return 0, err
	}
	topO := objElev + o.visualHeightM
	topH := homeElev + b.cfg.Viewshed.EyeHeightM
	occ, err := b.losOcclusion(ox, oy, hx, hy, topO, topH, year, occluders, correlationID)
	if err != nil {
		return 0, err
	}
	d := distanceM(ox, oy, hx, hy)
	seen := o.visualMagnitude * (1 - occ)
	seen *= b.cfg.Viewshed.SeenFalloffM / (b.cfg.Viewshed.SeenFalloffM + d)
	return seen, nil
}

// losOcclusion walks the integer cells on the straight line between the object
// and the home cell (excluding the endpoints), and returns a CONTINUOUS
// occlusion factor in [0,1]: the worst clearance deficit of the ground (natural
// elevation + any bund/tree-belt earthwork) above the sight-line, scaled by the
// data-sourced occlusion scale. 0 = fully visible, 1 = fully occluded. Raising
// intervening ground strictly raises the deficit and hence the occlusion —
// never a binary "is there a ridge" flag (AC-4).
func (b *BlightAPI) losOcclusion(ox, oy, hx, hy int, topO, topH float64, year int64, occluders map[occluderKey]occluder, correlationID string) (float64, error) {
	dx := hx - ox
	dy := hy - oy
	denom := float64(dx*dx + dy*dy)
	if denom == 0 {
		return 0, nil // same cell: nothing can interpose
	}
	maxDeficit := 0.0
	for _, c := range walkLine(ox, oy, hx, hy) {
		gx, gy := c[0], c[1]
		t := (float64(gx-ox)*float64(dx) + float64(gy-oy)*float64(dy)) / denom
		lineH := topO + t*(topH-topO)
		nat, err := b.elevationAt(gx, gy, correlationID)
		if err != nil {
			return 0, err
		}
		eff := nat + b.occluderHeightAt(occluders, gx, gy, year)
		if deficit := eff - lineH; deficit > maxDeficit {
			maxDeficit = deficit
		}
	}
	if maxDeficit <= 0 {
		return 0, nil
	}
	occ := maxDeficit / b.cfg.Viewshed.OcclusionScaleM
	if occ > 1 {
		occ = 1
	}
	return occ, nil
}

// occluderHeightAt returns the effective earthwork height at a global cell for
// the given year: a bund is always full height, a tree belt ramps 0→heightM
// over the grow-in window (AC-7).
func (b *BlightAPI) occluderHeightAt(occluders map[occluderKey]occluder, gx, gy int, year int64) float64 {
	tile, local, ok := cellFromGlobal(gx, gy)
	if !ok {
		return 0
	}
	o, ok := occluders[occluderKey{tile: tile, local: local}]
	if !ok {
		return 0
	}
	if o.kind == occluderBund {
		return o.heightM
	}
	age := year - o.plantedYear
	if age <= 0 {
		return 0
	}
	m := float64(age) / float64(b.cfg.TreeBelt.GrowInYears)
	if m > 1 {
		m = 1
	}
	return o.heightM * m
}

// elevationAt returns the real CellAt elevation at a global cell (GR#3: no
// mining-local terrain copy — this is the only elevation read in the package).
func (b *BlightAPI) elevationAt(gx, gy int, correlationID string) (float64, error) {
	tile, local, ok := cellFromGlobal(gx, gy)
	if !ok {
		return 0, errs.New(ErrBlightQueryOutOfBounds, correlationID, map[string]any{"gx": gx, "gy": gy})
	}
	cell, err := b.world.CellAt(tile, local, correlationID)
	if err != nil {
		return 0, err
	}
	return float64(cell.Elevation), nil
}

// SubsidenceRiskAt reports whether a cell lies inside any deep-coal site's
// subsidence-risk radius (AC-3): a distinct risk flag, separate from the
// noise/viewshed blight effect, carried per-site from data/minetypes.json.
func (b *BlightAPI) SubsidenceRiskAt(tile world.TileCoord, local world.CellLocal, correlationID string) (bool, error) {
	if err := b.checkNotCopied("SubsidenceRiskAt"); err != nil {
		return false, err
	}
	if !tile.InExtent() || !local.InBounds() {
		return false, errs.New(ErrBlightQueryOutOfBounds, correlationID, map[string]any{"tile": tile, "local": local})
	}
	gx, gy := globalCell(tile, local)
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.sites {
		if s.subsidenceRadiusM <= 0 || s.exhaustedAndReclaimed() {
			continue
		}
		sx, sy := globalCell(s.tile, s.local)
		if distanceM(sx, sy, gx, gy) <= s.subsidenceRadiusM {
			return true, nil
		}
	}
	return false, nil
}

// WorkforceAtRisk returns the mining-jobs-culture headcount tied to a specific
// mine (AC-10): the §32 "scripted-by-you Detroit test" figure the player can
// see coming before triggering closure. It is the site's own jobs count, never
// a generic layoff constant.
func (b *BlightAPI) WorkforceAtRisk(objectKey string, correlationID string) (int, error) {
	if err := b.checkNotCopied("WorkforceAtRisk"); err != nil {
		return 0, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.sites[objectKey]
	if !ok {
		return 0, errs.New(ErrUnknownBlightKey, correlationID, map[string]any{"key": objectKey})
	}
	return s.jobs, nil
}

// Reclaim converts a site to a lake or country park (AC-9): the site is no
// longer a registered blighting object and can never be reclaimed again. The
// landfill-void option is absent (see ReclaimOption's doc comment).
func (b *BlightAPI) Reclaim(objectKey string, option ReclaimOption, correlationID string) error {
	if err := b.checkNotCopied("Reclaim"); err != nil {
		return err
	}
	if option != ReclaimLake && option != ReclaimPark {
		return errs.New(ErrReclaimBlocked, correlationID, map[string]any{"option": option.String()})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sites[objectKey]
	if !ok {
		return errs.New(ErrUnknownBlightKey, correlationID, map[string]any{"key": objectKey})
	}
	if s.reclaimed != nil {
		return errs.New(ErrAlreadyReclaimed, correlationID, map[string]any{"key": objectKey, "option": s.reclaimed.String()})
	}
	s.reclaimed = &option
	// A reclaimed lake/park is no longer a registered blighting object.
	delete(b.objects, objectKey)
	if s.spoilTipKey != "" {
		delete(b.objects, s.spoilTipKey)
	}
	return nil
}

// globalCell maps a (tile, local) position to its global cell index (GR#21:
// deterministic integer arithmetic, no floats).
func globalCell(tile world.TileCoord, local world.CellLocal) (int, int) {
	return tile.X*world.TileSizeCells + local.Col, tile.Y*world.TileSizeCells + local.Row
}

// cellFromGlobal maps a global cell index back to (tile, local). ok is false
// when the index is negative or outside the expansion extent.
func cellFromGlobal(gx, gy int) (world.TileCoord, world.CellLocal, bool) {
	if gx < 0 || gy < 0 {
		return world.TileCoord{}, world.CellLocal{}, false
	}
	tx := gx / world.TileSizeCells
	ty := gy / world.TileSizeCells
	if tx >= world.TilesPerSide || ty >= world.TilesPerSide {
		return world.TileCoord{}, world.CellLocal{}, false
	}
	return world.TileCoord{X: tx, Y: ty},
		world.CellLocal{Row: gy % world.TileSizeCells, Col: gx % world.TileSizeCells}, true
}

// distanceM is the centre-to-centre distance in metres between two global
// cells.
func distanceM(ax, ay, bx, by int) float64 {
	dx := float64(ax - bx)
	dy := float64(ay - by)
	return math.Sqrt(dx*dx+dy*dy) * float64(world.CellSizeM)
}

// walkLine returns the integer cells strictly BETWEEN (ox,oy) and (hx,hy) on
// a Bresenham line — the endpoints are excluded because the object's own top
// and the home cell's top are the sight-line's anchors, never occluders.
func walkLine(ox, oy, hx, hy int) [][2]int {
	dx := hx - ox
	dy := hy - oy
	adx := dx
	if adx < 0 {
		adx = -adx
	}
	ady := dy
	if ady < 0 {
		ady = -ady
	}
	sx := 1
	if dx < 0 {
		sx = -1
	}
	sy := 1
	if dy < 0 {
		sy = -1
	}
	var cells [][2]int
	x, y := ox, oy
	err := adx - ady
	for x != hx || y != hy {
		e2 := 2 * err
		if e2 > -ady {
			err -= ady
			x += sx
		}
		if e2 < adx {
			err += adx
			y += sy
		}
		if x == hx && y == hy {
			break // the step landed on the endpoint — never append it
		}
		cells = append(cells, [2]int{x, y})
	}
	return cells
}
