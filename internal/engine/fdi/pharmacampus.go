package fdi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the feat.pharmacampus feature (FEAT-101): the pharma/R&D
// campus (Groton-Pfizer-class, §46 archetype — Pfizer's real UK campus was
// Sandwich, Kent) modelled as a DISTINCT, data-sourced facility whose long
// bet is the education↔FDI compounding loop. It shares internal/engine/fdi
// with engine.fdi (MOD-059) and has no inbound contract of its own — it
// surfaces through MOD-059's eventual FdiAPI (code.json: feat.pharmacampus
// inbound is null, outbound calls are engine.fdi/engine.education/
// engine.firms, plus a pending engine.freight trade edge — see TradeEdge).
//
// # The two-directional compounding loop (the north-star Q5 long bet)
//
// The loop, not the single transaction, is the point:
//
//	education → FDI:   EducationBidTerm reads the city's education-output
//	                   term (graduates/research, via the registered
//	                   engine.education edge) and turns it into a strictly
//	                   monotonic bid-quality contribution, so a better-
//	                   educated city wins the pharma prospect (or wins on
//	                   strictly better terms) than an otherwise-identical
//	                   city — and a sufficiently under-educated city loses
//	                   to the off-map region (the lose branch is reachable).
//	FDI → education:   a won campus emits graduate/research demand back into
//	                   the education system (via the same registered edge),
//	                   scaling with its own employment, closing the loop.
//
// Both legs are directional (checked by shape, never a pinned magnitude);
// the bid resolution itself is MOD-059's stub surface in anchor.go, not
// reimplemented here.
//
// # Permit & decommission inheritance (AC-7, inherited not reimplemented)
//
// The campus build is permit-gated via feat.facilitypermits (FEAT-053) and
// carries the day-one "put back to nature" decommission liability via
// feat.decommission (FEAT-054, §7). Neither edge is registered in code.json
// today (ES-2), so this package declares the inheritance in the package doc
// and declares no pharma-local permit-state or decommission-liability field
// — the composition root wires the edges when they land.

// JobsCharacter is the per-type jobs-character ordinal the §46 archetype
// sheet names ("labs: high-wage white collar, low freight" vs heavy
// industry). It is a junior-invented three-value ordinal until MOD-059's
// prospect catalogue names its own scale (logged as an assumption); the
// per-type value is a placeholder read from data/pharmacampus.json (GR#15).
type JobsCharacter uint8

const (
	JobsWhiteCollar JobsCharacter = iota // labs: high-wage white collar
	JobsIndustrial                       // heavy industry: blue collar
	JobsMixed                            // mixed-skill
)

// String returns the canonical data-file name of the jobs character. It is
// the single name table jobsCharacterByName derives from (GR#3).
func (j JobsCharacter) String() string {
	switch j {
	case JobsWhiteCollar:
		return "white-collar-skilled"
	case JobsIndustrial:
		return "industrial"
	case JobsMixed:
		return "mixed"
	default:
		return "unrecognised"
	}
}

// jobsCharacterByName resolves a data/pharmacampus.json "jobsCharacter"
// string to its JobsCharacter ordinal, iterating the enum's canonical
// String names.
func jobsCharacterByName(name string) (JobsCharacter, bool) {
	for j := JobsWhiteCollar; j <= JobsMixed; j++ {
		if j.String() == name {
			return j, true
		}
	}
	return 0, false
}

// PharmaBidParams is the pharma prospect's bid-curve parameter set, loaded
// from data/pharmacampus.json (GR#15). Every figure is a placeholder; the
// shape it pins is the ordering claim of AC-3 (higher education output →
// strictly better bid), never a magnitude.
type PharmaBidParams struct {
	QualityBase              int64 // education-independent base bid quality (points)
	EducationTermPerGraduate int64 // bid points contributed per unit of education output
	CompetingFloor           int64 // the off-map competing region's bid-quality floor
	JitterMax                int64 // seeded bid jitter half-range (points, symmetric)
	GraduateDemandPerWorker  int64 // graduate/research demand emitted per worker
}

