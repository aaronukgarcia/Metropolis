# Backlog comb: asm-1 (111 items)

## Summary
Classes: STRONG 74, DONE? 18, RULING 10, WEAK 4, DUP 4, STALE 1, NONE 0.
Value: INFRA 95, GAME 11, ENABLER 5, NOISE 0.

Depth note: 100 of the 111 are assumptions logged today (2026-11-17) by dev and review lanes for the in-flight offload features (inc2 FEAT-9995385594, meter FEAT-9995385596, protocol FEAT-9995385597, workers FEAT-9995385598) and for landed work. Those got a title plus first-260-character description look, checked against git log and A2Bev001.md; only the overridden rows were checked against specific docs. Unmarked STRONG rows were judged from the description text and nothing deeper.

Most worth doing next:
1. ASM-1537 (cell 0 cannot be a home): a real housing bug class on a landed feature; one offset fix plus a test.
2. ASM-1538/1549 (household co-housing and overcrowding): player-visible family behaviour; merge the two and schedule with household-level housing.
3. ASM-1574 (cmd/metroserve has no code.json key): cheap, blocks edge registration for inc2 consumers.
4. ASM-1536/1542 (BUG-1184 restore notice and fallback path): "not verified end to end"; add one test per path.
5. ASM-1619 (metered classes refused at boot until writer fencing): a fail-closed design that gates the meter; confirm it before build.

Close or merge: close as DONE? the 18 items whose parent landed (ASM-1540, 1541, 1543, 1544, 1545-1548, 1632, 1634, 1636-1638, 1577, 1578, 1579, 1552, 579) after folding each into its parent item's notes (Aaron's standing fold-then-close directive in asm-disposition.md). Merge DUPs: 1550 into 1611 (STALE), 1580 into 1626, 1588 into 1618, 1598 into 1619, 1639 into 1597. 1538 into 1549.

Rulings Aaron owes: ASM-1012 (harbour vs coastal: lifeboats or ferry arrivals), ASM-909 (finite-depleting per mine type), ASM-1489 (98M vs 100M top rung), ASM-1535 (UX-2 target count), ASM-1555 (R2 tiering default), ASM-1568 (SIGTERM 20 s at 30M and the uplink assumed), ASM-1572 (R2 prices; cost gate), ASM-1573 (play profile for forecast), ASM-1595 (reconnect to loaded city at 100 percent budget), ASM-1601 (distinct wake-ready message).

