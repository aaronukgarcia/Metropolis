package policies

import (
	"sync"
	"testing"
)

// TestConcurrentEnactAndQuery runs enactment and queries across goroutines
// under -race (AC-16): the PoliciesAPI's mutex-guarded state must never
// tear when policies are enacted and queried concurrently across shards.
func TestConcurrentEnactAndQuery(t *testing.T) {
	a := testAPI(t)
	a.projections = &recordingProjections{horizon: 72}
	a.finance = &recordingFinance{}

	addPolicy(t, a, simplePolicy("cycling", ScopeCitywide, "movement.cycling.share", 0.15))
	addPolicy(t, a, simplePolicy("wage", ScopeCitywide, "economy.wage.level", 0.05))
	districtID, err := a.CreateDistrict("CBD", cells(1, 2, 3))
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	addPolicy(t, a, simplePolicy("parkAccess", ScopeDistrict, "wellbeing.parkAccess", 0.20))

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := a.Enact("cycling", Scope{Kind: ScopeCitywide}); err == nil {
					_ = a.Policies()
				}
				_, _ = a.CombinedEffect("movement.cycling.share", Scope{Kind: ScopeCitywide})
				_, _ = a.ResolveScope("parkAccess", Scope{Kind: ScopeDistrict, District: districtID})
				_ = a.CoefficientState()
			}
		}()
	}
	wg.Wait()
}
