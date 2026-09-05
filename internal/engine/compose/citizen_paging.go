package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-764 — wiring citizens.CitizensAPI.EnableDiskPaging (the BUG-664 disk-
// paging seam, hardened by BUG-712/BUG-713) in as a REAL compose.Wire
// option. Before this file, EnableDiskPaging had zero production callers:
// compose never called it, so no runnable path (cmd/metropolis,
// cmd/metroserve, internal/harness/headless) could ever bound resident
// citizen-shard memory, which is a hard blocker for the 100M-citizens goal
// (docs/planning/go-engine-100m-proving-plan.md) — there was no way to even
// MEASURE the cloud engine at that scale, let alone run it.
//
// # Why this lives in compose, not citizens or persist
//
// citizens.CitizensAPI has no concept of "which city this is" beyond its own
// WorldSeed (registry.go's own doc comment on EnableDiskPaging: "a separate
// lineage id lives one layer up at the composition root/persist.CityKey and
// is out of this package's registered dependency graph"). persist.CityKey IS
// that lineage id, but internal/persist has no registered edge to
// internal/engine/citizens (GR#20) and must not gain one just for this.
// compose already holds BOTH edges (compose->citizens, compose->persist are
// pre-existing, registered), so the per-identity page-directory derivation
// and the identity-mismatch refusal belong here — no new cross-module edge
// is introduced (per BUG-764's own scope).
//
// # Round finding (opus-round-bug764, F1/F2) — REJECT on the first landing
//
// The identity stamp alone answers "does this directory belong to my
// lineage" — it says NOTHING about whether ANOTHER LIVE composition of the
// exact same lineage is already writing to it right now. Two compositions
// of the identical persist.CityKey wired against the same PageDir (the
// CityHost eviction race below, or two metroserve processes sharing one
// /data mount across a Container Apps revision rollover) pass the identity
// check trivially (it IS their own identity) and then silently interleave
// writes to the SAME .page files — no error, no latch, a PopulationHash
// that diverges from a solo control on every run. pageDirClaim (below)
// closes that: an EXCLUSIVE, O_EXCL-created claim file, checked/created
// alongside the identity stamp, released only on Composition.Close(). F2
// (a directory holding real .page files but no identity stamp was silently
// ADOPTED) is closed by checkForeignUnstampedPages.

// pageIdentityFileName is the sidecar Wire writes into a citizen-paging
// directory the FIRST time any city enables paging under it, recording the
// exact identity (persist.CityKey + WorldSeed) that directory belongs to —
// mirroring persist.DiskStore's own seed.json/meta.json sidecar discipline
// (internal/persist/diskstore.go) one level up, at the paging-directory
// granularity rather than the persist-city granularity.
const pageIdentityFileName = "identity.json"

// pageClaimFileName is the EXCLUSIVE-open marker (round finding F1) proving
// at most one live composition holds a given page directory at a time. See
// pageDirClaim's own doc comment.
const pageClaimFileName = "claim.pid"

// DefaultCitizenPagingMaxResidentShards is the suggested
// CitizenPagingOptions.MaxResidentShards a caller may use as a sensible
// starting budget when it has no better number of its own (e.g.
// cmd/metroserve's -citizen-page-budget flag default): a quarter of the
// numColdShards total (256/4 = 64), which keeps a meaningful working set
// resident (avoiding thrash on an amortised cold-pass sweep, coldpass.go,
// which touches one shard per day-tick) while still bounding resident
// memory well below "every shard, always" — the pre-BUG-764 default this
// knob exists to move away from. Compose itself never reads this constant
// (Wire has no default of its own: MaxResidentShards < 1 when Enabled is
// simply refused, ErrCitizenPagingConfigInvalid) — it exists purely as a
// documented, SSOT starting point for callers (GR#3: no re-typed magic
// literal at each call site).
const DefaultCitizenPagingMaxResidentShards = 64

// pageIdentity is the sidecar payload. Deliberately opaque strings/uint64,
// never a persist.CityKey value directly, so this file has no import-time
// coupling to persist.CityKey's own JSON shape changing independently.
type pageIdentity struct {
	TenantID  string `json:"tenant_id"`
	CityID    string `json:"city_id"`
	WorldSeed uint64 `json:"world_seed"`
}

// pageClaimRecord is the JSON payload of pageClaimFileName: enough to
// diagnose a stale claim by hand (pid + which lineage + when), never
// enough on its own to PROVE staleness across a container boundary — see
// processLooksAlive's own per-GOOS doc comment (citizen_paging_pid_*.go)
// for exactly what it can and cannot tell an operator.
type pageClaimRecord struct {
	PID      int    `json:"pid"`
	TenantID string `json:"tenant_id"`
	CityID   string `json:"city_id"`
	// Epoch is a monotonic-within-process nanosecond timestamp (time.Now().
	// UnixNano()) identifying THIS claim attempt — not a cross-restart
	// sequence counter (that would require a durably-persisted counter this
	// item's scope does not need): its only job is to let an operator or a
	// later claim attempt tell two successive claims of the same directory
	// apart in a log/diagnostic, never to itself prove liveness.
	Epoch int64 `json:"epoch"`
}

// CitizenPagingOptions is BUG-764's Wire-time knob for enabling
// citizens.CitizensAPI's disk-backed shard paging (BUG-664) in a real,
// running composition. The zero value (Enabled: false) is the default and
// reproduces every pre-BUG-764 Wire call byte-for-byte: paging stays off,
// every cold shard stays permanently resident, exactly as before this knob
// existed.
type CitizenPagingOptions struct {
	// Enabled turns disk paging on for the citizens module this Wire call
	// constructs/receives. false (the default) is a pure no-op — Wire never
	// even looks at PageDir/MaxResidentShards.
	Enabled bool

	// MaxResidentShards is the residency ceiling passed straight to
	// citizens.CitizensAPI.EnableDiskPaging: beyond this many of the
	// numColdShards (256) cold shards resident at once, the least-recently-
	// used resident shards are evicted to PageDir. Must be >= 1 when Enabled
	// (ErrCitizenPagingConfigInvalid otherwise) — citizens' own
	// ErrInvalidPagingBudget guard would refuse the same value one layer
	// down, but failing here gives a compose-owned error a caller of this
	// package already knows how to handle, rather than an unexpected
	// wrapped citizens-package code.
	MaxResidentShards int

	// PageDir is the root directory citizen page files are written under
	// for THIS composition. Must be non-empty when Enabled. Callers building
	// a durable, per-lineage path (the production shape — see CitizenPageDir
	// below) pass compose.CitizenPageDir(root, city) here; a caller that
	// passes a bare, hand-picked directory still gets the identity-mismatch
	// AND exclusive-claim safety nets below as a backstop, but loses the
	// "two lineages, two directories" guarantee CitizenPageDir provides by
	// construction.
	PageDir string

	// Reclaim (round finding F1, opus-round-bug764) forces this Wire call
	// to TAKE OVER an existing exclusive claim on PageDir rather than
	// refuse (ErrCitizenPagingDirectoryClaimed). This is the operator-driven
	// recovery path for a claim left behind by a process that crashed
	// without releasing it (Composition.Close() never ran).
	//
	// Reclaim is NOT auto-inferred from a liveness check except on linux
	// (citizen_paging_pid_linux.go's processLooksAlive: a real, same-
	// container kill(pid,0) check) — and even there ONLY within the SAME
	// container/PID-namespace the claim was recorded in. Two overlapping
	// metroserve processes on separate Container Apps revisions sharing one
	// /data mount each have their OWN pid 1, 2, 3...: container B's pid 42
	// existing or not says nothing about container A's claimant, so a
	// cross-container stale claim can NEVER be auto-detected as dead by
	// this process — it always requires an operator to set Reclaim
	// explicitly (cmd/metroserve's -citizen-paging-reclaim flag), after
	// confirming out-of-band (e.g. the old revision is fully drained) that
	// no other live composition still holds it. Setting Reclaim against a
	// claim that is NOT actually stale reintroduces exactly the F1 hazard
	// this claim exists to prevent — it is a deliberate override, not a
	// safe default.
	Reclaim bool
}

// pageDirClaim is the live handle Wire hands back (via wireCitizenPaging)
// for a successfully claimed PageDir. Composition.Close() calls Release to
// give the directory back — idempotent, safe to call on a nil claim (the
// paging-disabled case) or twice.
//
// RE-ROUND FIX (P2, opus-reround-bug764): record is the EXACT stamp this
// handle itself wrote to claim.pid (pid/tenant/city/epoch) — Release
// compares it against the file's CURRENT content before deleting anything.
// Without this, an unconditional os.Remove let a STALE handle's Close()
// (e.g. composition A, superseded by a Reclaim into composition B) delete
// B's live claim out from under it — a third opener would then wire
// cleanly against the same directory B still believes it exclusively
// holds: the F1 corruption shape again, just reached through Close()
// instead of a second concurrent Wire.
type pageDirClaim struct {
	path   string
	record pageClaimRecord
}

// Release removes this claim's marker file, making the directory claimable
// again — but ONLY if the file's CURRENT content still matches the exact
// stamp this handle itself wrote. A mismatch means someone else (a Reclaim,
// almost certainly) has since taken over the directory; deleting THEIR
// claim would strip their exclusion guard, so Release logs
// ErrCitizenPagingClaimOwnershipMismatch (a registry warning, GR#1 — never
// silently dropped) and leaves the file untouched instead.
//
// A nil receiver (paging was never enabled), a missing file (already
// released, or never successfully created), and an unreadable/corrupt file
// (logged, not deleted — an ownership check that CANNOT be positively
// confirmed must never fall back to deleting anyway) are all silent-or-
// logged no-ops — Release must never be the thing that panics or fatally
// errors during shutdown.
func (p *pageDirClaim) Release() {
	if p == nil {
		return
	}
	current, err := readPageClaim(p.path)
	if err != nil {
		if os.IsNotExist(err) {
			return // already released (or never created) -- normal
		}
		// Unreadable (corrupt/torn) claim file: cannot confirm ownership,
		// so do NOT delete -- an unconfirmed guess here risks the exact
		// live-holder-disarmed shape this fix exists to close.
		_ = errs.New(ErrCitizenPagingClaimOwnershipMismatch, errs.NewCorrelationID(), map[string]any{
			"dir": filepath.Dir(p.path), "reason": "claim file unreadable at Release time", "cause": err.Error(),
		})
		return
	}
	if current != p.record {
		_ = errs.New(ErrCitizenPagingClaimOwnershipMismatch, errs.NewCorrelationID(), map[string]any{
			"dir":           filepath.Dir(p.path),
			"recordedPid":   current.PID,
			"recordedEpoch": current.Epoch,
			"pid":           p.record.PID,
			"epoch":         p.record.Epoch,
		})
		return
	}
	_ = os.Remove(p.path) // confirmed ours; best-effort remove (a leaked claim only blocks a FUTURE re-open, never corrupts data)
}

// CitizenPageDir derives the per-identity citizen-paging directory BUG-764
// requires: <root>/pages/<hash>, where hash is a deterministic, fixed-length,
// filesystem-safe digest of (city.TenantID, city.CityID, worldSeed) — mirrors
// internal/persist/key.go's encodeSegment (SHA-256 over each field
// separately, then concatenated, exactly like cmd/metroserve's own
// seedForCity in cityhost.go) so two different lineages, even ones sharing
// the SAME world seed (the BUG-713 round's proven aliasing risk: "worldSeed
// alone aliases two same-seed cities"), always resolve to two different
// directories.
//
// root is typically the same durable root a caller's persist.Store lives
// under (e.g. cmd/metroserve's -persist-dir, which the Azure deploy docs
// already document as the /data mount) — pages/ sits alongside that store's
// own tenant/city hash tree, never inside it, so paging never collides with
// journal/snapshot files.
//
// city may be the zero value (no persist identity available — an ephemeral,
// non-persisted composition). In that case the directory is keyed on
// worldSeed alone; this is a DOCUMENTED, narrower guarantee (two different
// ephemeral compositions sharing a seed WOULD alias — CitizenPageDir alone
// canNOT distinguish them, since both would derive the identical path). The
// identity stamp (wireCitizenPaging, below) still refuses that specific
// collision case if the two ephemeral compositions carry a non-zero,
// DIFFERENT persist.CityKey; it does NOT protect two compositions that are
// BOTH zero-value CityKey at the SAME seed — those are truly
// indistinguishable by any identity this function or the stamp can observe,
// which is exactly why production callers (cmd/metroserve) always pass a
// real, non-zero CityKey.
func CitizenPageDir(root string, city persist.CityKey, worldSeed uint64) string {
	tenant := sha256.Sum256([]byte(city.TenantID))
	cityH := sha256.Sum256([]byte(city.CityID))
	var seedBuf [8]byte
	for i := range seedBuf {
		seedBuf[i] = byte(worldSeed >> (8 * (7 - i)))
	}
	sum := sha256.Sum256(append(append(tenant[:], cityH[:]...), seedBuf[:]...))
	return filepath.Join(root, "pages", hex.EncodeToString(sum[:]))
}

// wireCitizenPaging is Wire's BUG-764 call site: validates opts, creates
// PageDir, refuses an unstamped directory that already holds foreign pages
// (F2), verifies (or stamps) the identity sidecar, takes the EXCLUSIVE
// directory claim (F1), and calls c.EnableDiskPaging. Called AFTER c is
// fully resolved (deps.Citizens or a freshly constructed
// citizens.NewCitizensAPI) and BEFORE any other hook registers, mirroring
// c.SetSeason's own post-construction wiring call immediately above it in
// Wire.
//
// Returns the claim handle the caller (Wire) must store on the resulting
// Composition and release via Composition.Close() when the composition is
// torn down — nil when paging is disabled. A validation failure, identity
// mismatch, foreign-pages refusal, or claim refusal returns a nil claim and
// an error, and leaves c untouched (EnableDiskPaging is never called) —
// Wire's existing "no partially-wired engine on error" contract (AC-4)
// extends unchanged to this knob.
func wireCitizenPaging(c *citizens.CitizensAPI, opts CitizenPagingOptions, city persist.CityKey, worldSeed uint64, correlationID string) (*pageDirClaim, error) {
	if !opts.Enabled {
		return nil, nil
	}
	if opts.PageDir == "" {
		return nil, errs.New(ErrCitizenPagingConfigInvalid, correlationID, map[string]any{"reason": "PageDir is empty"})
	}
	if opts.MaxResidentShards < 1 {
		return nil, errs.New(ErrCitizenPagingConfigInvalid, correlationID, map[string]any{
			"reason": "MaxResidentShards must be >= 1", "maxResidentShards": opts.MaxResidentShards,
		})
	}

	if err := os.MkdirAll(opts.PageDir, 0o755); err != nil {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
			"reason": "MkdirAll failed", "pageDir": opts.PageDir, "cause": err.Error(),
		})
	}

	// F2: an unstamped directory that already holds real .page files is
	// unidentifiable and possibly foreign -- refuse rather than silently
	// adopt it. A genuinely fresh directory (no .page files yet) is exempt
	// -- that is the ordinary first-use case every existing test relies on.
	stampPath := filepath.Join(opts.PageDir, pageIdentityFileName)
	if _, statErr := os.Stat(stampPath); os.IsNotExist(statErr) {
		n, err := countPageFiles(opts.PageDir)
		if err != nil {
			return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
				"reason": "page-file scan failed", "pageDir": opts.PageDir, "cause": err.Error(),
			})
		}
		if n > 0 {
			return nil, errs.New(ErrCitizenPagingUnstampedForeignPages, correlationID, map[string]any{
				"dir": opts.PageDir, "pageFileCount": n,
			})
		}
	}

	want := pageIdentity{TenantID: city.TenantID, CityID: city.CityID, WorldSeed: worldSeed}
	existing, ok, err := readPageIdentity(stampPath)
	if err != nil {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
			"reason": "identity sidecar read failed", "pageDir": opts.PageDir, "cause": err.Error(),
		})
	}
	if ok {
		if existing != want {
			return nil, errs.New(ErrCitizenPagingIdentityMismatch, correlationID, map[string]any{
				"dir":            opts.PageDir,
				"recordedTenant": existing.TenantID,
				"recordedCity":   existing.CityID,
				"recordedSeed":   existing.WorldSeed,
				"tenant":         want.TenantID,
				"city":           want.CityID,
				"seed":           want.WorldSeed,
			})
		}
	} else if err := writePageIdentity(stampPath, want); err != nil {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
			"reason": "identity sidecar write failed", "pageDir": opts.PageDir, "cause": err.Error(),
		})
	}

	// F1: take the EXCLUSIVE claim. Identity passing only proves "this
	// directory belongs to my lineage" -- it says nothing about whether
	// ANOTHER live composition of that exact same lineage already holds it.
	claim, err := claimPageDir(opts.PageDir, city, opts.Reclaim, correlationID)
	if err != nil {
		return nil, err
	}

	if err := c.EnableDiskPaging(opts.PageDir, opts.MaxResidentShards, correlationID); err != nil {
		claim.Release() // never leave a claim behind for a composition that never actually wired
		return nil, err
	}
	return claim, nil
}

