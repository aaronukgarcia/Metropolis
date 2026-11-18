package compose

import (
	"errors"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"os"
	"path/filepath"
	"testing"
)

// BUG-1178: the page-identity stamp must never be left truncated or
// half-written, and a damaged stamp must fail closed (never re-created).

func bug1178Entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestBUG1178_IdentityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, pageIdentityFileName)
	want := pageIdentity{TenantID: "t", CityID: "c", WorldSeed: 42}
	if err := writePageIdentity(p, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, ok, err := readPageIdentity(p)
	if err != nil || !ok || got != want {
		t.Fatalf("round trip: got=%+v ok=%v err=%v", got, ok, err)
	}
	if n := bug1178Entries(t, dir); len(n) != 1 {
		t.Fatalf("expected only the identity file, got %v", n)
	}
}

func TestBUG1178_CrashBeforeRenameKeepsOldIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, pageIdentityFileName)
	old := pageIdentity{TenantID: "t", CityID: "old", WorldSeed: 1}
	if err := writePageIdentity(p, old); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	crash := errors.New("simulated crash")
	pageIdentityBeforeRename = func() error { return crash }
	defer func() { pageIdentityBeforeRename = nil }()

	err := writePageIdentity(p, pageIdentity{TenantID: "t", CityID: "new", WorldSeed: 2})
	if !errors.Is(err, crash) {
		t.Fatalf("expected simulated crash, got %v", err)
	}
	got, ok, rerr := readPageIdentity(p)
	if rerr != nil || !ok || got != old {
		t.Fatalf("old identity damaged: got=%+v ok=%v err=%v", got, ok, rerr)
	}
	if n := bug1178Entries(t, dir); len(n) != 1 {
		t.Fatalf("temp file left behind: %v", n)
	}
}

func TestBUG1178_CrashOnFreshDirLeavesNoIdentity(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, pageIdentityFileName)
	pageIdentityBeforeRename = func() error { return errors.New("simulated crash") }
	defer func() { pageIdentityBeforeRename = nil }()
	if err := writePageIdentity(p, pageIdentity{TenantID: "t", CityID: "c", WorldSeed: 3}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("a partial identity.json exists after a crash: %v", err)
	}
	if n := bug1178Entries(t, dir); len(n) != 0 {
		t.Fatalf("debris left: %v", n)
	}
}

func TestBUG1178_DamagedIdentityRefusedNotRecreated(t *testing.T) {
	good := `{"tenantId":"local","cityId":"city-reround","worldSeed":764950}`
	cases := map[string]string{
		"empty":     "",
		"truncated": good[:len(good)/2],
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, pageIdentityFileName)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := rr764Wire(t, dir, false)
			if err == nil {
				t.Fatal("a damaged identity was accepted")
			}
			if code := rr764Code(t, err); code != ErrCitizenPagingConfigInvalid {
				t.Fatalf("code = %s, want %s", code, ErrCitizenPagingConfigInvalid)
			}
			after, rerr := os.ReadFile(p)
			if rerr != nil || string(after) != body {
				t.Fatalf("damaged identity was rewritten: %q err=%v", after, rerr)
			}
		})
	}
}

// ---- BUG-1178 r2: identity is read and stamped only under the claim ----

func bug1178WireCity(t *testing.T, dir string, city persist.CityKey, reclaim bool) (*Composition, error) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(rr764Seed))
	return Wire(e, &Deps{
		PersistCity: city,
		CitizenPaging: CitizenPagingOptions{
			Enabled: true, MaxResidentShards: 2, PageDir: dir, Reclaim: reclaim,
		},
	})
}

var bug1178OtherCity = persist.CityKey{TenantID: "local", CityID: "city-other"}

func TestBUG1178_MismatchRefusalLeavesNoClaim(t *testing.T) {
	dir := t.TempDir()
	first, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	_, err = bug1178WireCity(t, dir, bug1178OtherCity, false)
	if err == nil || rr764Code(t, err) != ErrCitizenPagingIdentityMismatch {
		t.Fatalf("want %s, got %v", ErrCitizenPagingIdentityMismatch, err)
	}
	if rr764ClaimExists(t, dir) {
		t.Fatal("a refused opener left its claim behind")
	}
	ok, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("correct opener refused after a refused one: %v", err)
	}
	_ = ok.Close()
}

