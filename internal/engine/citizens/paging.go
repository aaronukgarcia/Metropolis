package citizens

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// PageStore is the disk-backed LRU paging seam for cold shards (A7, §5.3,
// AC-19): beyond the hot+warm-resident ceiling, cold shards page to disk
// and are reloaded on demand, so resident memory stays bounded regardless
// of city size. It is deliberately decoupled from the serialization format
// — binary cold-shard serialization is int.serializer's reserved
// BinarySerializer (Out of scope), so this package ships a placeholder gob
// codec (the wire struct below) that the real serializer will replace.
//
// NVMe SSD is the stated hardware requirement for >20M local citizens
// (doc.go); this paging path is what that requirement enables.
type PageStore struct {
	dir         string
	maxResident int

	mu       sync.Mutex
	resident map[int]*ColdShard
	order    []int // LRU order: index 0 = least recently used

	// self is the SEC-020 copy guard (atomic.Pointer, mirroring
	// CitizensAPI.self in this package). mu is a sync.Mutex VALUE while
	// resident (a map) and order (a slice) are reference types a struct
	// copy ALIASES — a copy gets its own, independently-zeroed mu over the
	// same referents (the "two locks, one referent" hazard). Stored exactly
	// once, in NewPageStore, before the value is returned to any caller.
	self atomic.Pointer[PageStore]
}

// errPageStoreCopied is returned by Store when called on a struct copy of
// the *PageStore NewPageStore returned (SEC-020 family). A plain sentinel
// (errors.New), mirroring internal/foundation/errs.ErrLoggerCopied's
// precedent for a copy-guard rejection on a type with no registry-sourced
// error of its own.
var errPageStoreCopied = errors.New("citizens: PageStore is a struct copy of another PageStore value (construct via NewPageStore and use that same pointer; do not copy the struct)")

