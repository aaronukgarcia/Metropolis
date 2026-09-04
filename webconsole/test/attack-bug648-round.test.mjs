// attack-bug648-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) against
// BUG-648's power-density catalogue rebalance (pow_wind 8->6 MW; a FIRST DRAFT
// also shrunk pow_nuke's footprint 13x13->5x4, which this round REJECTED — see
// finding (2)). Attacker is NOT the author. Verdict: REJECT on the footprint
// shrink; everything else held. The shrink was reverted (pow_nuke back to
// 13x13, mw/cost/upkeep unchanged) and this file was flipped, per the round's
// own instruction, from "documents the live regression" to "pins the FIXED
// behaviour as a permanent regression test."
//
// Verdict-shaping findings from this round:
//
//  (1) BROWNOUT QUESTION (Aaron's ask, and the primary reject criterion):
//      on Aaron's FRESH real savepoint (49,174 buildings, 3.2m pop,
//      decoded via saveCodec.decode from the LZv1 blob), the true gross
//      capacity hit is -14.57% (bigger than the author's -3.0% fixture
//      estimate, because his city grew from 1,991 to 4,535 online turbines
//      AND — at the time of the round — the footprint shrink was silently
//      taking his one placed nuclear plant offline too, see finding (2)).
//      Even so, cap (59,745 MW) still clears need (39,534 MW) with a
//      healthy 51% margin — NO brownout flip on this save. This half of
//      the attack did not block accept.
//
//  (2) THE NUKE SHRINK — REAL, LIVE REGRESSION FOUND AND FIXED: the FIRST
//      DRAFT's data.ts header comment claimed "a shrink is safe ...
//      occupiedSet only grows a hazard on GROW, never on shrink". That is
//      true for TILE-OVERLAP safety but FALSE for ROAD-ADJACENCY safety,
//      and this round proved it against Aaron's OWN placed nuke (id 3331,
//      x:33,y:141), not a contrived fixture: isRoadAdjacent(state, nuke)
//      was TRUE against the pre-fix 13x13 footprint (his real road network
//      really does touch the plant's outer edge) and FALSE against the
//      (now-reverted) 5x4 footprint (the same real road tile was 8+ tiles
//      away from the shrunk footprint's edge) — the shrink would have
//      silently taken his one nuclear plant OFFLINE, deleting 1,120 MW of
//      capacity with no warning. FIX SHIPPED: pow_nuke's footprint is back
//      at 13x13 (mw/cost/upkeep unchanged throughout), so this file's tests
//      below now assert the plant STAYS online/road-adjacent — the
//      opposite assertions from the reject-evidence draft, pinned
//      permanently so the shrink can never silently return.
//
//  (3) fittingTier is unaffected either way (mw term dominates the
//      footprint term at this spec's scale) — no unintended road-tier
//      downgrade from the footprint choice.
//
//  (4) Consolidator cross-check (worktree agent-a9a51b56bbaa2cfdb,
//      consolidator.ts groupSizeOf, verified by copying the READ-ONLY,
//      unmodified module in for analysis only — not shipped from this
//      branch): pow_wind->pow_windfarm groupSize is now EXACTLY 10 (was
//      8) — which makes Aaron's own literal worked example ("10 wind
//      turbines -> 1 wind farm", cited in the FEAT-2326609761 planning doc
//      as something the OLD catalogue could never produce) land exactly
//      right. pow_wind->pow_offshore groupSize rises 38->50, still well
//      inside the doc's measured 69-max-co-located-per-800m-section
//      figure (footprint is unchanged for pow_wind, so that measurement
//      is untouched by this diff). Positive, not a regression. pow_nuke's
//      "10 nuke plants -> 1 XXL nuke" example is NOT expected to fall out
//      of density (pow_nuke is a documented footprint-realism exception,
//      see bug648-power-density.test.mjs) — it is intended to be handled
//      by pow_nuke's own capacityTiers reactor ladder (count-reduction),
//      a separate mechanism this round did not need to re-verify.
//
// Run with the scoped test runner from webconsole/:
//   node ../tools/test/scoped.mjs test/attack-bug648-round.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { decode } from '../src/sim/saveCodec.ts';
import {
  SPECS,
  powerStats,
  brownoutOf,
  isOnline,
  isRoadAdjacent,
  isRoadConnected,
  fittingTier,
} from '../src/sim/data.ts';

const SAVE_PATH = 'C:\\Users\\aarongarcia\\.claude\\jobs\\f9ac9353\\tmp\\aaron-49k.lz';

function densityOf(sp) {
  return sp.mw / (sp.w * sp.h);
}

