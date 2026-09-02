import type { SimState } from './types.ts';
import type { Journal } from './journal.ts';
import type { Savepoint } from './replay.ts';
import type { MapViewState } from './uistate.ts';
import { createSavepoint } from './replay.ts';
import { emptyJournal } from './journal.ts';
import { gameDate } from './utils.ts';
import { sanitizeTreasury } from './engine.ts';
import { codedError } from './backend.ts';

export const GAME_SAVE_FORMAT = 'metropolis-save/1';

export interface GameSave {
  format: typeof GAME_SAVE_FORMAT;
  name: string;
  savedAt: string;
  buildVersion: string;
  savepoint: Savepoint;
  journal: Journal;
}

export interface ParseGameSaveResult {
  ok: boolean;
  save?: GameSave;
  reason?: string;
}

export function suggestedSaveName(tick: number, label?: string): string {
  const date = gameDate(tick).replace(/[·\s]/g, '-');
  const base = (label ?? 'Metropolis').replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'Metropolis';
  return `${base}-${date}.json`;
}

export function buildGameSave(opts: {
  state: SimState;
  journal: Journal;
  journalTail: Journal['entries'];
  name: string;
  buildVersion: string;
  camera?: MapViewState | null;
  now?: Date;
}): GameSave {
  const now = opts.now ?? new Date();
  const snapshot = sanitizeTreasury(opts.state);
  return {
    format: GAME_SAVE_FORMAT,
    name: opts.name,
    savedAt: now.toISOString(),
    buildVersion: opts.buildVersion,
    savepoint: createSavepoint(snapshot, opts.journalTail, now, opts.buildVersion, opts.camera ?? null),
    journal: opts.journal ?? emptyJournal(),
  };
}

/**
 * BUG-446/GR#7: every structural rejection is a registry-sourced, trapped
 * error (MET-V850) — never a bare `throw new Error(...)`, never a silently
 * coerced/partial object. Mirrors the codedError convention already used by
 * captureBeforeWipe.ts (MET-V807) and simContext.ts (MET-V800).
 */
function rejectSave(reason: string): never {
  throw codedError('MET-V850', reason);
}

/**
 * BUG-446/AC-3/AC-8: validate one entry of snapshot.buildings against the
 * REAL Building shape (types.ts) — the exact fields buildGameSave/the engine
 * actually write, not an invented schema. Required: id (number), spec
 * (string), x (number), y (number). Optional fields (builtTick, bridgeOver,
 * capacityTier, lastAutoScaleTick), if present, must carry their declared
 * type — a wrong-typed optional is still garbage that would break the
 * reducer downstream, so it is rejected too rather than silently coerced.
 */
function validateBuildingElement(value: unknown, index: number): void {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    rejectSave(`Snapshot buildings[${index}] is not an object`);
  }
  const b = value as Record<string, unknown>;
  if (typeof b.id !== 'number' || !Number.isFinite(b.id)) {
    rejectSave(`Snapshot buildings[${index}] is missing a numeric id`);
  }
  if (typeof b.spec !== 'string' || b.spec.length === 0) {
    rejectSave(`Snapshot buildings[${index}] is missing a spec`);
  }
  if (typeof b.x !== 'number' || !Number.isFinite(b.x) || typeof b.y !== 'number' || !Number.isFinite(b.y)) {
    rejectSave(`Snapshot buildings[${index}] is missing a valid x/y position`);
  }
  if (b.builtTick !== undefined && typeof b.builtTick !== 'number') {
    rejectSave(`Snapshot buildings[${index}] has a wrong-typed builtTick`);
  }
  if (b.bridgeOver !== undefined && typeof b.bridgeOver !== 'string') {
    rejectSave(`Snapshot buildings[${index}] has a wrong-typed bridgeOver`);
  }
  if (b.capacityTier !== undefined && typeof b.capacityTier !== 'number') {
    rejectSave(`Snapshot buildings[${index}] has a wrong-typed capacityTier`);
  }
  if (b.lastAutoScaleTick !== undefined && typeof b.lastAutoScaleTick !== 'number') {
    rejectSave(`Snapshot buildings[${index}] has a wrong-typed lastAutoScaleTick`);
  }
}