// claimPageDir takes the exclusive, O_EXCL-created claim on dir. Portable
// across every GOOS this project builds on/deploys to (O_EXCL is POSIX and
// Windows both): the ENFORCEMENT is "at most one process/goroutine can
// successfully create this exact file at a time", regardless of pid --
// this is what correctly refuses TWO goroutines of the SAME process (the
// round's own TestAttackBUG764_TwoLiveCompositionsShareOneDirectory shape),
// not just two different processes. The claim file's pid/tenant/city/epoch
// content is diagnostic only (see pageClaimRecord's own doc comment), never
// the enforcement mechanism.
//
// reclaim=true forces removal of an existing claim before retrying the
// exclusive create -- see CitizenPagingOptions.Reclaim's own doc comment
// for why this is never auto-inferred except on linux, and even there only
// within the same container.
func claimPageDir(dir string, city persist.CityKey, reclaim bool, correlationID string) (*pageDirClaim, error) {
	claimPath := filepath.Join(dir, pageClaimFileName)
	rec := pageClaimRecord{PID: os.Getpid(), TenantID: city.TenantID, CityID: city.CityID, Epoch: time.Now().UnixNano()}
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{"reason": "claim record encode failed"})
	}

	tryCreate := func() error {
		f, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			return werr
		}
		return cerr
	}

	if err := tryCreate(); err == nil {
		return &pageDirClaim{path: claimPath, record: rec}, nil
	} else if !os.IsExist(err) {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
			"reason": "claim file create failed", "pageDir": dir, "cause": err.Error(),
		})
	}

	// A claim already exists. Read it for diagnostics regardless of what we
	// do next -- ctx on refusal should always show what's really there.
	existing, readErr := readPageClaim(claimPath)

	if reclaim {
		if err := os.Remove(claimPath); err != nil && !os.IsNotExist(err) {
			return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
				"reason": "reclaim: failed to remove stale claim", "pageDir": dir, "cause": err.Error(),
			})
		}
		if err := tryCreate(); err != nil {
			return nil, errs.Wrap(ErrCitizenPagingDirectoryClaimed, correlationID, err, map[string]any{
				"dir": dir, "reason": "reclaim raced a concurrent claimant",
			})
		}
		return &pageDirClaim{path: claimPath, record: rec}, nil
	}

	// pidLivenessKnown/processLooksAlive (citizen_paging_pid_*.go): on
	// linux, and ONLY within the same container/PID-namespace, a
	// confidently-dead pid auto-reclaims -- this is the same-container
	// crash-restart case. Everything else (a live pid, an indeterminate
	// GOOS, or a claim we could not even read) requires the operator's
	// explicit Reclaim.
	if readErr == nil && pidLivenessKnown && !processLooksAlive(existing.PID) {
		if err := os.Remove(claimPath); err == nil || os.IsNotExist(err) {
			if err := tryCreate(); err == nil {
				return &pageDirClaim{path: claimPath, record: rec}, nil
			}
		}
		// fall through to refuse if the auto-reclaim race lost
	}

	ctx := map[string]any{"dir": dir}
	if readErr == nil {
		ctx["recordedPid"] = existing.PID
		ctx["recordedTenant"] = existing.TenantID
		ctx["recordedCity"] = existing.CityID
		ctx["recordedEpoch"] = existing.Epoch
	} else {
		ctx["claimReadError"] = readErr.Error()
	}
	return nil, errs.New(ErrCitizenPagingDirectoryClaimed, correlationID, ctx)
}

