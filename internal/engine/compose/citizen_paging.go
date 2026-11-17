package compose

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
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
	// Nonce (BUG-799) makes two claims taken by goroutines of the SAME
	// process in the SAME clock tick unequal, so Release's ownership compare
	// (and a reclaimer's "is this still the claim I judged stale" re-check)
	// can never confuse them. Diagnostic + identity only.
	Nonce string `json:"nonce,omitempty"`
	// Reclaimed (BUG-799) marks a claim that was itself taken by a takeover
	// (operator Reclaim or dead-pid auto-reclaim). A takeover refuses to steal
	// a FRESH Reclaimed claim of a process that may still be alive: it is the
	// previous takeover's winner, not the dead process's stale claim.
	Reclaimed bool `json:"reclaimed,omitempty"`
}

// reclaimFreshWindow is how long a Reclaimed claim is presumed to belong to a
// live takeover winner (BUG-799). Package var, not const, so tests can age a
// claim deterministically via claimNow instead of sleeping.
var reclaimFreshWindow = 30 * time.Second

// claimNow is the clock claimPageDir stamps and ages claims with (tests swap
// it; production is time.Now).
var claimNow = time.Now

// newClaimNonce returns 8 random bytes hex-encoded. A crypto/rand failure
// falls back to a clock+counter-derived value rather than failing the claim:
// the nonce only needs to differ between claims, and the O_EXCL create and the
// takeover lock below remain the actual exclusion mechanism.
func newClaimNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := uint64(claimNow().UnixNano()) ^ (claimNonceFallback.Add(1) << 48)
		for i := range b {
			b[i] = byte(n >> (8 * i))
		}
	}
	return hex.EncodeToString(b[:])
}