// PharmaCampusParams is the resolved, data-sourced parameter set for one
// FDI-anchor facility (AC-1). Footprint, output, jobs, jobs character,
// utility draw, exports and money figures are all named, typed fields — the
// type key is the lookup key, never a field of this set (the
// distinct-facility rule). Monetary fields are finance.Money (int64
// micro-pounds); jobs/throughput/utility/exports are int64 — never a float
// (AC-10).
type PharmaCampusParams struct {
	Name                  string
	Footprint             int
	OutputRate            int64 // t/day
	Jobs                  int64 // skilled headcount
	JobsCharacter         JobsCharacter
	UtilityPowerKW        int64
	UtilityWaterLPD       int64 // litres/day
	ExportsPerDay         int64 // t/day
	Capex                 finance.Money
	OpexPerWorker         finance.Money // micro-pounds per worker per month
	WagesPerWorker        finance.Money // micro-pounds per worker per month
	SupplyChainFirms      int64         // base count of supply-chain firms spawned on win
	SupplyChainPerWorkers int64         // one extra supply-chain firm per this many workers
	Bid                   PharmaBidParams
}

// EducationBidTerm returns the education contribution to the pharma bid
// quality (AC-3, first leg): the base bid quality plus a strictly monotonic
// education term. It is a pure, saturating function of the education-output
// scalar the caller already read from the registered edge — never a
// resolver, never wall clock.
func (p PharmaCampusParams) EducationBidTerm(eduOutput int64) int64 {
	term, _ := num.SafeMul(eduOutput, p.Bid.EducationTermPerGraduate)
	return num.SatAdd(p.Bid.QualityBase, term)
}

// GraduateDemandFor returns the graduate/research demand the won campus
// emits (AC-4, second leg), saturating-monotonic in employment.
func (p PharmaCampusParams) GraduateDemandFor(employment int64) int64 {
	demand, _ := num.SafeMul(employment, p.Bid.GraduateDemandPerWorker)
	return demand
}

// SupplyChainCountFor returns the number of supply-chain firms the won
// campus spawns (§45 demand injection, AC-5), non-decreasing in employment.
// The per-worker divisor is a data figure, so no scaling constant lives in
// code (GR#15).
func (p PharmaCampusParams) SupplyChainCountFor(employment int64) int64 {
	extra := int64(0)
	if p.SupplyChainPerWorkers > 0 {
		extra = employment / p.SupplyChainPerWorkers
	}
	return num.SatAdd(p.SupplyChainFirms, extra)
}

// maxSupplyChainFirms bounds the number of supply-chain firms a single pharma
// win may spawn (§45 demand injection, AC-5). It is a validation ceiling —
// the same shape as engine.core's MaxAdvanceTicksPerCall — not a balance
// number: the balance figures live in data/pharmacampus.json, and this
// ceiling exists only so a hostile or malformed parameter set cannot turn
// Win's spawn loop into a hang/OOM or unbounded FirmsAPI growth (SEC-122).
// The shipped data figures (6 base + ~7 scaled at 3500 jobs) sit several
// orders of magnitude below it, so a legitimate balance pass never reaches it.
const maxSupplyChainFirms int64 = 100_000

// maxJitter bounds the bid jitter half-range so the symmetric draw span
// 2*jitterMax+1 stays exactly representable in int64 (SEC-123). At
// jitterMax >= 2^62 the span saturates to MaxInt64 and the draw becomes
// negative-biased and asymmetric, silently breaking the documented
// [-jitterMax, +jitterMax] contract. MaxInt64/2 is the largest half-range
// whose 2*n+1 span does not overflow; no tighter game-sane bound is imposed
// here — that is Aaron's balance pass, not a correctness ceiling.
const maxJitter int64 = math.MaxInt64 / 2

