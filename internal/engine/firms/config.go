package firms

import (
	"encoding/json"
	"os"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GR#15 data-file contract: config is the validated,
// ordered view of data/firms.json, and LoadConfig is this package's
// self-contained loader (os.ReadFile + encoding/json + buildConfig, the
// engine.freight/engine.mining pattern — GR#20: a module's own data file
// is loaded without importing an unregistered edge). Every tunable number
// the firms module consumes — stage staff floors, premise classes, the
// founding-composite weights, the services-demand exponent/multiplier,
// and the credit deposit-to-lending ratio / stage spreads / base-rate
// cycle — comes from here, never a Go literal (GR#15). Loading is
// all-or-nothing: any failure returns ErrFirmsDataInvalid and no config.

// config is the fully-validated, ordered view of data/firms.json.
type config struct {
	Stages         []stageConfig
	Founding       foundingConfig
	ServicesDemand servicesDemandConfig
	Credit         creditConfig
	LabourMarket   labourMarketConfig
}

// stageConfig is one data/firms.json "stages" entry: the staff-count floor
// that gates entry to the stage and the build zone class that must be
// securable as premises (AC-6/AC-7).
type stageConfig struct {
	Stage        Stage
	MinStaff     int64
	PremiseClass string // a build.ZoneType slug
}

// foundingConfig is the founding-composite weight set (AC-2/AC-3), all
// per-mille contributions added to the base before the [0,1000] clamp.
type foundingConfig struct {
	BasePerMille             int64
	AmbitionPerMille         int64
	EducationPerMille        int64
	SectorExperiencePerMille int64
	WealthPerMille           int64
	PremisesPerMille         int64
	DemandPerMille           int64
	ExitFounderBoostPerMille int64
}

// servicesDemandConfig parameterises the superlinear services-demand form
// Demand(n) = multiplier × n^(exponent) (AC-11).
type servicesDemandConfig struct {
	ExponentPerMille int64 // 1300 = 1.3
	Multiplier       int64
}

// creditConfig parameterises the banking layer (AC-13/AC-14) and the
// culture-index window (AC-10).
type creditConfig struct {
	DepositToLendingRatioPerMille int64
	CultureWindowMonths           int64
	StageSpreadBp                 map[Stage]int64
	BaseRateCycle                 []ratePoint
}

// labourMarketConfig parameterises the vacancy-vs-workforce aggregate
// (AC-21): the data-declared staff ceiling for the Enterprise stage. §45
// leaves Enterprise unbounded ("250+"), so its vacancy band has no spec
// upper edge and must be data-sourced (never a Go-literal headcount,
// GR#15) — a directional placeholder pending M2 tuning (balance-number
// regime).
type labourMarketConfig struct {
	EnterpriseCeiling int64
}

// ratePoint is one step of the off-map base-rate cycle (AC-14).
type ratePoint struct {
	Month      int64
	BaseRateBp int64
}

// rawFirmsData is data/firms.json's JSON wire shape, decoded only to be
// validated and folded into the ordered config above.
type rawFirmsData struct {
	Version        int             `json:"version"`
	Stages         []rawStage      `json:"stages"`
	Founding       rawFounding     `json:"founding"`
	ServicesDemand rawServices     `json:"servicesDemand"`
	Credit         rawCredit       `json:"credit"`
	LabourMarket   rawLabourMarket `json:"labourMarket"`
}

type rawStage struct {
	Stage        string `json:"stage"`
	MinStaff     int64  `json:"minStaff"`
	PremiseClass string `json:"premiseClass"`
}

type rawFounding struct {
	BasePerMille             int64 `json:"basePerMille"`
	AmbitionPerMille         int64 `json:"ambitionPerMille"`
	EducationPerMille        int64 `json:"educationPerMille"`
	SectorExperiencePerMille int64 `json:"sectorExperiencePerMille"`
	WealthPerMille           int64 `json:"wealthPerMille"`
	PremisesPerMille         int64 `json:"premisesPerMille"`
	DemandPerMille           int64 `json:"demandPerMille"`
	ExitFounderBoostPerMille int64 `json:"exitFounderBoostPerMille"`
}

type rawServices struct {
	ExponentPerMille int64 `json:"exponentPerMille"`
	Multiplier       int64 `json:"multiplier"`
}

type rawCredit struct {
	DepositToLendingRatioPerMille int64            `json:"depositToLendingRatioPerMille"`
	CultureWindowMonths           int64            `json:"cultureWindowMonths"`
	StageSpreadBp                 map[string]int64 `json:"stageSpreadBp"`
	BaseRateCycle                 []rawRatePoint   `json:"baseRateCycle"`
}

type rawRatePoint struct {
	Month      int64 `json:"month"`
	BaseRateBp int64 `json:"baseRateBp"`
}

type rawLabourMarket struct {
	EnterpriseCeiling int64 `json:"enterpriseCeiling"`
}

// fileFirms is data/firms.json's filename, relative to the resolved data
// directory (see foundation/data.ResolveDataDir).
const fileFirms = "firms.json"

// stageOrder is the canonical Startup→Small→Medium→Enterprise order
// (AC-6), a slice so LoadConfig validates and stores stages in this fixed
// order (GR#21).
var stageOrder = []Stage{StageStartup, StageSmall, StageMedium, StageEnterprise}

// LoadConfig reads, decodes and validates data/firms.json from path,
// returning the ordered config or ErrFirmsDataInvalid. Every failure is a
// registry-sourced *errs.E — never a panic, never a silent default.
func LoadConfig(path, correlationID string) (config, error) {
	var zero config
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrFirmsDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	var raw rawFirmsData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrFirmsDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}
	return buildConfig(raw, path, correlationID)
}