func TestBUG1178_DamagedIdentityRefusalLeavesNoClaim(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, pageIdentityFileName)
	if err := os.WriteFile(p, []byte(`{"tenantId":"loc`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := rr764Wire(t, dir, false)
	if err == nil || rr764Code(t, err) != ErrCitizenPagingConfigInvalid {
		t.Fatalf("want %s, got %v", ErrCitizenPagingConfigInvalid, err)
	}
	if rr764ClaimExists(t, dir) {
		t.Fatal("a refused opener left its claim behind")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	ok, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("correct opener refused after operator repair: %v", err)
	}
	_ = ok.Close()
}

func TestBUG1178_FailedStampLeavesNoClaimAndNoTemp(t *testing.T) {
	dir := t.TempDir()
	pageIdentityBeforeRename = func() error { return errors.New("simulated crash") }
	_, err := rr764Wire(t, dir, false)
	pageIdentityBeforeRename = nil
	if err == nil || rr764Code(t, err) != ErrCitizenPagingConfigInvalid {
		t.Fatalf("want %s, got %v", ErrCitizenPagingConfigInvalid, err)
	}
	if rr764ClaimExists(t, dir) {
		t.Fatal("a failed stamp left the claim behind")
	}
	if n := bug1178Entries(t, dir); len(n) > 1 { // .reclaim lock file may persist by design
		for _, x := range n {
			if x != pageClaimFileName+".reclaim" {
				t.Fatalf("debris after failed stamp: %v", n)
			}
		}
	}
	ok, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("correct opener refused after a failed stamp: %v", err)
	}
	_ = ok.Close()
}

func TestBUG1178_LeftoverIdentityTempsRemovedUnderClaim(t *testing.T) {
	dir := t.TempDir()
	tmpA := filepath.Join(dir, pageIdentityFileName+".tmp-111")
	tmpB := filepath.Join(dir, pageIdentityFileName+".tmp-222")
	for _, p := range []string{tmpA, tmpB} {
		if err := os.WriteFile(p, []byte(`{"tenantId":"x"`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with only temp files is a fresh directory (not foreign pages).
	c, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("temp-only directory refused: %v", err)
	}
	defer func() { _ = c.Close() }()
	for _, p := range []string{tmpA, tmpB} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("leftover temp %s survived the open: %v", p, err)
		}
	}
	got, ok, rerr := readPageIdentity(filepath.Join(dir, pageIdentityFileName))
	if rerr != nil || !ok || got.CityID != rr764City.CityID {
		t.Fatalf("fresh stamp missing: %+v ok=%v err=%v", got, ok, rerr)
	}
}

// A second opener that arrives while the first sits between claim and stamp
// must be refused by the CLAIM, never by an identity error.
func TestBUG1178_SecondOpenerBetweenClaimAndStampGetsClaimRefusal(t *testing.T) {
	dir := t.TempDir()
	var secondErr error
	inHook := false
	pageIdentityBeforeRename = func() error {
		if inHook {
			return errors.New("reentered: second opener reached the stamp")
		}
		inHook = true
		_, secondErr = rr764Wire(t, dir, false)
		return nil
	}
	first, err := rr764Wire(t, dir, false)
	pageIdentityBeforeRename = nil
	if err != nil {
		t.Fatalf("first opener: %v", err)
	}
	defer func() { _ = first.Close() }()
	if secondErr == nil {
		t.Fatal("second opener won while the first held the claim")
	}
	if code := rr764Code(t, secondErr); code != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("second opener refused with %s, want %s", code, ErrCitizenPagingDirectoryClaimed)
	}
}

// A reclaimer whose identity does not match must be refused WITHOUT having
// destroyed the live holder's claim.
func TestBUG1178_ReclaimMismatchDoesNotDestroyHolderClaim(t *testing.T) {
	dir := t.TempDir()
	holder, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	claimPath := filepath.Join(dir, pageClaimFileName)
	before, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = bug1178WireCity(t, dir, bug1178OtherCity, true)
	if err == nil || rr764Code(t, err) != ErrCitizenPagingIdentityMismatch {
		t.Fatalf("want %s, got %v", ErrCitizenPagingIdentityMismatch, err)
	}
	after, err := os.ReadFile(claimPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("holder's claim was destroyed or altered by a refused reclaimer: %q -> %q (%v)", before, after, err)
	}
}