// ---------------------------------------------------------------------------
// (5) RED-PROOF: perturbing pow_windfarm (not pow_nuke, per the attack brief)
// must flip the ladder to failing — proves the ordering check genuinely
// derives from SPECS (GR#15) and is not a tautology that always passes.
// ---------------------------------------------------------------------------
test('BUG-648 RED-PROOF: perturbing pow_windfarm.mw down flips the density ladder to failing', () => {
  const ladderIds = ['pow_wind', 'pow_windfarm', 'pow_ccgt'];
  const real = ladderIds.map((id) => SPECS[id]);
  for (let i = 1; i < real.length; i++) {
    assert.ok(densityOf(real[i]) > densityOf(real[i - 1]), `sanity: shipped ladder must already be ordered at ${real[i].id}`);
  }

  // Perturb ONLY pow_windfarm: drop its mw so it no longer out-densifies
  // pow_wind (6 MW / 1 tile = 6.00; pow_windfarm at mw=6 -> 6/9 = 0.67).
  const perturbed = real.map((sp) => ({ ...sp }));
  const wfIdx = ladderIds.indexOf('pow_windfarm');
  perturbed[wfIdx].mw = 6;
  let brokenAt = -1;
  for (let i = 1; i < perturbed.length; i++) {
    if (!(densityOf(perturbed[i]) > densityOf(perturbed[i - 1]))) {
      brokenAt = i;
      break;
    }
  }
  assert.equal(brokenAt, wfIdx, 'perturbing pow_windfarm.mw must break the ladder exactly at pow_windfarm, proving the check is data-derived, not a tautology');
});

// ---------------------------------------------------------------------------
// (1) THE BROWNOUT QUESTION — Aaron's fresh real savepoint, decoded via the
// real saveCodec, need/cap computed via the real powerStats/brownoutOf SSOT.
// Skips gracefully (does not fail CI) if the local savepoint file is not
// present on this machine — it is a local dogfood artifact, not a repo fixture.
// ---------------------------------------------------------------------------
test('BUG-648: Aaron\'s fresh real savepoint does not flip into brownout/deficit after the fix', { skip: !existsSync(SAVE_PATH) ? 'local savepoint not present on this machine' : false }, () => {
  const state = JSON.parse(decode(readFileSync(SAVE_PATH, 'utf8'))).snapshot;
  assert.ok(state.buildings.length > 10000, 'sanity: this is the real large savepoint, not a stub');

  const after = powerStats(state);
  const afterBrownout = brownoutOf(state);
  assert.equal(afterBrownout.active, false, `AFTER the fix, Aaron's real city must not brownout (need=${after.need} cap=${after.cap})`);
  assert.ok(after.cap > after.need, 'AFTER: capacity must still clear need');

  // TRUE "before" reconstruction: revert pow_wind.mw only. pow_nuke's
  // footprint is UNCHANGED by the shipped fix (the shrink draft was
  // reverted post-round), so — unlike the reject-evidence draft of this
  // test — no footprint monkeypatch is needed to get a faithful "before".
  const oldWindMw = SPECS.pow_wind.mw;
  SPECS.pow_wind.mw = 8;
  const stateForBefore = { ...state }; // fresh object ref busts the memoOnState cache
  const before = powerStats(stateForBefore);
  SPECS.pow_wind.mw = oldWindMw;

  assert.ok(before.cap > after.cap, 'sanity: the true pre-fix catalogue must have had MORE capacity than the shipped one');
  const deltaPct = ((after.cap - before.cap) / before.cap) * 100;
  // Document the real number: this is now driven PURELY by the pow_wind mw
  // change (no nuke road-disconnect confound, since that draft was
  // reverted) — bounded, and the fix must be a real reduction, not a no-op.
  assert.ok(deltaPct < 0, 'the fix must be a real capacity reduction on this save (not a no-op)');
  assert.ok(Math.abs(deltaPct) < 20, `capacity delta ${deltaPct.toFixed(2)}% must stay within a sane blast-radius bound`);
});