// NewPageStore constructs a page store under dir, keeping at most
// maxResident shards resident. maxResident < 1 means "evict everything"
// (useful for tests that force the paging path).
func NewPageStore(dir string, maxResident int) *PageStore {
	p := &PageStore{
		dir:         dir,
		maxResident: maxResident,
		resident:    make(map[int]*ColdShard),
	}
	// Armed exactly once, before p is returned to any caller (SEC-020).
	p.self.Store(p)
	return p
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other *PageStore value. Deliberately lock-free (a single
// atomic.Pointer.Load) so it is safe to call before p.mu is ever touched —
// see internal/foundation/errs/log.go's Logger.checkNotCopied for the full
// SEC-016 ordering argument.
func (p *PageStore) checkNotCopied() bool {
	return p.self.Load() == p
}

// coldShardWire is the placeholder gob wire format for a ColdShard: an
// exported-field mirror of the columnar layout, pending int.serializer's
// real BinarySerializer.
type coldShardWire struct {
	EpochMonth int64

	IDs            []uint64
	BirthDelta     []int16
	Sexes          []uint8
	Households     []uint64 // widened from uint32 — births-unblock lane, 2026-09-02 (mirrors ColdShard.households)
	Partners       []uint64 // widened from uint32 — births-unblock lane, 2026-09-02 (mirrors ColdShard.partners)
	ChildCount     []uint8
	HomeCells      []uint32
	Districts      []uint16
	Workplaces     []uint32
	Schools        []uint32
	PSociability   []int8
	PAmbition      []int8
	PConscient     []int8
	PNovelty       []int8
	PPhysicality   []int8
	PCommunity     []int8
	PPatience      []int8
	PAesthetic     []int8
	Attainment     []int16
	Stages         []uint8
	Schooling      []int16
	HealthBands    []uint8
	Access         []uint8
	Wealth         []int64
	Employment     []uint8
	SatHousing     []int8
	SatServices    []int8
	SatEnvironment []int8
	SatLeisureFit  []int8
	SatCommute     []int8
	MonthlyUpdates []uint32
}

func (s *ColdShard) toWire() coldShardWire {
	return coldShardWire{
		EpochMonth: s.epochMonth,
		IDs:        s.ids, BirthDelta: s.birthDelta, Sexes: s.sexes,
		Households: s.households, Partners: s.partners, ChildCount: s.childCount,
		HomeCells: s.homeCells, Districts: s.districts,
		Workplaces: s.workplaces, Schools: s.schools,
		PSociability: s.pSociability, PAmbition: s.pAmbition,
		PConscient: s.pConscientious, PNovelty: s.pNovelty,
		PPhysicality: s.pPhysicality, PCommunity: s.pCommunity,
		PPatience: s.pPatience, PAesthetic: s.pAesthetic,
		Attainment: s.attainment, Stages: s.stages, Schooling: s.schooling,
		HealthBands: s.healthBands, Access: s.access, Wealth: s.wealth,
		Employment: s.employment, SatHousing: s.satHousing,
		SatServices: s.satServices, SatEnvironment: s.satEnvironment,
		SatLeisureFit: s.satLeisureFit, SatCommute: s.satCommute,
		MonthlyUpdates: s.monthlyUpdates,
	}
}

func wireToColdShard(w coldShardWire) *ColdShard {
	return &ColdShard{
		epochMonth: w.EpochMonth,
		ids:        w.IDs, birthDelta: w.BirthDelta, sexes: w.Sexes,
		households: w.Households, partners: w.Partners, childCount: w.ChildCount,
		homeCells: w.HomeCells, districts: w.Districts,
		workplaces: w.Workplaces, schools: w.Schools,
		pSociability: w.PSociability, pAmbition: w.PAmbition,
		pConscientious: w.PConscient, pNovelty: w.PNovelty,
		pPhysicality: w.PPhysicality, pCommunity: w.PCommunity,
		pPatience: w.PPatience, pAesthetic: w.PAesthetic,
		attainment: w.Attainment, stages: w.Stages, schooling: w.Schooling,
		healthBands: w.HealthBands, access: w.Access, wealth: w.Wealth,
		employment: w.Employment, satHousing: w.SatHousing,
		satServices: w.SatServices, satEnvironment: w.SatEnvironment,
		satLeisureFit: w.SatLeisureFit, satCommute: w.SatCommute,
		monthlyUpdates: w.MonthlyUpdates,
	}
}

// pathFor returns the on-disk path for a shard's page file.
func (p *PageStore) pathFor(shard int) string {
	if !p.checkNotCopied() {
		return ""
	}
	return filepath.Join(p.dir, fmt.Sprintf("shard-%03d.page", shard))
}

// Load returns the shard, reloading it from disk (and making it resident)
// if it is not already resident. Returns (nil, false) if the shard is
// neither resident nor on disk.
func (p *PageStore) Load(shard int) (*ColdShard, bool) {
	if !p.checkNotCopied() {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.resident[shard]; ok {
		p.touchLocked(shard)
		return s, true
	}
	data, err := os.ReadFile(p.pathFor(shard))
	if err != nil {
		return nil, false
	}
	var w coldShardWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return nil, false
	}
	s := wireToColdShard(w)
	p.makeResidentLocked(shard, s)
	return s, true
}

// Store makes the shard resident, evicting the least-recently-used
// resident shard to disk first if the resident set would exceed
// maxResident. It always persists the shard so a later Load (or a
// different page store over the same dir) can recover it.
func (p *PageStore) Store(shard int, s *ColdShard) error {
	if !p.checkNotCopied() {
		return errPageStoreCopied
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s.toWire()); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p.pathFor(shard), buf.Bytes(), 0o644); err != nil {
		return err
	}
	p.makeResidentLocked(shard, s)
	for p.maxResident >= 0 && len(p.resident) > p.maxResident {
		if err := p.evictOneLocked(); err != nil {
			return err
		}
	}
	return nil
}

// ResidentCount returns the number of currently resident shards.
func (p *PageStore) ResidentCount() int {
	if !p.checkNotCopied() {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.resident)
}

// makeResidentLocked adds shard to the resident set (caller holds mu).
func (p *PageStore) makeResidentLocked(shard int, s *ColdShard) {
	if !p.checkNotCopied() {
		return
	}
	if _, ok := p.resident[shard]; !ok {
		p.order = append(p.order, shard)
	}
	p.resident[shard] = s
}

// touchLocked moves shard to the most-recently-used end (caller holds mu).
func (p *PageStore) touchLocked(shard int) {
	if !p.checkNotCopied() {
		return
	}
	for i, v := range p.order {
		if v == shard {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
	p.order = append(p.order, shard)
}

// evictOneLocked evicts the least-recently-used resident shard (its data
// is already persisted by Store, so eviction only drops the in-memory
// copy).
func (p *PageStore) evictOneLocked() error {
	if !p.checkNotCopied() {
		return errPageStoreCopied
	}
	if len(p.order) == 0 {
		return nil
	}
	oldest := p.order[0]
	p.order = p.order[1:]
	delete(p.resident, oldest)
	return nil
}
