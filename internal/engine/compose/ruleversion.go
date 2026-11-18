package compose

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 2 — the composition owns the live rule version.
//
// Rule versions record how a save judged player commands (accepted or
// refused, and what they cost), so an old city replays under the rules it
// was made under and new play gets the new rules. The unit is ONE integer
// for the whole engine (internal/engine/rules). This file holds:
//
//   - the live version and the version a load found (ruleVersionState);
//   - the injection of a getter into every versioned module at Wire, and the
//     enforcement that none is left without one (ruleVersionState.wire,
//     ruleVersionState.verify): a module with no getter would silently run
//     the CURRENT rules during replay, which is the failure this item exists
//     to prevent;
//   - the shared replay function (replayFrames) used by tail replay, genesis
//     replay and the validation replay, which applies each frame's recorded
//     version and checks the whole sequence before touching any state;
//   - the fail-closed classification of snapshot and frame versions;
//   - the operator report of a restore that replayed older rules.
//
// The version is data, not clock or randomness, and is never hashed into
// StateDigest. It changes only between commands (a restore, or a test
// pinning), never inside one.

// ruleVersioned is what a module with a versioned rule implements. Wire
// injects the composition's live-version getter with SetRuleVersionSource;
// HasRuleVersionSource lets the composition prove the getter is held. A
// module reads its version with rules.Active(getter) and branches with
// rules.Has(active, rules.TheConstant).
type ruleVersioned interface {
	SetRuleVersionSource(func() rules.Version)
	HasRuleVersionSource() bool
}

// versionedEntry is one registered versioned module.
type versionedEntry struct {
	name string
	mod  ruleVersioned
}

// ruleVersionState is the composition's rule-version bookkeeping.
type ruleVersionState struct {
	// live is the version commands are judged by right now. It starts at
	// rules.Current. Replay sets it per frame; the restore function releases
	// it back to rules.Current when it returns.
	live atomic.Int64
	// loaded is the version the last Load found in the bundle's Meta (0 when
	// absent). Replay of a snapshot's tail starts from it.
	loaded atomic.Int64
	// modules is the registry of versioned modules, in registration order
	// (never a map, GR#21). Written only during Wire (and by tests, before
	// the composition is shared).
	modules []versionedEntry
}

func newRuleVersionState() *ruleVersionState {
	s := &ruleVersionState{}
	s.live.Store(int64(rules.Effective()))
	return s
}

// get is the getter injected into versioned modules and the journaler.
func (s *ruleVersionState) get() rules.Version { return rules.Version(s.live.Load()) }

// wire registers m as a versioned module and injects the live
// getter. engine.build joins here in lane 4:
//
//	if err := st.rv.wire(st.cid, "engine.build", st.buildAPI); err != nil { ... }
func (s *ruleVersionState) wire(cid, name string, m ruleVersioned) error {
	if m == nil {
		return errs.New(ErrModuleFailed, cid, map[string]any{"module": name, "step": "rule-version-getter", "cause": "versioned module is nil"})
	}
	m.SetRuleVersionSource(s.get)
	s.modules = append(s.modules, versionedEntry{name: name, mod: m})
	return s.verify(cid)
}

// verify fails when any registered versioned module holds no
// getter. It runs at the end of Wire and at the start of every replay, so a
// module whose getter was dropped can never judge replayed history by the
// current rules.
func (s *ruleVersionState) verify(cid string) error {
	for _, v := range s.modules {
		if v.mod == nil || !v.mod.HasRuleVersionSource() {
			return errs.New(ErrModuleFailed, cid, map[string]any{
				"module": v.name,
				"step":   "rule-version-getter",
				"cause":  "module is registered as rule-versioned but holds no rule-version getter; it would judge replayed history by the current rules",
			})
		}
	}
	return nil
}

// RuleVersion is the version commands are judged by right now: rules.Current
// for live play, the recorded version of a frame while replay applies it.
func (c *Composition) RuleVersion() rules.Version { return c.state.rv.get() }

