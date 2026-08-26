package chemicals

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// ChemAPI is MOD-063's (engine.chemicals) chain-stage machinery, built to
// stub-for-baseline depth: the "each stage a firm with input t/day → output
// t/day" surface engine.chemicals.md AC-2 specifies, plus the import-at-margin
// path engine.chemicals.md AC-4 owns. feat.refinery registers its two stages
// (the refinery and the petrochemical works) against this surface (AC-5) and
// reuses the import path for its make-vs-buy comparison (AC-3) — it does not
// author a second, refinery-owned chain (ASM-702). The five-stage chain, the
// pipeline network, and the leak-risk machinery are MOD-063's full build; this
// stub carries only stage registration, tonnage-conserving routing, and the
// import margin — enough for feat.refinery's two stages and its make-vs-buy
// read.
//
// The zero value is not usable; construct via NewChemAPI. A *ChemAPI is safe
// for concurrent use: every mutable field is guarded by mu and checkNotCopied
// rejects a method call on a struct-copied value (SEC-020 family).
type ChemAPI struct {
	mu sync.RWMutex

	order  []string
	stages map[string]ChainStage

	importMargin map[string]int64 // refined commodity -> micro-pounds per tonne

	correlationID string
	self          atomic.Pointer[ChemAPI]
}

// ChainStage is one registered chain stage: its declared input demands and
// output production, both in tonnes/day. The stage key is the lookup key; the
// stage never carries its own key as a discriminator field (mirroring the
// freight chain-stage and engine.mining mine-type conventions).
type ChainStage struct {
	Key     string
	Inputs  map[string]int64 // commodity -> t/day demand
	Outputs map[string]int64 // commodity -> t/day production at full input
}

// NewChemAPI constructs an empty, ready-to-register ChemAPI. correlationID is
// attached to every error this surface constructs (GR#1).
func NewChemAPI(correlationID string) *ChemAPI {
	c := &ChemAPI{
		stages:        make(map[string]ChainStage),
		importMargin:  make(map[string]int64),
		correlationID: correlationID,
	}
	c.self.Store(c) // armed exactly once, before c escapes (SEC-020)
	return c
}

// checkNotCopied rejects a method call on a struct-copied *ChemAPI. Lock-free
// (a single atomic.Pointer.Load), so it is safe to run before mu is touched.
func (c *ChemAPI) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return errs.New(ErrRefineryCopied, c.correlationID, map[string]any{"method": method})
	}
	return nil
}

// RegisterStage registers (or overwrites) one chain stage. The inputs/outputs
// maps are copied, so a caller cannot mutate the registry through them. A
// non-empty key is required; an empty key is ErrUnregisteredStage. The stage
// tonnage surface enforces the same domain the data loader does (SEC-132's
// loader-plus-setter rule, FEAT-135): every input and output entry must name a
// non-empty commodity and carry a strictly positive t/day figure, so a
// negative or zero tonnage — or an empty commodity — is rejected with
// ErrRefineryDataInvalid rather than stored, and StageInput/StageOutput never
// surface a negative figure (SEC-169). Validation runs before the lock, so a
// rejected registration never mutates the registry.
func (c *ChemAPI) RegisterStage(key string, inputs, outputs map[string]int64) error {
	if err := c.checkNotCopied("RegisterStage"); err != nil {
		return err
	}
	if key == "" {
		return errs.New(ErrUnregisteredStage, c.correlationID, map[string]any{"stage": key})
	}
	if err := validateStageTonnage(key, "input", inputs, c.correlationID); err != nil {
		return err
	}
	if err := validateStageTonnage(key, "output", outputs, c.correlationID); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.stages[key]; !exists {
		c.order = append(c.order, key)
	}
	c.stages[key] = ChainStage{Key: key, Inputs: cloneInt64Map(inputs), Outputs: cloneInt64Map(outputs)}
	return nil
}

// validateStageTonnage enforces the stage-tonnage domain on one map (inputs or
// outputs): each entry must name a non-empty commodity and carry a strictly
// positive t/day figure. This is the exported-surface half of the loader's
// buildFacilityProfile checks (ThroughputTonnesPerDay > 0, output TonnesPerDay
// > 0, non-empty commodity names) — the same domain, enforced at the setter so
// a caller cannot store a negative or zero figure that StageInput/StageOutput
// would then surface (SEC-169). Keys are validated in sorted order so the first
// reported violation is deterministic (GR#21), never a Go-map iteration order.
func validateStageTonnage(stage, kind string, m map[string]int64, correlationID string) error {
	comms := make([]string, 0, len(m))
	for comm := range m {
		comms = append(comms, comm)
	}
	sort.Strings(comms)
	for _, comm := range comms {
		if comm == "" {
			return errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
				"stage": stage, "field": kind + ".commodity", "cause": "empty " + kind + " commodity",
			})
		}
		if m[comm] <= 0 {
			return errs.New(ErrRefineryDataInvalid, correlationID, map[string]any{
				"stage": stage, "commodity": comm, "tonnes": m[comm], "cause": "non-positive " + kind + " tonnage",
			})
		}
	}
	return nil
}

