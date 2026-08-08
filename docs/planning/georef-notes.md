# Georeference notes — FEAT-009 (start tile + expansion extent)

Spec refs: V.2.5; §2.1 (start tile); §2.3 (expansion). Companion data file: `data/georef.json`.

**Status: first-pass research pin, not surveyed.** Everything below is derived from public web sources (Wikipedia, OS's own docs.os.uk pages, and third-party grid-reference lookups), not from an actual downloaded OS Terrain 50 tile or an authoritative lat/lon→BNG converter. Confidence levels are called out per claim. Treat the coordinates as a strong starting point for `tools/terrain/` to load and verify, not as ground truth.

---

## 1. OS Terrain 50 — product facts

- **Tiling:** distributed as 10 km × 10 km tiles, each named by the National Grid reference of its **south-west corner** (e.g. tile `TR23` = SW corner easting 620000, northing 130000). *(Confidence: high — [OS Terrain 50 Technical Specification](https://docs.os.uk/os-downloads/height-and-imagery/os-terrain-50/os-terrain-50-technical-specification), [Grid data](https://docs.os.uk/os-downloads/products/land-and-terrain-portfolio/os-terrain-50/os-terrain-50-technical-specification/grid-data))*
- **Native resolution:** 50 m post spacing, 200×200 points per 10 km tile. Metropolis downsamples/interpolates this to its own 10 m, 200×200-cells-per-2km-tile grid at build time. *(High confidence — same source.)*
- **Formats:** ESRI ASCII grid (`.asc`) and GML 3.2.1, each tile shipped with a `.prj`, an `.asc.aux.xml`, and a `Metadata_<tile>.xml`. *(High confidence — same source.)*
- **Download source:** OS Data Hub, `https://osdatahub.os.uk/downloads/open/Terrain50`. Free, OGL-licensed OS OpenData. *(Medium-high confidence — [OS Terrain 50 Overview](https://docs.os.uk/os-downloads/products/land-and-terrain-portfolio/os-terrain-50/os-terrain-50-overview) states the Data Hub download path and "free to view, download and use for commercial, educational and personal purposes," but I could not fetch the product's own licence.txt to confirm the exact OGL variant string.)*
- **Attribution:** the standard OS OpenData / OGL statement is **"Contains OS data © Crown copyright and database right [YEAR]."** *(Medium confidence — this is OS's generic published OGL attribution wording for OpenData products found via their public licensing pages; I have not confirmed it's word-for-word what ships inside an actual Terrain 50 download. Verify against the bundled licence file before shipping any exported map that carries this string.)*

---

## 2. Start tile — Folkestone West to Sandgate/Seabrook

### Chosen square

```
CRS:  EPSG:27700 (British National Grid)
SW:   (620000, 135000)
NE:   (622000, 137000)
Size: 2000 m x 2000 m, snapped to 1 km grid lines
OS Terrain 50 tile: TR23 (single tile — the whole start tile lives inside one 10km square)
```

### Evidence used to place it

| Landmark | Grid ref (source) | BNG (E, N) | Role | Confidence |
|---|---|---|---|---|
| Sandgate seafront/promenade | `TR 20504 35139` (slipway record) | (620500, 135140) | south shoreline anchor | medium |
| Seabrook | `TR1859534983` | (618595, 134983) | named shoreline place in spec text — **falls ~1.4 km WEST of this tile**, not inside it | high (as a source ref) |
| Folkestone West station | `TR209364` (6-fig) | (620900, 136400) | namesake for "Folkestone West"; central in the tile | medium |
| M20 J13 / Castle Hill Interchange | WGS84 51.095285, 1.156569 → linearly interpolated to BNG against the station anchor | (621100, 137500), ±~200 m | the one grade-separated junction wanted in-tile | **low-medium** |
| Cheriton Hill (escarpment) | `TR197396` | (619700, 139600) | true escarpment high ground | high (as a source ref), but placement relative to tile is a compromise (see below) |

Sources: [Folkestone West station grid ref via web search of station records](https://uktransport.fandom.com/wiki/Folkestone_West_railway_station); [Sandgate/Seabrook grid refs](https://democracy.kent.gov.uk/documents/s48111/Item%2005%20Appendix%20A1.pdf) and [Kent Heritage monument record](https://heritage.kent.gov.uk/Monument/MWX44019); [M20 Junction 13 / Castle Hill Interchange](https://www.roads.org.uk/motorway/m20/140); [Cheriton Hill Wikipedia](https://en.wikipedia.org/wiki/Cheriton_Hill).

### Honest problem: the real distances don't fit in 2 km

Measured from the sources above:
- Shore (~N135000-135200) to Folkestone West station (N136400): ~1.2-1.3 km — **fits** comfortably.
- Shore to M20 J13 (N~137500 estimated): ~2.3-2.5 km — **does not fit** inside a 2 km span.
- Shore to the actual escarpment crest, Cheriton Hill (N139600): ~4.4 km — **does not fit** by more than double.

The design doc (§2.1) wants, north to south, in one 2 km tile: escarpment rising past 120 m → M20/A20 with one junction in the upper third → buildable shelf → shore. That is not a literal crop of this real location — the true escarpment crest sits well over 2 km inland of the coast here, with the motorway junction itself already outside a straightforward 2 km strip if the strip's south edge is anchored on the beach.

**Compromise chosen for `data/georef.json`:** anchor the south edge on the shoreline (N=135000) and the north edge 2 km further at N=137000. This:
- keeps the shore in the tile (south edge, as required) — high confidence;
- keeps Folkestone West station centrally placed — medium confidence, good fit;
- puts J13 AT OR JUST PAST the north edge (best estimate 137500 vs. edge at 137000) — this is the main risk, flagged as an open question, not silently assumed to work;
- can only capture the *start* of the rising scarp slope near the north edge, not the escarpment ridge/summit itself (which is ~2.6 km further north than this tile's edge).

**Flag for Bill/Aaron:** decide whether to (a) accept this as an intentionally compressed/stylised real-ground tile — elevations and features present but real-world distances foreshortened, which is normal for a city-builder; (b) shift the tile ~0.5-1 km north to more safely land J13 inside it, accepting a thinner shoreline strip at the south edge; or (c) treat the escarpment crest as explicitly out-of-tile content that only appears once the player expands north, and read §2.1's "north edge: escarpment... rising past 120m" as describing the *start* of the rise, not the summit — which is the reading this pin currently assumes.

### ASCII sketch of the chosen tile (not to scale, north up)

```
   N=137000  +--------------------------------------------------+
             | ~~ rising ground / start of chalk scarp slope ~~ |  <- north edge: J13 estimated
             | ~~~~~~~ (escarpment CREST is ~2.6km further  ~~~ |     to sit AT/JUST PAST here
             | ~~~~~~~ north — NOT inside this tile)        ~~~ |     (open question)
             |                                                  |
             |     M20/A20 =====[J13?]=====================     |  <- upper third: motorway
             |     ...........A20 local road..................  |     + one grade-sep junction
             |                                                  |
             |          o Folkestone West station               |  <- central: namesake landmark
             |                                                  |
             |     ################################             |  <- middle: flat/gentle
             |     ####### buildable shelf ########             |     buildable shelf
             |     ################################             |
             |                                                  |
             | ,.,.,.,.,.,.,. shingle/sand shore ,.,.,.,.,.,.,.  |  <- south edge: shore
   N=135000  +--------------------------------------------------+
           E=620000                                          E=622000
                        ~~~ sea (south of tile) ~~~
```

---

## 3. Expansion extent — East Kent, ~60×60 km

### Chosen box

```
CRS:  EPSG:27700
SW:   (590000, 110000)
NE:   (650000, 170000)
Size: 60 km x 60 km, snapped to 10 km grid lines
```

### Evidence used to place it

| Landmark | Grid ref (source) | BNG (E, N) |
|---|---|---|
| Ashford | `TR005425` | (600500, 142500) |
| Dover | `TR315415` | (631500, 141500) |
| Dungeness | `TR0917` (4-fig, ~1km precision) | (609000, 117000) |
| Folkestone start tile | — | (620000-622000, 135000-137000) |
| Canterbury (context) | ~`TR1495 7580` | (614950, 157580) — low confidence, approximate |
| Margate/Thanet (context) | ~`TR35xx 71xxx` | (637000, 171000) — low confidence, approximate; **falls just outside the box's north edge (170000)** |

Sources: [Ashford grid ref](https://www.genuki.org.uk/big/eng/KEN/Ashford)/[Wikidata](https://www.wikidata.org/wiki/Q725261); [Dover, Dungeness grid refs via web search of place records](https://en.wikipedia.org/wiki/Dover), [Dungeness](https://en.wikipedia.org/wiki/Dungeness).

The box comfortably contains Ashford, Dover, Dungeness, and the whole start tile. South edge sits ~7 km south of Dungeness Point, into the Channel — matches "sea on two sides" for the south. East edge sits well offshore past Dover, with Deal/Sandwich/Ramsgate land clipped within the box's eastern band rather than the box ending exactly on the coast — also consistent with a sea-bounded east side, though imprecisely.

**Flag:** Margate/Thanet's approximate position falls just past the box's north edge (170000 vs. ~171000 estimated). If full Thanet coverage is wanted, extend north (e.g. to 180000, making the box 60×70 km) — flagged as an open question rather than silently fixed, since I have no confirmed grid reference for Margate, only a rough estimate.

### OS Terrain 50 10 km tiles intersected

6 rows × 6 columns = **36 tiles total** (60 km / 10 km = 6 per side — this count is exact by construction, unlike the land/sea split below):

```
TQ91 TQ92 TQ93 TQ94 TQ95 TQ96
TR01 TR02 TR03 TR04 TR05 TR06
TR11 TR12 TR13 TR14 TR15 TR16
TR21 TR22 TR23 TR24 TR25 TR26
TR31 TR32 TR33 TR34 TR35 TR36
TR41 TR42 TR43 TR44 TR45 TR46
```

(`TR23`, the start tile's square, is inside this set as expected.)

**On-land count: NOT verified.** I estimated "roughly 24-28 of the 36" from general knowledge of the Kent coastline (Dungeness/Denge Marsh land in the south, Channel sea filling much of the southern and eastern rows, Thanet/Canterbury land filling the north), but this is a judgement call, not a coastline-dataset intersection. **Low confidence — do not treat as a real count.** The terrain importer (`tools/terrain/`) should compute the true land/sea split per tile programmatically when it actually downloads and clips these tiles; that result should replace this estimate.

---

## 4. Summary of confidence levels

| Item | Confidence |
|---|---|
| OS Terrain 50 tiling/format/download source | high |
| OGL attribution exact wording | medium (generic OGL text, not confirmed against an actual Terrain 50 licence file) |
| Start tile SW/NE corners | medium |
| Shore inclusion (south edge) | medium-high |
| Folkestone West station inside tile | medium |
| M20 J13 inside tile | **low-medium — may sit just outside the north edge** |
| Escarpment crest inside tile | n/a — known NOT to be inside; only the start of the rise is claimed |
| Expansion box corners | medium |
| Ashford/Dover/Dungeness inside box | high |
| Margate/Thanet inside box | low — likely just outside north edge |
| 10 km tile list (36 total) | high (exact by construction) |
| On-land tile count within that list | low — unverified estimate only |

## 5. Open questions for Bill/Aaron

See the `openQuestions` array in `data/georef.json` for the authoritative list (kept in sync with this section):

1. Does M20 J13 actually land inside the chosen start tile, or just outside it? Needs a real BNG conversion or an actual OS Open Roads/Terrain 50 lookup for TR23.
2. Accept the tile as a compressed/stylised real-ground crop, or nudge it ~0.5-1 km north (losing shoreline depth) to better anchor J13?
3. Is "Seabrook" a literal inclusion requirement (tile would need to shift west, likely losing J13 instead) or just a loose place-name for this stretch of coast?
4. Should the expansion box extend further north to guarantee Thanet/Margate coverage?
5. Should the on-land tile count for the expansion box be computed for real before this is treated as final?
6. Should the exact OGL attribution string be re-confirmed against an actual downloaded Terrain 50 licence file?
