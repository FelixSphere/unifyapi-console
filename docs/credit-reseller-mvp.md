# Credit reseller MVP

## Outcome

Let a customer tenant offer lawfully controlled provider credits to UnifyAI,
let operations verify and activate those credits as contributed inventory in an
existing credit pool, and let that tenant track consumption, earnings, and
recorded payouts.

This extends the existing credit-pool ledger. It does not replace tenant cash,
grants, channel routing, or provider accounting.

## P0 user stories

- As a tenant owner, I can submit an OpenAI, Anthropic, or other provider-credit
  offer with face value, model scope, requested acquisition ratio, and an
  authorization attestation.
- As an operator, I can request credentials through the approved secret-sharing
  process, verify the provider account, and bind an enabled channel in the
  pool's routing group.
- As an operator, I can activate verified credits. Activation creates a
  contributed pool lot; customer grants and normal pool routing decide who may
  consume it.
- As an operator, I can reset a renewable provider balance. Resetting disables
  the previous lot and creates a new cycle instead of rewriting history.
- As an operator, I can revoke future use without changing already-settled
  consumption or earnings.
- As a supplier, I can see inventory remaining, settled consumption, lifetime
  payable, committed payouts, and available payable for my tenant.
- As an operator, I can create, approve, mark paid, or void payout records.

## Non-goals for the MVP

- No provider credential is accepted by the contribution API or stored on the
  contribution. Existing channels remain the credential boundary.
- No automated bank, card, crypto, or marketplace payout is initiated.
- No transferability promise is made for a provider's promotional credits.
  Suppliers attest that they own or control the account and are authorized to
  let UnifyAI use and resell its capacity; operations remains responsible for
  provider-policy review.
- No customer chooses an individual supplier. Customers receive a grant to a
  pool; the existing routing and lot-allocation order consume eligible inventory.

## Lifecycle

```text
submitted -> needs_credentials -> verifying -> active -> revoked
     |               |                |
     +-------------> rejected <-------+
     |
     +-------------> cancelled

active -- reset --> active (new immutable cycle/lot)
```

`expired` and `exhausted` are effective display states derived from the active
cycle. A later verified reset can replenish the contribution.

## Accounting invariants

1. Tenant cash, customer promotional grants, and provider pool inventory are
   three independent meters.
2. A contribution becomes routable only after root activation and only through
   an enabled channel in the selected pool's routing group.
3. Earnings are calculated only from settled reservation allocations:
   `allocated provider quota × the immutable lot acquisition ratio`.
4. Draft and approved payouts reserve payable. Paid payouts are final; they
   cannot be voided. A later usage correction appears as a positive or negative
   balance for the next reconciliation rather than changing a paid record.
5. Reset and revoke disable inventory but never delete lots, allocations,
   lifecycle events, or payouts.

## API surface

Customer tenant:

- `GET /api/credit-contribution/self/`
- `POST /api/credit-contribution/self/`
- `POST /api/credit-contribution/self/:id/cancel`

Operations:

- `GET /api/credit-contribution/` (admin)
- `POST /api/credit-contribution/:id/review` (root)
- `POST /api/credit-contribution/:id/activate` (root)
- `POST /api/credit-contribution/:id/reset` (root)
- `POST /api/credit-contribution/:id/revoke` (root)
- `POST /api/credit-contribution/:id/payouts` (root)
- `POST /api/credit-contribution/payouts/:payout_id` (root)

All mutations are transactionally recorded in the contribution event timeline;
root mutations also use the existing operations audit log.

## Delivery slices

1. Backend lifecycle and accounting: additive tables/fields, APIs, authorization,
   reset/revoke semantics, payout ledger, and model/controller tests.
2. Supplier and operations UI: offer form and status/earnings view for customers;
   review, activation, reset/revoke, and payout controls for root operators.

## Acceptance criteria

- A member of a tenant cannot submit or cancel an offer on behalf of its owner.
- Obvious provider secrets are rejected from all supplier-entered text fields.
- Activation fails when the channel is disabled or outside the pool routing group.
- Reset creates a new lot and leaves the old lot's quota and allocations intact.
- Revocation immediately removes the contribution's remaining inventory from
  pool routing.
- Two payout drafts cannot reserve more than the currently available payable.
- One tenant never sees another tenant's contribution data.