// readPageClaim reads dir's claim record, if any.
func readPageClaim(path string) (pageClaimRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pageClaimRecord{}, err
	}
	var rec pageClaimRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return pageClaimRecord{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return rec, nil
}

// countPageFiles counts entries with a .page extension directly under dir
// (round finding F2's tamper-evidence check).
func countPageFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".page" {
			n++
		}
	}
	return n, nil
}

// readPageIdentity reads path's identity sidecar, if any. ok=false (no
// error) means the file does not exist yet — a fresh PageDir, not a
// mismatch.
func readPageIdentity(path string) (pageIdentity, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pageIdentity{}, false, nil
		}
		return pageIdentity{}, false, err
	}
	var id pageIdentity
	if err := json.Unmarshal(data, &id); err != nil {
		return pageIdentity{}, false, fmt.Errorf("decode %s: %w", path, err)
	}
	return id, true, nil
}

// writePageIdentity durably stamps path with id. Not atomic-rename (unlike
// persist.DiskStore's own snapshot writes) — this is a one-time, once-per-
// directory bookkeeping file written under Wire's own single-threaded
// construction path, never concurrently, so a torn write here is no worse
// than the directory itself failing to be created.
func writePageIdentity(path string, id pageIdentity) error {
	data, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return os.WriteFile(path, data, 0o644)
}
