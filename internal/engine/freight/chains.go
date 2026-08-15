package freight

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Firm is a chain stage's registered firm identity and firm-shape snapshot
// (staff, premises) — the value [FirmRegistrar.RegisterFirm] returns (AC-4
// "stages register as firms"). See doc.go: the concrete registration goes
// through engine.firms (MOD-058); until that module lands, the FirmRegistrar
// seam is unset and every stage carries the zero Firm.
type Firm struct {
	ID       uint64
	Staff    int64
	Premises string
}

// FirmRegistrar is the dependency-inversion seam freight defines for
// engine.firms (MOD-058, still open at build time). When engine.firms lands,
// its FirmsAPI implements this interface and freight's Load wires it in;
// every chain stage then registers itself as a real firm through it, exactly
// as code.json's "stages register as firms" pattern promises (AC-4). Until
// then the seam is unset and stages carry the zero Firm — this is the
// documented block, not a freight-owned pseudo-firm lifecycle.
type FirmRegistrar interface {
	RegisterFirm(name string, staff int64, premises string) (Firm, error)
}

// ChainStage is the queryable view of one production-chain stage (AC-5):
// its documented input commodity t/day and output commodity t/day, plus
// jobs, power/water draw (carried as §17-consumption-coefficient inputs),
// and its blight class — and its registered firm (AC-4).
type ChainStage struct {
	ID                StageID
	Name              string
	Family            ChainFamily
	Inputs            []StageInput
	Outputs           []StageOutput
	Jobs              int64
	PowerKWhPerDay    int64
	WaterLitresPerDay int64
	BlightClass       int
	Firm              Firm
}

// Stage returns the queryable view of the named chain stage, or
// ErrUnknownStage for an unregistered stage ID (AC-12) — never a
// silently-created zero-value stage.
func (f *FreightAPI) Stage(id StageID) (ChainStage, error) {
	if err := f.checkNotCopied("Stage"); err != nil {
		return ChainStage{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	st, ok := f.stages[id]
	if !ok {
		return ChainStage{}, errs.New(ErrUnknownStage, f.correlationID, map[string]any{
			"stage": string(id),
		})
	}
	return snapshotStage(st.cfg, st.firm), nil
}

// Stages returns every chain stage in data order (the five families in
// order, each family's stages in order — deterministic, GR#21).
func (f *FreightAPI) Stages() []ChainStage {
	if err := f.checkNotCopied("Stages"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]ChainStage, 0, len(f.cfg.stageConfigs))
	for _, sc := range f.cfg.stageConfigs {
		out = append(out, snapshotStage(sc, f.stages[sc.ID].firm))
	}
	return out
}

// RegisterFirms registers every chain stage as a firm through the supplied
// FirmRegistrar (AC-4's "stages register as firms" seam — engine.firms'
// FirmsAPI implements this when MOD-058 lands). Each stage's firm is
// recorded against the stage, so [Stage]/[Stages] then carry a non-zero
// [Firm] with the stage's jobs as staff. A nil registrar is a no-op
// (stages stay at the zero Firm — the documented blocked state). Pure
// registration — it never invents firm lifecycle behaviour (GR#3).
func (f *FreightAPI) RegisterFirms(reg FirmRegistrar) error {
	if reg == nil {
		return nil
	}
	if err := f.checkNotCopied("RegisterFirms"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sc := range f.cfg.stageConfigs {
		firm, err := reg.RegisterFirm(sc.Name, sc.Jobs, string(sc.ID))
		if err != nil {
			return err
		}
		f.stages[sc.ID].firm = firm
	}
	return nil
}

func snapshotStage(sc stageConfig, firm Firm) ChainStage {
	inputs := make([]StageInput, len(sc.Inputs))
	copy(inputs, sc.Inputs)
	outputs := make([]StageOutput, len(sc.Outputs))
	copy(outputs, sc.Outputs)
	return ChainStage{
		ID:                sc.ID,
		Name:              sc.Name,
		Family:            sc.Family,
		Inputs:            inputs,
		Outputs:           outputs,
		Jobs:              sc.Jobs,
		PowerKWhPerDay:    sc.PowerKWhPerDay,
		WaterLitresPerDay: sc.WaterLitresPerDay,
		BlightClass:       sc.BlightClass,
		Firm:              firm,
	}
}

// Throughput is a stage's resolved per-day flow given input availability
// (AC-5): the inputs actually consumed and the outputs actually produced,
// with output bounded proportionally to input availability (a stage whose
// input is constrained below its documented rate produces proportionally
// less, never its full documented output regardless of supply).
type Throughput struct {
	Outputs  map[Commodity]int64
	Consumed map[Commodity]int64
	Scale    float64 // in [0,1]; 1 = fully supplied
}

// StageThroughput computes a chain stage's input→output flow given the
// available input tonnage per commodity (AC-5). A primary producer (no
// documented inputs) runs at Scale 1. For each documented input, available
// below the documented rate reduces Scale to the minimum available/required
// fraction; every output is then scaled by that fraction. Errors with
// ErrUnknownStage for an unregistered stage ID (AC-12). Pure and
// side-effect-free — it never mutates freight state.
func (f *FreightAPI) StageThroughput(id StageID, availableInputs map[Commodity]int64) (Throughput, error) {
	if err := f.checkNotCopied("StageThroughput"); err != nil {
		return Throughput{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	st, ok := f.stages[id]
	if !ok {
		return Throughput{}, errs.New(ErrUnknownStage, f.correlationID, map[string]any{
			"stage": string(id),
		})
	}

	scale := int64(1_000_000)
	if len(st.cfg.Inputs) > 0 {
		for _, in := range st.cfg.Inputs {
			available := availableInputs[in.Commodity]
			if available <= 0 {
				scale = 0
				break
			}
			if available < in.TonnesPerDay {
				fraction := num.ClampInt64FromFloat(float64(available) / float64(in.TonnesPerDay) * 1e6)
				if fraction < scale {
					scale = fraction
				}
			}
		}
	}

	t := Throughput{
		Outputs:  make(map[Commodity]int64, len(st.cfg.Outputs)),
		Consumed: make(map[Commodity]int64, len(st.cfg.Inputs)),
		Scale:    float64(scale) / 1e6,
	}
	for _, in := range st.cfg.Inputs {
		t.Consumed[in.Commodity] = scaleTonnes(in.TonnesPerDay, scale)
	}
	for _, out := range st.cfg.Outputs {
		t.Outputs[out.Commodity] = scaleTonnes(out.TonnesPerDay, scale)
	}
	return t, nil
}