// Validate reports whether the parameter set lies inside the accepted domain
// (AC-8). It is the in-process trust-boundary twin of buildPharmaCatalogue's
// data-file checks: PharmaCampusParams has every field exported, so a caller
// can bypass the data loader entirely and construct a parameter set directly;
// [NewPharma] calls Validate so no out-of-domain figure can reach Win or
// ResolveBid. It checks every field Win/ResolveBid consume, plus the two
// derived figures that reach looping/saturating arithmetic — the supply-chain
// spawn count (SEC-122) and the bid jitter half-range (SEC-123). Every
// rejection is a registry-sourced ErrPharmaDataInvalid; the correlation ID is
// minted here because Validate is the entry boundary (the caller receives the
// error directly, there is no upstream ID to propagate).
func (p PharmaCampusParams) Validate() error {
	correlationID := errs.NewCorrelationID()
	fail := func(field, rule string) error {
		return errs.New(ErrPharmaDataInvalid, correlationID, map[string]any{
			"field": field,
			"rule":  rule,
			"cause": rule,
		})
	}

	if p.Name == "" {
		return fail("Name", "required, must name the facility")
	}
	if p.Footprint <= 0 {
		return fail("Footprint", "must be a positive cell count")
	}
	if p.OutputRate <= 0 {
		return fail("OutputRate", "must be a positive t/day rate")
	}
	if p.Jobs <= 0 {
		return fail("Jobs", "must be a positive headcount (missing jobs figure)")
	}
	if p.UtilityPowerKW < 0 {
		return fail("UtilityPowerKW", "must be non-negative (negative utility draw)")
	}
	if p.UtilityWaterLPD < 0 {
		return fail("UtilityWaterLPD", "must be non-negative (negative utility draw)")
	}
	if p.ExportsPerDay < 0 {
		return fail("ExportsPerDay", "must be non-negative")
	}
	if p.Capex < 0 {
		return fail("Capex", "must be non-negative")
	}
	if p.OpexPerWorker < 0 {
		return fail("OpexPerWorker", "must be non-negative")
	}
	if p.WagesPerWorker < 0 {
		return fail("WagesPerWorker", "must be non-negative")
	}
	if p.SupplyChainFirms < 0 {
		return fail("SupplyChainFirms", "must be non-negative")
	}
	if p.SupplyChainPerWorkers < 0 {
		return fail("SupplyChainPerWorkers", "must be non-negative")
	}
	if p.Bid.QualityBase < 0 {
		return fail("Bid.QualityBase", "must be non-negative")
	}
	if p.Bid.EducationTermPerGraduate <= 0 {
		return fail("Bid.EducationTermPerGraduate", "must be positive (the education term must be able to move the bid)")
	}
	if p.Bid.CompetingFloor <= 0 {
		return fail("Bid.CompetingFloor", "must be positive (the off-map region must have a real floor)")
	}
	if p.Bid.JitterMax < 0 {
		return fail("Bid.JitterMax", "must be non-negative")
	}
	if p.Bid.JitterMax > maxJitter {
		return fail("Bid.JitterMax", "exceeds the symmetric-draw ceiling (2*jitterMax+1 would overflow int64)")
	}
	if p.Bid.GraduateDemandPerWorker < 0 {
		return fail("Bid.GraduateDemandPerWorker", "must be non-negative")
	}
	if spawn := p.SupplyChainCountFor(p.Jobs); spawn > maxSupplyChainFirms {
		return fail("SupplyChainFirms", "supply-chain spawn exceeds the validation ceiling")
	}
	return nil
}

// TotalOpex returns the monthly opex for a campus of the given employment,
// using the project's saturating multiply (GR#16 / AC-10).
func (p PharmaCampusParams) TotalOpex(employment int64) finance.Money {
	v, _ := num.SafeMul(int64(p.OpexPerWorker), employment)
	return finance.Money(v)
}

// WageBill returns the monthly wage bill for a campus of the given
// employment, saturating (GR#16 / AC-10).
func (p PharmaCampusParams) WageBill(employment int64) finance.Money {
	v, _ := num.SafeMul(int64(p.WagesPerWorker), employment)
	return finance.Money(v)
}

// PharmaCampus pairs an anchor key with its resolved parameter set
// (AC-1/AC-3). The key lives on the catalogue entry, NOT on
// PharmaCampusParams — the parameter set carries no type discriminator.
type PharmaCampus struct {
	Key    string
	Params PharmaCampusParams
}

// Catalogue is the immutable, fully-validated FDI-anchor catalogue loaded
// from data/pharmacampus.json (AC-8: all-or-nothing — a load that fails
// validation yields no partial catalogue). Resolution is a pure function of
// (data file, key); the canonical key order is the data file's declared
// array order (GR#21).
type Catalogue struct {
	ordered []PharmaCampus
	byKey   map[string]PharmaCampusParams
}

// Resolve returns the parameter set for the named anchor facility. An
// unknown key yields ErrUnknownAnchor — never a panic, never a silent
// default-substituted parameter set (AC-8).
func (c Catalogue) Resolve(key string) (PharmaCampusParams, error) {
	p, ok := c.byKey[key]
	if !ok {
		return PharmaCampusParams{}, errs.New(ErrUnknownAnchor, errs.NewCorrelationID(), map[string]any{"key": key})
	}
	return p, nil
}

// Keys returns the catalogue's anchor keys in canonical (data-file) order.
func (c Catalogue) Keys() []string {
	out := make([]string, len(c.ordered))
	for i, ac := range c.ordered {
		out[i] = ac.Key
	}
	return out
}

