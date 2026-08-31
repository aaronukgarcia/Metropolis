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
	key       CityKey
	journal   [][]byte
	snapshots map[SnapshotID][]byte
	seqs      []int64
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
		if c.key.TenantID == tenant {
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
	_, ok := s.cities[cityMapKey(city)]
	return ok, nil
}