/**
 * BUG-446/BUG-577: the structural-validation CORE shared by every entry
 * point that turns an arbitrary parsed-JSON value into a GameSave —
 * File→Open's parseGameSave AND the named-save (Load → Saved cities) path
 * in namedsaves.ts. Extracted so the two callers can never drift (GR#3
 * SSOT): a malformed shape rejects identically everywhere, via the SAME
 * registry-sourced MET-V850 error (GR#1/GR#7), rather than one path
 * validating and the other trusting a bare cast.
 */
function validateGameSaveShape(parsed: unknown): {
  o: Record<string, unknown>;
  savepoint: Savepoint;
  journal: Journal;
} {
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    rejectSave('Save root must be an object');
  }
  const o = parsed as Record<string, unknown>;
  if (o.format !== GAME_SAVE_FORMAT) {
    if (o.format === 'metropolis-debug/1') {
      rejectSave('That file is a debug dump, not a save. Use File → Save As to write a loadable city.');
    }
    rejectSave(`Not a ${GAME_SAVE_FORMAT} save (got ${String(o.format)})`);
  }
  if (typeof o.name !== 'string' || typeof o.savedAt !== 'string' || typeof o.buildVersion !== 'string') {
    rejectSave('Save is missing name, savedAt, or buildVersion');
  }
  const sp = o.savepoint;
  if (!sp || typeof sp !== 'object' || Array.isArray(sp)) {
    rejectSave('Save is missing savepoint');
  }
  const snapshot = (sp as Record<string, unknown>).snapshot;
  if (!snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) {
    rejectSave('Savepoint is missing snapshot');
  }
  const snap = snapshot as Record<string, unknown>;
  if (typeof snap.tick !== 'number' || !Array.isArray(snap.buildings)) {
    rejectSave('Snapshot is missing tick or buildings');
  }
  (snap.buildings as unknown[]).forEach((b, i) => validateBuildingElement(b, i));
  const journal = o.journal;
  if (!journal || typeof journal !== 'object' || Array.isArray(journal)) {
    rejectSave('Save is missing journal');
  }
  const entries = (journal as Record<string, unknown>).entries;
  if (!Array.isArray(entries)) {
    rejectSave('Journal is missing entries');
  }
  return { o, savepoint: sp as Savepoint, journal: { entries: entries as Journal['entries'] } };
}

/**
 * BUG-577: validates an ALREADY-PARSED value (e.g. a named save decoded
 * from localStorage) against the identical structural rules parseGameSave
 * enforces on a File→Open text blob, and returns a fully-verified GameSave.
 * Throws the same registry-sourced MET-V850 on any malformed shape — never
 * returns a partially-valid object. Used by namedsaves.ts's readNamedSave so
 * the "Load → Saved cities" route can no longer skip validation and hand an
 * unchecked object straight to applyLoadedSave (which dereferences
 * save.savepoint.camera outside any try/catch).
 */
export function validateGameSaveObject(parsed: unknown): GameSave {
  const { o, savepoint, journal } = validateGameSaveShape(parsed);
  savepoint.snapshot = sanitizeTreasury(savepoint.snapshot);
  return {
    format: GAME_SAVE_FORMAT,
    name: o.name as string,
    savedAt: o.savedAt as string,
    buildVersion: o.buildVersion as string,
    savepoint,
    journal,
  };
}

/**
 * BUG-446: parses and structurally validates a saved-city JSON blob. On ANY
 * malformed shape — a non-object root, a missing/wrong-typed required field,
 * or a garbage element inside snapshot.buildings — this THROWS a
 * registry-sourced MET-V850 error (GR#1/GR#7) rather than returning a
 * partially-valid or silently-coerced GameSave. The only successful return
 * is `{ ok: true, save }` with a save whose shape has been fully verified.
 */
export function parseGameSave(text: string): ParseGameSaveResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    rejectSave('File is not JSON');
  }
  return { ok: true, save: validateGameSaveObject(parsed) };
}

export function gameSaveText(save: GameSave): string {
  return JSON.stringify(save);
}
