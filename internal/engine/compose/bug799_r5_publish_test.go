package compose

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-799 r5 (lead ruling after r4 F1): the claim is PUBLISHED atomically
// (temp file + fsync + hard link), an empty/truncated claim is only crash
// residue once older than claimTornGrace, a crash-left temp is never a claim,
// and a filesystem without hard links fails closed with MET-G824.

// r5PlantClaim writes raw bytes at claim.pid and sets its mtime to at.
func r5PlantClaim(t *testing.T, dir string, raw []byte, at time.Time) string {
	t.Helper()
	p := filepath.Join(dir, pageClaimFileName)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

// The instant claim.pid becomes visible at its path it must already hold the
// complete record of the claimant that published it -- on the first create
// AND on the takeover create. Kills a revert of publishClaim to
// create-then-write (the r4 F1 window: claim.pid exists and is empty).
func TestBUG799R5_ClaimNeverVisibleIncomplete(t *testing.T) {
	var fired, bad atomic.Int32
	var badMsg atomic.Value
	hook := func(path string) {
		fired.Add(1)
		if _, err := readPageClaim(path); err != nil {
			bad.Add(1)
			badMsg.Store(err.Error())
		}
	}
	claimPublishHook.Store(&hook)
	t.Cleanup(func() { claimPublishHook.Store(nil) })

	// First create on an empty directory.
	dir := t.TempDir()
	c, err := claimPageDir(dir, rr764City, false, "r5-visible")
	if err != nil {
		t.Fatalf("plain open of an empty dir: %v", err)
	}
	c.Release()

	// Takeover create over a stale (not Reclaimed) claim.
	stale, _ := json.Marshal(pageClaimRecord{PID: 999999, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1, Nonce: "stale"})
	r5PlantClaim(t, dir, stale, time.Now().Add(-time.Hour))
	c2, err := claimPageDir(dir, rr764City, true, "r5-visible-takeover")
	if err != nil {
		t.Fatalf("reclaim over a stale claim: %v", err)
	}
	c2.Release()

	if fired.Load() != 2 {
		t.Fatalf("publish hook fired %d times, want 2 (first create + takeover create)", fired.Load())
	}
	if bad.Load() != 0 {
		t.Fatalf("FINDING: claim.pid was visible INCOMPLETE %d time(s): %v", bad.Load(), badMsg.Load())
	}
	r5AssertNoTemps(t, dir)
}

// An empty or truncated claim.pid younger than claimTornGrace may be a legacy
// writer mid-write: a reclaim must refuse it (MET-G823, "could not be read")
// and leave it alone. Kills removing the grace check.
func TestBUG799R5_FreshInFlightTornNotStolen(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty":     nil,
		"truncated": []byte(`{"pid":12,"tenant_id":"lo`),
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := r5PlantClaim(t, dir, raw, time.Now())
			c, err := claimPageDir(dir, rr764City, true, "r5-fresh-torn")
			if err == nil {
				c.Release()
				t.Fatalf("FINDING: reclaim stole a %s claim.pid younger than the %v grace", name, claimTornGrace)
			}
			if code := rr764CodeSafe(err); code != ErrCitizenPagingClaimUnreadable {
				t.Fatalf("refused with %s (%v), want %s", code, err, ErrCitizenPagingClaimUnreadable)
			}
			if !strings.Contains(err.Error(), "could not be read") || strings.Contains(err.Error(), "{claimReadError}") || strings.Contains(err.Error(), "{dir}") {
				t.Fatalf("refusal text does not say the claim could not be read (or left a template token): %v", err)
			}
			if b, rerr := os.ReadFile(p); rerr != nil || string(b) != string(raw) {
				t.Fatalf("fresh torn claim was touched: %q %v", b, rerr)
			}
		})
	}
}