## Items
| Code | P | Type | Class | Value | Note (<= 25 words) |
|---|---|---|---|---|---|
| ASM-1012 | P2 | ASM | RULING | ENABLER | AARON-DECISION in engine.coastal.md AC-19; harbour->coastal edge unregistered; lifeboat vs ferry-arrival still unruled. |
| ASM-1489 | P2 | ASM | RULING | ENABLER | 98M rung is documented in traffic-assumptions.md; Aaron must say deliberate headroom or move to 100M. |
| ASM-1535 | P2 | ASM | RULING | GAME | UX-2 landed (3684474); hover shows running vs supporting population. Aaron decides whether sim must publish a target count. |
| ASM-1536 | P2 | ASM | STRONG | GAME | BUG-1184 landed (d55ee88) but notice-survival flagged not verified end to end; needs one e2e test. |
| ASM-1537 | P2 | ASM | STRONG | ENABLER | Cell 0 cannot be a home; consequence stated (tile 0,0 houses nobody). Home half landed f2d3ffc; fix needs +1 CellRef offset. |
| ASM-1538 | P2 | ASM | STRONG | GAME | Family members may land in different buildings; revisit at household housing. Refined by ASM-1549. |
| ASM-1539 | P2 | ASM | WEAK | GAME | Placeholder 8/job capacities; covered by balance-pass regime, but no gate names which test moves with the data. |
| ASM-1540 | P2 | ASM | DONE? | GAME | UX-1 landed b127698; Recent-spec behaviour is a design note. Close after folding into UX-1 criteria. |
| ASM-1541 | P2 | ASM | DONE? | GAME | UX-2 landed 3684474; label qualifier is shipped behaviour. Fold and close. |
| ASM-1542 | P2 | ASM | STRONG | INFRA | BUG-1184 fallback restore path; monitors not cleaned stated. Keep until a test covers fallback restore. |
| ASM-1543 | P2 | ASM | DONE? | GAME | BUG-1160 landed 52583cc (notifications stop stacking). Fold into BUG-1160 notes and close. |
| ASM-1544 | P2 | ASM | DONE? | INFRA | BUG-1198 landed 779d10b; doc figure corrections recorded. Close. |
| ASM-1545 | P2 | ASM | DONE? | INFRA | BUG-1097 landed (3ed078d); wire field is shipped. Close after fold. |
| ASM-1546 | P2 | ASM | DONE? | INFRA | BUG-1097 landed; CompareStamped decision is shipped. Close after fold. |
| ASM-1547 | P2 | ASM | DONE? | INFRA | BUG-1097 landed; per-Server counter shipped. Close after fold. |
| ASM-1548 | P2 | ASM | DONE? | GAME | BUG-1097 landed; amber badge shipped. Close after fold. |
| ASM-1550 | P2 | ASM | STALE | INFRA | Superseded by ASM-1611: Aaron ruled minio-go, no hand-rolled SigV4 (A2Bev001 v23.3). |
| ASM-1551 | P2 | ASM | STRONG | INFRA | Journal on S3/R2 is object-per-flush; a2 AppendBlock is Azure-only and absent |
| ASM-1552 | P2 | ASM | DONE? | INFRA | Strict head-CAS fencing ruled in A2Bev001 v23.3 inc2 storage rulings; assumption now a ruling. |
| ASM-1553 | P2 | ASM | STRONG | INFRA | R2 honours If-Match/If-None-Match on PutObject; probed at startup, fail closed |
| ASM-1554 | P2 | ASM | STRONG | INFRA | R2/S3 give strong read-after-write and list-after-write consistency |
| ASM-1555 | P2 | ASM | RULING | INFRA | No R2 archive tier; Aaron confirmed journal in R2 standard (v23.3) but tiering flag default unruled. |
| ASM-1556 | P2 | ASM | STRONG | INFRA | Game-year = 360 day-ticks; keep newest snapshot per tick/360 bucket after 30 days |
| ASM-1557 | P2 | ASM | STRONG | INFRA | Manifest carries createdAtUnix (wall clock), excluded from every determinism digest |
| ASM-1558 | P2 | ASM | STRONG | INFRA | Cloud retention policy lives on the S3 store; MaxRetainedSnapshots=5 stays for disk |
| ASM-1559 | P2 | ASM | STRONG | INFRA | Byte-determinism holds for fixed Go toolchain and amd64; rawSha256 is the cross-toolchain check |
| ASM-1560 | P2 | ASM | STRONG | INFRA | SoA columns = ColdShard columns as stored plus monthlyUpdates; wire widths differ from coldCitizenWire |
| ASM-1561 | P2 | ASM | STRONG | INFRA | Part layout: 256 parts citizens/shard-NNN plus core part(s) holding the rest of the Composition.Save bundle |
| ASM-1562 | P2 | ASM | STRONG | INFRA | Part transfer concurrency defaults: upload 8, download 16 (a2 range 16-32); memory = concurrency x part size |
| ASM-1563 | P2 | ASM | STRONG | INFRA | Group commit defaults N=64, T=200 ms or tick boundary; an empty batch writes nothing |
| ASM-1564 | P2 | ASM | STRONG | INFRA | Dedup window: last 4096 (session, correlationId) keys per session, rebuilt from tail on restore |
| ASM-1565 | P2 | ASM | STRONG | INFRA | kind=ext record body is opaque bytes; no ExternalEvent type exists today |
| ASM-1566 | P2 | ASM | STRONG | INFRA | Record v2 = JSON envelope inside the existing CRC frame; v1 bare-Command records stay readable |
| ASM-1567 | P2 | ASM | STRONG | INFRA | Store interface gains methods all three impls implement |
| ASM-1568 | P2 | ASM | RULING | INFRA | 20 s SIGTERM budget at 30M rests on unverified compressed size and 250 Mbit/s uplink; Aaron owns the target. |
| ASM-1569 | P2 | ASM | STRONG | INFRA | 20 s budget runs from SIGTERM receipt to manifest commit ack incl. final flush and tick quiesce |
| ASM-1570 | P2 | ASM | STRONG | INFRA | Wall-clock budgets (20 s, 15 s) verified by a recorded measurement run, not a CI timing assert |
| ASM-1571 | P2 | ASM | STRONG | INFRA | Snapshot commit order: CAS city manifest first (proves ownership), then PUT immutable snapshot manifest |
| ASM-1572 | P2 | ASM | RULING | INFRA | R2 prices from memory, not re-verified; cost-gate forecast must precede enabling R2 (Aaron authorise). |
| ASM-1573 | P2 | ASM | RULING | INFRA | Invented play profile drives cost forecast; Aaron should supply ops/hour and snapshot cadence. |
| ASM-1574 | P2 | ASM | STRONG | INFRA | cmd/metroserve has no code.json key; consumer edge must be registered (GGR53). Concrete and checkable. |
| ASM-1575 | P2 | ASM | STRONG | INFRA | Credentials only from env or secret-file path; token scoped to one bucket, object RW+list only |
| ASM-1576 | P2 | ASM | STRONG | INFRA | CitizensAPI.workers has no setter; detgate 1-vs-14 never varies it |
| ASM-1577 | P2 | ASM | DONE? | INFRA | Verified code fact (PopulationHash covers Wealth etc.), not an assumption. Fold into AC and close. |
| ASM-1578 | P2 | ASM | DONE? | INFRA | Resolved by ASM-1625 (private-sector gate only commits when firms post accepted). Close. |
| ASM-1579 | P2 | ASM | DONE? | INFRA | Verified fact: fertility already daily per shard. Fold into ACs and close. |
| ASM-1580 | P2 | ASM | DUP | INFRA | Superseded by ASM-1626 (barrier snapshot set empty for today's passes). |
| ASM-1581 | P2 | ASM | STRONG | INFRA | Default workers min(GOMAXPROCS,8) applies at metroserve only |
| ASM-1582 | P2 | ASM | STRONG | INFRA | Month-start snapshot is a copy-on-write per-shard view, not a full copy |
| ASM-1583 | P2 | ASM | STRONG | INFRA | Employment changes outside shard day are paid by shard-day state only |
| ASM-1584 | P2 | ASM | STRONG | INFRA | Per-shard accumulators become save state, serialized in shard order |
| ASM-1585 | P2 | ASM | STRONG | INFRA | Wage bill sized from sum of per-shard employed counts at day 30 |
| ASM-1586 | P2 | ASM | STRONG | INFRA | Active second = process has >=1 city loaded/building or >=1 WS connection |
| ASM-1587 | P2 | ASM | STRONG | INFRA | Meter is per process (whole host), not per tenant; tenant seconds informational only |
| ASM-1588 | P2 | ASM | DUP | INFRA | Superseded by ASM-1618 (host-scoped persist.Store methods with sequence CAS). |
| ASM-1589 | P2 | ASM | STRONG | INFRA | Counter buckets seconds by host class; budget applies only to the running host's class |
| ASM-1590 | P2 | ASM | WEAK | INFRA | UTC month boundary stated with rationale but no gate or failure; add rollover test. |
| ASM-1591 | P2 | ASM | STRONG | INFRA | Wall clock is used; it never enters the simulation (GR#21) |
| ASM-1592 | P2 | ASM | STRONG | INFRA | Counter flushed every 30 s and at SIGTERM; unclean restart adds one flush interval |
| ASM-1593 | P2 | ASM | STRONG | INFRA | Missing/corrupt counter on a non-empty persistence root fails closed for metered classes |
| ASM-1594 | P2 | ASM | STRONG | INFRA | Exhausted budget refuses NEW wakes only; live sessions drain, hard stop at 110 percent |
| ASM-1595 | P2 | ASM | RULING | GAME | Reconnect to a loaded city at 100 percent budget: marked Open Q in description; Aaron decides. |
| ASM-1596 | P2 | ASM | STRONG | INFRA | Unconfigured host class defaults to dev/unmetered; paid classes must be explicit with budget >0 |
| ASM-1597 | P2 | ASM | STRONG | INFRA | Sampling delta capped at 60 s per sample to ignore laptop sleep clock jumps |
| ASM-1598 | P2 | ASM | DUP | INFRA | Superseded by ASM-1619: no root lock exists on main; metered classes refused until fencing. |
| ASM-1599 | P2 | ASM | STRONG | INFRA | Lifecycle messages are 3 capability flags, no wire minor bump |
| ASM-1600 | P2 | ASM | STRONG | INFRA | Handshake splits resolver: sync claim before reply, async load after |
| ASM-1601 | P2 | ASM | RULING | GAME | Description says Aaron may prefer a distinct ready message; wire shape needs his call before build. |
| ASM-1602 | P2 | ASM | STRONG | INFRA | Client sessionId in handshake enables same-session takeover |
| ASM-1603 | P2 | ASM | STRONG | INFRA | Auto-retry of in-flight commands gated on command.dedup capability |
| ASM-1604 | P2 | ASM | STRONG | INFRA | Commands issued while not live are rejected, never queued |
| ASM-1605 | P2 | ASM | STRONG | INFRA | Backoff 1s doubling to 30s cap, full jitter, reset on live |
| ASM-1606 | P2 | ASM | STRONG | INFRA | Wake timeout 60s resets on each waking progress message |
| ASM-1607 | P2 | ASM | STRONG | INFRA | Drain budget equals host SIGTERM grace minus margin, host-agnostic |
| ASM-1608 | P2 | ASM | STRONG | INFRA | Busy refusal MET-P043 is retried only a bounded number of times |
| ASM-1609 | P2 | ASM | STRONG | INFRA | Reconnect resubscribes by replaying viewName+params, new SubscriptionIDs |
| ASM-1610 | P2 | ASM | STRONG | INFRA | Page Visibility hibernation client disconnect is out of scope here |
| ASM-1611 | P2 | ASM | STRONG | INFRA | inc2 S3 client is minio-go v7 behind a narrow internal interface; must expose If-Match/If-None-Match on PutObject |
| ASM-1612 | P2 | ASM | STRONG | INFRA | inc2 object keys zero-pad epoch and seq to 20 digits so LIST lexical order equals numeric order |
| ASM-1613 | P2 | ASM | STRONG | INFRA | inc2 soa-v1 citizen codec lives in internal/engine/citizens; persist treats every part as opaque bytes |
| ASM-1614 | P2 | ASM | STRONG | INFRA | inc2 hot-set citizens and households travel in the core part in their existing encoding, not in soa-v1 shard parts |
| ASM-1615 | P2 | ASM | STRONG | INFRA | inc2 tryLockFile moves into a persist-owned sub-package and compose imports it from there |
| ASM-1616 | P2 | ASM | STRONG | INFRA | inc2 periodic save read as per wall-clock hour of active play, not per game-year (game-year at turbo is about 58 s) |
| ASM-1617 | P2 | ASM | STRONG | INFRA | inc2 ambiguous retries: create-only 412 on retry is success iff stored sha matches; head CAS resolved by re-reading head |
| ASM-1618 | P2 | ASM | STRONG | INFRA | Meter host record needs new host-scoped persist.Store methods (Store is per-city today) |
| ASM-1619 | P2 | ASM | STRONG | INFRA | Metered host classes refused at boot until store writer fencing exists (no root lock on main) |
| ASM-1620 | P2 | ASM | STRONG | INFRA | 100 percent stop: one bounded snapshot attempt then unload regardless; journal is the durability guarantee |
| ASM-1621 | P2 | ASM | STRONG | INFRA | Exhausted meter at boot skips default-city pre-create instead of exiting non-zero |
| ASM-1622 | P2 | ASM | STRONG | INFRA | Meter rollback detection is in-process only; a pre-boot restore of an older valid record is undetectable |
| ASM-1623 | P2 | ASM | STRONG | INFRA | Staggered compose wage/employment passes cannot call ApplyLifeEventCommand inside runShardsParallel (c.mu deadlock) |
| ASM-1624 | P2 | ASM | STRONG | INFRA | Pending wage credit of a citizen who departs before the month-close commit is dropped, not inherited |
| ASM-1625 | P2 | ASM | STRONG | INFRA | Public-sector pending credit commits unconditionally as today; only the firms post gates commit |
| ASM-1626 | P2 | ASM | STRONG | INFRA | No barrier pass reads a staggered-mutated field; month-start snapshot set is empty for today's passes |
| ASM-1627 | P2 | ASM | STRONG | INFRA | Wage paid on Employed state after the shard-day's cold pass and marking, not at the start of the shard-day |
| ASM-1628 | P2 | ASM | STRONG | INFRA | Split handshake: claim before reply needs a reply write deadline and token-carried confirm/release |
| ASM-1629 | P2 | ASM | STRONG | INFRA | Same-session supersede only from a confirmed holder; new pump starts only after old pump fully joins |
| ASM-1630 | P2 | ASM | STRONG | INFRA | clientSessionId is minted per page load in memory, not sessionStorage |
| ASM-1631 | P2 | ASM | STRONG | INFRA | Server releases a connection's subscriptions on close; re-subscribe journals frames but leaves sim state unchanged |
| ASM-1632 | P2 | ASM | DONE? | INFRA | BUG-1197 landed 2fa4531; trailing-command behaviour is lead ruling, shipped. Close. |
| ASM-1633 | P2 | ASM | STRONG | INFRA | Drain notifies and closes sockets before BUG-1197 CityHost.Close snapshot, under one shutdown budget |
| ASM-1634 | P2 | ASM | DONE? | INFRA | BUG-1197 landed; eviction waits for build, as the commit states. BUG-1208 tracks residue. |
| ASM-1635 | P2 | ASM | STRONG | INFRA | Waking progress: only queued and ready are mandatory phases; intermediate phases come from an optional progress sink |
| ASM-1636 | P2 | ASM | DONE? | INFRA | BUG-1197 landed 2fa4531; snapshotEvery<=0 no-op shipped. Close. |
| ASM-1637 | P2 | ASM | DONE? | INFRA | BUG-1197 landed; P060-P069 range in use. Close after verifying errors.json. |
| ASM-1638 | P2 | ASM | DONE? | INFRA | BUG-1197 r2 fail-closed repair shipped in 2fa4531 (MET-P063). Close. |
| ASM-1639 | P2 | ASM | DUP | INFRA | Duplicates ASM-1597 (60 s sample cap); description empty. |
| ASM-1640 | P2 | ASM | WEAK | INFRA | Empty description; AC-6 behaviour named in title but no consequence or gate. |
| ASM-1641 | P2 | ASM | WEAK | INFRA | Empty description; records two deliberate non-builds (pix advisory, AC-9) with no consequence stated. |
| ASM-579 | P2 | ASM | DONE? | ENABLER | Resolved in feat.deathwave.md ASM-579 amendment to AC-7 (SeasonAPI curves; disasters drought blocked). |
| ASM-909 | P2 | ASM | RULING | ENABLER | Marked AARON-DECISION in feat.minetypes.md: finite-depleting not uniform across six mine types. |
| ASM-1549 | P3 | ASM | STRONG | GAME | Precise restatement of ASM-1538 overcrowding rule; merge 1538 into it. Trims highest id first on capacity cuts. |

## Improved criteria
### ASM-1537 Cell 0 structure cannot be a home
1. A dwelling built at tile 0,0 (linear cell 0) houses residents: SetHomeCapacities counts cell 0 and AssignHomes can place a citizen there.
2. Gate: a compose test builds a dwelling at 0,0, runs AssignHomes, and asserts HomeOccupancy at that cell is above 0. Goes red on today's sentinel collision.
3. CellRef and passport decode round-trip cell 0 distinctly from "no home" (unverified which files hold the +1 offset).

### ASM-1539 Placeholder dwelling and job capacities
1. buildings.json residentCapacity 8 and per-zone jobCapacity stay tagged AARON BALANCE PASS; the compose e2e test reads the figure from data, not a literal 8.
2. Gate: changing the dwelling capacity in data changes the e2e expectation with no test edit; directional tests (more dwellings, more housed) still pass.
3. Failure caught: a data change that silently desyncs the hardcoded figure.

### ASM-1590 UTC month boundary
1. The meter rolls over by comparing stored month key to the current UTC month key, never by file absence.
2. Gate: injected-clock test steps 2026-12-31T23:59:59Z to 2027-01-01T00:00:00Z and asserts a fresh bucket; a second test uses a local-offset clock and gets the same result.
3. Failure caught: DST or locale shifting a rollover by hours.

### ASM-1640 CityHost conns counter counts a connection with no city
1. State the consequence: a connected tab with no loaded city counts as an active second (or does not), and which is intended (unverified).
2. Gate: meter test with one WS connection and zero cities asserts active seconds increase by the sample delta.
3. Failure caught: an idle open tab being under- or over-billed against the monthly budget.

### ASM-1641 pix advisory and AC-9 not built
1. pix class logs the advisory once per month rollover and never refuses a wake or stops a city.
2. Gate: test at 150 percent of a pix budget asserts the wake succeeds and exactly one advisory line is logged.
3. AC-9 per-tenant breakdown: record explicitly as deferred with the owning item code, or build it.

### ASM-1489 Scale ladder top rung 98M
1. Aaron records whether 98,000,000 is deliberate; if not, the ladder gains a 100,000,000 rung in scale_ladder.json.
2. Gate: traffic tests at pop=100M do not extrapolate (a never-extrapolate assertion) or assert the documented clamp.
3. Failure caught: the last 2 percent of population getting flat or extrapolated trip rates.

### ASM-1012 harbour and engine.coastal cross-cut
1. Aaron picks one: lifeboat/rescue capacity, ferry as arrival surface, or neither for now.
2. Register the chosen edge in code.json (GGR53) or record the explicit no-edge decision on feat.harbour.
3. Gate: edge-lint passes; engine.coastal acceptance AC-19 is amended to the ruling.

### ASM-1535 Hover shows running buildings, not a target
1. Aaron decides: keep the supporting-population view, or the sim publishes a target shop/industry count first.
2. If target wanted: a new SimAPI field plus a test that the hover value equals it; otherwise AC-2.3 is reworded to match shipped behaviour.

### ASM-1573 Modelled play profile
1. Aaron supplies command rate, monthly active hours and snapshot cadence, or approves 30 commands/min, 125 h/month, 1 snapshot per hour.
2. Gate: the cost forecast cites those figures and a re-run with a different profile changes the forecast.

### ASM-1601 Wake-ready signal
1. Aaron chooses: reuse server.waking with phase ready/failed, or add a distinct message kind.
2. Gate: protocol test that a client sees exactly one terminal waking event per wake and treats a missing terminal as timeout (60 s, resets on progress).
