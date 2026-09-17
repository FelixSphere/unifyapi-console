# Builder customer provisioning and account repair

The v1 path and HMAC contract remain unchanged. The signed `program_name` and
optional `customer_name` are exact names; request or account errors must not be
worked around by changing these fields.

## Stable responses

| HTTP | Code | Meaning and caller action |
| --- | --- | --- |
| 409 | `UNIFY_ACCOUNT_REPAIR_REQUIRED` | The subject has a saved identity but its user is missing or soft-deleted. Contact an operator. Do not offer another connect, retry a grant, change Program, or create another account. |
| 503 | `UNIFY_INTEGRATION_UNAVAILABLE` | Storage or provisioning failed. Retry later or contact support; this does not mean the Program name is wrong. Internal database details are not exposed. |
| 409 | `UNIFY_CUSTOMER_UNAVAILABLE` | The named live customer is disabled or ambiguous. Operators must resolve it; reconnect never re-enables a disabled customer. |

A genuinely unseen subject still returns `200 {"connected":false}` from
`workspace`. Account repair applies to connect, workspace, claim, key,
credit-status and checkout. Checkout keeps the existing Stripe-availability gate:
when payments are unavailable it returns `503 UNIFY_STRIPE_UNAVAILABLE` first.

## Reusing a removed customer's display name

Removed customer rows retain their codes and settlement groups, including the
existing database uniqueness constraints. Creating a new customer with that
same display name allocates a new ID, code and unoccupied group. It does not
reactivate or rename the removed customer, move its enrollments, transfer its
wallet, or rewrite historical invoices. A same-named customer in another Program
is never a fallback target.

The final chosen group is used consistently in `GroupRatio`, `TopupGroupRatio`
and `UserUsableGroups`. All three durable settings and the customer insert share
one transaction and the existing database integrity lock. Failed provisioning
rolls them all back. Pricing audit/cache publication happens after commit;
existing negotiated values are preserved. New groups do not automatically gain
provider channel capacity; an operator must configure availability as for any
new pricing group.

## Recovering a deleted linked account

This release deliberately does not automatically restore deleted accounts.
Deletion may have been intentional; a missing user does not authorize discarding
its Builder identity, payment history, enrollment or grant receipt. In particular,
a surviving tenant can still hold paid credit even when it has no members.

An operator must first verify the external subject and email, review who deleted
the user, check all surviving balance/payment/grant records, and obtain explicit
authorization to restore that account. Prefer restoring the exact user row from
a trusted backup. If none exists, reconstruction requires a separately reviewed
plan: preserve user/tenant IDs and payment references, reject identity/email
collisions, issue fresh credentials only, preserve existing grant receipts, and
record a bounded transactional audit plus rollback evidence. Do not infer an old
password, reuse a revoked key, repay a top-up, or grant launch credit again.

## Release and verification

No schema migration, seed or new environment variable is required. The UnifyAI
CI/CD agent releases the merged code. Builder should understand both new codes
before enabling its revised error UI. A healthy build alone is not an integration
acceptance test: exercise signed requests through the HTTP layer and check the
actual connect/workspace response. Do not claim a deleted account can reconnect
until its separately authorized recovery and normal Builder flow have passed.

Regression fixtures live in `model/builder_recovery_test.go` and
`controller/builder_recovery_test.go`: removed-key collision with unchanged
history, concurrent idempotent creation, occupied suffixes, disabled/ambiguous
customers, transaction rollback, absent/soft-deleted user, retained paid balance,
no repeated grant, and signed HTTP error classification.

An explicitly approved fresh-account recovery may archive the old identity
under a unique `unifyapi:archived:` subject while retaining the old user reference
and receipts. This prefix is rejected by signed assertions and the model's
connect/read/claim functions; it can never be used as a new external identity.
Such a recovery still requires the guarded snapshots, balance authorization and
rollback checks above. The original verified Builder subject reconnects through
the normal flow; no old credentials or administrator privileges are restored.
