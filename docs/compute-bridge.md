# Compute billing bridge (console side)

Status: implemented, **dark**. The routes answer `404` until
`COMPUTE_BRIDGE_SECRET` (at least 32 characters) is set. Not yet exercised
against a real compute-api; staging is the first place both sides meet.

The contract is owned by unify-compute
(`docs/spec/console-billing-bridge.md` in FelixSphere/unify-compute). This page
says what the console does with it. The console holds no compute logic and no
compute prices: it moves the quota integers compute-api sends, through the same
code paths the relay uses.

## Files

| File | What |
|---|---|
| `controller/compute_bridge.go` | HMAC check, the five handlers, the expired-hold sweeper |
| `model/compute_hold.go` | `compute_holds` and `compute_settlements` tables and the quota movements |
| `router/api-router.go` | one `UNIFYAPI-BRAND` block wiring `/api/compute/v1` |
| `model/main.go` | `UNIFYAPI-BRAND` lines migrating the two tables |

## Authentication

`X-Compute-Timestamp` (Unix seconds, within 30 s either way) and
`X-Compute-Signature`, hex HMAC-SHA256 over
`METHOD \n PATH \n TIMESTAMP \n EXACT_BODY` with `COMPUTE_BRIDGE_SECRET`. No
session or token auth applies to these routes; the global API rate limiter does.
A bad or stale signature is `401`. A disabled bridge is a bare `404` with no
JSON body, which is how a caller tells it apart from `404 hold_not_found`.

## How each verb moves quota

"Billing entity" is the tenant wallet for a tenanted login and the user's own
`users.quota` for an untenanted one (`model/billing_entity.go`).

| Verb | Billing entity | Token `remain_quota` | Consume log |
|---|---|---|---|
| `verify` | read | read | — |
| `holds` | `− quota`, refused (`402`) if it would overdraw | `− quota` through the token cache/batch path; refused (`402`) only when the token is limited and short | — |
| `settle` | unchanged (the hold already took it) | unchanged | one row for `cumulative − previously settled`, when that is above 0 |
| `settle` with `final` | `+ unsettled remainder` | `+ remainder` | as above |
| `extend` | `− additional`, refused like `holds` | `− additional` | — |
| `release` | `+ unsettled remainder` | `+ remainder` | — |
| sweeper (expired) | `+ unsettled remainder` | `+ remainder` | — |

Wallet movements happen inside one database transaction with the hold row,
using `TryDecreaseUserQuotaWithTx` / `adjustBillingQuotaWithTx`, followed by the
billing-entity cache invalidation. Token movements follow the commit through
`DecreaseTokenQuota` / `IncreaseTokenQuota`, exactly as `BillingSession` does
after funding has committed. Nothing writes `users.quota` around the cache.

Consume rows: `model_name = "compute/<sku>"`, zero prompt/completion tokens,
`quota = delta`, channel 0 (so neither the supplier credit-lot draw-down nor
channel TPM counts it), `other = {gpu_seconds, job_id, hold_id, event_id}`. Each
row also goes through `UpdateUserUsedQuotaAndRequestCount`, so user and tenant
`used_quota` count settled spend.

Compute draws on the **wallet only**. Subscriptions and promotional credit
grants are not consulted.

## Idempotency and errors

- `holds` is idempotent on `hold_id`. A replay answers `200` with the stored
  hold and is answered before the token and login are re-checked. The same id
  with a different user, token, job or SKU is `409 hold_id_conflict`.
- `settle` is idempotent on `event_id`. An event id already used on another hold
  is `409 event_id_conflict`. A refused settle records nothing, so it can be
  retried with the same id once corrected.
- `release` is idempotent, including after a final settle.
- `extend` is **not** idempotent; see the open question in the PR that added this.

| Status | `message` |
|---|---|
| `400` | `invalid_request`, `quota_out_of_range`, `sku_mismatch` |
| `401` | `invalid_signature`; or the console's token-invalid text |
| `402` | `insufficient_quota` (wallet or token) |
| `403` | the console's user-banned text, `data.status` = `suspended` or `disabled` |
| `404` | `hold_not_found` (enveloped); bare `404` = bridge disabled |
| `409` | `hold_expired`, `hold_closed`, `hold_exceeded`, `cumulative_quota_regressed`, `hold_id_conflict`, `event_id_conflict` |

## Expiry

On the master node a sweeper runs every minute and expires up to 100 active
holds whose `expires_at` has passed, returning their unsettled remainder. It
runs whether or not the secret is set, so turning the bridge off does not
strand quota. A settle, extend or release that finds a lapsed hold before the
sweeper does expires it itself and answers `409 hold_expired`.
