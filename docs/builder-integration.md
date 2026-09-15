# Builder API integration bridge

Release scope: the opt-in v1 account/grant bridge using either an exact Program name from the signed request (its default customer) or a configured legacy customer registration code. The program/team target below is future work, not part of this release. Builder must document this limitation in its integration PR before enabling the bridge.

## Clarified target: program, team and customer group

Status: exact Program name selection is implemented; per-team customer mapping remains future work.

UnifyAPI must already contain the named partnership program. For local testing,
the program name is `Builder Local Test`. Builder's server will supply that
configured program name together with verified user information and authoritative
team information (stable IDs, display names, owner ID and membership role).

Each Builder team maps to a distinct customer and Pricing Group under that
program. A repeated connection reuses the mapping; names are display metadata,
not identity keys. Products with the same owner form one team. Adding products
or renaming a team does not create another grant allowance.

A Pricing Group controls pricing/model access; it does not by itself establish
a shared balance or permission to spend the owner's funds. The confirmed grant
rule remains USD 10 once per team, shared across the owner's products.

The bridge links individual users to the configured offer. Name mode selects the
existing Program default customer. It does not transmit a team object or provision
a distinct customer/Pricing Group for each team.
Do not describe this v1 release as a completed implementation of that target.

### Remaining implementation and product decisions

- Define and validate the signed user/team/program contract on both servers.
- Persist an idempotent team-to-customer/Pricing Group mapping.
- Decide whether users belonging to multiple teams select an active team.
- Decide whether ordinary members may view or spend team credit, or only owners.
- Preserve existing account funds and require an explicit policy for group moves.
- Verify two-team isolation, rename/retry stability and shared-grant uniqueness.
- Complete real provider and Stripe test-mode integration validation before claiming end-to-end integration works.


The bridge provides server-authenticated account provisioning/connection,
account and request projections, a dedicated inference key and the existing
Stripe checkout, plus a transactional $10 team launch grant. All products with
the same owner form one team and share that owner's account balance. The
Builder subject is the durable team identity; products cannot reset the grant.

## Trust boundary

Builder's API verifies its normal Firebase session and verified email. It signs
`POST\n/path\nunix_timestamp\nexact_body` using HMAC-SHA256, sending the signature
as hexadecimal in `X-Builder-Signature` and the time in `X-Builder-Timestamp`.
Assertions expire after 30 seconds. The HMAC credential authenticates Builder's
backend; never expose it to the browser. The trusted backend selects an existing
Program in the signed request, and therefore this credential can select any
valid Program in the database. It is not a credential scoped to one Program.

For name mode, set only `BUILDER_INTEGRATION_SECRET` (minimum 32 characters) on
UnifyAPI. Send, for example:

```json
{"subject":"<builder-user-id>","program_name":"Builder_hub_2026_Sep_Batch","range":"30d"}
```

`BUILDER_INTEGRATION_PROGRAM_NAME` is no longer read or required on UnifyAPI;
any old value has no effect. Program selection is business data resolved against
the database, not a deployment-level name match. Builder may retain its own
`BUILDER_UNIFY_PROGRAM_NAME` setting to construct the signed request.

Legacy code mode remains compatible: set `BUILDER_INTEGRATION_PARTNERSHIP_CODE`
and send the matching `partnership_code`. This legacy setting does not restrict
name-mode requests. Send exactly one nonempty selector; missing/both selectors,
invalid names, missing/short secrets, invalid signatures and legacy code
mismatches return HTTP 401 `UNIFY_UNAUTHORIZED`. There is no name/code fallback
or lowercasing. Names are 1–120 Unicode code points, unchanged by JavaScript
`trim()`, without Unicode Cc controls; internal spaces and case are preserved.

Name lookup compares exact strings after the database query, independently of
case/accent-insensitive collations. It requires exactly one matching Program
(including disabled matches in ambiguity checks), an active date window and
exactly one non-removed, enabled default customer. Missing/duplicate/inactive
Programs or unusable defaults return HTTP 409 `UNIFY_PROGRAM_UNAVAILABLE`.
All name-mode actions validate this offer; existing identities cannot switch
Program/customer when the request selects a different Program. Name resolution
never creates a Program/customer and does not fall back to a public registration code.

