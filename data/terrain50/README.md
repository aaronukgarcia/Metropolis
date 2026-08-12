# OS Terrain 50 — East Kent tiles

Contains OS data © Crown copyright and database right 2026.

- **Product:** OS Terrain 50, ASCII Grid (ESRI `.asc`), 50 m post spacing
- **Source:** Ordnance Survey OpenData Downloads API (`api.os.uk/downloads/v1/products/Terrain50`), release `20260529`, fetched 2026-08-11
- **Licence:** Open Government Licence — see `licence.txt` (full terms: www.ordnancesurvey.co.uk/opendata/licence)
- **Coverage:** all 27 populated 10 km tiles of 100 km grid square **TR** (East Kent: the Dover–Ashford–Dungeness triangle, sea on two sides). The Folkestone start tile is inside **TR23** (`xllcorner 620000, yllcorner 130000`).
- **Consumed by:** `internal/engine/world/terrain_import.go` (`ParseAsciiGrid`) — heightmap generated at build time, downsampled to the 10 m cell grid (master doc §2).

Only the `.asc` grids are vendored, not the full GB product (154 MB) — these 27 tiles (~5 MB) are the project's entire geographic extent (§2.3).
