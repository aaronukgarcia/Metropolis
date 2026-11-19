package compose

// FEAT-9995385620 round r4: the host's rule-version pre-check IS the restore,
// run dry. Three attempts to PREDICT the restore from a static reading of the
// journal and the snapshot headers were each broken by a history the
// prediction could not see (the last: a snapshot whose tail is rejected on
// replay makes the restore walk back to an older snapshot whose version is
// then refused). So the pre-check no longer predicts anything: it runs the
// real restoreLatest on a throwaway composition over a store that can only
// read.

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// errDryRunWriteRefused is the distinct sentinel a [DryRunStore] returns for
// every refused write. It is internal: the dry run only ever counts it.
var errDryRunWriteRefused = errors.New("rule-version dry run: write refused")

// DryRunStore is a persist.Store wrapper that passes every read through to
// the wrapped store and REFUSES every write with errDryRunWriteRefused,
// counting each attempt. It does not embed persist.Store: every method is
// written out, so a method added to the interface later fails to compile here
// instead of passing a new write through silently.
//
// Refused: AppendJournal, PutSnapshot, DeleteSnapshot, SetGameModeIfAbsent,
// SetGameModeEpoch. SetWorldSeedIfAbsent is counted and writes nothing, but
// reports success (see its comment): refusing it would stop a dry restore of
// a never-stamped city before the rule checks that follow the stamp.
type DryRunStore struct {
	inner persist.Store

	readJournal, getSnapshot, listSnapshots, otherReads atomic.Int64
	appendJournal, putSnapshot, deleteSnapshot          atomic.Int64
	setWorldSeed, setGameMode, setGameModeEpoch         atomic.Int64
}

var _ persist.Store = (*DryRunStore)(nil)

// NewDryRunStore wraps inner. It is exported for the host's tests, which
// count the reads and writes of a dry run.
func NewDryRunStore(inner persist.Store) *DryRunStore { return &DryRunStore{inner: inner} }

// DryRunCounts is a snapshot of what a dry run did to its store.
type DryRunCounts struct {
	JournalReads, SnapshotReads, SnapshotLists int64
	// Writes is every write ATTEMPTED (refused or simulated), by method.
	Writes map[string]int64
}

// Counts returns the counters. Writes holds only methods attempted at least once.
func (s *DryRunStore) Counts() DryRunCounts {
	c := DryRunCounts{
		JournalReads:  s.readJournal.Load(),
		SnapshotReads: s.getSnapshot.Load(),
		SnapshotLists: s.listSnapshots.Load(),
		Writes:        map[string]int64{},
	}
	for name, n := range map[string]*atomic.Int64{
		"AppendJournal": &s.appendJournal, "PutSnapshot": &s.putSnapshot, "DeleteSnapshot": &s.deleteSnapshot,
		"SetWorldSeedIfAbsent": &s.setWorldSeed, "SetGameModeIfAbsent": &s.setGameMode, "SetGameModeEpoch": &s.setGameModeEpoch,
	} {
		if v := n.Load(); v > 0 {
			c.Writes[name] = v
		}
	}
	return c
}

// AppendJournal is refused and counted.
func (s *DryRunStore) AppendJournal(_ context.Context, _ persist.CityKey, _ []byte) error {
	s.appendJournal.Add(1)
	return errDryRunWriteRefused
}

// ReadJournal passes through.
func (s *DryRunStore) ReadJournal(ctx context.Context, city persist.CityKey) ([][]byte, error) {
	s.readJournal.Add(1)
	return s.inner.ReadJournal(ctx, city)
}

// PutSnapshot is refused and counted.
func (s *DryRunStore) PutSnapshot(_ context.Context, _ persist.CityKey, _ []byte) (persist.SnapshotID, error) {
	s.putSnapshot.Add(1)
	return "", errDryRunWriteRefused
}

// GetSnapshot passes through.
func (s *DryRunStore) GetSnapshot(ctx context.Context, city persist.CityKey, id persist.SnapshotID) ([]byte, error) {
	s.getSnapshot.Add(1)
	return s.inner.GetSnapshot(ctx, city, id)
}

// ListSnapshots passes through.
func (s *DryRunStore) ListSnapshots(ctx context.Context, city persist.CityKey) ([]persist.SnapshotID, error) {
	s.listSnapshots.Add(1)
	return s.inner.ListSnapshots(ctx, city)
}

// DeleteSnapshot is refused and counted.
func (s *DryRunStore) DeleteSnapshot(_ context.Context, _ persist.CityKey, _ persist.SnapshotID) error {
	s.deleteSnapshot.Add(1)
	return errDryRunWriteRefused
}

