//go:build !race

package core

// allocCountingReliable — see the race-tagged twin of this file
// (screen_registry_racecheck_race_test.go) for the full rationale.
// Without -race, testing.AllocsPerRun's mallocs-counter diff is exact
// and reproducible run over run (verified: 10/10 and more, see the test
// doc comment), so the allocation-count equality assertion stays live.
const allocCountingReliable = true