Production Builder origin is `https://app.unifyapi.ai` (no `/v1` suffix);
inference base is `https://app.unifyapi.ai/v1`, and the signed bridge is
`https://app.unifyapi.ai/api/builder/v1/{action}`. Deploy this backend before
switching Builder to name mode; include the selector in the signed request
without changing the shared HMAC protocol. Existing identities selecting the same
default customer reuse their account/key and cannot claim another grant.
No schema migration or automatic production configuration is included.

The existing global API rate limit still applies; reads do not consume the
shared login/registration critical-rate bucket.

Optional `BUILDER_INTEGRATION_RETURN_URL` controls the return from Stripe.
It must also pass the existing payment redirect allowlist. Checkout and its
signed webhook remain owned by the existing Stripe implementation.

## Account behavior

New accounts get the selected Program default customer group and zero initial
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
A browser cannot choose the owner, grant amount or group. The active selected
offer must match the original identity's program/customer, have grant quota
equal to USD 10 in quota units, and have remaining capacity.

A transaction locks the offer, identity, user and enrollment, consumes one
program slot, credits the owner's existing billing entity and records the quota
and timestamp on BuilderIdentity. A unique subject and unique user prevent
cross-product and concurrent duplicate grants. Prior partnership signup grants
also count as claimed. Failed quota writes roll back the capacity and receipt.
Changing the requested Program never creates another identity or receipt for
an already linked subject.

The workspace credit feed includes launch receipts (negative identity IDs) and
Stripe top-ups (positive IDs). Membership and product transfers do not transfer
the owner's balance or reveal their key to another user.

Focused tests cover concurrent claims, exhausted capacity, previous grants and
transaction rollback, in addition to existing provisioning and isolation tests.
No live payment or production grant was issued during implementation.

## Limited v1 release boundary

Only `/api/builder/v1/:action` is implemented. There is no v2 endpoint, per-team customer provisioning, member wallet sharing, multi-team selection, or automatic account/group/fund migration. Builder must assert verified identity and owner eligibility server-side; it must not share an owner credential with ordinary members. Grant uniqueness is the linked owner subject/user, shared by that owner's products.

Stripe availability and checkout use the existing full payment configuration/compliance gate. A missing or short `BUILDER_INTEGRATION_SECRET` keeps the bridge disabled. An installation with a valid secret can accept signed name-mode requests after this upgrade, even if neither selector environment variable is configured. Legacy code mode still requires its configured code. Deployment does not confirm payment compliance. The additive identity/grant receipt table must be retained on rollback; disabling the bridge does not reverse grants or revoke issued inference keys. Revoke those keys separately if required.

Program-name change validation (2026-09-14): focused Builder and Partnership
model/controller tests passed, plus `go vet ./model ./controller`. Coverage
includes signed selector/config conflicts, Unicode length/whitespace validation,
exact-name and code collisions, missing/duplicate/inactive programs, default
customer rejection, cross-mode identity reuse, preservation of existing funds
and group, and rollback/retry without duplicate launch grants. Tests use SQLite;
MySQL/PostgreSQL runtime validation and live Stripe/provider checks were not run.


## Request-selected Program correction (2026-09-15)

UnifyAPI now resolves the signed `program_name` from the database without an
UnifyAPI Program environment variable. The existing HMAC secret remains the
server trust boundary. No tables, grants, existing account groups or deployment
configuration are changed. Reverting to the previous application restores its
selector-environment requirement, so name-mode calls can return 401 again on a
secret-only installation; existing enrollments and keys remain stored.

Regression coverage includes secret-only workspace/connect requests, persisted
Program/default-customer enrollment, retry idempotency, signed-body tampering,
exact-name rejection and protection against switching an existing identity's
Program. Local tests do not establish a hosted release or a paid provider/Stripe
transaction.