// All returns the catalogue's entries in canonical order.
func (c Catalogue) All() []PharmaCampus {
	out := make([]PharmaCampus, len(c.ordered))
	copy(out, c.ordered)
	return out
}

// Len reports the number of anchor facilities in the catalogue.
func (c Catalogue) Len() int { return len(c.ordered) }

// rawPharmaAnchor is the JSON wire shape of one data/pharmacampus.json
// anchor entry, decoded only to be validated and folded into
// PharmaCampusParams. The disclosure/$comment documentation blocks are
// author-facing commentary, not simulation parameters — encoding/json skips
// them.
type rawPharmaAnchor struct {
	Key                  string       `json:"key"`
	Name                 string       `json:"name"`
	Footprint            int          `json:"footprint"`
	OutputRate           int64        `json:"outputTPerDay"`
	Jobs                 int64        `json:"jobs"`
	JobsCharacter        string       `json:"jobsCharacter"`
	UtilityPowerKW       int64        `json:"utilityPowerKW"`
	UtilityWaterLPD      int64        `json:"utilityWaterLitresPerDay"`
	ExportsPerDay        int64        `json:"exportsTPerDay"`
	Capex                int64        `json:"capexMicroPounds"`
	OpexPerWorker        int64        `json:"opexMicroPoundsPerWorkerPerMonth"`
	WagesPerWorker       int64        `json:"wagesMicroPoundsPerWorkerPerMonth"`
	SupplyChainFirms     int64        `json:"supplyChainFirms"`
	SupplyChainPerWorker int64        `json:"supplyChainPerWorkers"`
	Bid                  rawPharmaBid `json:"bid"`
}

type rawPharmaBid struct {
	QualityBase              int64 `json:"qualityBase"`
	EducationTermPerGraduate int64 `json:"educationTermPerGraduate"`
	CompetingFloor           int64 `json:"competingFloor"`
	JitterMax                int64 `json:"jitterMax"`
	GraduateDemandPerWorker  int64 `json:"graduateDemandPerWorker"`
}

type rawPharmaData struct {
	Version int               `json:"version"`
	Anchors []rawPharmaAnchor `json:"anchors"`
}

// LoadPharmaCampus reads, decodes and validates data/pharmacampus.json from
// path, returning the ordered Catalogue or ErrPharmaDataInvalid (AC-8, the
// MET-
// code declared in errors.go). Every failure is a registry-sourced *errs.E —
// never a panic, never a silent default substitution, never a
// partially-populated result. Wrong JSON types are rejected by typed
// decoding (GR#16).
func LoadPharmaCampus(path, correlationID string) (Catalogue, error) {
	var zero Catalogue
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrPharmaDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawPharmaData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrPharmaDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildPharmaCatalogue(raw, path, correlationID)
}