// The same in-flight-shaped torn claim IS crash residue once it is older than
// the grace, in either clock direction, and a reclaim takes it -- the ruling's
// "never wedge real crash residue". Aged three ways: an old mtime, a future
// mtime (clock stepped back), and the injectable claimNow seam (no sleeping).
func TestBUG799R5_StaleInFlightTornReclaimed(t *testing.T) {
	cases := map[string]func(t *testing.T) time.Time{
		"old-mtime":    func(*testing.T) time.Time { return time.Now().Add(-time.Hour) },
		"future-mtime": func(*testing.T) time.Time { return time.Now().Add(time.Hour) },
		"clock-seam": func(t *testing.T) time.Time {
			realNow := claimNow
			t.Cleanup(func() { claimNow = realNow })
			claimNow = func() time.Time { return realNow().Add(claimTornGrace + time.Second) }
			return time.Now()
		},
	}
	for name, at := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := r5PlantClaim(t, dir, nil, at(t))
			c, err := claimPageDir(dir, rr764City, true, "r5-stale-torn")
			if err != nil {
				t.Fatalf("FINDING: crash-residue empty claim wedged the directory: %v", err)
			}
			disk, derr := readPageClaim(p)
			if derr != nil || disk != c.record {
				t.Fatalf("disk %+v err %v != holder %+v", disk, derr, c.record)
			}
			c.Release()
			r5AssertNoTemps(t, dir)
		})
	}
	// A plain opener never steals, stale or not.
	dir := t.TempDir()
	r5PlantClaim(t, dir, nil, time.Now().Add(-time.Hour))
	if c, err := claimPageDir(dir, rr764City, false, "r5-stale-torn-plain"); err == nil {
		c.Release()
		t.Fatal("FINDING: a plain opener took a torn claim")
	}
}

// Corrupt content no writer is still producing (not empty, not a JSON
// prefix) is stale at once: classification pinned so a fresh "{torn" stays
// reclaimable (the final round's kept liveness tests rely on it).
func TestBUG799R5_TornShapeClassification(t *testing.T) {
	for raw, want := range map[string]bool{
		"":                          true,
		"  \n":                      true,
		"{":                         true,
		`{"pid":12`:                 true,
		`{"pid":12,"tenant_id":"a`:  true,
		"{torn":                     false,
		`{"pid":"x"}`:               false,
		`{"pid":1}trailing garbage`: false,
	} {
		if got := claimBytesInFlight([]byte(raw)); got != want {
			t.Errorf("claimBytesInFlight(%q) = %v, want %v", raw, got, want)
		}
	}
}

