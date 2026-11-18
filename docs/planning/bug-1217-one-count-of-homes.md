# BUG-1217: one count of homes (options paper)

Author: Designer/BA (Sonnet), 2026-11-18. Docs only. For Aaron to rule on.
Facts come from the code and the BUG-1214 evidence on main at a7d7ded. Nothing was run.

## In one paragraph

The engine counts homes three times, in three places, and they never meet. A player who opens a starting citizen sees "no fixed address". The finance and migration code say the city has free homes. The housing-cost code says there are a million homes of every type. I recommend option A in two steps: first the cheap fix that makes every starting citizen live in a real home, then the move of the other two counts onto that one stock.

## 1. The facts

| | Count 1: citizens housing ledger | Count 2: households stock | Count 3: vacancy rule (BUG-1214) |
|---|---|---|---|
| Lives in | `internal/engine/citizens/housing.go`: `SetHomeCapacities`, `AssignHomes`, `HomeOccupancy`, `HomelessCount`. Fed by `compose_housing.go: syncHousing` once a day | `compose.go` ~line 1280: `ReportStock` called once at wiring, `Count = baselineOneHousingVacancy` (1,000,000) for every typology | `compose_vacancy.go: housingVacancyFor`, called from `compose.go: applyMigration` (~line 3358) once a month |
| What it counts | Real standing dwellings, by map cell (`dwelling` zone, 8 places each in `data/buildings.json`). Each citizen has a `Home` cell, 0 meaning none | Dwelling units per housing typology. A typology is a kind of home | `120` seed places plus the resident capacity of every standing building, minus total population, divided by 2 (people per migrant household) |
| Who reads it | The citizen inspector ("no fixed address" when `Home` is 0, `citizen_query.go`), the census "Homeless" KPI, occupant lists | `households.UnhousedByPreference` and `HousingAffordability`, which feeds attract's housing term | Attract's `HousingVacancy`, the cap on how many migrants arrive |
| What the player sees | Every starting citizen homeless. A built dwelling takes in the homeless, 8 at a time, lowest id first | Nothing directly. Affordability never reflects a shortage, however full the city is | Population stops near 120 and rises when dwellings are built (control 148 at month 12, six dwellings 190) |
| Seed numbers | Tick 0: 64 citizens, 0 housed, 64 homeless. Month 6: about 134 citizens, 0 housed without building. With six dwellings, 48 housed and the rest homeless | 1,000,000 per typology, always | Tick 0: 120 places, 64 people, 56 spare places, 28 units. Month 6: stock 120, pop about 122, 0 units, migration stops |

Sources: BUG-1214 comments and `docs/planning/evidence/feat-083/leg4-housing-response.txt` (122 vs 134 at month 6, 148 vs 190 at month 12). The file `leg4-control-vs-treatment.txt` in the same folder shows 244 at month 12 for both runs and is from before the vacancy rule, so ignore it.

Other facts that matter:

- The seed city has no dwellings at all. Its "120 homes" exist only as one constant, `baselineOneSeedHomeCapacityPeople`. They are not buildings, have no map cell and cannot be demolished. 120 is a balance placeholder (ASM-1644). The 2 people per migrant household is another (ASM-1645).
- `citizen_query.go` already says in a comment that nothing in the composed engine gives a citizen a Home. The ledger later started giving homes to the homeless once the player builds. The comment is now only half true.
- The ledger ignores a building on map cell 0, because 0 also means "no home". Rare, but it is a real hole.
- The ledger is built to be cheap at scale. It is opt-in. Switching it on costs one pass over all citizens, then each move is cheap. It only re-scans the citizens when a home is demolished or shrunk.
- The vacancy rule is not cheap in the same way. `StandingCapacities()` walks and sorts every standing building. It runs monthly, not per tick, so it is fine today. It is not "flat" once there are hundreds of thousands of buildings.
- Newborns inherit a parent's home without checking capacity, so a dwelling can be overfull (ASM-1538, documented).

## 2. What "one count" must satisfy