// buildPharmaCatalogue folds the decoded raw data into an ordered, validated
// Catalogue. The canonical order is the data file's array order (not a Go
// map), so Keys/All are deterministic (GR#21).
func buildPharmaCatalogue(raw rawPharmaData, path, correlationID string) (Catalogue, error) {
	fail := func(field, rule string) (Catalogue, error) {
		return Catalogue{}, errs.New(ErrPharmaDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
			"cause": rule,
		})
	}

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if len(raw.Anchors) == 0 {
		return fail("anchors", "at least one anchor facility is required")
	}

	ordered := make([]PharmaCampus, 0, len(raw.Anchors))
	byKey := make(map[string]PharmaCampusParams, len(raw.Anchors))
	for i, ra := range raw.Anchors {
		field := func(s string) string { return fmt.Sprintf("anchors[%d].%s", i, s) }

		if ra.Key == "" {
			return fail(field("key"), "required, must be a non-empty anchor key")
		}
		if _, dup := byKey[ra.Key]; dup {
			return fail(field("key"), "duplicate anchor key")
		}
		if ra.Name == "" {
			return fail(field("name"), "required, must name the facility")
		}
		if ra.Footprint <= 0 {
			return fail(field("footprint"), "required, must be a positive cell count")
		}
		if ra.OutputRate <= 0 {
			return fail(field("outputTPerDay"), "required, must be a positive t/day rate")
		}
		if ra.Jobs <= 0 {
			return fail(field("jobs"), "required, must be a positive headcount (missing jobs figure)")
		}
		jc, ok := jobsCharacterByName(ra.JobsCharacter)
		if !ok {
			return fail(field("jobsCharacter"), "unrecognised archetype name (want white-collar-skilled/industrial/mixed)")
		}
		if ra.UtilityPowerKW < 0 {
			return fail(field("utilityPowerKW"), "must be non-negative (negative utility draw)")
		}
		if ra.UtilityWaterLPD < 0 {
			return fail(field("utilityWaterLitresPerDay"), "must be non-negative (negative utility draw)")
		}
		if ra.ExportsPerDay < 0 {
			return fail(field("exportsTPerDay"), "must be non-negative")
		}
		if ra.Capex < 0 {
			return fail(field("capexMicroPounds"), "must be non-negative")
		}
		if ra.OpexPerWorker < 0 {
			return fail(field("opexMicroPoundsPerWorkerPerMonth"), "must be non-negative")
		}
		if ra.WagesPerWorker < 0 {
			return fail(field("wagesMicroPoundsPerWorkerPerMonth"), "must be non-negative")
		}
		if ra.SupplyChainFirms < 0 {
			return fail(field("supplyChainFirms"), "must be non-negative")
		}
		if ra.SupplyChainPerWorker < 0 {
			return fail(field("supplyChainPerWorkers"), "must be non-negative")
		}
		if ra.Bid.QualityBase < 0 {
			return fail(field("bid.qualityBase"), "must be non-negative")
		}
		if ra.Bid.EducationTermPerGraduate <= 0 {
			return fail(field("bid.educationTermPerGraduate"), "must be positive (the education term must be able to move the bid)")
		}
		if ra.Bid.CompetingFloor <= 0 {
			return fail(field("bid.competingFloor"), "must be positive (the off-map region must have a real floor)")
		}
		if ra.Bid.JitterMax < 0 {
			return fail(field("bid.jitterMax"), "must be non-negative")
		}
		if ra.Bid.GraduateDemandPerWorker < 0 {
			return fail(field("bid.graduateDemandPerWorker"), "must be non-negative")
		}

		p := PharmaCampusParams{
			Name:                  ra.Name,
			Footprint:             ra.Footprint,
			OutputRate:            ra.OutputRate,
			Jobs:                  ra.Jobs,
			JobsCharacter:         jc,
			UtilityPowerKW:        ra.UtilityPowerKW,
			UtilityWaterLPD:       ra.UtilityWaterLPD,
			ExportsPerDay:         ra.ExportsPerDay,
			Capex:                 finance.Money(ra.Capex),
			OpexPerWorker:         finance.Money(ra.OpexPerWorker),
			WagesPerWorker:        finance.Money(ra.WagesPerWorker),
			SupplyChainFirms:      ra.SupplyChainFirms,
			SupplyChainPerWorkers: ra.SupplyChainPerWorker,
			Bid: PharmaBidParams{
				QualityBase:              ra.Bid.QualityBase,
				EducationTermPerGraduate: ra.Bid.EducationTermPerGraduate,
				CompetingFloor:           ra.Bid.CompetingFloor,
				JitterMax:                ra.Bid.JitterMax,
				GraduateDemandPerWorker:  ra.Bid.GraduateDemandPerWorker,
			},
		}
		if p.Bid.JitterMax > maxJitter {
			return fail(field("bid.jitterMax"), "exceeds the symmetric-draw ceiling (2*jitterMax+1 would overflow int64)")
		}
		if spawn := p.SupplyChainCountFor(p.Jobs); spawn > maxSupplyChainFirms {
			return fail(field("supplyChainFirms"), "supply-chain spawn exceeds the validation ceiling")
		}

		ordered = append(ordered, PharmaCampus{Key: ra.Key, Params: p})
		byKey[ra.Key] = p
	}

	return Catalogue{ordered: ordered, byKey: byKey}, nil
}

// EducationEdge is feat.pharmacampus's narrow view of the registered
// engine.education edge (code.json: feat.pharmacampus → engine.education,
// inbound EducationAPI). The exact term/demand shape is the open
// cross-module contract ASM-698, so the composition root adapts the real
// *education.EducationAPI (ResearchPoints and, once it exists, a graduate
// counter) to this surface; tests inject a fake (contract-first,
// stub-forever — the same seam education itself uses for its TrafficAPI).
type EducationEdge interface {
	// GraduateOutput returns the city's education-output term (graduates +
	// research points) that feeds the pharma bid (AC-3), and whether it is
	// available. An unavailable output (unregistered education system)
	// makes the bid fail with ErrEducationOutputUnavailable (AC-8).
	GraduateOutput() (int64, bool)
	// AddGraduateDemand registers graduate/research demand the won campus
	// emits (AC-4). A rejected demand fails the win (AC-8).
	AddGraduateDemand(amount int64) error
	// RemoveGraduateDemand is the compensating inverse of AddGraduateDemand:
	// Win calls it to roll the demand back when a later win-time side effect
	// (the export flow or a firm registration) fails, so a refused win leaves
	// no phantom graduate demand (SEC-121). It is the same compensating-removal
	// shape engine.accelerator's FdiSource uses for its anchor prospect.
	RemoveGraduateDemand(amount int64) error
}

