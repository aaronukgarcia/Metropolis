# Property-card art (FEAT-2326609841)

Provenance and rules for the per-spec picture plate of the property card.

- **Guaranteed plate:** `src/components/buildingMotifs.tsx` generates a deterministic,
  hand-composed cartoon SVG per building kind (roofs, windows, towers, smoke, trees),
  tinted from the spec's own colour and scaled from its footprint. Authored in-repo by
  the FEAT-2326609841 lane (2026-10-31); no external assets, no licence encumbrance.
  It is the fallback and never disappears.
- **Art slot:** drop `<specId>.svg` in this folder, then add the spec id to `LANDED_ART`
  in `buildingMotifs.tsx`. The art is layered over the motif; if it fails to load it hides
  itself and the motif shows.
- **Approval:** Aaron's ruling 2026-10-31 - merged source (motifs first, generated sprite
  set lands per spec as it arrives) is AUTO-APPROVED, no style sign-off gate.
- **Provenance line for every landed file:** append `<specId> | generator | date |
  prompt/seed reference` below. No file may be landed without its line.
- **Status:** no generated sprites landed yet (the AI sprite set is a follow-up batch;
  nothing here was produced by an image model).

## Landed files

(none)
