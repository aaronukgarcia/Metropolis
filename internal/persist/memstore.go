package persist

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
)

// MemStore is an in-memory Store, used to fast-unit-test everything
// built on top of Store without touching disk (AC-1's "false-pass
// guard": callers must be typed against the Store interface, provably
// so by swapping this fake in for DiskStore and confirming the calling
// code's tests still pass unmodified). It is not durable — process
// exit loses everything — and exists purely as a test double.
type MemStore struct {
	self   atomic.Pointer[MemStore] // SEC-020 copy guard
	mu     sync.Mutex
	cities map[string]*memCity
}

type memCity struct {
	key          CityKey
	journal      [][]byte
	snapshots    map[SnapshotID][]byte
	seqs         []int64
	worldSeed    uint64
	worldSeedSet bool
	gameMode     string // BUG-737
	gameModeSet  bool   // BUG-737
	modeEpoch    bool   // BUG-737 round-2 lead ruling, 2026-09-05
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns an empty in-memory Store.
func NewMemStore() *MemStore {
	m := &MemStore{cities: make(map[string]*memCity)}
	m.self.Store(m)
	return m
}

// checkNotCopied guards against a MemStore value being copied after
// construction (which would copy its sync.Mutex in a locked state and
// alias the cities map). SEC-020: every mutating method calls this
// before taking any lock. A MemStore must always be used via the
// *MemStore returned by NewMemStore.
func (s *MemStore) checkNotCopied() error {
	if s.self.Load() != s {
		return ErrStoreCopied
	}
	return nil
}

func cityMapKey(city CityKey) string {
	return city.TenantID + "\x00" + city.CityID
}

func (s *MemStore) getOrCreate(city CityKey) *memCity {
	k := cityMapKey(city)
	c, ok := s.cities[k]
	if !ok {
		c = &memCity{key: city, snapshots: make(map[SnapshotID][]byte)}
		s.cities[k] = c
	}
	return c
}

// AppendJournal implements Store.
func (s *MemStore) AppendJournal(ctx context.Context, city CityKey, record []byte) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cp := make([]byte, len(record))
	copy(cp, record)

	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.getOrCreate(city)
	c.journal = append(c.journal, cp)
	return nil
}

// ReadJournal implements Store.
func (s *MemStore) ReadJournal(ctx context.Context, city CityKey) ([][]byte, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := cityMapKey(city)
	c, ok := s.cities[k]
	if !ok {
		return [][]byte{}, nil
	}
	out := make([][]byte, len(c.journal))
	copy(out, c.journal)
	return out, nil
}

// PutSnapshot implements Store.
func (s *MemStore) PutSnapshot(ctx context.Context, city CityKey, snapshot []byte) (SnapshotID, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	cp := make([]byte, len(snapshot))
	copy(cp, snapshot)

	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.getOrCreate(city)
	next := int64(1)
	if len(c.seqs) > 0 {
		next = c.seqs[len(c.seqs)-1] + 1
	}
	id := SnapshotID(formatSeq(next))
	c.seqs = append(c.seqs, next)
	c.snapshots[id] = cp
	return id, nil
}

// GetSnapshot implements Store.
func (s *MemStore) GetSnapshot(ctx context.Context, city CityKey, id SnapshotID) ([]byte, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok {
		return nil, ErrNotFound
	}
	data, ok := c.snapshots[id]
	if !ok {
		return nil, ErrNotFound
	}
	return data, nil
}

// ListSnapshots implements Store.
func (s *MemStore) ListSnapshots(ctx context.Context, city CityKey) ([]SnapshotID, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok {
		return []SnapshotID{}, nil
	}
	seqs := make([]int64, len(c.seqs))
	copy(seqs, c.seqs)
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	ids := make([]SnapshotID, 0, len(seqs))
	for _, sq := range seqs {
		ids = append(ids, SnapshotID(formatSeq(sq)))
	}
	return ids, nil
}

// DeleteSnapshot implements Store. Removing an id that does not exist (or
// never existed, or the city was never written) is a no-op success —
// pruning is idempotent.
func (s *MemStore) DeleteSnapshot(ctx context.Context, city CityKey, id SnapshotID) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok {
		return nil
	}
	delete(c.snapshots, id)
	kept := make([]int64, 0, len(c.seqs))
	for _, sq := range c.seqs {
		if SnapshotID(formatSeq(sq)) != id {
			kept = append(kept, sq)
		}
	}
	c.seqs = kept
	return nil
}