// FirmsEdge is feat.pharmacampus's narrow view of the registered engine.firms
// edge (code.json: feat.pharmacampus → engine.firms, inbound FirmsAPI).
// RegisterFirm and RemoveFirm are both satisfied directly by the real
// *firms.FirmsAPI (SEC-159): RegisterFirm already returns freight.Firm —
// freight's shared firm-snapshot contract — and RemoveFirm is the real API's
// genuine compensating inverse. RemoveFirm removes a registered firm,
// decrements the founded count, and retracts the EventFounded and foundedEvents
// entry RegisterFirm emitted, with NO insolvency semantics — no EventFailed, no
// failedCount++, no unemployment processing — because a refused win must undo a
// registration, not run the §32 one-employer-town shock. The real FirmsAPI's
// §32 closure path (Fail) is consumed directly by the composition root for AC-6,
// never through this seam, so a partial win can never reach insolvency semantics
// by mistake. Tests inject a fake (contract-first, stub-forever).
type FirmsEdge interface {
	// RegisterFirm registers a firm with the given staff headcount and
	// premises zone, returning its firm snapshot. A failure refuses the win
	// (AC-8).
	RegisterFirm(name string, staff int64, premises string) (freight.Firm, error)
	// RemoveFirm is the compensating inverse of RegisterFirm (SEC-140): it
	// removes a previously registered firm, decrements the founded count, and
	// retracts the EventFounded RegisterFirm emitted, with no EventFailed and
	// no failedCount++ — a refused win must leave the FirmsAPI churn ledger
	// exactly as if the firm had never been registered. It must NOT be the §32
	// insolvency path — that is the real FirmsAPI's Fail, whose failedCount++
	// and EventFailed skew the entrepreneur-culture index and churn assertions
	// on every refused win. An unknown FirmID is an error (ErrFirmNotFound on
	// the real API), never a silent no-op — a rollback must surface a removal
	// that could not be performed rather than believe it unwound a registration.
	RemoveFirm(id firms.FirmID) error
}

// TradeEdge is feat.pharmacampus's narrow view of the registered
// engine.freight trade edge (code.json: feat.pharmacampus → engine.freight,
// inbound FreightAPI — the outbound registration is the composition root's to
// wire; see the package doc). The campus's per-day export tonnage routes
// through it so the balance-of-trade screen reads a real flow, never a
// pharma-local export counter (AC-6). The composition root adapts the real
// *freight.FreightAPI (its Export command + Exports ledger) to this surface,
// choosing the output commodity and mode — the manufacture-side split is
// FEAT-052's (ASM-488/ASM-696). Tests inject a fake (contract-first,
// stub-forever — the same seam education uses for its TrafficAPI).
type TradeEdge interface {
	// AddExports routes the campus's per-day export tonnage through the
	// registered trade surface (AC-6). A rejected flow fails the win (AC-8).
	AddExports(tonnes int64) error
	// RemoveExports is the compensating inverse of AddExports: Win calls it to
	// roll the export flow back when a later firm registration fails, so a
	// refused win leaves no phantom export tonnage (SEC-121).
	RemoveExports(tonnes int64) error
}

// WinResult is the outcome of a pharma campus anchor win (AC-4/AC-5/AC-6):
// the registered firm, its employment, the graduate/research demand emitted
// back to education, the supply-chain firms spawned, and the queryable
// export flow.
type WinResult struct {
	FirmID     firms.FirmID
	Employment int64
	Demand     int64
	Spawned    int64
	Exports    int64
}

// Pharma is the resolved pharma-campus facility plus the education/firms
// edges it consumes. The zero value is not usable; construct via [NewPharma].
// It is not itself goroutine-safe — the composition root serializes tick
// work and the consumed edges (FirmsAPI/EducationAPI) carry their own
// locking (AC-11's race suite exercises the real edges).
type Pharma struct {
	params    PharmaCampusParams
	education EducationEdge
	firms     FirmsEdge
	trade     TradeEdge
	seed      uint64

	won     bool
	firmID  firms.FirmID
	spawned int64
	demand  int64
}