func TestBUG1178_NineDamagedIdentityCasesFailClosed(t *testing.T) {
	good := `{"tenantId":"local","cityId":"city-reround","worldSeed":764950}`
	cases := map[string]string{
		"empty":       "",
		"truncated":   good[:len(good)/2],
		"whitespace":  "  \n\t ",
		"nul":         "\x00\x00\x00\x00",
		"emptyobject": `{}`,
		"null":        `null`,
		"wrongcity":   `{"tenantId":"local","cityId":"city-other","worldSeed":764950}`,
		"wrongseed":   `{"tenantId":"local","cityId":"city-reround","worldSeed":1}`,
		"extrafields": `{"tenantId":"local","cityId":"city-other","worldSeed":764950,"extra":true}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, pageIdentityFileName)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := rr764Wire(t, dir, false); err == nil {
				t.Fatal("damaged identity accepted")
			}
			after, rerr := os.ReadFile(p)
			if rerr != nil || string(after) != body {
				t.Fatalf("identity rewritten: %q err=%v", after, rerr)
			}
			if rr764ClaimExists(t, dir) {
				t.Fatal("claim left behind")
			}
		})
	}
}

// BUG-1178 r3 ruling (b): an I/O error reading identity.json in the reclaim
// pre-flight refuses at once with MET-G817 and leaves the live holder's claim
// untouched. A directory standing where identity.json should be makes the
// read fail deterministically on every GOOS without being "not exist".
func TestBUG1178_ReclaimIdentityReadErrorRefusesAndKeepsHolderClaim(t *testing.T) {
	dir := t.TempDir()
	holder, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	claimPath := filepath.Join(dir, pageClaimFileName)
	before, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatal(err)
	}
	idPath := filepath.Join(dir, pageIdentityFileName)
	if err := os.Remove(idPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(idPath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = rr764Wire(t, dir, true)
	if err == nil || rr764Code(t, err) != ErrCitizenPagingConfigInvalid {
		t.Fatalf("want %s, got %v", ErrCitizenPagingConfigInvalid, err)
	}
	after, err := os.ReadFile(claimPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("holder's claim was destroyed or altered: %q -> %q (%v)", before, after, err)
	}
	// The holder is still the owner: a plain opener is still refused as claimed.
	if _, err := rr764Wire(t, dir, false); err == nil {
		t.Fatal("holder lost its claim: a second opener succeeded")
	}
}

// BUG-1178 r3 ruling (a): a MATCHING identity carrying unknown extra fields
// stays accepted (forward compatibility). Pinned so nobody tightens it.
func TestBUG1178_MatchingIdentityWithExtraFieldsAccepted(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, pageIdentityFileName)
	body := `{"tenant_id":"local","city_id":"city-reround","world_seed":764950,"futureField":{"x":1}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("matching identity with extra fields was refused: %v", err)
	}
	_ = c.Close()
}

// BUG-1178 r4 ruling (c): a directory with a POSITIVE identity problem is
// refused with its own code on EVERY open, plain or reclaim, while another
// opener holds the claim, and without touching that claim.
func TestBUG1178_PositiveProblemRefusedWithOwnCodeEvenWhileClaimHeld(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		city   persist.CityKey
		want   string
	}{
		{"damagedIdentity", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, pageIdentityFileName), []byte(`{"tenantId":"loc`), 0o644); err != nil {
				t.Fatal(err)
			}
		}, rr764City, ErrCitizenPagingConfigInvalid},
		{"identityMismatch", func(t *testing.T, dir string) {}, bug1178OtherCity, ErrCitizenPagingIdentityMismatch},
		{"unstampedForeignPages", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, pageIdentityFileName)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "shard-0.page"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, rr764City, ErrCitizenPagingUnstampedForeignPages},
	}
	for _, tc := range cases {
		for _, reclaim := range []bool{false, true} {
			name := tc.name + "/plain"
			if reclaim {
				name = tc.name + "/reclaim"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				holder, err := rr764Wire(t, dir, false)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = holder.Close() }()
				tc.break_(t, dir)
				claimPath := filepath.Join(dir, pageClaimFileName)
				before, err := os.ReadFile(claimPath)
				if err != nil {
					t.Fatal(err)
				}
				_, err = bug1178WireCity(t, dir, tc.city, reclaim)
				if err == nil || rr764Code(t, err) != tc.want {
					t.Fatalf("want %s with the claim held, got %v", tc.want, err)
				}
				after, rerr := os.ReadFile(claimPath)
				if rerr != nil || string(after) != string(before) {
					t.Fatalf("holder's claim touched: %q -> %q (%v)", before, after, rerr)
				}
			})
		}
	}
}
