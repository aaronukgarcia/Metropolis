# inc1 cost-gate forecast: one engine up, one round trip measured

**Date:** 2026-11-17 · **Author:** Bev · **For:** Aaron's Authorise through `/cost-gate`, before **any** Azure resource is provisioned.
**Authority:**
- `A2Bev001.md` v23: Q100154, £20/month hard cap; Q100157, hibernation.
- `docs/planning/azure-cloud-engine-design.md` §7 inc1 and §3.3.
- `a2.md` §2.2.

**Payment instrument:** the existing Azure subscription (Q100024). It already hosts the Prix Six Container Apps environment, the `prixsixacr` registry and the `garcialtdstorage` storage account.

---

## 1. What inc1 provisions

| # | Resource | New or reused | Purpose |
|---|---|---|---|
| R1 | Container App `metroserve`, 0.5 vCPU / 1 GiB, `min-replicas 0`, `max-replicas 1` | **new** app in the existing Container Apps environment | the engine plus `/health` |
| R2 | Azure Files share on `garcialtdstorage` (Hot LRS, 1 GiB quota) | **new share** on an existing account | `/data`: journal and snapshots (disk store over SMB) |
| R3 | Image in `prixsixacr` | reused | the build pushed by `azure-deploy.yml` |
| R4 | Log Analytics: the environment's existing workspace, with a **daily ingestion cap of 0.1 GB** | reused, cap added | container logs |
| R5 | Azure **Budget** on the Metropolis resource group: **£20**, alerts at 50 / 80 / 100 % | **new**, free | spend alarm |
| R6 | Action group (email to Aaron) | **new**, free | alert delivery |
| R7 | Automation account + **kill-switch runbook**: `az containerapp update --min-replicas 0 --max-replicas 0`, triggered by the 100 % alert | **new** | the hard stop (Budgets alone only alert) |
| R8 | In-app **active-hours meter** in metroserve: refuses a wake once the month's hours are spent | code, no Azure cost | a guard with no billing lag |

## 2. Unit prices: uksouth, GBP, Azure Retail Prices API, pulled 2026-11-17

| Meter | Price | Free allowance |
|---|---|---|
| Container Apps Consumption: vCPU active | **NOT RETURNED by the API above the free tier.** Only the £0 free-tier row is published. USD list price as last known: ~$0.000024/vCPU-s ≈ **£0.000019/vCPU-s** (**UNVERIFIED**, confirm in the Azure pricing calculator) | 180,000 vCPU-s / month **per subscription** |
| Container Apps Consumption: memory active | as above: ~$0.000003/GiB-s ≈ **£0.0000024/GiB-s** (**UNVERIFIED**) | 360,000 GiB-s / month per subscription |
| Container Apps requests | £0.3007 per 1M | 2M / month |
| Azure Files Hot LRS: data stored | £0.0199 per GB-month | — |
| Azure Files Hot LRS: write ops | £0.0577 per 10K | — |
| Azure Files Hot LRS: read / other ops | £0.0046 per 10K | — |
| Log Analytics: Analytics Logs ingestion | £2.1653 per GB above 5 GB | **5 GB / month** |
| Log Analytics: retention | £0.0977 per GB-month beyond the included period | 31 days included |
| Automation: process runtime | £0.0015 per minute above 500 | **500 min / month** |
| Budgets, action-group email | free | — |

**Caveat.** The Container Apps free grant is **per subscription**. If the Prix Six apps in the same subscription already use some of it, Metropolis compute is billed from the first second. That should be checked in Cost Management before Authorise.

## 3. Forecast

**inc1's working month:** the smoke test, about 4 hours of active compute, plus about 40 hours a month of play.

| Item | Usage | Cost |
|---|---|---|
| Compute (0.5 vCPU / 1 GiB, ~44 h active) | 79,200 vCPU-s + 158,400 GiB-s | **£0** if the free grant is unused by Prix Six; otherwise ≈ £1.50 + £0.38 = **≈ £1.90** |
| Requests | well under 2M | £0 |
| Files: storage | < 1 GB | ≈ £0.02 |
| Files: journal writes | per-record appends: ~44 h × 3,600 s × 1/s ≈ 160K writes | ≈ £0.92 (≈ £0.10 after group commit) |
| Files: reads/other | small | < £0.05 |
| Log Analytics | under the 0.1 GB/day cap, so at most 3 GB a month, inside the 5 GB free | £0 |
| Automation | the runbook fires rarely, seconds per run | £0 |
| Budget, alerts | | £0 |
| **inc1 expected monthly total** | | **≈ £1–3** |

**Worst case without the guards** (the forgotten-tab trap, `a2.md` §2.2: the replica never scales to zero):
- 720 h at 0.5 vCPU is 1,296,000 vCPU-s; less the 180,000 free, that is ≈ **£21**.
- 720 h at 1 GiB is 2,592,000 GiB-s; less the 360,000 free, that is ≈ **£5.40**.
- **≈ £26/month. That breaches the £20 cap even at inc1's smallest size.**

**So the guards are part of inc1, not an option:**
- the active-hours meter, R8 (for example 100 h/month at inc1);
- the server hibernation rule (Q100157: 15 min idle or paused; hidden tab after 5 min);
- the kill-switch runbook, R7.

With all three, the worst case is bounded by the meter at **≈ £2–3**. The runbook's backstop is the £20 Budget.

## 4. What Aaron is asked to Authorise

One `/cost-gate` token, bound to the provisioning pattern for R1, R2, R4's cap, R5, R6 and R7 in the Metropolis resource group in uksouth, with:
- **Forecast:** ≈ £1–3/month expected, hard-capped at £20/month (Budget + runbook + in-app meter).
- **Payment instrument:** the existing Azure subscription.

**Before Authorise:**
- Confirm the Container Apps paid-tier rate in the pricing calculator. The API did not return it.
- Check how much of the subscription's free grant Prix Six already uses.

## 5. Prerequisites before provisioning (no cost)

- **BUG-1197:** snapshot on SIGTERM and eviction. Otherwise every revision restart loses play since the last 360-tick snapshot.
- **The hibernation rule and the active-hours meter (R8)** are written and pass their round.
- **BUG-1097:** relax the handshake build gate, so the first browser connection is not refused (as it was on 2026-09-11).
- **BUG-719:** the deploy secret seeding.
- **BUG-1196:** refuse a second connection.
- **The GitHub secret `METROSERVE_SHARED_SECRET`** is created by Aaron. The create path still writes a placeholder secret value (a BUG-719 follow-up).