var claimNonceFallback atomic.Uint64

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
		// Every template token of MET-G821 is supplied (the errs render gate
		// scans the whole tree for call sites that leave a {token} literal):
		// the recorded side is unknown here, so it is stated as such.
		_ = errs.New(ErrCitizenPagingClaimOwnershipMismatch, errs.NewCorrelationID(), map[string]any{
			"dir":           filepath.Dir(p.path),
			"reason":        "claim file unreadable at Release time",
			"cause":         err.Error(),
			"recordedPid":   "unreadable",
			"recordedEpoch": "unreadable",
			"pid":           p.record.PID,
			"epoch":         p.record.Epoch,
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
	lockPath := claimPath + ".reclaim"
	rec := pageClaimRecord{
		PID: os.Getpid(), TenantID: city.TenantID, CityID: city.CityID,
		Epoch: claimNow().UnixNano(), Nonce: newClaimNonce(),
	}

	// A reclaim=true opener's claim is Reclaimed from its FIRST create: it can
	// land in the gap another takeover leaves between Remove and create (r5:
	// that gap is now under the takeover lock every first create also takes,
	// so this mark is defence in depth), and
	// a late takeover must then see a fresh live reclaim, not a stealable
	// stale claim (BUG-799: that steal let two openers both run Wire).
	rec.Reclaimed = reclaim

	// BUG-799 r5: the claim is PUBLISHED atomically (full record written to a
	// unique temp file, then hard-linked into place), so a live claimant's
	// claim.pid is never observable empty or partial. createWithRetry absorbs
	// the Windows delete-pending ACCESS_DENIED (neither IsExist nor a real
	// failure) so it resolves to a clean create or IsExist (a clean refusal).
	createErr := createWithRetry(func() error { return publishClaimLocked(claimPath, lockPath, rec) })
	if createErr == nil {
		return &pageDirClaim{path: claimPath, record: rec}, nil
	} else if errors.Is(createErr, errTakeoverBusy) {
		return nil, errs.New(ErrCitizenPagingTakeoverInProgress, correlationID, map[string]any{"dir": dir})
	} else if errors.Is(createErr, errClaimPublishUnsupported) {
		return nil, errs.Wrap(ErrCitizenPagingClaimPublishUnsupported, correlationID, createErr, map[string]any{
			"dir": dir, "cause": createErr.Error(),
		})
	} else if err := createErr; !os.IsExist(err) {
		return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
			"reason": "claim file create failed", "pageDir": dir, "cause": err.Error(),
		})
	}

	// A claim already exists. Read it for diagnostics regardless of what we
	// do next -- ctx on refusal should always show what's really there.
	existing, readErr := readPageClaim(claimPath)

	// pidLivenessKnown/processLooksAlive (citizen_paging_pid_*.go): on
	// linux, and ONLY within the same container/PID-namespace, a
	// confidently-dead pid auto-reclaims -- this is the same-container
	// crash-restart case. Everything else (a live pid, an indeterminate
	// GOOS, or a claim we could not even read) requires the operator's
	// explicit Reclaim.
	autoReclaim := readErr == nil && pidLivenessKnown && !processLooksAlive(existing.PID)

	if reclaim || autoReclaim {
		// BUG-799: remove-then-create is two syscalls, so two takeovers could
		// interleave (B's Remove deleting A's FRESH claim: both "hold" it, or
		// all lose). The pair is serialised by an OS advisory takeover lock
		// (LockFileEx / flock, released by the kernel on process death), and the claim is re-judged UNDER the lock, so a
		// late takeover that observed the stale claim before the winner
		// replaced it sees the winner's fresh Reclaimed claim and backs off.
		stealable := func(cur pageClaimRecord, rerr error) bool {
			if reclaim {
				if rerr != nil {
					// torn/corrupt: nothing live can be vouched for -- but an
					// empty/truncated record may be a legacy (create-then-
					// write) writer mid-write, so it is only stale once older
					// than claimTornGrace (BUG-799 r5 F1; tornClaimStale). An
					// I/O error (delete-pending, sharing) is NOT evidence of
					// staleness: the file may be a rival's fresh claim.
					return errors.Is(rerr, errClaimTorn) && tornClaimStale(rerr)
				}
				// A negative age (claim epoch in the FUTURE: clock stepped back
				// or skewed writer) is STALE, never fresh, so the wedge stays
				// bounded by reclaimFreshWindow (BUG-799 r1 F3).
				age := claimNow().UnixNano() - cur.Epoch
				fresh := age >= 0 && age < int64(reclaimFreshWindow)
				return !cur.Reclaimed || !fresh || (pidLivenessKnown && !processLooksAlive(cur.PID))
			}
			return rerr == nil && pidLivenessKnown && !processLooksAlive(cur.PID)
		}
		rec.Reclaimed = true
		won, err := takeOverClaim(claimPath, lockPath, existing, readErr, stealable,
			func() error { return publishClaim(claimPath, rec) })
		if errors.Is(err, errTakeoverBusy) {
			return nil, errs.New(ErrCitizenPagingTakeoverInProgress, correlationID, map[string]any{"dir": dir})
		}
		if errors.Is(err, errClaimPublishUnsupported) {
			return nil, errs.Wrap(ErrCitizenPagingClaimPublishUnsupported, correlationID, err, map[string]any{
				"dir": dir, "cause": err.Error(),
			})
		}
		if err != nil {
			return nil, errs.Wrap(ErrCitizenPagingConfigInvalid, correlationID, err, map[string]any{
				"reason": "reclaim: takeover failed", "pageDir": dir, "cause": err.Error(),
			})
		}
		if won {
			return &pageDirClaim{path: claimPath, record: rec}, nil
		}
		// Lost the takeover (another takeover won, a plain opener slipped in,
		// or the claim is a fresh live reclaim): refuse, showing what is
		// really there now.
		existing, readErr = readPageClaim(claimPath)
	}

	// A claim that exists but could not be read is NOT "claimed by another
	// live composition" -- nothing live was read. Say so (BUG-799 r5 advisory),
	// with every MET-G823 token filled inline (errs render gate).
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, errs.Wrap(ErrCitizenPagingClaimUnreadable, correlationID, readErr, map[string]any{
			"dir":            dir,
			"claimReadError": readErr.Error(),
		})
	}

	// Inline literal with every MET-G819 token on BOTH branches (the errs
	// render gate proves inline literals statically; a named ctx var is only
	// reported as dynamic-ctx, and the old else branch would have rendered
	// literal {recordedPid}-style tokens). The only non-nil readErr left here
	// is NotExist: the claim existed at create time and was released since.
	var recPid, recTenant, recCity, recEpoch any = "released", "released", "released", "released"
	claimReadError := ""
	if readErr == nil {
		recPid, recTenant, recCity, recEpoch = existing.PID, existing.TenantID, existing.CityID, existing.Epoch
	} else {
		claimReadError = readErr.Error()
	}
	return nil, errs.New(ErrCitizenPagingDirectoryClaimed, correlationID, map[string]any{
		"dir":            dir,
		"recordedPid":    recPid,
		"recordedTenant": recTenant,
		"recordedCity":   recCity,
		"recordedEpoch":  recEpoch,
		"claimReadError": claimReadError,
	})
}