// ListCities implements Store.
func (s *MemStore) ListCities(ctx context.Context, tenant string) ([]CityKey, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []CityKey
	for _, c := range s.cities {
		// BUG-488: same "seed-only entry is invisible" rule as Exists —
		// a city that only ever had SetWorldSeedIfAbsent called for it,
		// with no real journal record or snapshot, must not appear
		// here, matching DiskStore's meta.json-gated ListCities.
		if c.key.TenantID == tenant && (len(c.journal) > 0 || len(c.snapshots) > 0) {
			keys = append(keys, c.key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CityID < keys[j].CityID })
	return keys, nil
}

// Exists implements Store.
func (s *MemStore) Exists(ctx context.Context, city CityKey) (bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok {
		return false, nil
	}
	// BUG-488: a city entry can now exist in the map purely because
	// SetWorldSeedIfAbsent stamped it (getOrCreate is also called from
	// there) — Exists' documented contract is "has any durably committed
	// journal or snapshot data", so a seed-only entry with neither must
	// still report false, exactly matching DiskStore's meta.json-gated
	// semantics (meta.json is only ever written by a real
	// AppendJournal/PutSnapshot, never by the seed stamp).
	return len(c.journal) > 0 || len(c.snapshots) > 0, nil
}

// SetWorldSeedIfAbsent implements Store (BUG-488).
func (s *MemStore) SetWorldSeedIfAbsent(ctx context.Context, city CityKey, seed uint64) (uint64, error) {
	if err := s.checkNotCopied(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.getOrCreate(city)
	if !c.worldSeedSet {
		c.worldSeed = seed
		c.worldSeedSet = true
	}
	return c.worldSeed, nil
}

// WorldSeed implements Store (BUG-488).
func (s *MemStore) WorldSeed(ctx context.Context, city CityKey) (uint64, bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return 0, false, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok || !c.worldSeedSet {
		return 0, false, nil
	}
	return c.worldSeed, true, nil
}

// SetGameModeIfAbsent implements Store (BUG-737, mirroring
// SetWorldSeedIfAbsent above exactly).
func (s *MemStore) SetGameModeIfAbsent(ctx context.Context, city CityKey, mode string) (string, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// BUG-737 P1-2 (round finding): never stamp an empty mode -- mirrors
	// DiskStore.SetGameModeIfAbsent's identical guard exactly. Report
	// whatever (if anything) is already on record without creating a map
	// entry as a side effect of a no-op call.
	if mode == "" {
		if c, ok := s.cities[cityMapKey(city)]; ok && c.gameModeSet && c.gameMode != "" {
			return c.gameMode, nil
		}
		return "", nil
	}

	c := s.getOrCreate(city)
	if !c.gameModeSet || c.gameMode == "" {
		c.gameMode = mode
		c.gameModeSet = true
	}
	return c.gameMode, nil
}

// GameMode implements Store (BUG-737, mirroring WorldSeed above exactly).
func (s *MemStore) GameMode(ctx context.Context, city CityKey) (string, bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", false, err
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	// BUG-737 P1-2 (round finding): a stored EMPTY mode reads back as
	// absent too -- defensive-in-depth mirroring DiskStore's identical
	// read-side guard.
	if !ok || !c.gameModeSet || c.gameMode == "" {
		return "", false, nil
	}
	return c.gameMode, true, nil
}

// SetGameModeEpoch implements Store (BUG-737 round-2 lead ruling,
// 2026-09-05, mirroring DiskStore's identical seed.json-embedded flag
// exactly): idempotent, a no-op once already marked for city.
func (s *MemStore) SetGameModeEpoch(ctx context.Context, city CityKey) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// BUG-737 re-round-3 finding P2-1: requires a durably recorded world
	// seed, mirroring DiskStore's identical guard exactly (see
	// ErrGameModeEpochWithoutSeed's doc comment) — MemStore's own
	// in-memory representation was never at risk of the seedless-0-seed
	// bug (worldSeedSet is a genuinely separate bool from worldSeed's
	// value, unlike DiskStore's pre-fix seed.json which had no way to
	// represent "no seed" other than omitting the whole file), but the
	// Store CONTRACT must reject the same caller ordering identically on
	// both implementations, not just whichever one happened to be safe
	// by accident. Deliberately does NOT call getOrCreate on the refusal
	// path — no side effect on a rejected call, matching every other
	// refusal in this file.
	c, ok := s.cities[cityMapKey(city)]
	if !ok || !c.worldSeedSet {
		return ErrGameModeEpochWithoutSeed
	}
	c.modeEpoch = true
	return nil
}

// HasGameModeEpoch implements Store (BUG-737 round-2 lead ruling,
// 2026-09-05).
func (s *MemStore) HasGameModeEpoch(ctx context.Context, city CityKey) (bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cities[cityMapKey(city)]
	if !ok {
		return false, nil
	}
	return c.modeEpoch, nil
}