// A temp left behind by a crash mid-publish is never a claim, even when it
// holds a perfectly valid, fresh, Reclaimed record of a live pid: plain and
// reclaim openers both win, and the residue is left alone.
func TestBUG799R5_TempResidueIsNotAClaim(t *testing.T) {
	for _, reclaim := range []bool{false, true} {
		dir := t.TempDir()
		claimPath := filepath.Join(dir, pageClaimFileName)
		live := pageClaimRecord{PID: os.Getpid(), TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: time.Now().UnixNano(), Nonce: "residue", Reclaimed: true}
		data, _ := json.Marshal(live)
		residue := fmt.Sprintf("%s%s%d-%s", claimPath, claimTempInfix, os.Getpid(), "deadbeefdeadbeef")
		if err := os.WriteFile(residue, data, 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := claimPageDir(dir, rr764City, reclaim, "r5-residue")
		if err != nil {
			t.Fatalf("FINDING: reclaim=%v refused because of a crash-left temp: %v", reclaim, err)
		}
		disk, derr := readPageClaim(claimPath)
		if derr != nil || disk != c.record || disk.Nonce == "residue" {
			t.Fatalf("reclaim=%v: claim.pid %+v err %v, want holder %+v", reclaim, disk, derr, c.record)
		}
		c.Release()
		if b, err := os.ReadFile(residue); err != nil || string(b) != string(data) {
			t.Fatalf("residue touched: %q %v", b, err)
		}
	}
}

// No hard links on the filesystem: fail closed with MET-G824 on BOTH the first
// create and the takeover create, never fall back to create-then-write, and
// leave no claim and no temp behind. syscall.Errno(1) is
// ERROR_INVALID_FUNCTION on Windows (FAT's answer) and EPERM on unix (vfat's
// answer), so one value drives linkUnsupported on every GOOS that has it.
func TestBUG799R5_LinkUnsupportedFailsClosed(t *testing.T) {
	realLink := claimLink
	t.Cleanup(func() { claimLink = realLink })
	var calls atomic.Int32
	claimLink = func(oldname, newname string) error {
		calls.Add(1)
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.Errno(1)}
	}

	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	_, err := claimPageDir(dir, rr764City, false, "r5-nolink")
	r5AssertCode(t, err, ErrCitizenPagingClaimPublishUnsupported)
	if calls.Load() != 1 {
		t.Fatalf("unsupported link retried %d times, want 1 (fail closed at once)", calls.Load())
	}
	if _, serr := os.Stat(claimPath); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("FINDING: a claim.pid exists after an unsupported publish (fallback?): %v", serr)
	}
	r5AssertNoTemps(t, dir)

	// A reclaim opener over an existing stale claim also fails closed with
	// MET-G824. Its first publish takes the fast IsExist refusal (the claim
	// is present), so the takeover judges the stale claim, removes it, and
	// then cannot link: the directory is left with NO claim (never a
	// non-atomic fallback claim, never a holder) and no temp.
	stale, _ := json.Marshal(pageClaimRecord{PID: 999999, TenantID: "t", CityID: "c", Epoch: 1, Nonce: "stale"})
	r5PlantClaim(t, dir, stale, time.Now().Add(-time.Hour))
	_, err = claimPageDir(dir, rr764City, true, "r5-nolink-reclaim")
	r5AssertCode(t, err, ErrCitizenPagingClaimPublishUnsupported)
	if b, rerr := os.ReadFile(claimPath); rerr == nil && string(b) != string(stale) {
		t.Fatalf("FINDING: reclaim on an unsupported filesystem left a claim it did not link: %q", b)
	}
	r5AssertNoTemps(t, dir)

	// The takeover's exclusive create directly (the claim vanished between
	// the opener's first look and the takeover lock): unsupported surfaces
	// as errClaimPublishUnsupported, unretried, with no fallback.
	r5PlantClaim(t, dir, stale, time.Now().Add(-time.Hour))
	observed, oerr := readPageClaim(claimPath)
	if oerr != nil {
		t.Fatal(oerr)
	}
	if err := os.Remove(claimPath); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	rec := pageClaimRecord{PID: os.Getpid(), TenantID: "t", CityID: "c", Epoch: time.Now().UnixNano(), Nonce: "r5", Reclaimed: true}
	won, terr := takeOverClaim(claimPath, claimPath+".reclaim", observed, nil,
		func(pageClaimRecord, error) bool { return true },
		func() error { return publishClaim(claimPath, rec) })
	if won || !errors.Is(terr, errClaimPublishUnsupported) {
		t.Fatalf("takeover on an unsupported filesystem: won=%v err=%v, want errClaimPublishUnsupported", won, terr)
	}
	if calls.Load() != 1 {
		t.Fatalf("takeover retried an unsupported link %d times, want 1", calls.Load())
	}
	if _, serr := os.Stat(claimPath); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("FINDING: takeover wrote a claim.pid without a link (fallback?): %v", serr)
	}
	r5AssertNoTemps(t, dir)
}

func r5AssertCode(t *testing.T, err error, want string) {
	t.Helper()
	var re *errs.E
	if !errors.As(err, &re) || re.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
	if strings.Contains(err.Error(), "{dir}") || strings.Contains(err.Error(), "{cause}") {
		t.Fatalf("refusal left a template token unfilled: %v", err)
	}
}

func r5AssertNoTemps(t *testing.T, dir string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, pageClaimFileName+claimTempInfix+"*"))
	if len(m) != 0 {
		t.Fatalf("publish temp(s) left behind: %v", m)
	}
}