// transientRetries bounds the retry of filesystem ops that fail spuriously on
// Windows while a sibling holds a file open or delete-pending
// (ERROR_ACCESS_DENIED / sharing violation), which are not IsExist/IsNotExist.
const transientRetries = 200

// errTakeoverBusy: another opener holds the takeover lock right now.
var errTakeoverBusy = errors.New("citizen paging: claim takeover lock is held by another opener")

// takeOverClaim atomically (w.r.t. other takeovers) replaces the existing claim
// at claimPath. BUG-799 r3: it takes an exclusive, non-blocking OS advisory
// lock (LockFileEx / flock, see citizen_paging_lock_*.go) on the lock file at
// lockPath, which is created once and NEVER deleted by the protocol, and holds
// it across the whole re-read / decide / remove / create section (every
// opener's first create takes the same lock, BUG-799 r5, so nothing can be
// created in the remove->create gap). The kernel
// releases the lock if the process dies, so there is no stale-lock concept
// and no break path. A contended lock is waited for (bounded, see
// lockTakeoverBounded), then reported as errTakeoverBusy. The claim is re-read UNDER the lock and
// stealable decides whether it may be taken -- but only if it is still the very
// claim (observed) the caller judged before locking. won=false with nil error means
// "back off" (claim not stealable, or a plain opener created the claim first);
// any other error is a hard filesystem failure.
//
// create is called while this function holds the takeover lock, so it must
// NOT take the lock itself (publishClaim, never publishClaimLocked).
func takeOverClaim(claimPath, lockPath string, observed pageClaimRecord, observedErr error, stealable func(pageClaimRecord, error) bool, create func() error) (won bool, err error) {
	lf, err := openTakeoverLockFile(lockPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = lf.Close() }()

	if err := lockTakeoverBounded(lf); err != nil {
		return false, err
	}
	defer func() { _ = unlockFile(lf) }()

	cur, rerr := readPageClaim(claimPath)
	if rerr != nil && os.IsNotExist(rerr) {
		// Released meanwhile: just create, with the same transient retry as
		// every other create path (BUG-799 r4 advisory: this was one try).
		if cerr := createWithRetry(create); cerr != nil {
			if os.IsExist(cerr) {
				return false, nil
			}
			return false, cerr
		}
		return true, nil
	}
	if rerr != nil && !errors.Is(rerr, errClaimTorn) {
		// A transient I/O error (not NotExist, not torn): never take over a
		// claim that could not be read -- it may be a rival's fresh claim
		// (BUG-799 r4: delete-pending read errors let two reclaim/plain
		// openers both hold).
		return false, nil
	}
	if observedErr != nil && !errors.Is(observedErr, errClaimTorn) {
		return false, nil // never examined the claim it would replace
	}
	// Only ever take the claim this opener actually JUDGED stealable. A
	// different claim now (a plain opener landed in a rival takeover's
	// pre-r5 remove->create gap, a Release then a fresh create, or the claim
	// was rewritten) is a new holder this
	// opener never examined: back off (BUG-799 r3 -- the lock retry makes a
	// late taker common, and it must not steal a plain opener's claim).
	if (observedErr == nil) != (rerr == nil) || (rerr == nil && cur != observed) {
		return false, nil
	}
	if !stealable(cur, rerr) {
		return false, nil
	}
	for i := 0; ; i++ {
		rmErr := os.Remove(claimPath)
		if rmErr == nil || os.IsNotExist(rmErr) {
			break
		}
		if i >= transientRetries {
			return false, rmErr
		}
		time.Sleep(time.Millisecond)
	}
	cerr := createWithRetry(create)
	if cerr == nil {
		return true, nil
	}
	if os.IsExist(cerr) {
		// Cannot happen while every exclusive create also holds the takeover
		// lock (BUG-799 r5, publishClaimLocked); kept as a clean back-off.
		return false, nil
	}
	return false, cerr
}