// NewPharma constructs a Pharma from its resolved data-sourced parameter set,
// the education edge, the firms edge, the trade edge, and the world seed
// (consumed by the bid jitter draw — AC-9). The firms/trade edges are nil in
// a bid-only Pharma (ResolveBid never touches them); Win requires all three
// edges wired.
//
// The parameter set is validated on entry (SEC-122/SEC-123): a
// directly-constructed PharmaCampusParams — every field is exported, so it
// bypasses the data-file validation — that falls outside the accepted domain
// is rejected with a registry-sourced ErrPharmaDataInvalid rather than being
// allowed to reach Win's spawn loop or ResolveBid's jitter draw.
func NewPharma(params PharmaCampusParams, education EducationEdge, firmsEdge FirmsEdge, trade TradeEdge, seed uint64) (*Pharma, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	return &Pharma{
		params:    params,
		education: education,
		firms:     firmsEdge,
		trade:     trade,
		seed:      seed,
	}, nil
}

// Employment returns the campus's skilled headcount (AC-6).
func (p *Pharma) Employment() int64 { return p.params.Jobs }

// Exports returns the campus's queryable export flow in t/day (AC-6). It is
// the data-sourced figure the won campus routes through the registered trade
// edge ([TradeEdge.AddExports]) at win time — never a pharma-local counter
// that bypasses the balance-of-trade surface.
func (p *Pharma) Exports() int64 { return p.params.ExportsPerDay }

// Won reports whether the campus is a won anchor.
func (p *Pharma) Won() bool { return p.won }

// FirmID returns the registered anchor firm's ID once won, zero otherwise.
func (p *Pharma) FirmID() firms.FirmID { return p.firmID }

// ResolveBid reads the city's education-output term through the registered
// education edge and hands it to MOD-059's bid-resolution stub (anchor.go).
// It does NOT reimplement prospect generation or the competitive-bid
// comparison — it supplies the education term and returns the stub's
// outcome. An unavailable education output fails with
// ErrEducationOutputUnavailable before any state is created (AC-8).
func (p *Pharma) ResolveBid(correlationID string) (BidOutcome, error) {
	if p.education == nil {
		return BidOutcome{}, errs.New(ErrEducationOutputUnavailable, correlationID, map[string]any{
			"reason": "education edge not wired",
		})
	}
	out, ok := p.education.GraduateOutput()
	if !ok {
		return BidOutcome{}, errs.New(ErrEducationOutputUnavailable, correlationID, map[string]any{
			"reason": "education output unavailable",
		})
	}
	// Validate the VALUE, not just the availability flag (SEC-124): a negative
	// output (a buggy or hostile adapter) would flow into EducationBidTerm as a
	// negative term and silently lower the bid below an uneducated city's. The
	// seam contract is non-negative graduates/research points, so a negative
	// figure is rejected rather than trusted.
	if out < 0 {
		return BidOutcome{}, errs.New(ErrEducationOutputUnavailable, correlationID, map[string]any{
			"reason": "education output invalid (negative)",
			"output": out,
		})
	}
	term := p.params.EducationBidTerm(out)
	return ResolvePharmaBid(term, p.params.Bid.CompetingFloor, p.params.Bid.JitterMax, p.seed), nil
}