1. Every citizen either has a home cell or is counted homeless, exactly once.
2. A dwelling's places are offered once. A place is either lived in or free.
3. The starting city's homes are real homes: they have a place on the map or at least a ledger row, and a starting citizen can say where they live.
4. Migration (attract's vacancy) and housing affordability (households) read the same stock as the ledger.
5. Save and load carry it. An old save must still load and must not change any citizen's home on load (the ledger's existing rule).
6. Cost per tick stays flat at 1M+ citizens. No per-building or per-citizen walk on the tick. Any walk happens only on a change (the structure epoch) or monthly at worst, and is O(buildings), never O(citizens).
7. Deterministic (same seed, same history, same homes). The converge fixtures and the determinism gate stay green, or are deliberately regenerated.

## 3. Options

### Option A: the ledger becomes the single source

What changes:
- Genesis creates the starting city's homes as real dwelling records, in the build module or as a ledger-only "seed block" with its own cells. `spawnCitizens` then assigns seed citizens to them, or the first `AssignHomes` does.
- `housingVacancyFor` stops using the constant 120 and the building walk. It reads the ledger: free places = total capacity minus occupancy, kept as two running numbers updated when capacity changes (daily sync) and when homes are assigned or released. No walk.
- `compose.go` stops the 1,000,000 `ReportStock`. Households read the real dwelling count from the same ledger numbers, per typology where the ledger can tell the typology, otherwise one lump.
- Files: `citizens/housing.go` (add the two running totals, give a free-places accessor), `compose_housing.go`, `compose_vacancy.go`, `compose.go` (genesis and `ReportStock`), `build` (only if seed dwellings become real buildings), the citizen inspector text, tests.
- Player notices: starting citizens have addresses. The census "Homeless" figure at the start drops from everyone to zero. A full city shows a real shortage in affordability, so rents and attraction react. If seed dwellings are real buildings, the map shows them and they can be demolished.
- Save format: a ledger snapshot already saves. Seed dwellings as real buildings add records, so the save format moves by one version. Old saves load with the ledger off, as now (the code already documents this). The first daily sync then rebuilds capacity from standing buildings. Seed citizens in an old save keep Home 0 and are treated as homeless until we ship a one-off "seed homes" step on first sync after load. Say so in the release note.
- Test fallout: high. Five fixtures that pass `SeedHomeCapacityPeople` change meaning. Converge fixtures and the determinism baseline change, because citizens now carry homes from tick 0. Households tests that assume 1M stock change. Expect roughly 15 to 30 test files touched.
- Size: L, about 20 to 28 lane-hours (build genesis 6, ledger totals 5, households wiring 5, tests and fixtures 8, round fixes 4).
- Risk: medium-high. It touches determinism baselines and the perf gate. The perf risk is controlled by the running totals. The known trap (per-building walk on the tick) is avoided only if the totals are kept honest on every demolition and shrink.

### Option B: the aggregate vacancy rule is the source, the ledger becomes a view

What changes:
- Keep stock = seed + built, minus population, as the one number for migration and households.
- The ledger no longer decides who is housed. A citizen's `Home` is derived from the stock: the first N citizens by id are "housed" up to stock, the rest "homeless". Homes would be assigned only for display.
- Households read the same stock (replace the 1M).
- Files: `compose_vacancy.go`, `compose.go`, `compose_housing.go`, much of `citizens/housing.go` becomes thin or is retired.
- Player notices: the homeless count becomes simply max(0, population minus stock). Citizens have no specific dwelling except the one derived for display, so "which building do I live in" stays vague.
- Save format: little change. Old saves behave the same.
- Test fallout: medium. The ledger's tests (eviction, determinism, restore, ASM-1538) mostly lose their reason to exist or must be rewritten.
- Size: M, about 10 to 14 lane-hours.
- Risk: medium. It throws away FEAT-2326609845, which was just built and verified, and the `Home` cell the passport and occupants features rely on becomes fiction. It also fails invariant 3 in spirit: the homes are a number, not homes. I do not recommend it.

### Option C: minimal, close the gap now