// ---------------------------------------------------------------------------
// (2) THE CRITICAL FINDING, FIXED: pow_nuke's footprint stays 13x13, so
// Aaron's REAL, already-placed nuclear plant stays road-adjacent and ONLINE,
// proven against his real building record and real road network, not a
// synthetic fixture. This test now PASSES because the regression was FIXED
// (footprint reverted) — it is the permanent proof the reject finding stays
// closed. A second half proves the MECHANISM: a hypothetical shrink (never
// applied to the shipped catalogue) would still reproduce the exact failure
// this round found, so a future regression of the same shape is caught here
// too, not just by the "footprint === 13x13" pin in bug648-power-density.
// ---------------------------------------------------------------------------
test('BUG-648 FIXED: pow_nuke keeps its footprint at 13x13, so Aaron\'s real placed nuke stays road-adjacent and online', { skip: !existsSync(SAVE_PATH) ? 'local savepoint not present on this machine' : false }, () => {
  const state = JSON.parse(decode(readFileSync(SAVE_PATH, 'utf8'))).snapshot;
  const nuke = state.buildings.find((b) => b.spec === 'pow_nuke');
  assert.ok(nuke, 'sanity: the real save must carry the one placed pow_nuke');
  assert.equal(nuke.footprintW, undefined, 'sanity: this nuke has never been auto-scaled (tier 0, base footprint from the catalogue)');

  // Sanity: the shipped catalogue really is 13x13, not the reverted 5x4 draft.
  assert.equal(SPECS.pow_nuke.w, 13, 'sanity: pow_nuke.w must be the original, un-shrunk 13');
  assert.equal(SPECS.pow_nuke.h, 13, 'sanity: pow_nuke.h must be the original, un-shrunk 13');

  // SHIPPED (13x13): the real building, against Aaron's real road network,
  // IS road-adjacent -> IS online. This is the FIXED behaviour — no
  // monkeypatching needed, this is what actually ships.
  assert.equal(isRoadAdjacent(state, nuke), true, 'SHIPPED: the real nuke reads as road-adjacent against Aaron\'s real road network');
  assert.equal(isRoadConnected(state, nuke), true, 'SHIPPED: the real nuke is road-connected');
  assert.equal(isOnline(state, nuke), true, 'SHIPPED: the real nuke is ONLINE — its 1,120 MW is NOT silently lost');

  // MECHANISM CHECK (never applied to the shipped catalogue — restored
  // immediately): proves the exact failure mode this round found would
  // still reproduce if a future change ever shrinks the footprint again,
  // so this test doubles as a tripwire for that whole regression class.
  const oldW = SPECS.pow_nuke.w;
  const oldH = SPECS.pow_nuke.h;
  SPECS.pow_nuke.w = 5;
  SPECS.pow_nuke.h = 4;
  const roadAdjacentIfShrunk = isRoadAdjacent(state, nuke);
  SPECS.pow_nuke.w = oldW;
  SPECS.pow_nuke.h = oldH;

  assert.equal(roadAdjacentIfShrunk, false, 'MECHANISM: a 5x4 footprint on this SAME real building/road network would still road-disconnect it — confirms the fix (keeping 13x13) is the thing actually preventing the regression, not a coincidence');
});

// ---------------------------------------------------------------------------
// (3) fittingTier sanity — the mw term dominates the footprint term at
// pow_nuke's scale, so a footprint choice either way does not change its
// road-tier requirement (recorded so a future footprint change, if ever
// proposed again via a proper grandfathered migration, has this precedent).
// ---------------------------------------------------------------------------
test('BUG-648: pow_nuke keeps its Motorway (tier 5) fitting requirement regardless of footprint', () => {
  const shipped = SPECS.pow_nuke;
  assert.equal(fittingTier(shipped), 5, 'shipped 13x13 pow_nuke must be tier 5 (Motorway) — mw:1120 alone already clears the tier-5 threshold');

  const hypotheticalShrunk = { ...shipped, w: 5, h: 4 };
  assert.equal(fittingTier(hypotheticalShrunk), 5, 'a hypothetical 5x4 shape would ALSO be tier 5 — confirms footprint choice does not affect road-tier fitting either way');
});

// ---------------------------------------------------------------------------
// (6) Consolidator ladder interaction (worktree agent-a9a51b56bbaa2cfdb,
// FEAT-2326609761) — spot-checked by direct computation from the SAME
// groupSize formula (ceil(capacityOf(b)/capacityOf(a))) that module's
// groupSizeOf() implements, using this branch's live SPECS. Not imported
// cross-worktree (that module isn't shipped on this branch) — the exact
// numbers were independently cross-checked during this round by copying
// the read-only, unmodified consolidator.ts in for analysis (see the
// round's report), and are pinned here as a regression check on the
// catalogue values that interaction depends on.
// ---------------------------------------------------------------------------
test('BUG-648: the new pow_wind mw makes Aaron\'s own "10 turbines -> 1 wind farm" example exact', () => {
  const groupSize = Math.ceil(SPECS.pow_windfarm.mw / SPECS.pow_wind.mw);
  assert.equal(groupSize, 10, 'ceil(pow_windfarm.mw / pow_wind.mw) must be exactly 10 turbines per farm, matching the FEAT-2326609761 worked example this fix was cited as fixing');
});

test('BUG-648: the new pow_wind->pow_offshore consolidation group size stays inside the measured max-co-location bound', () => {
  const groupSize = Math.ceil(SPECS.pow_offshore.mw / SPECS.pow_wind.mw);
  // 69 is the FEAT-2326609761 doc's measured max co-located pow_wind count
  // in an 800m section on Aaron's real city — a footprint-driven figure,
  // unaffected by this mw-only catalogue change (pow_wind's w/h did not
  // change), so it remains a valid ceiling to check the new group size against.
  const MEASURED_MAX_CO_LOCATED_PER_SECTION = 69;
  assert.ok(groupSize <= MEASURED_MAX_CO_LOCATED_PER_SECTION,
    `group size ${groupSize} must still fit within an 800m section (max ${MEASURED_MAX_CO_LOCATED_PER_SECTION} co-located turbines measured)`);
});
