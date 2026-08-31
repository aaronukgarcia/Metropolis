import type { SimState } from './types.ts';
import type { Journal } from './journal.ts';
import type { Savepoint } from './replay.ts';
import type { MapViewState } from './uistate.ts';
import { createSavepoint } from './replay.ts';
import { emptyJournal } from './journal.ts';
import { gameDate } from './utils.ts';
import { sanitizeTreasury } from './engine.ts';

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

export function parseGameSave(text: string): ParseGameSaveResult {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { ok: false, reason: 'File is not JSON' };
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, reason: 'Save root must be an object' };
  }
  const o = parsed as Record<string, unknown>;
  if (o.format !== GAME_SAVE_FORMAT) {
    if (o.format === 'metropolis-debug/1') {
      return { ok: false, reason: 'That file is a debug dump, not a save. Use File → Save As to write a loadable city.' };
    }
    return { ok: false, reason: `Not a ${GAME_SAVE_FORMAT} save (got ${String(o.format)})` };
  }
  if (typeof o.name !== 'string' || typeof o.savedAt !== 'string' || typeof o.buildVersion !== 'string') {
    return { ok: false, reason: 'Save is missing name, savedAt, or buildVersion' };
  }
  const sp = o.savepoint;
  if (!sp || typeof sp !== 'object' || Array.isArray(sp)) {
    return { ok: false, reason: 'Save is missing savepoint' };
  }
  const snapshot = (sp as Record<string, unknown>).snapshot;
  if (!snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) {
    return { ok: false, reason: 'Savepoint is missing snapshot' };
  }
  const snap = snapshot as Record<string, unknown>;
  if (typeof snap.tick !== 'number' || !Array.isArray(snap.buildings)) {
    return { ok: false, reason: 'Snapshot is missing tick or buildings' };
  }
  const journal = o.journal;
  if (!journal || typeof journal !== 'object' || Array.isArray(journal)) {
    return { ok: false, reason: 'Save is missing journal' };
  }
  const entries = (journal as Record<string, unknown>).entries;
  if (!Array.isArray(entries)) {
    return { ok: false, reason: 'Journal is missing entries' };
  }
  const savepoint = sp as Savepoint;
  savepoint.snapshot = sanitizeTreasury(savepoint.snapshot);
  return {
    ok: true,
    save: {
      format: GAME_SAVE_FORMAT,
      name: o.name,
      savedAt: o.savedAt,
      buildVersion: o.buildVersion,
      savepoint,
      journal: { entries: entries as Journal['entries'] },
    },
  };
}

export function gameSaveText(save: GameSave): string {
  return JSON.stringify(save);
}