func buildConfig(raw rawFirmsData, path, correlationID string) (config, error) {
	fail := func(field, rule string) (config, error) {
		return config{}, errs.New(ErrFirmsDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
		})
	}
	var c config

	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}

	// Stages: all four required, in canonical order, with strictly
	// increasing non-negative staff floors (AC-6's advancement bands).
	byStage := make(map[Stage]rawStage, len(raw.Stages))
	for _, rs := range raw.Stages {
		st, ok := parseStage(rs.Stage)
		if !ok {
			return fail("stages.stage", "unknown stage (want startup/small/medium/enterprise)")
		}
		if _, dup := byStage[st]; dup {
			return fail("stages."+rs.Stage, "duplicate stage entry")
		}
		byStage[st] = rs
	}
	var prevFloor int64 = -1
	for _, st := range stageOrder {
		rs, ok := byStage[st]
		if !ok {
			return fail("stages."+stageName(st), "required stage missing")
		}
		if rs.MinStaff < 0 {
			return fail("stages."+stageName(st)+".minStaff", "must be >= 0")
		}
		if prevFloor >= 0 && rs.MinStaff <= prevFloor {
			return fail("stages."+stageName(st)+".minStaff", "must be strictly greater than the previous stage floor")
		}
		prevFloor = rs.MinStaff
		if rs.PremiseClass == "" {
			return fail("stages."+stageName(st)+".premiseClass", "required, must be a build zone slug")
		}
		c.Stages = append(c.Stages, stageConfig{Stage: st, MinStaff: rs.MinStaff, PremiseClass: rs.PremiseClass})
	}

	// Labour market: Enterprise has no §45 upper bound ("250+"), so its
	// vacancy band ceiling is data-declared (AC-21). It must be at least the
	// Enterprise staff floor so the band is non-degenerate (a ceiling below
	// the floor would make every Enterprise firm's headroom clamp to 0).
	enterpriseFloor := c.Stages[len(c.Stages)-1].MinStaff // StageEnterprise is last in stageOrder
	if raw.LabourMarket.EnterpriseCeiling < enterpriseFloor {
		return fail("labourMarket.enterpriseCeiling", "must be >= the Enterprise minStaff floor")
	}
	c.LabourMarket = labourMarketConfig{EnterpriseCeiling: raw.LabourMarket.EnterpriseCeiling}

	// Founding weights: every per-mille contribution non-negative.
	f := raw.Founding
	nonNeg := []struct {
		field string
		v     int64
	}{
		{"founding.basePerMille", f.BasePerMille},
		{"founding.ambitionPerMille", f.AmbitionPerMille},
		{"founding.educationPerMille", f.EducationPerMille},
		{"founding.sectorExperiencePerMille", f.SectorExperiencePerMille},
		{"founding.wealthPerMille", f.WealthPerMille},
		{"founding.premisesPerMille", f.PremisesPerMille},
		{"founding.demandPerMille", f.DemandPerMille},
		{"founding.exitFounderBoostPerMille", f.ExitFounderBoostPerMille},
	}
	for _, n := range nonNeg {
		if n.v < 0 {
			return fail(n.field, "must be >= 0")
		}
	}
	c.Founding = foundingConfig(f)

	// Services demand: a superlinear exponent (>1000 per-mille = >1.0), and
	// — because integer truncation can defeat superlinearity for exponents
	// barely above 1 (SEC-105) — the configured form must ACTUALLY satisfy
	// Demand(2) > 2·Demand(1), the tightest truncation point.
	if raw.ServicesDemand.ExponentPerMille <= 1000 {
		return fail("servicesDemand.exponentPerMille", "must be > 1000 (superlinear, AC-11)")
	}
	if raw.ServicesDemand.Multiplier <= 0 {
		return fail("servicesDemand.multiplier", "must be > 0")
	}
	if servicesDemandAt(raw.ServicesDemand.ExponentPerMille, raw.ServicesDemand.Multiplier, 2) <=
		2*servicesDemandAt(raw.ServicesDemand.ExponentPerMille, raw.ServicesDemand.Multiplier, 1) {
		return fail("servicesDemand.exponentPerMille",
			"must make Demand(2) > 2*Demand(1) under truncation (superlinearity, AC-11/SEC-105)")
	}
	c.ServicesDemand = servicesDemandConfig{
		ExponentPerMille: raw.ServicesDemand.ExponentPerMille,
		Multiplier:       raw.ServicesDemand.Multiplier,
	}

	// Credit: ratio in (0,1000], non-negative window, per-stage spreads,
	// and a non-empty month-sorted base-rate cycle.
	cr := raw.Credit
	if cr.DepositToLendingRatioPerMille <= 0 || cr.DepositToLendingRatioPerMille > 1000 {
		return fail("credit.depositToLendingRatioPerMille", "must be in (0,1000]")
	}
	if cr.CultureWindowMonths <= 0 {
		return fail("credit.cultureWindowMonths", "must be > 0")
	}
	c.Credit.DepositToLendingRatioPerMille = cr.DepositToLendingRatioPerMille
	c.Credit.CultureWindowMonths = cr.CultureWindowMonths

	c.Credit.StageSpreadBp = make(map[Stage]int64, len(cr.StageSpreadBp))
	for _, st := range stageOrder {
		v, ok := cr.StageSpreadBp[stageName(st)]
		if !ok {
			return fail("credit.stageSpreadBp."+stageName(st), "required stage spread missing")
		}
		if v < 0 {
			return fail("credit.stageSpreadBp."+stageName(st), "must be >= 0")
		}
		c.Credit.StageSpreadBp[st] = v
	}
	if len(cr.BaseRateCycle) == 0 {
		return fail("credit.baseRateCycle", "required, must be non-empty")
	}
	var prevMonth int64 = -1
	for i, rp := range cr.BaseRateCycle {
		if rp.Month < 0 {
			return fail("credit.baseRateCycle["+itoa(i)+"].month", "must be >= 0")
		}
		if prevMonth >= 0 && rp.Month <= prevMonth {
			return fail("credit.baseRateCycle["+itoa(i)+"].month", "must be strictly increasing")
		}
		prevMonth = rp.Month
		if rp.BaseRateBp < 0 {
			return fail("credit.baseRateCycle["+itoa(i)+"].baseRateBp", "must be >= 0")
		}
		c.Credit.BaseRateCycle = append(c.Credit.BaseRateCycle, ratePoint(rp))
	}
	// Ensure the cycle starts at month 0 so BorrowingRate is defined for
	// every non-negative month.
	if c.Credit.BaseRateCycle[0].Month != 0 {
		return fail("credit.baseRateCycle[0].month", "must start at month 0")
	}

	return c, nil
}

// parseStage maps a stage slug onto the Stage enum.
func parseStage(s string) (Stage, bool) {
	for _, st := range stageOrder {
		if stageName(st) == s {
			return st, true
		}
	}
	return 0, false
}