// createWithRetry runs create until it succeeds, reports IsExist (a clean
// "someone else holds it"), reports that the filesystem cannot publish a claim
// at all (errClaimPublishUnsupported: fail closed at once, never retried),
// reports the takeover lock stayed busy past its bounded wait (errTakeoverBusy:
// already waited, never retried), or
// has failed transientRetries+1 times with a transient error (Windows
// delete-pending ACCESS_DENIED / sharing violation, 1ms apart).
func createWithRetry(create func() error) error {
	for i := 0; ; i++ {
		err := create()
		if err == nil || os.IsExist(err) || errors.Is(err, errClaimPublishUnsupported) || errors.Is(err, errTakeoverBusy) || i >= transientRetries {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

// claimTempInfix names the private temp files publishClaim stages a record
// in: claim.pid.tmp-<pid>-<nonce>. A temp file is NEVER a claim: nothing reads
// it, and only os.Link's success at claim.pid publishes anything. A temp left
// behind by a crash is inert litter (it is not .page, not claim.pid, and is
// never consulted by readPageClaim or countPageFiles).
const claimTempInfix = ".tmp-"

// errClaimPublishUnsupported: the filesystem has no hard links, so the claim
// cannot be published atomically. Fail closed (MET-G824); never fall back to
// create-then-write.
var errClaimPublishUnsupported = errors.New("citizen paging: hard links unsupported, claim cannot be published atomically")

// claimLink is the publishing link (os.Link); a test seam so the
// unsupported-filesystem fail-closed path can be driven without a FAT volume.
var claimLink = os.Link

// claimPublishHook is a test seam: called the instant claim.pid becomes
// visible at its path (immediately after the publishing link succeeds).
var claimPublishHook atomic.Pointer[func(path string)]

// publishClaim atomically AND exclusively creates claimPath holding r
// (BUG-799 r5, lead ruling F1). The full record is written and fsynced to a
// unique temp file in the same directory, then hard-linked to claimPath:
// link(2) / CreateHardLinkW fail if the target exists (EEXIST /
// ERROR_ALREADY_EXISTS, both os.IsExist), so the publish is exclusive like
// O_EXCL, and the target appears with its complete content in one step, so no
// reader can ever see a live claimant's claim.pid empty or partial (the r4 F1
// two-holder window: create, then a reclaimer reads it empty, calls it torn
// and steals it, then the first claimant writes and also holds). The temp is
// removed on every path; if that removal itself keeps failing the temp stays
// as inert litter (see claimTempInfix), never as a claim.
//
// Returns the os.Link error unwrapped on a link failure (so os.IsExist and
// the transient-retry classification see it exactly), errClaimPublishUnsupported
// (wrapping the cause) when the filesystem has no hard links, and a wrapped,
// non-IsExist error when the temp could not be staged.
func publishClaim(claimPath string, r pageClaimRecord) error {
	tmp, err := stageClaimTemp(claimPath, r)
	if err != nil {
		return err
	}
	defer removeClaimTemp(tmp)
	return linkClaim(tmp, claimPath)
}

// linkClaim publishes the staged temp at claimPath with one hard link.
func linkClaim(tmp, claimPath string) error {
	lerr := claimLink(tmp, claimPath)
	if lerr == nil {
		claimPublished(claimPath)
		return nil
	}
	if !os.IsExist(lerr) && linkUnsupported(lerr) {
		return fmt.Errorf("%w: %w", errClaimPublishUnsupported, lerr)
	}
	return lerr
}

// publishClaimLocked is the FIRST-create publish of every opener: publishClaim
// with the hard link made while holding the takeover lock (BUG-799 r5). A
// takeover's remove->link pair runs under that same lock, so no exclusive
// create can land in the gap between them. Before r5 a plain opener could
// link its claim into that gap; a second reclaimer then observed the plain
// claim and stole it (live plain claims are stealable by an explicit Reclaim),
// so the plain opener and both reclaimers all held
// (TestBUG799R1_PlainOpenerVsTakeoverGapAgreement, 3 holders once publishing
// grew its temp+fsync step). The record is staged (written + fsynced) BEFORE
// the lock is taken, so the lock covers one link syscall. The lock wait is
// bounded (claimLockWait): a holder is a live process inside a section of a
// few syscalls (the kernel releases a dead holder's lock), so exceeding it
// reports errTakeoverBusy (MET-G822) rather than hanging.
func publishClaimLocked(claimPath, lockPath string, r pageClaimRecord) error {
	// Fast refusal: a claim already present means the link would fail
	// IsExist anyway, so skip staging (a temp write + fsync per attempt made
	// spinning openers ~100x slower). Purely an optimisation: it never
	// creates anything, and a claim that appears after this check is still
	// caught by the exclusive link below.
	if _, serr := os.Lstat(claimPath); serr == nil {
		return &os.PathError{Op: "link", Path: claimPath, Err: os.ErrExist}
	}
	tmp, err := stageClaimTemp(claimPath, r)
	if err != nil {
		return err
	}
	defer removeClaimTemp(tmp)
	lf, err := openTakeoverLockFile(lockPath)
	if err != nil {
		return fmt.Errorf("claim lock open %s: %w", lockPath, err)
	}
	defer func() { _ = lf.Close() }()
	if err := lockTakeoverBounded(lf); err != nil {
		if errors.Is(err, errTakeoverBusy) {
			return err
		}
		return fmt.Errorf("claim lock %s: %w", lockPath, err)
	}
	defer func() { _ = unlockFile(lf) }()
	return linkClaim(tmp, claimPath)
}

// lockTakeoverBounded takes the exclusive takeover lock on lf, polling every
// 1ms for at most claimLockWait (BUG-799 r5; supersedes the r3 "one retry
// after 5ms": once every first create also takes the lock, a single retry
// let all reclaimers of a contended directory refuse at once -- zero holders,
// TestBUG799R1_PlainOpenerVsTakeoverGapAgreement). Holders sit in sections of
// a few syscalls and the kernel releases a dead holder's lock, so the bound is
// only reached when a live holder is wedged: errTakeoverBusy (MET-G822).
func lockTakeoverBounded(lf *os.File) error {
	deadline := time.Now().Add(claimLockWait)
	for {
		held, err := tryLockFile(lf)
		if err != nil {
			return err
		}
		if held {
			return nil
		}
		if time.Now().After(deadline) {
			return errTakeoverBusy
		}
		time.Sleep(time.Millisecond)
	}
}

// claimLockWait bounds how long a first-create publish waits for the
// takeover lock (wall clock, locking path only -- never sim state).
const claimLockWait = 2 * time.Second

// openTakeoverLockFile opens (creating if needed) the never-deleted takeover
// lock file, retrying Windows transient open failures.
func openTakeoverLockFile(lockPath string) (*os.File, error) {
	for i := 0; ; i++ {
		lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err == nil || i >= transientRetries {
			return lf, err
		}
		time.Sleep(time.Millisecond)
	}
}

// stageClaimTemp writes r, fsynced, to a fresh unique temp file beside
// claimPath and returns its path. Every error is wrapped with fmt, so a
// (vanishingly unlikely) temp-name collision is never misread by os.IsExist
// as "claim.pid exists"; a temp that failed mid-write is removed.
func stageClaimTemp(claimPath string, r pageClaimRecord) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	tmp := fmt.Sprintf("%s%s%d-%s", claimPath, claimTempInfix, os.Getpid(), newClaimNonce())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("claim temp create %s: %w", tmp, err)
	}
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if werr == nil {
		werr = serr
	}
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		removeClaimTemp(tmp)
		return "", fmt.Errorf("claim temp write %s: %w", tmp, werr)
	}
	return tmp, nil
}

