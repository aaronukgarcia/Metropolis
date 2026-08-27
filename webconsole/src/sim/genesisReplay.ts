// genesisReplay.ts — FEAT-1972079897 inc1: deterministic GENESIS replay core.
//
// Design brief: docs/planning/hard-reset-replay-brief.md.
//
// The savepoint path in replay.ts replays a journal TAIL onto a frozen SimState
// SNAPSHOT — old-rules numbers baked in. That resurrects the old city; it does
// NOT re-derive it under new engine rules. This module is the complementary
// GENESIS replay: start from initialState() on the CURRENT engine and re-apply
// every journaled action in recorded order through the SAME pure reducer, so the
// city is re-derived on today's build (hard-reset + replay from genesis).
//
// Purity: no Date.now / Math.random, no I/O, no React, and the input journal is
// never mutated (reducer is pure; we only read journal.entries in order).
//
// inc1 scope is the HEADLESS CORE only — no UI, no build stamping, no defensive
// per-action skip/crash-catch (that is inc2), and no sparse-log / tick-synthesis
// optimization (that is a LATER increment — see the note on replayFromGenesis).

import type { SimState } from './types.ts';
import type { Journal } from './journal.ts';
import { initialState, reducer } from './engine.ts';

/**
 * Replay a journal from GENESIS: start at initialState() (NOT a snapshot) and
 * apply every recorded action in order through the pure reducer, returning the
 * final state. Re-derives the city under the CURRENT engine rules.
 *
 * The input journal is treated as read-only — entries are applied in their
 * recorded order and never reordered or mutated.
 *
 * inc1 FIDELITY NOTE: the journal already contains explicit `tick` entries
 * (journal.isStateAffecting classifies `tick` as state-affecting), so applying
 * them reproduces timing exactly. We deliberately do NOT synthesize tick
 * advances here — that keeps genesis replay byte-faithful to the recorded log.
 * The sparse-action-log optimization from §4.1/§4.2 of the brief (store only
 * sparse player actions, synthesize the ticks between them) is a LATER
 * increment and is intentionally out of scope for inc1.
 */
export function replayFromGenesis(journal: Journal): SimState {
  let state = initialState();
  for (const entry of journal.entries) {
    state = reducer(state, entry.action);
  }
  return state;
}

/**
 * Deterministic stable serialization: JSON with object keys sorted recursively,
 * so two structurally-equal states compare byte-identical regardless of key
 * insertion order. Used as the determinism-self-test oracle (brief §4.5).
 * Pure — no Date/Math/randomness.
 */
export function stableStringify(value: unknown): string {
  return JSON.stringify(value, replacerSortingKeys(value));
}

function replacerSortingKeys(root: unknown): (key: string, val: unknown) => unknown {
  // JSON.stringify visits nested values; for every plain object we return a new
  // object whose keys are inserted in sorted order, which fixes serialization
  // order deterministically. Arrays keep their (meaningful) order.
  void root;
  return (_key: string, val: unknown): unknown => {
    if (val && typeof val === 'object' && !Array.isArray(val)) {
      const src = val as Record<string, unknown>;
      const sorted: Record<string, unknown> = {};
      for (const k of Object.keys(src).sort()) {
        sorted[k] = src[k];
      }
      return sorted;
    }
    return val;
  };
}

/**
 * Determinism self-test (brief §4.5): replay the SAME journal from genesis twice
 * and confirm the two final states are byte-identical under stable JSON. Proves
 * the replay is deterministic on THIS build — a stray Date.now / Math.random in
 * engine code (GR#21 already bans them) would surface here as a mismatch.
 *
 * Returns true iff the two independent replays are byte-identical.
 */
export function replayIsDeterministic(journal: Journal): boolean {
  const first = replayFromGenesis(journal);
  const second = replayFromGenesis(journal);
  return stableStringify(first) === stableStringify(second);
}