What changes:
- At genesis, push a seed capacity snapshot into the ledger (a few "seed blocks" with their own cells outside the player's build area) and run `AssignHomes` so every seed citizen has a home. This is a daily-sync tweak plus one genesis call.
- Make the vacancy rule use the ledger's free places in place of `seed + built - population`. That closes the mismatch between vacancy and ledger. The 120 constant becomes the capacity of the seed blocks.
- Leave households at 1M, to do later.
- Files: `compose_housing.go`, `compose_vacancy.go`, `compose.go` (genesis), `citizens/housing.go` (a free-places accessor). Tests: the vacancy and housing tests, and the five fixtures.
- Player notices: starting citizens have addresses and the census starts at zero homeless. Migration reacts the same as now. Affordability still never sees a shortage.
- Save format: the ledger snapshot already covers it. Old saves load with the ledger off, as now. Seed homes appear on the first sync after load, so an old save's seed citizens gain homes at that point. This is the one visible change on load.
- Test fallout: medium-low. About 6 to 12 test files.
- Size: S to M, about 8 to 12 lane-hours.
- Risk: low-medium. The remaining risk is that we have fixed counts 1 and 3 and left 2 for a later bug. That means affordability stays dishonest until then. The monthly walk of `StandingCapacities` also stays.

## 4. Recommendation

Do Option A, but in two parts, so that C is the first part.

1. Real homes at the start are what the player sees first. Option C gives that cheaply and removes the mismatch between ledger and vacancy.
2. Option B keeps a number and loses homes, so it solves the wrong half.
3. Households at 1M is a lie that hides a full city. It needs the same stock, so finish it as part 2 of A.
4. Keeping running totals in the ledger (free places, total capacity) removes the monthly building walk, so the perf trap is closed at the same time.
5. The old-save story is the same in every option: ledger off on load, seed homes appear on the first daily sync, no Home changes on load itself.

Order of work:
1. Part 1 (about 10 lane-hours): seed blocks in the ledger, genesis assignment, vacancy reads the ledger, running totals, cell 0 hole closed or documented. Round after it.
2. Part 2 (about 12 lane-hours): households read the real stock (per typology if the ledger can say), remove the 1M `ReportStock`, regenerate converge and determinism fixtures, scale gate at 1M. Round after it.
3. Optional later: turn the seed blocks into real, visible, demolishable buildings (this is question Q1 below).

## 5. Questions for Aaron

Each has a recommended answer.

1. Should the starting city's homes be visible buildings on the map? Recommended: not yet. Start with ledger-only seed blocks, which is the cheap path. Make them real buildings only if you want the player to see and demolish the starting town. That is a separate, larger job.
2. What should homelessness do to a citizen? Recommended: nothing mechanical in this fix beyond the existing census "Homeless" count and the passport line "no fixed address". The social module already plans a homelessness pipeline (`engine.social` AC-7). Do not add effects here.
3. What should homelessness do to the city? Recommended: it should lower attraction. When the city is full, the housing term in attract goes to zero and migration stops. That already happens through vacancy. Add nothing else now.
4. The 120 seed places. Recommended: keep 120 for now, then raise it to match the real starting town when you rule on the balance pass. At 64 citizens it leaves room for about 28 migrant households before the city fills, which is why growth stops near month 6.
5. The 2 people per migrant household. Recommended: keep 2. It has to equal attract's own household size, so change both together or neither.
6. Old saves: should seed citizens in an old save be given homes on the first daily sync after load? Recommended: yes. They are only homeless because the old engine never gave anyone a home, and the player would otherwise see a whole town of rough sleepers appear.
7. Overcrowding (ASM-1538): should newborns be refused a parent's home when it is full? Recommended: not in this bug. File it as its own item. Today a full dwelling can still grow.

## Where the brief's problem statement is not quite right

- "Built dwellings are offered twice" is only partly true. The vacancy rule subtracts the whole population, which includes the homeless the ledger houses, so the same eight places are not counted free twice. The real fault is that the two counts disagree about who lives where: the ledger puts homeless starting citizens into built dwellings while the vacancy rule has already counted them as using up the 120 seed places.
- "HomelessCount equals the whole population in the default city" is true only until the player builds. With six dwellings 48 are housed.
- The BUG-1214 evidence file `leg4-control-vs-treatment.txt` shows no vacancy effect (identical populations, 244 at month 12). It predates the rule. The measured figures to use are in `leg4-housing-response.txt`.