// LoadedRuleVersion is the version the last Load found in the bundle (0 for
// a bundle with no stamp). It says what the loaded state was made under; it
// does not change what new commands are judged by.
func (c *Composition) LoadedRuleVersion() rules.Version {
	return rules.Version(c.state.rv.loaded.Load())
}

// PinRuleVersion sets the live version. It exists for fixture generators,
// the replay harness and tests that must run the engine under an older rule
// set; production code never calls it (the restore function releases the
// live version back to rules.Current by itself). It refuses a version this
// build does not know. Call it only between commands.
func (c *Composition) PinRuleVersion(v rules.Version) error {
	if !rules.IsKnown(v) {
		return versionRefusal(c.state.cid, v, "pin")
	}
	c.state.rv.live.Store(int64(v))
	return nil
}

// versionRefusal builds the registry error for an unusable version v:
// MET-G5610 (newer than this build), MET-G5613 (negative), or nil-safe
// MET-G5611 text via classifyVersion. where names the artefact for the
// malformed case.
func versionRefusal(cid string, v rules.Version, where string) error {
	if err := classifyVersion(cid, v, true); err != nil {
		return err
	}
	return errs.New(rules.ErrMalformedRuleStamp, cid, map[string]any{"reason": where + ": unusable rule version " + strconv.Itoa(int(v))})
}

// classifyVersion maps rules.Classify to the registry errors. checkOld is
// false when only "newer than this build" and "malformed" matter (the
// whole-journal pre-check, where frames before a snapshot need no old rule).
func classifyVersion(cid string, v rules.Version, checkOld bool) error {
	switch rules.Classify(v) {
	case rules.SupportTooNew:
		return errs.New(rules.ErrSavedUnderNewerRules, cid, map[string]any{
			"savedVersion":   int(v),
			"currentVersion": int(rules.Effective()),
		})
	case rules.SupportInvalid:
		return errs.New(rules.ErrMalformedRuleStamp, cid, map[string]any{
			"reason": "rule version " + strconv.Itoa(int(v)) + " is negative",
		})
	case rules.SupportTooOld:
		if checkOld {
			return errs.New(rules.ErrSavedUnderRetiredRules, cid, map[string]any{
				"savedVersion":    int(v),
				"oldestSupported": int(rules.OldestSupported),
			})
		}
	}
	return nil
}

// checkFrames validates the version sequence of frames, starting from the
// version before the first frame (0 for a whole journal, the snapshot's
// version for its tail). It is pure: it touches no engine, composition or
// store. Every frame, including the Subscribe and Unsubscribe frames replay
// skips, takes part in the ordering check. A frame newer than this build is
// MET-G5610 (never clamped); an older-than-supported frame is MET-G5611 when
// checkOld; a frame lower than the one before it is MET-G5612.
func checkFrames(cid string, frames []JournalFrame, from rules.Version, checkOld bool) error {
	prev := from
	for _, f := range frames {
		if err := classifyVersion(cid, f.Version, checkOld); err != nil {
			return err
		}
		if f.Version < prev {
			return errs.New(rules.ErrRuleVersionWentBackwards, cid, map[string]any{
				"version":         int(f.Version),
				"previousVersion": int(prev),
			})
		}
		prev = f.Version
	}
	return nil
}

// checkSnapshotVersion classifies a bundle's Meta version (an int in the
// save package, which does not import rules). Called by Load before any
// participant is touched.
func checkSnapshotVersion(cid string, metaVersion int) error {
	return classifyVersion(cid, rules.Version(metaVersion), true)
}

// readBundleRuleVersion reads dir's Meta rule version. found is false when
// the Meta file cannot be read; the real Load then fails on its own with its
// own error, so the pre-check never masks it.
func readBundleRuleVersion(dir string) (version int, found bool) {
	meta, err := save.ReadMeta(dir)
	if err != nil {
		return 0, false
	}
	return meta.RuleVersion, true
}

