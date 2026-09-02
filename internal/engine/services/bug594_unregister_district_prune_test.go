package services

import (
	"reflect"
	"testing"
)

// TestUnregisterService_PrunesEmptiedDistrictKey is the direct regression
// test for BUG-594 (the save-participant round's finding): UnregisterService
// must delete a district's key from districtDemand the instant stripping the
// unregistered service empties that district's inner map, because the save
// participant emits one "services.districtdemand" record per (district,
// service) pair — a district key that survives only as an empty map does
// NOT survive a save/load round-trip, producing a live-vs-restored
// divergence (DistrictIDs lists it live, ErrUnknownDistrict after restore).
//
// Table cases cover both branches the round's ruling distinguishes:
//   - demolish the district's LAST service: the district must fully
//     disappear (DistrictIDs no longer lists it, CoverageForDistrict fails
//     MET-G1211-class ErrUnknownDistrict) — matching what a restore would
//     produce.
//   - demolish ONE of several services in a district: the district must
//     survive, carrying only the remaining service's demand.
//
// Each case also proves the fix via an explicit save/load round-trip: the
// live shape (DistrictIDs + CoverageForDistrict) must equal the restored
// shape byte-for-byte in the observable sense (same IDs, same success/error
// split, same coverage numbers for a surviving district).
func TestUnregisterService_PrunesEmptiedDistrictKey(t *testing.T) {
	cases := []struct {
		name           string
		build          func(t *testing.T, a *ServicesAPI)
		wantDistricts  []DistrictID
		survivingDistr DistrictID // "" if none expected to remain
	}{
		{
			name: "demolish the only service in a district removes the district",
			build: func(t *testing.T, a *ServicesAPI) {
				t.Helper()
				registerService(t, a, "clinic-1", ServiceHealthcare, 10, 5, 1)
				if err := a.UpdateDistrictDemand("D1", "clinic-1", 50, 1); err != nil {
					t.Fatalf("UpdateDistrictDemand: %v", err)
				}
				if err := a.UnregisterService("clinic-1"); err != nil {
					t.Fatalf("UnregisterService: %v", err)
				}
			},
			wantDistricts: nil,
		},
		{
			name: "demolish one of two services in a district leaves the district with the survivor",
			build: func(t *testing.T, a *ServicesAPI) {
				t.Helper()
				registerService(t, a, "clinic-1", ServiceHealthcare, 10, 5, 1)
				registerService(t, a, "clinic-2", ServiceHealthcare, 20, 5, 1)
				if err := a.UpdateDistrictDemand("D1", "clinic-1", 50, 1); err != nil {
					t.Fatalf("UpdateDistrictDemand(clinic-1): %v", err)
				}
				if err := a.UpdateDistrictDemand("D1", "clinic-2", 30, 1); err != nil {
					t.Fatalf("UpdateDistrictDemand(clinic-2): %v", err)
				}
				if err := a.UnregisterService("clinic-1"); err != nil {
					t.Fatalf("UnregisterService: %v", err)
				}
			},
			wantDistricts:  []DistrictID{"D1"},
			survivingDistr: "D1",
		},
		{
			name: "demolishing the last service in one of two districts prunes only that district",
			build: func(t *testing.T, a *ServicesAPI) {
				t.Helper()
				registerService(t, a, "clinic-1", ServiceHealthcare, 10, 5, 1)
				registerService(t, a, "clinic-2", ServiceHealthcare, 20, 5, 1)
				if err := a.UpdateDistrictDemand("D1", "clinic-1", 50, 1); err != nil {
					t.Fatalf("UpdateDistrictDemand(D1,clinic-1): %v", err)
				}
				if err := a.UpdateDistrictDemand("D2", "clinic-2", 30, 1); err != nil {
					t.Fatalf("UpdateDistrictDemand(D2,clinic-2): %v", err)
				}
				if err := a.UnregisterService("clinic-1"); err != nil {
					t.Fatalf("UnregisterService: %v", err)
				}
			},
			wantDistricts:  []DistrictID{"D2"},
			survivingDistr: "D2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testAPI(t)
			tc.build(t, a)

			// --- Live assertions ---
			liveIDs, err := a.DistrictIDs()
			if err != nil {
				t.Fatalf("DistrictIDs: %v", err)
			}
			if len(liveIDs) != len(tc.wantDistricts) || !reflect.DeepEqual(liveIDs, append([]DistrictID{}, tc.wantDistricts...)) {
				t.Fatalf("live DistrictIDs = %v, want %v", liveIDs, tc.wantDistricts)
			}

			var liveCov DistrictCoverage
			var liveCovErr error
			if tc.survivingDistr != "" {
				liveCov, liveCovErr = a.CoverageForDistrict(tc.survivingDistr)
				if liveCovErr != nil {
					t.Fatalf("CoverageForDistrict(%s) live: %v", tc.survivingDistr, liveCovErr)
				}
			} else {
				_, liveCovErr = a.CoverageForDistrict("D1")
				if liveCovErr == nil {
					t.Fatalf("CoverageForDistrict(D1) live: want ErrUnknownDistrict, got nil")
				}
				assertCode(t, liveCovErr, ErrUnknownDistrict)
			}

			// --- Save/load round-trip: restored shape must equal live shape ---
			dst := testAPI(t)
			recs := drain(t, NewSaveParticipant(a))
			if err := replay(t, dst, recs); err != nil {
				t.Fatalf("replay: %v", err)
			}

			loadedIDs, err := dst.DistrictIDs()
			if err != nil {
				t.Fatalf("DistrictIDs (loaded): %v", err)
			}
			if !reflect.DeepEqual(loadedIDs, liveIDs) {
				t.Fatalf("BUG-594 divergence: live DistrictIDs=%v, restored=%v", liveIDs, loadedIDs)
			}

			if tc.survivingDistr != "" {
				loadedCov, err := dst.CoverageForDistrict(tc.survivingDistr)
				if err != nil {
					t.Fatalf("CoverageForDistrict(%s) loaded: %v", tc.survivingDistr, err)
				}
				if loadedCov != liveCov {
					t.Fatalf("BUG-594 divergence: live coverage=%+v, restored=%+v", liveCov, loadedCov)
				}
			} else {
				_, loadedErr := dst.CoverageForDistrict("D1")
				if loadedErr == nil {
					t.Fatalf("CoverageForDistrict(D1) loaded: want ErrUnknownDistrict, got nil")
				}
				assertCode(t, loadedErr, ErrUnknownDistrict)
			}
		})
	}
}
