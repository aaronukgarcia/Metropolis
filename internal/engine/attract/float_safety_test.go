package attract

import (
	"math"
	"testing"
)

// TestNonFiniteConfigRejected is the FEAT-086 float64-path regression for
// defect #1: a finite-but-absurd config value (MigrationRate = 1e308, a
// reputation Max of 1e308, an A_world of 1e308) is rejected at construction
// with a registry-sourced error, rather than accepted and later producing
// Net=+Inf. Pre-fix, Config.validate only checked isFinite (no upper bound),
// so MigrationRate=1e308 passed and `rate*(score-aWorld)` overflowed to +Inf.
func TestNonFiniteConfigRejected(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"hugeMigrationRate", func(c *Config) { c.MigrationRate = 1e308 }},
		{"hugeReputationMax", func(c *Config) { c.Reputation.Max = 1e308 }},
		{"hugeAWorld", func(c *Config) { c.World = NewStaticWorldPool(1e308) }},
		{"negativeAWorldHuge", func(c *Config) { c.World = NewStaticWorldPool(-1e308) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg)
			if _, err := New(cfg, 7, "corr-attract"); err == nil {
				t.Fatalf("New accepted %s (a value that drives the migration math non-finite)", tc.name)
			} else {
				isErr(t, err, ErrConfigInvalid)
			}
		})
	}
}

// TestGBackstopRejectsNonFinite is the FEAT-086 float64-path regression for
// G(): a NaN/±Inf gap (or a gap whose product with the rate overflows) must
// return a registry-sourced error, never +Inf/NaN with err==nil. Pre-fix, G
// returned the raw product unchecked.
func TestGBackstopRejectsNonFinite(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())

	for _, x := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		if _, err := a.G(x); err == nil {
			t.Fatalf("G(%v) returned no error for a non-finite gap", x)
		} else {
			isErr(t, err, ErrConfigInvalid)
		}
	}

	got, err := a.G(35.0)
	if err != nil {
		t.Fatalf("G(35) errored: %v", err)
	}
	if got != 35.0 {
		t.Fatalf("G(35) = %v, want 35", got)
	}
}

// flakyWorldPool is a stateful WorldPool that returns a finite value at
// construction and can be poisoned to return NaN/±Inf later — standing in
// for the future dynamic §4 pool.
type flakyWorldPool struct {
	value    float64
	poison   float64
	poisoned bool
}

func (p *flakyWorldPool) AWorld() float64 {
	if p.poisoned {
		return p.poison
	}
	return p.value
}

func (p *flakyWorldPool) poisonWith(v float64) { p.poison = v; p.poisoned = true }

// TestWorldPoolRevalidationRejectsNonFinite is the FEAT-086 float64-path
// regression for defect #2: New validates A_world once, but ApplyMigration
// re-reads it — a dynamic pool that turns NaN/±Inf after construction must
// be re-validated on every read and surface a registry error, never
// Net=NaN/±Inf with err==nil. Pre-fix, the re-read was unvalidated. The
// re-validation happens before any dependency query, so no wiring is needed.
func TestWorldPoolRevalidationRejectsNonFinite(t *testing.T) {
	for _, poison := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		pool := &flakyWorldPool{value: 50}
		cfg := validConfig()
		cfg.World = pool
		a, err := New(cfg, 7, "corr-attract")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		pool.poisonWith(poison)
		res, err := a.ApplyMigration(MigrationCommand{Month: 0, HousingVacancy: 0, JunctionThroughput: 0})
		if err == nil {
			t.Fatalf("ApplyMigration returned Net=%v (err nil) with a poisoned A_world %v", res.Net, poison)
		}
		if !isFinite(res.Net) {
			t.Fatalf("MigrationResult.Net = %v on the error path, want a finite zero value", res.Net)
		}
		isErr(t, err, ErrConfigInvalid)
	}
}

// TestFiniteScoreNeverEscapesNonFinite is the class-sweep assertion: for a
// normal valid config, every observable float (A, Net, AWorld, Reputation)
// is finite across a migration run.
func TestFiniteScoreNeverEscapesNonFinite(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())
	var ids []uint64
	for id := uint64(1); id <= 10; id++ {
		ids = append(ids, id)
	}
	if err := ca.SeedColdRecords(maxAmbitionRecords(ids), "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	_ = a.SetTermInputs(TermInputs{JobAvailability: 60, ServiceCoverage: 60, Environment: 60, LeisureFit: 60, Safety: 60})
	for m := int64(0); m < 5; m++ {
		res, err := a.ApplyMigration(MigrationCommand{Month: m, ResidentIDs: ids, HousingVacancy: 100, JunctionThroughput: 100})
		if err != nil {
			t.Fatalf("ApplyMigration: %v", err)
		}
		if !isFinite(res.A) || !isFinite(res.Net) || !isFinite(res.AWorld) || !isFinite(res.Reputation) {
			t.Fatalf("non-finite observable escaped a valid config: %+v", res)
		}
	}
}