// ListCities passes through.
func (s *DryRunStore) ListCities(ctx context.Context, tenant string) ([]persist.CityKey, error) {
	s.otherReads.Add(1)
	return s.inner.ListCities(ctx, tenant)
}

// Exists passes through.
func (s *DryRunStore) Exists(ctx context.Context, city persist.CityKey) (bool, error) {
	s.otherReads.Add(1)
	return s.inner.Exists(ctx, city)
}

// SetWorldSeedIfAbsent writes nothing and reports that seed is the one of
// record. The real wake stamps the seed inside Wire, BEFORE the restore runs,
// so by the time the real restore calls this (stampWorldSeedIfNeeded) the
// seed is always recorded and the call never happens. The throwaway
// composition is wired with no store, so it has not stamped; a refused call
// here would end the dry restore early for a never-stamped (pre-BUG-488) city
// and let a retired-version refusal that follows pass the pre-check. Reporting
// success without writing gives the dry run the same path as the real one.
func (s *DryRunStore) SetWorldSeedIfAbsent(_ context.Context, _ persist.CityKey, seed uint64) (uint64, error) {
	s.setWorldSeed.Add(1)
	return seed, nil
}

// WorldSeed passes through.
func (s *DryRunStore) WorldSeed(ctx context.Context, city persist.CityKey) (uint64, bool, error) {
	s.otherReads.Add(1)
	return s.inner.WorldSeed(ctx, city)
}

// SetGameModeIfAbsent is refused and counted.
func (s *DryRunStore) SetGameModeIfAbsent(_ context.Context, _ persist.CityKey, _ string) (string, error) {
	s.setGameMode.Add(1)
	return "", errDryRunWriteRefused
}

// GameMode passes through.
func (s *DryRunStore) GameMode(ctx context.Context, city persist.CityKey) (string, bool, error) {
	s.otherReads.Add(1)
	return s.inner.GameMode(ctx, city)
}

// SetGameModeEpoch is refused and counted.
func (s *DryRunStore) SetGameModeEpoch(_ context.Context, _ persist.CityKey) error {
	s.setGameModeEpoch.Add(1)
	return errDryRunWriteRefused
}

// HasGameModeEpoch passes through.
func (s *DryRunStore) HasGameModeEpoch(ctx context.Context, city persist.CityKey) (bool, error) {
	s.otherReads.Add(1)
	return s.inner.HasGameModeEpoch(ctx, city)
}

// PrecheckRuleVersions is the host's rule-version pre-check, and it is a DRY
// RUN of the real restore: it builds a throwaway engine (the caller's world
// seed) and a throwaway composition wired like the real wake's (same game
// mode; no store, so no journaling and no stamps; no citizen paging, so no
// folder, claim file or page file), runs the real restoreLatest on it over a
// [DryRunStore] and closes it. A rule-version refusal (IsRuleVersionRefusal)
// is returned; every other outcome (success, a non-rule error, the store's
// write refusal) returns nil and leaves the decision to the real restore,
// which reports its own error. A host calls it BEFORE it repairs a journal or
// wires the real composition, so a city that will be refused for its rule
// version is not modified at all.
//
// Cost, accepted by the lead for now: a wake restores twice (this dry run and
// the real one), because only the restore itself can know that a snapshot's
// tail is rejected on replay and that the walk goes back to an older snapshot.
//
// The dry run reads the journal once and the snapshots the restore itself
// reads. tools/journalscan, which cannot run an engine, uses the static
// [DecideRuleVersions] instead.
func PrecheckRuleVersions(ctx context.Context, store persist.Store, city persist.CityKey, worldSeed uint64, gameMode string) error {
	return precheckRuleVersionsDeps(ctx, NewDryRunStore(store), city, worldSeed, &Deps{GameMode: gameMode})
}

// Test seam: tests dry-run under Deps the server never uses; that cannot
// change a rule verdict because every rule check runs before any command is
// replayed and the snapshot walk uses its own throwaway composition.
//
// precheckRuleVersionsDeps is the dry run with the throwaway composition's
// Deps supplied by the caller (the tests build their cities with a quiet
// coastal module and must dry-run under the same one). deps must carry no
// PersistStore, CommandJournaler or CitizenPaging: the dry run writes nothing.
func precheckRuleVersionsDeps(ctx context.Context, ro *DryRunStore, city persist.CityKey, worldSeed uint64, deps *Deps) error {
	e := core.NewEngine(core.WithWorldSeed(worldSeed))
	c, err := Wire(e, deps)
	if err != nil {
		return nil // not a rule-version question; the real wake reports it
	}
	defer func() { _ = c.Close() }()
	if _, _, _, err := restoreLatest(ctx, e, c, ro, city); err != nil && IsRuleVersionRefusal(err) {
		return err
	}
	return nil
}
