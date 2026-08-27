// profile.ts — FEAT-1972079866: per-object economic profile helper.
//
// Pure, deterministic derivation of a building's full economic profile FROM ITS
// SPEC (data.ts `SPECS[b.spec]`) plus, optionally, the current tax rates so the
// council/business-tax contributions can be shown as real numbers. NO Date.now,
// NO Math.random, NO engine/reducer/state mutation — display-only.
//
// The card (BuildingCard in MapView.tsx) renders straight from the structured
// result; keeping the logic here (a .ts module with no JSX) makes it importable
// by a plain node --test .mjs suite via Node's type-stripping.
//
// Cash-generation policy (Aaron's ask):
//   - Derive a per-building number ONLY where an EXISTING fiscal.ts helper
//     exposes a clean per-building contribution:
//       · residential `residents` -> councilTaxPerTick(residents, rate.residential)
//         (the council tax this dwelling drives at full occupancy)
//       · commercial            -> businessTaxPerTick(1, rate.commercial)
//         (business tax is charged per commercial ZONE, so one zone = one call)
//   - Everything else that drives revenue but has NO clean per-building helper
//     (office tax, freight tax, tourism income — all computed inline in the
//     engine, not in fiscal.ts) is shown as a CAPABILITY with value === null.
//     We never invent a number.

import type { Spec } from './data.ts';
import { SPECS } from './data.ts';
import type { TaxRates, Building } from './types.ts';
import { councilTaxPerTick, businessTaxPerTick } from './fiscal.ts';

// ── CLASS-TYPE NUMBER (Aaron's ask) ──────────────────────────────────────────
// Every building TYPE (spec) gets a stable, deterministic "class number":
// the index of the spec id in the FIXED insertion ordering of SPECS. Object key
// order in JS is insertion order for string keys, and data.ts declares SPECS as
// one object literal, so this ordering is fixed at module load and identical on
// every run — no Date, no Math.random, no runtime state. Same spec → same number
// forever; add a spec at the END and existing numbers are untouched.
//
// Displayed as NNNN.L where NNNN is that index zero-padded to 4 digits and L is
// the spec's LEVEL — the `unlock` field (the P(...) positional the existing gate
// reads as `sp.unlock`). Example: an spec at index 323 unlocking at level 5 →
// "0323.5". This is per-TYPE and distinct from the per-INSTANCE `#id` ref.
const SPEC_ORDER: readonly string[] = Object.keys(SPECS);

/**
 * Stable, deterministic class number for a spec: its index in the fixed SPECS
 * ordering. Unknown ids (not in SPECS) return -1 so callers can spot drift.
 */
export function specClass(spec: Pick<Spec, 'id'>): number {
  return SPEC_ORDER.indexOf(spec.id);
}

/**
 * The class-type label `NNNN.L`: the class number zero-padded to 4 digits, a
 * dot, and the spec's level (`unlock`). Deterministic — same spec → same label.
 * An unknown id (class -1) pads to "0000" so the label is always well-formed.
 */
export function specClassLabel(spec: Spec): string {
  const idx = specClass(spec);
  const num = idx < 0 ? 0 : idx;
  return `${String(num).padStart(4, '0')}.${spec.unlock}`;
}

/** How the card should format a line's numeric value. */
export type ProfileUnit = 'power' | 'count' | 'moneyPerTick';

export interface ProfileLine {
  /** Stable machine key (e.g. 'power', 'jobs', 'councilTax') — test-friendly. */
  key: string;
  /** Human label for the card (e.g. 'Power', 'Residents'). */
  label: string;
  /**
   * Raw numeric magnitude, or null when this is a capability we can name but
   * cannot honestly attach a per-building number to (revenue with no clean
   * fiscal helper). null lines render as "<label> — <note>" with no figure.
   */
  value: number | null;
  /** Formatting hint for the card; ignored when value === null. */
  unit: ProfileUnit;
  /** Optional clarifier shown in muted text. */
  note?: string;
}

export interface BuildingProfile {
  /** Build cost (spec.cost). */
  capex: number;
  /** Upkeep per tick (spec.upkeep). */
  opex: number;
  /** Inputs the building consumes: power draw, water, workers. */
  requires: ProfileLine[];
  /** Outputs the building generates: jobs, plant power, capacity, tourism, revenue. */
  produces: ProfileLine[];
}

/**
 * Read an optional numeric field that may not (yet) exist on the Spec interface
 * without editing data.ts. Water draw is not currently a spec field, but if one
 * is ever added the profile picks it up automatically under REQUIRES.
 */
