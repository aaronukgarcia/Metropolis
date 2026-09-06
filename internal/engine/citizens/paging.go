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

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
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
	// worldSeed stamps every page file this store writes (BUG-713 P3: page
	// files carry no city identity) with the owning CitizensAPI's world
	// seed, and Load refuses to hand back a shard whose stamped seed
	// disagrees with this one -- a page directory accidentally reused
	// across two different cities/worlds must never silently resurrect the
	// wrong city's data under a matching shard index. worldSeed == 0 means
	// "identity checking not requested" (every pre-BUG-664/pre-stamp test
	// call site, and any caller that legitimately does not care) -- Load
	// skips verification entirely in that case, exactly today's behaviour.
	worldSeed uint64

	mu       sync.Mutex
	resident map[int]*ColdShard
	order    []int // LRU order: index 0 = least recently used

	// dirReady (BUG-775) records that dir has already been created by a
	// prior Store call, so Store's os.MkdirAll -- a real syscall on every
	// single call, measured at ~36s of a 332s profiled run (~11%) purely
	// from being re-issued once per shard eviction rather than once ever --
	// runs at most once per PageStore lifetime. mu-guarded (Store already
	// holds mu for the write itself); false is the correct zero value, so a
	// freshly-constructed PageStore behaves exactly as before its first
	// Store call.
	dirReady bool

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
// (useful for tests that force the paging path). worldSeed stamps every
// page file this store writes and is verified on every Load (BUG-713 P3);
// pass 0 to opt out of identity checking (every existing direct PageStore
// test call site does this deliberately, since they are not exercising
// cross-city identity at all).
func NewPageStore(dir string, maxResident int, worldSeed uint64) *PageStore {
	p := &PageStore{
		dir:         dir,
		maxResident: maxResident,
		worldSeed:   worldSeed,
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
	// WorldSeed (BUG-713 P3) stamps which CitizensAPI world this page file
	// belongs to -- set by PageStore.Store from the store's own worldSeed,
	// never by ColdShard.toWire (a ColdShard has no notion of world
	// identity of its own). Zero on any page file written before this
	// stamp existed -- gob's self-describing wire format decodes a field
	// absent from the old encoding as its zero value, so an old .page file
	// loads exactly as before (decode-and-ignore, no migration needed);
	// Load treats a zero stamp as "unstamped, not verifiable" rather than
	// a mismatch.
	WorldSeed uint64

	// ShardIndex (round ACCEPT on BUG-712/BUG-713, F2) stamps which shard
	// slot this page file's OWN data belongs to -- set by PageStore.Store
	// from the shard argument it was called with, never derived from the
	// file's path. Without this, coldShardWire carried no shard identity
	// of its own: copying/renaming shard-005.page to shard-009.page's path
	// made Load adopt shard 5's citizens wholesale as shard 9's data (a
	// pathFor-trusts-the-caller hole, distinct from the WorldSeed check
	// above, which only catches a WHOLE OTHER CITY's page directory, not a
	// same-city shard shuffled to the wrong slot). ShardIndexStamped
	// mirrors the WorldSeed==0 "unstamped" convention but as an explicit
	// bool rather than overloading zero, because shard 0 is itself a
	// legitimate, real shard index -- unlike WorldSeed, 0 cannot double as
	// "never stamped" here without falsely flagging every genuine shard-0
	// page file written before this stamp existed as a mismatch on shard
	// 0's own path. Zero-value ShardIndexStamped=false on any page file
	// written before this stamp existed (gob decode-and-ignore), so Load
	// skips the check entirely for old files exactly as it does for
	// WorldSeed==0.
	ShardIndex        int
	ShardIndexStamped bool

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

// wireToColdShard reconstructs a *ColdShard from its placeholder gob wire
// form. BUG-666: the id->row index is a derived, non-serialized structure
// (nothing in coldShardWire carries it), so a shard built this way — bypassing
// append entirely — must have its index rebuilt before any rowOf lookup
// against it can be trusted; rebuildIndexLocked does that from the decoded
// ids column.
func wireToColdShard(w coldShardWire) *ColdShard {
	s := &ColdShard{
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
	s.rebuildIndexLocked()
	return s
}

// pathFor returns the on-disk path for a shard's page file.
func (p *PageStore) pathFor(shard int) string {
	if !p.checkNotCopied() {
		return ""
	}
	return filepath.Join(p.dir, fmt.Sprintf("shard-%03d.page", shard))
}

// Load returns the shard, reloading it from disk (and making it resident)
// if it is not already resident. Returns (nil, false, nil) ONLY if no page
// file exists at all for shard -- a genuine "never persisted" cache miss,
// safe for the caller to substitute a fresh empty shard for. Returns a
// non-nil error (registry-sourced, GR#7) -- and NEVER a shard -- for every
// other failure mode, each DISTINCT so a caller can never confuse one for
// "never persisted" again (round ACCEPT on BUG-712/BUG-713, F1/F2, closing
// the BUG-687-class defect where a corrupt page file and an absent one were
// both silently treated as "no citizens here"):
//   - a page file IS found but gob decode fails (truncated/corrupt file):
//     ErrPageDecodeCorrupt.
//   - a page file IS found and decodes, but was stamped with a different
//     world's seed than p.worldSeed (BUG-713 P3, a page directory reused
//     across two different cities): ErrPageWorldMismatch.
//   - a page file IS found and decodes, but its OWN stamped shard index
//     disagrees with the shard argument (BUG-713 F2, e.g. copied/renamed
//     to a different shard's path): ErrPageShardIndexMismatch.
//
// correlationID is attached to every such error per GR#1. p.worldSeed == 0
// (identity checking not requested, e.g. every pre-BUG-713 direct PageStore
// test) skips the WorldSeed check; an unstamped ShardIndexStamped == false
// page file (written before F2 existed) skips the shard-index check --
// both exactly the pre-stamp decode-and-ignore behaviour.
func (p *PageStore) Load(shard int, correlationID string) (*ColdShard, bool, error) {
	if !p.checkNotCopied() {
		return nil, false, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.resident[shard]; ok {
		p.touchLocked(shard)
		return s, true, nil
	}
	data, err := os.ReadFile(p.pathFor(shard))
	if err != nil {
		return nil, false, nil
	}
	var w coldShardWire
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return nil, false, errs.Wrap(ErrPageDecodeCorrupt, correlationID, err, map[string]any{
			"shard": shard,
		})
	}
	if p.worldSeed != 0 && w.WorldSeed != 0 && w.WorldSeed != p.worldSeed {
		return nil, false, errs.New(ErrPageWorldMismatch, correlationID, map[string]any{
			"shard": shard, "stamped": w.WorldSeed, "want": p.worldSeed,
		})
	}
	if w.ShardIndexStamped && w.ShardIndex != shard {
		return nil, false, errs.New(ErrPageShardIndexMismatch, correlationID, map[string]any{
			"shard": shard, "stamped": w.ShardIndex,
		})
	}
	s := wireToColdShard(w)
	p.makeResidentLocked(shard, s)
	return s, true, nil
}

// Store makes the shard resident, evicting the least-recently-used
// resident shard to disk first if the resident set would exceed
// maxResident. It always persists the shard so a later Load (or a
// different page store over the same dir) can recover it. Stamps the
// written page file with p.worldSeed (BUG-713 P3), 0 if identity checking
// was not requested.
func (p *PageStore) Store(shard int, s *ColdShard) error {
	if !p.checkNotCopied() {
		return errPageStoreCopied
	}
	w := s.toWire()
	w.WorldSeed = p.worldSeed
	w.ShardIndex = shard
	w.ShardIndexStamped = true
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(w); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dirReady {
		if err := os.MkdirAll(p.dir, 0o755); err != nil {
			return err
		}
		p.dirReady = true
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

// Forget removes shard from PageStore's own resident cache WITHOUT
// touching disk (BUG-713: see evictOverBudgetLocked's doc comment in
// registry.go for the double-cache aliasing this closes). Safe/no-op if
// shard is not currently resident. The caller is asserting the shard's
// current in-memory state is already durably persisted (normally, this is
// called immediately after a successful Store of the same shard) -- Forget
// itself never writes or reads the disk file, it only drops the pointer
// this store was holding onto.
func (p *PageStore) Forget(shard int) {
	if !p.checkNotCopied() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forgetLocked(shard)
}

// forgetLocked is Forget's caller-holds-mu half.
func (p *PageStore) forgetLocked(shard int) {
	if !p.checkNotCopied() {
		return
	}
	if _, ok := p.resident[shard]; !ok {
		return
	}
	delete(p.resident, shard)
	for i, v := range p.order {
		if v == shard {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
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
