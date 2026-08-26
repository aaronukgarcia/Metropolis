package social

import (
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fileName is this module's balance-data file (GR#15): every numeric
// magnitude §40 leaves unquantified lives here, never as a Go literal.
const fileName = "social.json"

// CaseloadData is the JSON shape of the decomposed per-driver caseload rate
// set. Each rate is only ever read by field name (never ranged), so JSON
// object-key order is irrelevant to determinism (GR#21).
type CaseloadData struct {
	FamilyPerDeprivation             float64 `json:"familyPerDeprivation"`
	FamilyPerCrowdingStress          float64 `json:"familyPerCrowdingStress"`
	FamilyPerFinancialStress         float64 `json:"familyPerFinancialStress"`
	CrisisFamilyCases                float64 `json:"crisisFamilyCases"`
	HomelessnessPerDeprivation       float64 `json:"homelessnessPerDeprivation"`
	HomelessnessPerUnemploymentMonth float64 `json:"homelessnessPerUnemploymentMonth"`
	HomelessnessPerFinancialStress   float64 `json:"homelessnessPerFinancialStress"`
	DisabilityPerDeprivation         float64 `json:"disabilityPerDeprivation"`
	FosteringPerCrowdingStress       float64 `json:"fosteringPerCrowdingStress"`
	FosteringPerFinancialStress      float64 `json:"fosteringPerFinancialStress"`
	AddictionPerPressure             float64 `json:"addictionPerPressure"`
	UnemploymentCapMonths            float64 `json:"unemploymentCapMonths"`
}

// SocialData is the JSON shape of data/social.json.
type SocialData struct {
	Version                   int          `json:"version"`
	RoughSleepingLocation     string       `json:"roughSleepingLocation"`
	Caseload                  CaseloadData `json:"caseload"`
	HostelCapacity            int64        `json:"hostelCapacity"`
	FosterCapacity            int64        `json:"fosterCapacity"`
	CarersReleasedPerFunding  float64      `json:"carersReleasedPerFundingUnit"`
	InterventionHarmThreshold float64      `json:"interventionHarmThreshold"`
}

// validate satisfies the foundation/data.Validator contract (via pointer
// receiver) so the generic data.Load runs schema validation immediately
// after JSON decoding. It checks field presence; the per-field domain checks
// run in Config.validate after conversion (the split mirrors foundation.data's
// "per-field vs semantic" division).
func (d *SocialData) validate() error {
	if d.Version <= 0 {
		return &data.FieldError{Field: "version", Rule: "required, must be a positive integer"}
	}
	return nil
}

// Validate implements the foundation/data.Validator interface for
// *SocialData.
func (d *SocialData) Validate() error { return d.validate() }

// config converts the decoded JSON shape into the runtime Config (AC-2's
// data-sourced caseload rates).
func (d *SocialData) config() Config {
	return Config{
		RoughSleepingLocation: d.RoughSleepingLocation,
		Caseload: CaseloadConfig{
			FamilyPerDeprivation:             d.Caseload.FamilyPerDeprivation,
			FamilyPerCrowdingStress:          d.Caseload.FamilyPerCrowdingStress,
			FamilyPerFinancialStress:         d.Caseload.FamilyPerFinancialStress,
			CrisisFamilyCases:                d.Caseload.CrisisFamilyCases,
			HomelessnessPerDeprivation:       d.Caseload.HomelessnessPerDeprivation,
			HomelessnessPerUnemploymentMonth: d.Caseload.HomelessnessPerUnemploymentMonth,
			HomelessnessPerFinancialStress:   d.Caseload.HomelessnessPerFinancialStress,
			DisabilityPerDeprivation:         d.Caseload.DisabilityPerDeprivation,
			FosteringPerCrowdingStress:       d.Caseload.FosteringPerCrowdingStress,
			FosteringPerFinancialStress:      d.Caseload.FosteringPerFinancialStress,
			AddictionPerPressure:             d.Caseload.AddictionPerPressure,
			UnemploymentCapMonths:            d.Caseload.UnemploymentCapMonths,
		},
		HostelCapacity:               d.HostelCapacity,
		FosterCapacity:               d.FosterCapacity,
		CarersReleasedPerFundingUnit: d.CarersReleasedPerFunding,
		InterventionHarmThreshold:    d.InterventionHarmThreshold,
	}
}

// Load reads and schema-validates data/social.json from dir (via
// foundation/data's generic Load, GR#15/GR#17) and returns a ready
// *SocialAPI with its balance Config populated. correlationID is attached to
// every error this call (and the returned API's methods) construct (GR#1).
// Every failure is a registry-sourced *errs.E — never a silent default
// substitution, never a panic.
func Load(dir, correlationID string) (*SocialAPI, error) {
	path := filepath.Join(dir, fileName)
	d, err := data.Load[SocialData, *SocialData](path, correlationID)
	if err != nil {
		// Route load errors through the helper to supply full {field,dir,cause} ctx.
		// field is empty for load-time errors (the error came from data.Load, not a
		// specific field validation). dir is the directory we tried to load from.
		return nil, errs.Wrap(ErrSocialDataInvalid, correlationID, err, map[string]any{
			"field": "",
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	cfg := d.config()
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	return New(cfg, 0, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then Loads it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*SocialAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}
