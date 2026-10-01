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
| `model/compute_hold.go` | `compute_holds`, `compute_settlements` and `compute_extensions` tables and the quota movements |
| `router/api-router.go` | one `UNIFYAPI-BRAND` block wiring `/api/compute/v1` on the engine |
| `model/main.go` | `UNIFYAPI-BRAND` lines migrating the three tables |

## Authentication

`X-Compute-Timestamp` (Unix seconds, within 30 s either way) and
`X-Compute-Signature`, hex HMAC-SHA256 over
`METHOD \n PATH \n TIMESTAMP \n EXACT_BODY` with `COMPUTE_BRIDGE_SECRET`. No
session or token auth applies to these routes. A missing or stale timestamp is
`401 invalid_timestamp`; a bad signature is `401 invalid_signature`. A disabled
bridge is a `404` with **no body at all**, which is how a caller tells it apart
from `404 hold_not_found`.

## Rate limiting

The routes are registered on the engine rather than under `/api`'s group, so
the per-IP global API limiter does not apply: every bridge call comes from
compute-api's one address, and that limiter (360 per 180 s) would cap all
compute billing at about 2 requests a second. The bridge has its own token
bucket instead, checked **before** the HMAC so unsigned traffic is bounded too.

| Env | Default | Format |
|---|---|---|
| `COMPUTE_BRIDGE_RATE_LIMIT` | `50:100` | `RATE` or `RATE:BURST`, requests per second. `RATE` alone gives a burst of `2 × RATE`. A malformed value is logged and the default is kept. |

The bucket is per console process, so with several nodes the total is the
per-node limit times the node count. Over the limit: `429`, `Retry-After` in
whole seconds, `message: "rate_limited"`. The value is read once, on the first
bridge request.

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

- `holds` is idempotent on `hold_id`. A replay of the original create request
  (same user, token, quota, job, SKU and `expires_at`, compared with what was
  first stored, so a later extend does not break it) answers `200` with the
  hold and is answered before the token and login are re-checked. Any other
  body under that id is `409 idempotency_conflict` and moves nothing.
- `settle` is idempotent on `event_id`, unique across all holds. A replay of the
  same settlement answers `200` with the hold as it stands now. The same id on
  another hold, or with a different amount, is `409 idempotency_conflict`. A
  refused settle records nothing (no settlement row, no consume row, no
  used-quota change), so it can be retried once corrected.
- `extend` requires `extend_id` (non-empty, at most 64 characters; compute-api
  sends `ext_` + ULID). It is unique across all holds and written in the same
  transaction as the reservation. A replay with the same amount answers `200`
  with the hold's current `quota` and `remain_quota` and reserves nothing; a
  different amount, or the same id on another hold, is `409 idempotency_conflict`.
- `release` is idempotent, including after a final settle.

Every error body is `{success: false, message: <code>, code: <code>}`;
clients read `message`.

| Status | `message` |
|---|---|
| `400` | `invalid_request` (bad JSON, missing id, amount out of range) |
| `401` | `invalid_timestamp`, `invalid_signature`, `invalid_key` |
| `402` | `insufficient_balance` (wallet or a limited token) |
| `403` | `suspended` or `disabled`, also in `data.status` |
| `404` | `hold_not_found` (enveloped); empty body = bridge disabled |
| `409` | `hold_expired`, `hold_closed`, `hold_exceeded`, `idempotency_conflict`, `sku_mismatch`, `cumulative_quota_regressed` |
| `429` | `rate_limited`, with `Retry-After` |
| `500` | `internal_error`; the detail is in the console log |

## Expiry

On the master node a sweeper runs every minute and expires up to 100 active
holds whose `expires_at` has passed, returning their unsettled remainder. It
runs whether or not the secret is set, so turning the bridge off does not
strand quota. A settle, extend or release that finds a lapsed hold before the
sweeper does expires it itself and answers `409 hold_expired`.