function optNum(spec: Spec, field: string): number {
  const v = (spec as unknown as Record<string, unknown>)[field];
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

/**
 * Build the structured economic profile for a spec.
 *
 * @param spec     the building's catalogue spec (SPECS[b.spec]).
 * @param taxRates current tax rates; when omitted, tax-derived revenue lines
 *                 degrade to capability labels (value === null) instead of numbers.
 */
export function buildingProfile(spec: Spec, taxRates?: TaxRates): BuildingProfile {
  const requires: ProfileLine[] = [];
  const produces: ProfileLine[] = [];

  const isPlant = spec.kind === 'power';
  const mw = spec.mw ?? 0;
  const water = optNum(spec, 'water');

  // ── POWER ────────────────────────────────────────────────────────────────
  // Plants PRODUCE their `mw`; any non-plant carrying an mw draw REQUIRES it.
  if (mw > 0) {
    if (isPlant) {
      produces.push({ key: 'power', label: 'Power', value: mw, unit: 'power', note: 'generation' });
    } else {
      requires.push({ key: 'power', label: 'Power', value: mw, unit: 'power', note: 'draw' });
    }
  }

  // ── WATER (input) ──────────────────────────────────────────────────────────
  if (water > 0) {
    requires.push({ key: 'water', label: 'Water', value: water, unit: 'count', note: 'draw' });
  }

  // ── JOBS ────────────────────────────────────────────────────────────────────
  // A workplace PRODUCES job slots and REQUIRES workers to staff them (same N).
  const jobs = spec.jobs ?? 0;
  if (jobs > 0) {
    produces.push({ key: 'jobs', label: 'Jobs', value: jobs, unit: 'count', note: 'workplaces offered' });
    requires.push({ key: 'workers', label: 'Workers', value: jobs, unit: 'count', note: 'to staff jobs' });
  }

  // ── RESIDENTS / CHILDREN / SERVED / TOURISM (outputs) ────────────────────────
  const residents = spec.residents ?? 0;
  if (residents > 0) {
    produces.push({ key: 'residents', label: 'Residents', value: residents, unit: 'count', note: 'housing capacity' });
  }
  const children = spec.children ?? 0;
  if (children > 0) {
    produces.push({ key: 'children', label: 'School places', value: children, unit: 'count' });
  }
  const served = spec.served ?? 0;
  if (served > 0) {
    produces.push({ key: 'served', label: 'Capacity served', value: served, unit: 'count' });
  }
  const tourism = spec.tourism ?? 0;
  if (tourism > 0) {
    produces.push({ key: 'tourism', label: 'Tourism draw', value: tourism, unit: 'count' });
  }

  // ── CASH GENERATION (revenue) ────────────────────────────────────────────────
  // Only residential council tax and commercial business tax have clean
  // per-building fiscal helpers. All other revenue is a labelled capability.
  if (residents > 0) {
    // Council tax this dwelling drives at full occupancy (residents = population).
    const value = taxRates ? councilTaxPerTick(residents, taxRates.residential) : null;
    produces.push({
      key: 'councilTax',
      label: 'Council tax',
      value,
      unit: 'moneyPerTick',
      note: value === null ? 'drives council tax' : 'at full occupancy',
    });
  } else if (spec.kind === 'commercial') {
    // Business tax is charged per commercial zone → one building = one zone.
    const value = taxRates ? businessTaxPerTick(1, taxRates.commercial) : null;
    produces.push({
      key: 'businessTax',
      label: 'Business tax',
      value,
      unit: 'moneyPerTick',
      note: value === null ? 'drives business tax' : 'per zone',
    });
  } else if (spec.kind === 'office') {
    // Office tax is engine-inline (no fiscal helper) → capability only.
    produces.push({ key: 'officeTax', label: 'Office tax', value: null, unit: 'moneyPerTick', note: 'drives office tax' });
  } else if (spec.kind === 'industrial' || spec.kind === 'mine') {
    // Freight tax is engine-inline (no fiscal helper) → capability only.
    produces.push({ key: 'freightTax', label: 'Freight tax', value: null, unit: 'moneyPerTick', note: 'drives freight tax' });
  } else if (tourism > 0) {
    // Tourism income is engine-inline (no fiscal helper) → capability only.
    produces.push({ key: 'tourismIncome', label: 'Tourism income', value: null, unit: 'moneyPerTick', note: 'drives tourism revenue' });
  }

  return { capex: spec.cost, opex: spec.upkeep, requires, produces };
}

// ── COPY-AS-JSON PAYLOAD (Aaron's ask) ───────────────────────────────────────
// The info panel's Copy button serialises this so Aaron can paste a building's
// full data into chat. Factored out as a pure function (no clipboard, no React)
// so a node --test can assert its shape without a DOM.

/** Everything the Copy button writes to the clipboard, before JSON.stringify. */
export interface BuildingCopyPayload {
  /** Per-instance id (the #ref). */
  id: number;
  /** Spec/type id (SPECS key). */
  spec: string;
  /** Class-type label, NNNN.L (per-type number + level). */
  class: string;
  /** Building level = spec.unlock. */
  level: number;
  /** Full economic profile: capex/opex/requires/produces. */
  profile: BuildingProfile;
}

/**
 * Build the copy-as-JSON payload for a selected building. Pure and deterministic
 * over (building, spec, taxRates); mirrors exactly what the panel's Copy button
 * hands to navigator.clipboard.
 */
export function buildingCopyPayload(
  building: Pick<Building, 'id' | 'spec'>,
  spec: Spec,
  taxRates?: TaxRates,
): BuildingCopyPayload {
  return {
    id: building.id,
    spec: spec.id,
    class: specClassLabel(spec),
    level: spec.unlock,
    profile: buildingProfile(spec, taxRates),
  };
}
