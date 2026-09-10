# Builder API integration bridge

Status: implemented locally, disabled by default, not released.

The bridge provides server-authenticated account provisioning/connection,
account and request projections, a dedicated inference key and the existing
Stripe checkout, plus a transactional $10 team launch grant. All products with
the same owner form one team and share that owner's account balance. The
Builder subject is the durable team identity; products cannot reset the grant.

## Trust boundary

Builder's API verifies its normal Firebase session and verified email. It signs
`POST\n/path\nunix_timestamp\nexact_body` using HMAC-SHA256, sending the signature
as hexadecimal in `X-Builder-Signature` and the time in `X-Builder-Timestamp`.
Assertions expire after 30 seconds. The configured code is authoritative.

Set `BUILDER_INTEGRATION_SECRET` (minimum 32 characters) and
`BUILDER_INTEGRATION_PARTNERSHIP_CODE`. Missing settings disable the route.
The existing global API rate limit still applies; reads do not consume the
shared login/registration critical-rate bucket.

Optional `BUILDER_INTEGRATION_RETURN_URL` controls the return from Stripe.
It must also pass the existing payment redirect allowlist. Checkout and its
signed webhook remain owned by the existing Stripe implementation.

## Account behavior

New accounts get the configured partnership customer group and zero initial
credit. An enrollment records attribution without consuming grant capacity.
Existing accounts require proof using their existing management token and a
matching verified email; their group and balance remain unchanged. A management
token is never persisted in the bridge. Staff and disabled accounts cannot link.

`BuilderIdentity` is an additive GORM table linking one Builder subject to one
UnifyAPI user and dedicated inference key. No existing signup flow changes.
Keys are generated with the existing cryptographic key generator. Workspace
responses are explicit projections, excluding raw logs and secrets. Revoked
keys cannot be revealed or used through Builder. No user-facing unlink flow is
implemented yet; operators can revoke the dedicated key or disable the account.

## Scope and release

HTTP-only integration; source and storage remain separate products. No dependency
was added. Complete live integration validation before enabling this bridge. Deploy UnifyAPI first through its own release owner, then configure
the Builder API backend. Do not convert demonstration balances into real credit.

## Local validation

`scripts/verify.sh` passed on 2026-09-10, including brand invariants, Go vet,
model tests, billing coverage gates, frontend typecheck/format/license checks,
373 frontend tests and frontend/Go builds. The controller full suite and the
focused Builder account/projection/assertion tests also passed. No production
configuration, live user grant or Stripe payment was changed.

## Team launch grant

Builder's backend asserts active product ownership in the signed claim request.
A browser cannot choose the owner, grant amount or group. The active configured
offer must match the original identity's program/customer, have grant quota
equal to USD 10 in quota units, and have remaining capacity.

A transaction locks the offer, identity, user and enrollment, consumes one
program slot, credits the owner's existing billing entity and records the quota
and timestamp on BuilderIdentity. A unique subject and unique user prevent
cross-product and concurrent duplicate grants. Prior partnership signup grants
also count as claimed. Failed quota writes roll back the capacity and receipt.
Changing server configuration never creates another identity or receipt.

The workspace credit feed includes launch receipts (negative identity IDs) and
Stripe top-ups (positive IDs). Membership and product transfers do not transfer
the owner's balance or reveal their key to another user.

Focused tests cover concurrent claims, exhausted capacity, previous grants and
transaction rollback, in addition to existing provisioning and isolation tests.
No live payment or production grant was issued during implementation.