// replayFrames applies frames to e/c in order via e.HandleCommand, each
// under the rule version recorded with it, and is the ONE replay function:
// tail replay, genesis replay and the validation replay (tryRestoreCandidate)
// all call it, so the throwaway engine and the real engine cannot disagree.
//
// from is the version of the history before the first frame (0 for genesis,
// the snapshot's version for a tail). Before any command is applied it
// proves every versioned module holds a getter and that the whole sequence
// is replayable (checkFrames), so a refusal leaves the engine untouched.
// Each frame sets the live version BEFORE it is applied. When the function
// returns, success or not, the live version is rules.Current again: that is
// the switch from old rules to new, one assignment, and nothing is written
// at that moment (the next accepted command's frame carries the new version).
//
// A rejection stops the replay with ErrSnapshotTailReplayRejected, exactly
// as before rule versions. Subscribe and Unsubscribe are SESSION state, not
// simulation state (BUG-1225): a live subscription belongs to one connection
// (its owner epoch is process-local and never journaled), and a restored
// city has no connection attached, so replaying a journaled Subscribe would
// create a subscription no live connection owns and nothing could ever
// remove. They are skipped for application but still counted in the version
// ordering check above.
func replayFrames(e *core.Engine, c *Composition, frames []JournalFrame, from rules.Version) error {
	cid := c.state.cid
	if err := c.state.rv.verify(c.state.cid); err != nil {
		return err
	}
	if err := checkFrames(cid, frames, from, true); err != nil {
		return err
	}
	defer c.state.rv.live.Store(int64(rules.Effective()))
	for _, f := range frames {
		if f.Kind == protocol.KindSubscribe || f.Kind == protocol.KindUnsubscribe {
			continue
		}
		c.state.rv.live.Store(int64(f.Version))
		res := e.HandleCommand(f.Command)
		if !res.Accepted {
			return errs.New(ErrSnapshotTailReplayRejected, string(f.CorrelationID), map[string]any{
				"kind":  string(f.Kind),
				"error": fmt.Sprintf("%v", res.Error),
			})
		}
	}
	return nil
}

// RestoreReport is what a restore tells its host (lane 5: cmd/metroserve).
type RestoreReport struct {
	// UsedSnapshot is true when a snapshot plus its tail was applied, false
	// for a genesis replay.
	UsedSnapshot bool
	// Tick is the restored tick.
	Tick int64
	// ReplayedVersion is the rule version the last piece of replayed history
	// was recorded under: the later of the snapshot's version and the last
	// journal frame's version (0 when nothing was replayed).
	ReplayedVersion rules.Version
	// LiveVersion is the rule version new commands are judged by from now on
	// (always rules.Effective()).
	LiveVersion rules.Version
	// OlderRules is true when history was replayed and ReplayedVersion is
	// below LiveVersion. Then OperatorLine is set and the WARN registry event
	// MET-G5614 has been raised; otherwise both are empty and nothing was
	// raised (a second wake after a frame at the current version is silent).
	OlderRules bool
	// OperatorLine is the single stdout line for the host to print, built
	// from the rule registry's own Old and New text (rules.Describe).
	OperatorLine string
}

// finishRestore builds the report and, when older rules were replayed,
// raises the one WARN event. Nothing is written to any store.
func (c *Composition) finishRestore(usedSnapshot bool, tick int64, frames []JournalFrame) RestoreReport {
	replayed := rules.Version(c.state.rv.loaded.Load())
	if !usedSnapshot {
		replayed = 0
	}
	if n := len(frames); n > 0 && frames[n-1].Version > replayed {
		replayed = frames[n-1].Version
	}
	rep := RestoreReport{
		UsedSnapshot:    usedSnapshot,
		Tick:            tick,
		ReplayedVersion: replayed,
		LiveVersion:     rules.Effective(),
	}
	if (usedSnapshot || len(frames) > 0) && replayed < rules.Effective() {
		rep.OlderRules = true
		changes := rules.Describe(replayed, rules.Effective())
		// A WARN registry event: logged by errs.New, not returned.
		_ = errs.New(rules.WarnRestoredUnderOlderRules, c.state.cid, map[string]any{
			"fromVersion":    int(replayed),
			"toVersion":      int(rules.Effective()),
			"currentVersion": int(rules.Effective()),
			"changes":        changes,
		})
		rep.OperatorLine = "city restored under older rules: replayed rule version " + strconv.Itoa(int(replayed)) +
			" through " + strconv.Itoa(int(rules.Effective())) + ", now running rule version " + strconv.Itoa(int(rules.Effective())) + ". " + changes
	}
	return rep
}

