package compose

import (
	"os"
	"testing"
	"time"
)

// BUG-799 r1 F3: a Reclaimed claim whose epoch is in the future (clock stepped
// back) has a negative age and must be treated as STALE (stealable at once),
// even when its pid is alive, rather than fresh.
func TestBUG799_FutureEpochReclaimedClaimStealable(t *testing.T) {
	dir := t.TempDir()
	realNow := claimNow
	t.Cleanup(func() { claimNow = realNow })
	now := realNow()
	claimNow = func() time.Time { return now }
	r1PlantClaim(t, dir, pageClaimRecord{PID: os.Getpid(), TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: now.Add(time.Hour).UnixNano(), Reclaimed: true})
	c, err := claimPageDir(dir, rr764City, true, "f3")
	if err != nil {
		t.Fatalf("future-epoch Reclaimed claim must be stealable immediately, got %v", err)
	}
	_ = c
}
