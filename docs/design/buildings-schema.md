# `data/buildings.json` schema reference

**BOW code:** FEAT-010 (data.catalogue)
**Spec refs:** Part IV — Appendix A (Building & Object Catalogue); Catalogue Supplement / Supplement 2 / Supplement 3; §17 (Resource Consumption Model); §21 (housing appeal profiles); §23 (Expansion-Content Mapping); §32 (blight model); GR#15.
**Go types:** `internal/foundation/data/buildings.go` (`Buildings`, `BuildingEntry`, `UnlockGate`)
**Loaders:** `LoadBuildings` (schema-only), `LoadBuildingsCatalogue` (schema + §17 cross-check), both used inside `LoadAll`.

This document exists so a later data-editor (M2 Batch tuning) can edit `data/buildings.json` without reverse-engineering the loader. It lists every field this item introduces, per `data.catalogue.md` AC-16.

## Top level

```json
{
  "$comment": "...",
  "version": 1,
  "entries": [ ... ]
}
```

- `$comment` — free text citing Part IV/the supplements (AC-15). Not read by the loader; documentation only.
- `version` — must be a positive integer (shared config-file convention, `requireVersion`).
- `entries` — array of `BuildingEntry` (see below).

## `BuildingEntry` fields

| Field | Type | Required | Domain / notes |
|---|---|---|---|
| `id` | string | yes | `^[a-z][a-z0-9_.-]{2,63}$`. Globally unique (load-time rejection on duplicate, AC-10b). Stable — used as a foreign key from `unlock_trees.json`, save files, and future engine lookups. |
| `name` | string | yes | Display name, transcribed from the spec's "Object"/"Type" column. |
| `catalogueSection` | string | yes | Section code the entry belongs to. Base Part IV sections: `R`, `E`, `W`, `H`, `ED`, `F-P`, `G`, `PK`, `L`, `T`, `PT`, `HS`, `C-I`, `LM`, `CM-WF`. Supplement group codes: `MP`, `SEC`, `FARM`, `MINE`, `COMMS` (Supplement 1), `SUP2`, `SUP3`. |
| `supplement` | string | no (default `""`) | `""` (base Part IV entry), `"S1"`, `"S2"`, or `"S3"` (AC-3). |
| `supplementCategory` | string | required when `supplement` is set | The supplement's own named category, e.g. `"mega-projects"`, `"security-justice"`, `"farming-food"`, `"mining"`, `"comms-logistics"`, `"roads-v2"`, `"policies-v2"`, `"civic"`, `"business-finance"`, `"destination"`, `"fdi-anchors"`, `"fuel-chemicals"`, `"rail"`, `"tunnelling"`, `"health-splits"`, `"defence"`. |
| `sourcePack` | string | no (default `""`) | One of the six non-cosmetic §23 'Blue'-pack-equivalent groups: `waterfront-transport`, `coastal-shoreline`, `high-rise`, `high-street-retail`, `rail-stations`, `office-tiers` (AC-9). `"regional-cosmetic"` has no tag — §23 states it is cosmetic-only and skipped. |
| `unlock` | object | yes | See `UnlockGate` below (AC-4). |
| `costRaw` | string | no | The spec table's literal Cost/Cost-per-km column text, verbatim (e.g. `"250k"`, `"2.2M"`, `"80k-8M"`, `"opex"`). Empty only for the handful of supplement flat-list entries the spec gives no cost for (each such gap is logged as a BOW `ASM-` assumption). |
| `capacityRaw` | string | no | The spec table's literal Output/Cap/Density column text, verbatim. |
| `consumptionRef` | string | no | Key into `data/consumption.json`'s `classes` map (§17: the catalogue never hard-codes a utility number). Domain: `^[a-zA-Z][a-zA-Z0-9]{1,63}$` (camelCase, matching `consumption.json`'s existing key convention). Existence against a loaded `consumption.json` is checked by `ValidateConsumptionRefs` / `LoadBuildingsCatalogue`, not by `Buildings.Validate()` alone (that check needs a second file). Empty for HS housing entries (they draw against `consumption.json`'s `residential` baseline, not a `classes` entry) and for E/W section utility-infrastructure objects (they produce/store/distribute utilities rather than consuming against a class coefficient). |
| `blightClass` | string | yes (default `"none"`) | One of `none` / `low` / `medium` / `high` / `max` (AC-7). Assigned per the spec's own qualitative language where it names one (e.g. toxic/hazardous waste processing = `"max"`, refinery = `"max"` per §50's "top blight class", heavy industry estate = `"medium"`); `"none"` everywhere the spec is silent. |
| `appealProfile` | array of strings | required non-empty for `catalogueSection == "HS"` | Slug tags (`^[a-z][a-z0-9-]{1,31}$`) capturing the HS table's "Profile sketch" column (AC-8), e.g. `["garden-water-plus", "wealth"]`. Empty for non-HS entries unless the spec explicitly gives one (e.g. Supplement 3's social-housing / married-quarters typologies). |
| `notes` | string | no | Free-text notes column content, plus any ASM/assumption pointer for entries where the spec itself left a gap (flat comma-lists with no stated cost/unlock, one-row families split into tiers). |

## `UnlockGate` fields

| Field | Type | Notes |
|---|---|---|
| `raw` | string | The spec's literal Unlock-column text, verbatim (e.g. `"M5+DP"`, `"M9+ach"`, `"with sources"`, `"first 100 deaths"`). Always present. |
| `milestone` | string | Parsed `Mn` tier (`M1`-`M13`) when `raw` contains a clean `Mn` token. Empty when the gate isn't milestone-shaped (see `conditional`). |
| `developmentPoint` | bool | `raw` contains `+DP`. |
| `achievement` | bool | `raw` contains `+ach`/`ach`. |
| `university` | bool | `raw` contains `+univ`. |
| `funds` | bool | `raw` contains `+£`. |
| `research` | bool | `raw` mentions "research". |
| `policy` | bool | `raw` mentions "policy". |
| `conditional` | string | Set to the full `raw` text when no `Mn` milestone token was found (e.g. `"with sources"`, `"first 100 deaths"`, `"adult-ed upgrade"`) — the gate is a named condition rather than (or in addition to) a milestone tier. |

## Known gaps / modelling decisions

Every judgment call the spec's own inconsistent table shapes forced is logged as a BOW `ASM-` item against `data.catalogue`/`foundation.data` — see the FEAT-010 delivery report for the full list (row-family splits, e.g. R's junction-controls row and the various N-tier `X → Y → Z` chains; flat comma-list supplement sections with no stated per-item unlock/cost; the interpolated `sewage_works_medium` tier).