// RestoreLatestSnapshotOrGenesisReport is RestoreLatestSnapshotOrGenesis
// returning the full [RestoreReport] (the host prints OperatorLine when
// OlderRules is set). The restore itself is identical; see that function.
func RestoreLatestSnapshotOrGenesisReport(ctx context.Context, e *core.Engine, c *Composition, store persist.Store, city persist.CityKey) (RestoreReport, error) {
	used, tick, frames, err := restoreLatest(ctx, e, c, store, city)
	if err != nil {
		return RestoreReport{}, err
	}
	return c.finishRestore(used, tick, frames), nil
}

// FixtureLayout documents the rule-fixture directory contract.
//
// A rule fixture is a persist.DiskStore root for the city
// persist.CityKey{TenantID: "local", CityID: "default"}, recorded with the
// engine pinned to the version before the rule (Number-1): at least one
// journal frame, at least one snapshot, every frame and every snapshot at
// exactly that version. See testdata/rules/README.md.

// CheckRuleFixture proves the persist directory at dir is a usable rule
// fixture recorded at exactly want: it exists, holds at least one journal
// frame and one snapshot, every frame decodes, every frame's version and
// every snapshot's Meta version equals want. It reads only; it never
// repairs or writes.
func CheckRuleFixture(dir string, want rules.Version) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("rule fixture %s: directory does not exist", dir)
	}
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		return fmt.Errorf("rule fixture %s: not a persist directory: %w", dir, err)
	}
	ctx := context.Background()
	frames, err := RestoreCommands(ctx, store, defaultPersistCity)
	if err != nil {
		return fmt.Errorf("rule fixture %s: journal: %w", dir, err)
	}
	if len(frames) == 0 {
		return fmt.Errorf("rule fixture %s: journal holds no frames", dir)
	}
	for i, f := range frames {
		if f.Version != want {
			return fmt.Errorf("rule fixture %s: journal frame %d is recorded at rule version %d, want %d", dir, i, f.Version, want)
		}
	}
	ids, err := store.ListSnapshots(ctx, defaultPersistCity)
	if err != nil {
		return fmt.Errorf("rule fixture %s: snapshots: %w", dir, err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("rule fixture %s: holds no snapshot", dir)
	}
	for _, id := range ids {
		data, err := store.GetSnapshot(ctx, defaultPersistCity, id)
		if err != nil {
			return fmt.Errorf("rule fixture %s: snapshot %s: %w", dir, id, err)
		}
		v, err := snapshotRuleVersion(data)
		if err != nil {
			return fmt.Errorf("rule fixture %s: snapshot %s: %w", dir, id, err)
		}
		if v != int(want) {
			return fmt.Errorf("rule fixture %s: snapshot %s is recorded at rule version %d, want %d", dir, id, v, want)
		}
	}
	return nil
}

// snapshotRuleVersion reads a snapshot payload's Meta rule version without
// restoring it.
func snapshotRuleVersion(data []byte) (int, error) {
	dir, err := os.MkdirTemp("", "metropolis-snapver-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := unzipDir(data, dir); err != nil {
		return 0, err
	}
	summaries, _, err := save.List(dir)
	if err != nil {
		return 0, err
	}
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			meta, err := save.ReadMeta(s.Path)
			if err != nil {
				return 0, err
			}
			return meta.RuleVersion, nil
		}
	}
	return 0, fmt.Errorf("no composition bundle in snapshot")
}