// claimPublished fires claimPublishHook (test seam) the instant a claim
// becomes visible at claimPath.
func claimPublished(claimPath string) {
	if h := claimPublishHook.Load(); h != nil {
		(*h)(claimPath)
	}
}

// removeClaimTemp removes a publishClaim temp, retrying the Windows transient
// sharing/delete-pending failures (a scanner, or a reader holding the linked
// claim.pid open without delete sharing, blocks deleting ANY name of the
// file). Best-effort: a temp that cannot be removed is inert (claimTempInfix).
func removeClaimTemp(tmp string) {
	for i := 0; ; i++ {
		err := os.Remove(tmp)
		if err == nil || os.IsNotExist(err) || i >= transientRetries {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// claimTornGrace is how old (by file mtime) an EMPTY or TRUNCATED claim.pid
// must be before a reclaim may treat it as crash residue (BUG-799 r5). With
// atomic publish no writer of this code can leave such a file live, but a
// legacy (create-then-write) writer or an odd filesystem could be mid-write;
// several seconds is far beyond any real write. Package var so tests age a
// file via claimNow/os.Chtimes rather than sleeping. Wall clock is allowed
// here: this is the persistence/locking path, never sim state or a digest.
var claimTornGrace = 10 * time.Second

// errClaimTorn marks a claim file that exists and opened but does not decode
// (torn/corrupt). Distinct from a transient I/O error opening it. readPageClaim
// returns it inside a *claimTornError carrying the file's mtime and shape.
var errClaimTorn = errors.New("citizen paging: claim file is torn or corrupt")

// claimTornError is readPageClaim's decode failure. inFlight reports a shape
// a writer could still be in the middle of producing (empty, or a strict
// prefix of a JSON value: the decoder hit EOF mid-value); anything else (a
// syntax error, a wrong-typed field) is corruption no writer is still making.
type claimTornError struct {
	path     string
	modTime  time.Time
	inFlight bool
	cause    error
}

func (e *claimTornError) Error() string {
	return fmt.Sprintf("%v: decode %s: %v", errClaimTorn, e.path, e.cause)
}

// Is makes errors.Is(err, errClaimTorn) hold (part (c): only a decode failure
// is torn).
func (e *claimTornError) Is(target error) bool { return target == errClaimTorn }

func (e *claimTornError) Unwrap() error { return e.cause }

// tornClaimStale reports whether a torn claim may be treated as crash residue.
// Corrupt (not in-flight-shaped) content is stale at once: no writer of either
// generation produces it. An empty/truncated claim is stale only once its
// mtime is at least claimTornGrace away from now in EITHER direction (a mtime
// far in the future -- clock stepped back, skewed server -- is stale, never
// fresh forever, mirroring the r1 F3 epoch rule).
func tornClaimStale(err error) bool {
	var te *claimTornError
	if !errors.As(err, &te) {
		return false
	}
	if !te.inFlight {
		return true
	}
	age := claimNow().Sub(te.modTime)
	return age >= claimTornGrace || age <= -claimTornGrace
}

// claimReadOpenHook is a test seam: called while a claim read holds the file open.
var claimReadOpenHook atomic.Pointer[func(path string)]

// readPageClaim reads dir's claim record, if any.
func readPageClaim(path string) (pageClaimRecord, error) {
	// A claim a takeover has deleted but a reader still holds open is
	// delete-pending on Windows: opening it fails ERROR_ACCESS_DENIED until the
	// reader closes. That is a transient I/O condition, NOT a torn claim, so
	// retry it briefly and (if it persists) surface it as a plain I/O error.
	var f *os.File
	var err error
	for i := 0; ; i++ {
		f, err = openClaimForRead(path)
		if err == nil || os.IsNotExist(err) || i >= transientRetries {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		return pageClaimRecord{}, err
	}
	if h := claimReadOpenHook.Load(); h != nil {
		(*h)(path)
	}
	data, err := io.ReadAll(f)
	// mtime from the SAME open handle the bytes came from, so a torn verdict
	// and its age always describe one file (BUG-799 r5 torn grace).
	var modTime time.Time
	if err == nil {
		var fi os.FileInfo
		if fi, err = f.Stat(); err == nil {
			modTime = fi.ModTime()
		}
	}
	_ = f.Close()
	if err != nil {
		return pageClaimRecord{}, err
	}
	var rec pageClaimRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return pageClaimRecord{}, &claimTornError{path: path, modTime: modTime, inFlight: claimBytesInFlight(data), cause: err}
	}
	return rec, nil
}

// claimBytesInFlight reports whether data is a shape a create-then-write
// writer could still be producing: empty/whitespace, or a strict prefix of a
// JSON value (the decoder reaches EOF mid-value: io.ErrUnexpectedEOF).
func claimBytesInFlight(data []byte) bool {
	if len(bytes.TrimSpace(data)) == 0 {
		return true
	}
	var v any
	return errors.Is(json.NewDecoder(bytes.NewReader(data)).Decode(&v), io.ErrUnexpectedEOF)
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