// Win registers the anchor as a real firm through the firms edge, emits
// graduate/research demand back through the education edge, routes the
// campus's export flow through the registered trade edge, and spawns the
// supply-chain firms (§45 demand injection) — the two-directional loop's
// second leg plus the §45 snowball and the export flow (AC-4/AC-5/AC-6).
//
// The win is atomic (AC-8): the fallible education/trade steps are taken
// before any firm is registered, and every firm registration is tracked so a
// mid-sequence failure rolls back to zero registered firms — a failed win
// refuses the win rather than registering a firm. Every failure returns a
// registry-sourced error.
func (p *Pharma) Win(correlationID string) (WinResult, error) {
	if p.firms == nil {
		return WinResult{}, errs.New(ErrPharmaFirmRegistrationFailed, correlationID, map[string]any{
			"reason": "firms edge not wired",
		})
	}
	if p.education == nil {
		return WinResult{}, errs.New(ErrEducationDemandRejected, correlationID, map[string]any{
			"reason": "education edge not wired",
		})
	}
	if p.trade == nil {
		return WinResult{}, errs.New(ErrPharmaExportRejected, correlationID, map[string]any{
			"reason": "trade edge not wired",
		})
	}
	if p.won {
		return WinResult{}, errs.New(ErrPharmaFirmRegistrationFailed, correlationID, map[string]any{
			"reason": "already won",
		})
	}

	employment := p.params.Jobs
	demand := p.params.GraduateDemandFor(employment)
	exports := p.params.ExportsPerDay
	spawn := p.params.SupplyChainCountFor(employment)

	// Defence-in-depth cap (SEC-122): the parameter set was validated at
	// [NewPharma], but never register an unbounded number of supply-chain
	// firms regardless of how the Pharma was produced.
	if spawn > maxSupplyChainFirms {
		return WinResult{}, errs.New(ErrPharmaDataInvalid, correlationID, map[string]any{
			"reason": "supply-chain spawn exceeds the validation ceiling",
			"spawn":  spawn,
			"max":    maxSupplyChainFirms,
		})
	}

	// The win is atomic (AC-8/SEC-121): every fallible side effect — the
	// graduate/research demand, the export flow, and each registered firm —
	// has a compensating inverse, and rollback undoes every applied effect in
	// reverse order before the failure is surfaced. A registered firm is rolled
	// back via the compensating inverse RemoveFirm, never the §32 insolvency
	// path Fail (SEC-140) — a refused win must undo a registration without
	// emitting a failure for a firm that never existed. A rollback failure is
	// joined onto the primary error (GR#1) rather than silently left behind.
	demandApplied := false
	exportsApplied := false
	var registered []firms.FirmID

	rollback := func() error {
		var ferrs []error
		for _, id := range registered {
			if ferr := p.firms.RemoveFirm(id); ferr != nil {
				ferrs = append(ferrs, ferr)
			}
		}
		if exportsApplied {
			if rerr := p.trade.RemoveExports(exports); rerr != nil {
				ferrs = append(ferrs, rerr)
			}
		}
		if demandApplied {
			if rerr := p.education.RemoveGraduateDemand(demand); rerr != nil {
				ferrs = append(ferrs, rerr)
			}
		}
		return errors.Join(ferrs...)
	}

	// failWin compensates every applied side effect, then surfaces the primary
	// failure joined with any rollback failure — never a silent half-rolled-
	// back win (GR#1).
	failWin := func(code, reason string, cause error) (WinResult, error) {
		ctx := map[string]any{"reason": reason}
		if rbErr := rollback(); rbErr != nil {
			ctx["rollback"] = rbErr.Error()
			cause = errors.Join(cause, rbErr)
		}
		return WinResult{}, errs.Wrap(code, correlationID, cause, ctx)
	}

	// Emit the graduate/research demand FIRST (AC-8): rejecting it refuses the
	// win before any firm is registered, and nothing is applied yet so this
	// rejection needs no rollback.
	if err := p.education.AddGraduateDemand(demand); err != nil {
		return WinResult{}, errs.Wrap(ErrEducationDemandRejected, correlationID, err, map[string]any{
			"reason": "demand rejected",
			"demand": demand,
		})
	}
	demandApplied = true

	// Route the export flow through the registered trade surface (AC-6),
	// never a pharma-local export counter. On rejection the demand above is
	// compensated back (SEC-121).
	if err := p.trade.AddExports(exports); err != nil {
		return failWin(ErrPharmaExportRejected, "export rejected", err)
	}
	exportsApplied = true

	// Register the anchor firm, then the supply-chain firms, tracking every
	// registration so a mid-sequence failure compensates all of them (SEC-121).
	firm, err := p.firms.RegisterFirm(p.params.Name, employment, "SUP3")
	if err != nil {
		return failWin(ErrPharmaFirmRegistrationFailed, "anchor registration", err)
	}
	registered = append(registered, firms.FirmID(firm.ID))

	for i := int64(0); i < spawn; i++ {
		name := p.params.Name + " supply chain " + itoa(int(i))
		sc, err := p.firms.RegisterFirm(name, int64(1), "SUP3")
		if err != nil {
			return failWin(ErrPharmaFirmRegistrationFailed, "supply-chain registration", err)
		}
		registered = append(registered, firms.FirmID(sc.ID))
	}

	p.won = true
	p.firmID = firms.FirmID(firm.ID)
	p.spawned = spawn
	p.demand = demand

	return WinResult{
		FirmID:     p.firmID,
		Employment: employment,
		Demand:     demand,
		Spawned:    spawn,
		Exports:    exports,
	}, nil
}

// itoa is the package-local int→string helper (no strconv.Itoa spam and no
// numeric literal in this file).
func itoa(i int) string { return strconv.Itoa(i) }
