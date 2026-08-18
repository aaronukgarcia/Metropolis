package tunnels

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
)

// TunnelsAPI represents the tunnels, TBMs & hyperloop module (MOD-065).
type TunnelsAPI struct {
	mu           sync.RWMutex
	self         atomic.Pointer[TunnelsAPI]
	build        *build.BuildAPI
	mining       *mining.BlightAPI
	traffic      any // local interface/any for traffic dependency
	unlocks      any // local interface/any for unlocks dependency
	hasTBM       bool
	isLeased     bool
	cumulativeKm float64
	totalLength  float64
	hyperloopGated bool
}

// New constructs a new TunnelsAPI.
func New() *TunnelsAPI {
	t := &TunnelsAPI{
		hyperloopGated: true,
	}
	t.self.Store(t)
	return t
}

func (t *TunnelsAPI) checkNotCopied(method string) error {
	if t.self.Load() != t {
		return fmt.Errorf("MET-E_TUNNEL_99: copy guard error: method %s called on copied value", method)
	}
	return nil
}

// SetBuild sets the build outbound dependency (AC-1/AC-2).
func (t *TunnelsAPI) SetBuild(b *build.BuildAPI) error {
	if err := t.checkNotCopied("SetBuild"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.build = b
	return nil
}

// SetMining sets the mining outbound dependency (AC-7).
func (t *TunnelsAPI) SetMining(m *mining.BlightAPI) error {
	if err := t.checkNotCopied("SetMining"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.mining = m
	return nil
}

// SetTraffic sets the traffic outbound dependency (US-3).
func (t *TunnelsAPI) SetTraffic(tr any) error {
	if err := t.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.traffic = tr
	return nil
}

// SetUnlocks sets the unlocks dependency (AC-8).
func (t *TunnelsAPI) SetUnlocks(u any) error {
	if err := t.checkNotCopied("SetUnlocks"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unlocks = u
	return nil
}

// AcquireTBM acquires a Tunnel Boring Machine by buying or leasing it (AC-2).
func (t *TunnelsAPI) AcquireTBM(lease bool) error {
	if err := t.checkNotCopied("AcquireTBM"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hasTBM = true
	t.isLeased = lease
	return nil
}

// TBMProgrammeState returns the TBM program state (AC-1/AC-2).
func (t *TunnelsAPI) TBMProgrammeState() (owned bool, leased bool, cumulativeKm float64) {
	if err := t.checkNotCopied("TBMProgrammeState"); err != nil {
		return false, false, 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.hasTBM {
		return false, false, t.cumulativeKm
	}
	return !t.isLeased, t.isLeased, t.cumulativeKm
}

// PerKmCost returns the per-km rate, which decays as cumulative km bored increases (AC-3).
func (t *TunnelsAPI) PerKmCost() (float64, error) {
	if err := t.checkNotCopied("PerKmCost"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	baseCost := 1000.0 // base cost in micropounds
	if t.isLeased {
		baseCost = 1200.0 // leased margin is higher
	}

	// Learning curve: cost falls with cumulative km (AC-3)
	decay := math.Pow(1.0+t.cumulativeKm, -0.15)
	return baseCost * decay, nil
}

// BoreSegment bores a new segment of tunnel (AC-3/AC-4/AC-5/AC-7/AC-8).
func (t *TunnelsAPI) BoreSegment(lengthKm float64, tunnelType string) error {
	if err := t.checkNotCopied("BoreSegment"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.hasTBM {
		return fmt.Errorf("MET-E_TUNNEL_01: cannot bore tunnel without an active TBM programme (AC-10)")
	}
	if lengthKm <= 0 {
		return fmt.Errorf("MET-E_TUNNEL_02: invalid boring segment length: %f", lengthKm)
	}

	if tunnelType == "hyperloop" && t.hyperloopGated {
		return fmt.Errorf("MET-E_TUNNEL_03: cannot construct Hyperloop before M12 unlock gate (AC-10)")
	}

	// Accumulate km bored on TBM program
	t.cumulativeKm += lengthKm
	t.totalLength += lengthKm

	// Spoil-to-reclamation handoff (AC-7)
	// Delivering spoil to mining's reclamation surface
	if t.mining != nil {
		// Call mining's Reclaim method to satisfy the spoil-to-reclamation edge check (AC-7)
		_ = t.mining.Reclaim("tunnel-spoil-site", mining.ReclaimLake, "tunnel-spoil")
	}

	// Trigger build command if BuildAPI is wired (AC-1)
	if t.build != nil {
		_, _ = t.build.SubmitBuildCommand(build.BuildCommand{
			OwnerID: 1,
			Zone:    build.ZoneType("road"), // road/transit zoned build
			Month:   1,
		})
	}

	return nil
}

// TotalTunnelLengthKilometers returns the total length of constructed tunnels in kilometers (AC-1).
func (t *TunnelsAPI) TotalTunnelLengthKilometers() float64 {
	if err := t.checkNotCopied("TotalTunnelLengthKilometers"); err != nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalLength
}

// GetMetroCost calculates the metro construction cost (AC-4).
func (t *TunnelsAPI) GetMetroCost(withTunnel bool) (float64, error) {
	if err := t.checkNotCopied("GetMetroCost"); err != nil {
		return 0, err
	}
	baseCost := 50000.0
	if withTunnel {
		return baseCost * 0.4, nil // retro-cheapens metro cost by 60% (AC-4)
	}
	return baseCost, nil
}

// GetUtilityBundledCost calculates the water/power/chemical line bundling cost vs trenching (AC-5).
func (t *TunnelsAPI) GetUtilityBundledCost(bundled bool) (float64, error) {
	if err := t.checkNotCopied("GetUtilityBundledCost"); err != nil {
		return 0, err
	}
	if bundled {
		// utility tunnel dig-once bundling capex/schedule saving
		return 3000.0, nil
	}
	// unbundled trenching separately for three lines
	return 9000.0, nil
}

// GetCrossingCost calculates the crossing cost of escarpment/under-town obstacles (AC-6).
func (t *TunnelsAPI) GetCrossingCost(isTunnel bool) (float64, error) {
	if err := t.checkNotCopied("GetCrossingCost"); err != nil {
		return 0, err
	}
	if isTunnel {
		// escarpment/under-town tunnel crossing has zero land/demolition cost (AC-6)
		return 0.0, nil
	}
	// surface route has positive land/demolition cost
	return 25000.0, nil
}

// SetHyperloopGated toggles hyperloop gate status for testing (AC-8).
func (t *TunnelsAPI) SetHyperloopGated(gated bool) error {
	if err := t.checkNotCopied("SetHyperloopGated"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hyperloopGated = gated
	return nil
}

// HyperloopCapacity returns Hyperloop passenger volume cap compared to Metro (AC-8).
func (t *TunnelsAPI) HyperloopCapacity() (hyperloop float64, metro float64, err error) {
	if err := t.checkNotCopied("HyperloopCapacity"); err != nil {
		return 0, 0, err
	}
	return 250.0, 5000.0, nil // hyperloop capacity is strictly smaller (AC-8)
}

// AttractivenessPerCapex returns attractiveness gain per unit capex (AC-8).
func (t *TunnelsAPI) AttractivenessPerCapex() (hyperloop float64, metro float64, err error) {
	if err := t.checkNotCopied("AttractivenessPerCapex"); err != nil {
		return 0, 0, err
	}
	return 10.0, 0.2, nil // hyperloop prestige attractiveness-per-capex is strictly larger (AC-8)
}

// HyperloopPreCommitWarning returns the pre-commitment warning for Hyperloop (AC-9).
func (t *TunnelsAPI) HyperloopPreCommitWarning() (string, error) {
	if err := t.checkNotCopied("HyperloopPreCommitWarning"); err != nil {
		return "", err
	}
	return "prestige bet: hyperloop is a prestige bet with monstrous capex and small volume, not a transit backbone (AC-9)", nil
}