// Stage returns one registered stage by key. An unregistered key is
// ErrUnregisteredStage — never a silently-created zero-value stage.
func (c *ChemAPI) Stage(key string) (ChainStage, error) {
	if err := c.checkNotCopied("Stage"); err != nil {
		return ChainStage{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.stages[key]
	if !ok {
		return ChainStage{}, errs.New(ErrUnregisteredStage, c.correlationID, map[string]any{"stage": key})
	}
	return ChainStage{Key: s.Key, Inputs: cloneInt64Map(s.Inputs), Outputs: cloneInt64Map(s.Outputs)}, nil
}

// StageOutput returns a stage's declared output production (t/day). An
// unregistered key is ErrUnregisteredStage.
func (c *ChemAPI) StageOutput(key string) (map[string]int64, error) {
	if err := c.checkNotCopied("StageOutput"); err != nil {
		return nil, err
	}
	s, err := c.Stage(key)
	if err != nil {
		return nil, err
	}
	return s.Outputs, nil
}

// StageInput returns a stage's INPUT as actually available: for each input
// commodity, the minimum of its declared demand and the sum of upstream
// stages' declared output of that commodity. This is the tonnage-conservation
// identity (input ≤ upstream routed output, engine.chemicals.md AC-3): a
// downstream stage can never draw more than its upstream stage routed to it.
// "Upstream" is the stages registered BEFORE this stage (the registration order
// is the chain's topological order), never every other stage — a stage cannot
// draw from a downstream stage, and a mutual-dependency pair cannot manufacture
// tonnage from each other (SEC-135). Upstream summation iterates the
// registration order (c.order), never a Go map (GR#21). An unregistered key is
// ErrUnregisteredStage.
func (c *ChemAPI) StageInput(key string) (map[string]int64, error) {
	if err := c.checkNotCopied("StageInput"); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.stages[key]
	if !ok {
		return nil, errs.New(ErrUnregisteredStage, c.correlationID, map[string]any{"stage": key})
	}

	upstream := make(map[string]int64)
	for _, k := range c.order {
		if k == key {
			break // stages at or after this one are not upstream (topological order)
		}
		for comm, tonnes := range c.stages[k].Outputs {
			upstream[comm] = num.SatAdd(upstream[comm], tonnes)
		}
	}

	in := make(map[string]int64, len(s.Inputs))
	for comm, demand := range s.Inputs {
		if avail := upstream[comm]; avail < demand {
			in[comm] = avail
		} else {
			in[comm] = demand
		}
	}
	return in, nil
}

// SetImportMargin sets the import-at-margin unit cost (micro-pounds per tonne)
// for a refined commodity. This is ASM-321's margin data (MOD-063's), seeded
// from data/refinery.json as a stub placeholder until MOD-063's data file
// lands — consumed through this surface, never owned by feat.refinery (ASM-703).
//
// The exported setter enforces the same domain the data loader does (SEC-132):
// a non-empty commodity and a positive unit cost are required, and a violation
// is rejected with a registry-sourced error rather than silently stored — so a
// caller cannot configure a zero or negative margin that would make an import
// free or negatively priced. The margin is never stored on rejection.
func (c *ChemAPI) SetImportMargin(commodity string, micropoundsPerTonne int64) error {
	if err := c.checkNotCopied("SetImportMargin"); err != nil {
		return err
	}
	if commodity == "" {
		return errs.New(ErrRefineryDataInvalid, c.correlationID, map[string]any{"commodity": commodity, "cause": "empty import commodity"})
	}
	if micropoundsPerTonne <= 0 {
		return errs.New(ErrRefineryDataInvalid, c.correlationID, map[string]any{"commodity": commodity, "margin": micropoundsPerTonne, "cause": "non-positive import margin"})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.importMargin[commodity] = micropoundsPerTonne
	return nil
}

// ImportMargin returns the import-at-margin unit cost for a refined commodity,
// whether one is configured, and any error. A false return with a nil error
// means no margin is configured for that commodity (an incomplete import
// config); a struct-copied value is rejected with ErrRefineryCopied rather than
// collapsed into that "not configured" false (SEC-136 sentinel class).
func (c *ChemAPI) ImportMargin(commodity string) (int64, bool, error) {
	if err := c.checkNotCopied("ImportMargin"); err != nil {
		return 0, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.importMargin[commodity]
	return m, ok, nil
}

// ImportRefined returns the cost (micro-pounds) to import tonnes of a refined
// commodity at the documented margin — the make-vs-buy import path, available
// with no refinery built (AC-3). The unit cost comes from this surface's
// import margin (MOD-063's import path), never a refinery-local price table.
// A missing margin, a negative tonnage, a non-positive margin, or an import
// cost that overflows int64 is ErrRefineryDataInvalid. The multiplication uses
// the project's saturating multiplier and rejects saturation rather than
// silently undercharging a huge import (GR#16, SEC-137).
func (c *ChemAPI) ImportRefined(commodity string, tonnes int64) (int64, error) {
	if err := c.checkNotCopied("ImportRefined"); err != nil {
		return 0, err
	}
	if tonnes < 0 {
		return 0, refineryDataInvalidForCommodity(c.correlationID, commodity, fmt.Sprintf("negative tonnes %d", tonnes))
	}
	margin, ok, err := c.ImportMargin(commodity)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, refineryDataInvalidForCommodity(c.correlationID, commodity, "unknown commodity")
	}
	if margin <= 0 {
		return 0, errs.New(ErrRefineryDataInvalid, c.correlationID, map[string]any{"commodity": commodity, "margin": margin, "cause": "non-positive import margin"})
	}
	cost, overflow := num.SafeMul(margin, tonnes)
	if overflow {
		return 0, errs.New(ErrRefineryDataInvalid, c.correlationID, map[string]any{"commodity": commodity, "tonnes": tonnes, "margin": margin, "cause": "import cost overflow"})
	}
	return cost, nil
}

// cloneInt64Map returns a copy of m so a caller can never alias a registry's
// stored state through a returned map.
func cloneInt64Map(m map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
