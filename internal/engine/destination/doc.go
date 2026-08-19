// Package destination is the destination leisure & retail module
// (engine.destination, MOD-061): the two buildable regional-draw archetypes
// of §48 — the forest holiday resort and the mega-mall — and their named
// characteristics, exposed as a stateful [DestAPI] whose balance figures are
// loaded from data/destination.json.
//
// Module key: engine.destination (see code.json; inbound GUID
// 3d6b9b6a-d30c-49ea-a32d-0dfc70ae4be1 "DestinationAPI", outbound GUID
// af2de6ff-09ef-453c-b2a8-880326060b67). Spec refs: §48 (Destination
// Leisure & Retail); §32 (Mining, Extraction & the Blight Model —
// the mega-mall's reclamation-site eligibility and the general viewshed
// blight mechanic); §38 (Parking — the mega-mall's colossal parking demand).
//
// # The two archetypes (§48, AC-2)
//
// The forest holiday resort and the mega-mall are two DISTINCT exported
// archetypes, never one generic "destination" building with cosmetic
// variants. Each carries its spec-named characteristics as data-driven
// fields loaded from data/destination.json (GR#15), not Go literals: the
// resort's job count, its minimum woodland footprint, and its
// year-round staying-visitor draw shape; the mega-mall's job count, its
// shop-equivalent floorspace minimum, and its colossal parking demand.
// The named magnitudes themselves live only in the data file.
//
// # Shared regional-draw machinery (AC-1, ASM-326)
//
// The regional-draw computation is built directly atop [TourismAPI]'s
// decomposed portfolio-score machinery through the registered
// engine.destination → engine.tourism edge ([TourismDraw]), never a
// parallel scoring formula (GR#3). [DestAPI.RegionalDraw] multiplies the
// tourism portfolio score by the destination's own data-driven draw
// factor; the forest resort's factor additionally carries the §48 BDI
// synergy ("the resort *wants* your nature", AC-5).
//
// # The mega-mall's three §48 mechanics (AC-3/AC-4/AC-9)
//
//   - Reclamation-site eligibility (AC-3): a mega-mall is legal only on a
//     site the registered engine.mining blight model ([MiningBlight],
//     the engine.destination → engine.mining edge) reports as an exhausted,
//     not-yet-reclaimed extraction pit (§32). A non-reclamation site is
//     rejected, never accepted with a cosmetic "reclaimed quarry" skin.
//   - Viewshed screening (AC-4): the mega-mall registers as a blighting
//     object through the same [MiningBlight] surface; applying screening
//     adds a data-height wall bund into mining's own line-of-sight occluder
//     model (§32), so a screened mall measurably reduces the viewshed-blight
//     (SEEN) contribution to neighbouring cells — never a
//     destination-specific exemption from the general mechanic.
//   - Colossal parking (AC-9): placing a mega-mall pushes its data-driven
//     parking-space demand through the registered engine.destination →
//     engine.parking edge ([ParkingSink]).
//
// # Blocked mechanics (BUG-058)
//
// Three §48 mechanics remain blocked pending BUG-058 findings, and this
// package deliberately does NOT import engine.traffic, engine.shopping,
// engine.cafe, or engine.logistics (AC-12):
//
//   - AC-6 "bus/rail links required" — no engine.destination →
//     engine.traffic edge is registered (BUG-058 finding #10).
//   - AC-7 the town-centre-vs-mall pressure on high-street retail and café
//     vitality — no engine.destination → engine.shopping / engine.cafe
//     edges are registered (BUG-058 findings #11/#12).
//   - AC-8 "daily stock logistics in hundreds of tonnes" — no
//     engine.destination → engine.logistics edge is registered (BUG-058
//     finding #14).
//
// # Determinism (GR#21)
//
// Every computation (regional draw, viewshed read, parking demand) is a
// pure function of loaded data and current sim state. Nothing reads the
// wall clock and there is no unseeded randomness on any path; the only map
// the API holds ([DestAPI]'s destination registry) is keyed-lookup only,
// with any enumeration sorted by id.
package destination
