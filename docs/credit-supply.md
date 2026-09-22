# Credit supply

The credit supply is the **supply side** of the console. Third parties who hold
vendor credits (OpenAI, Anthropic, Google, OpenRouter, ...) let us consume them
and we route customer traffic through their key. There is one deal:

- **Hand us the key.** Nothing is paid up front; the owner keeps a posted
  share of what the credits sell for, settled as it builds up.

Operators manage it in **System Settings → Billing → Credit Supply**;
contributors see their own slice in the **Supplier portal**.

Payment in **platform credit** is booked here, into the payee's wallet, inside
the transaction that records it. An **external** transfer is recorded, not
moved: the operator has already sent it and enters the reference.

## Vocabulary

| Term | Meaning |
|---|---|
| **Supplier** | A counterparty we buy credits from. Settled under `supplier:<code>`. |
| **Lot** | One tranche of credits from one supplier for one vendor, bound to one channel. |
| **Face value** | What the lot is worth at the vendor's **official list price**. This is the denomination the vendor's own balance decrements in, so it is what we draw down. |
| **Acquisition rate** | What we pay per $1 of face value, in (0, 1]. 0.45 means we buy at 45 cents on the dollar. 0 is legal only on a contributed key, which is not bought at all. |
| **Payable** | `consumed face value × acquisition rate − already paid`. What we *still* owe. A lot bought outright is paid in full at activation, so it is 0 however much of it is consumed; only operator-entered lots that were never paid up front accrue here. |
| **Deal** | `purchase` (bought outright, one payment) or `revenue_share` (contributed, paid a dividend as it earns). See "Contributed keys" below. |

## How a lot flows

```
pending ──approve──▶ active ──(consumed ≥ face)──▶ exhausted
   │                   │  ▲                             │
   └──reject──▶ rejected │  └──reactivate──────────────┘
                       │
                       ├──(now ≥ expires_at)──▶ expired ──reactivate (after edit)──▶ active
                       │
                       └──suspend──▶ suspended ──reactivate──▶ active
```

- **Admin-created lots** may be born `active`. **Supplier-submitted lots** are
  born `pending`; the channel they create is disabled until approval.
- A channel backs **at most one live lot** (pending/active/suspended). Rebinding
  is refused while another live lot holds the channel.
- Activating a lot writes its acquisition rate into `ChannelCostRatio` for the
  bound channel. That is the whole integration with pricing: reconciliation,
  Profit and vendor Settlement all read that ratio already, so supplier traffic
  is costed correctly with no second model. Retiring a lot leaves the ratio in
  place, because issued statements were priced with it.
- **Exhaustion and expiry are enforced at consume time.** Every consume log
  with a channel bound to an active lot draws the lot down by the request's
  list-price cost (`ratio_setting.ListPriceUSD`). When consumed reaches face
  value, or the expiry passes, the lot is retired and the channel is
  auto-disabled — the same mechanism a failing upstream triggers. The
  transition is a conditional `UPDATE … WHERE status = 'active'`, so two
  concurrent requests cannot both retire the lot or double-notify.
- A request on a model the catalogue cannot price draws **nothing** and
  increments the lot's `unpriced_requests`. The lot is then understated until
  the model is catalogued; the admin screen flags it.
- `low_water_usd` fires one notification when remaining falls to or below it.
- A draw-down that finds the lot no longer active -- retired or suspended on
  another instance while this one still had it cached -- draws nothing, and
  writes nothing to the daily ledger either. `credit_lot_usages` and
  `consumed_usd` are two views of the same traffic and have to agree.

## Settlement

Supplier-backed channels are attributed to the supplier, not to the vendor host
the channel talks to (`service.UpstreamVendor` consults
`model.LookupChannelSupplier` first). The existing vendor settlement screen
therefore lists suppliers as counterparties with the correct modelled payable,
and issuing a statement freezes it exactly as for any vendor.

## Caches and multi-instance

The relay hot path caches channel → lot for 30 s and the channel → supplier
index for 60 s. Writes invalidate the local instance immediately; other
instances converge within the TTL. A newly approved lot can therefore be
un-drawn for up to 30 s on a sibling instance. Acceptable for accounting at
this grain; noted so nobody hunts it as a bug.

## API

Root-only, under `/api/credit-supply`:

- `GET /overview` — pool totals, per-vendor breakdown, items needing attention.
- `GET|POST /suppliers`, `PUT /suppliers/:id`
- `GET /lots?supplier_id=&status=`, `POST /lots`, `PUT /lots/:id`
- `POST /suppliers/:id/share-payout` `{ "method": "platform_credit|external", "reference": "...", "force": false }`
- `GET /share-payouts?supplier_id=&lot_id=`
- `POST /lots/:id/transition` `{ "to": "active|suspended|rejected" }`
- `GET /lots/:id/usage?days=30` — daily draw-down.

Supplier portal (authenticated user linked to a supplier), under
`/api/supplier`:

- `GET /me` — profile, lots, totals.
- `POST /lots` — submit a lot with an upstream key; creates a disabled channel
  tagged `supplier:<code>` and a `pending` lot.
- `GET /usage?days=30`, `GET /statements`.

## Selling credits: one deal, a share of what they sell for

There is no application, no negotiation and no purchase. **Credit Supply** is
one sidebar entry for everyone: an ordinary login lands on the seller's page, a
super admin lands on the operator's page (`web/src/features/supplier-portal/hub.tsx`).

A seller hands us a vendor API key. We route paying customer traffic through
it, draw the credits down at the vendor's list price, and the seller keeps a
**posted share of what those credits sold for** — settled as it builds up,
for as long as the key earns. Nothing is paid up front, so nothing is at risk
for us if a key never serves a request, and nothing is capped for the seller
if it serves a great many.

The operator posts the terms once, in Billing → Credit Supply → *Supply terms*
(option `CreditSupplyTerms`, `model/credit_supply_terms.go`):

| term | default | meaning |
|---|---|---|
| `revenue_share_rates.<vendor>` | 0.50 | the seller keeps this fraction of what their credits sell for (五五分) |
| `revenue_share_basis` | `revenue` | what the share is a share **of** — see below |
| `min_share_payout_usd` | 20 | a balance waits until it reaches this before it is paid |
| `min_face_usd` | 100 | smallest balance we take on |

Clearing every vendor share pauses intake without deleting the terms: sellers
then see no vendors and every submission is refused.
| `channel_priority` | 10 | supplier channels outrank our own accounts (0) so they drain first |
| `manual_review` | false | keep a verified key disabled until an operator accepts it |

Every term is editable on the screen and takes effect for the next
submission. A lot **snapshots** its share and basis at submission, so changing
the terms never reprices a key that is already earning. A vendor with no share
is refused, not taken at 0.

**Absent and empty are different.** A term the stored option does not mention
inherits the default; a map the operator has emptied stays empty and closes
that offer. The distinction is the difference between a working rollout and a
dead one: every `CreditSupplyTerms` row written before revenue share existed
omits `revenue_share_rates`, and reading that as "switched off" would have
served zero vendor cards to every seller. Production carried exactly such a
row. Pinned by `TestALegacyTermsRowDoesNotShipTheFeatureInert`.

**A threshold that can only be a typo is repaired, not obeyed.** `min_face_usd`
and `min_share_payout_usd` above $1,000,000 fall back to the default with a
loud log, because an absurd minimum passes every validation rule and then
blocks every submission in silence. The repair is per field: one bad threshold
must not reject the whole document and take the operator's real buy rates down
with it. The same row on production carried a $100bn minimum sale.

### What the share is a share of

Everything customers **actually paid** for traffic the key served, in dollars.
This is the figure the seller sees as *Sold for*, can check against their own
vendor dashboard's consumption, and is paid on.

Grant-funded and unbilled traffic (promotional credit pools) pays the customer
nothing, so it would earn the seller nothing while drawing their credits down.
Supplier channels are therefore **kept out of every promotional pool's routing
group** (`allCustomerGroups` in `model/credit_supplier_portal.go`). Traffic
paid with sign-up credit still counts as revenue: the customer's wallet was
debited, and that gift is our marketing cost, not the seller's.

### The seller's flow

1. **Payout account first.** Before anything else the seller files where their
   share goes (`PUT /api/supplier/payout-account`): platform credit (the
   wallet behind their login), or bank / PayPal / Wise / crypto with the
   account holder and details in their own words, plus a currency. Nothing is
   paid up front, so the account has to be on record before the first dollar
   is owed. A submission without one is refused with
   `code: payout_account_required`, which the page turns into the account form.
2. **Submit the key.** Vendor (the share is shown before anything is typed),
   the credit balance on the key at list price, optional expiry, the API key
   (write-only), optional model narrowing (never beyond our catalogue), and the
   right-to-transfer attestation. The seller names no price and chooses no
   deal.
3. **Verified on the spot.** The server creates the supplier record on first
   contact, a **disabled** channel carrying the key and a `pending` lot, then
   makes one real request through the key against the cheapest catalogue
   model. A failure is answered immediately with the vendor's error, the lot
   becomes `rejected` and the key is deleted — no operator round trip. The
   verification is **not consumption** (kept out of the consume log) but its
   list-price cost is booked on the lot.
4. **In service at once.** A verified key is activated automatically: channel
   enabled at supplier priority for every customer pricing group, the share
   written into `ChannelCostRatio` as the channel's cost basis (see below).
   With `manual_review` on, it waits `verified` until an operator accepts it.
5. **Watch it earn.** The seller's page shows, per lot and in total: drawn
   down at list price, **sold for** (revenue), their share earned, paid so far
   and unpaid; a 30-day chart of draw-down and revenue; and every payout.

```
pending ──verify ok──▶ verified ──auto (or accept)──▶ active ──▶ exhausted | expired
   │                      │                              │
   └──verify failed───▶ rejected ◀───────reject──────────┘   active ⇄ suspended
```

### What the seller can check for themselves

The one thing a seller cannot verify is the revenue: our price list, our
customers. What they **can** verify is the usage that revenue is computed
from, and that is the number they actually worry about.

`GET /api/supplier/usage/detail` reports their traffic **in the shape their
own vendor console reports it**: day, model, input / cached / output tokens,
the list-price value of it, what it sold for, and their share.
`GET /api/supplier/usage/export` hands over the same rows as CSV for diffing
against a vendor export. Both are scoped to the channels their own lots are
bound to, and neither carries a user id or username: a seller audits their
usage, never our customers.

If the token counts agree with their console, our consumption figure is
honest, and the revenue follows from it by arithmetic against a published
price list. If they ever disagree, the seller holds the strongest remedy
there is, and the screen says so: **the key is theirs**. They can cap the
spend or revoke it in their own vendor account at any moment.

### The operator's flow

- **Supply terms** — post and adjust the share per vendor, the basis, the
  minimum payout, the minimum balance, channel priority and manual review.
- **Suppliers** — each seller with lots, drawn down, **revenue share owed**,
  and their payout account (method and masked details in the table, in full in
  the edit dialog: root is the one who has to send the money). **Pay revenue
  share** settles everything a seller is owed across all their lots in one
  transaction (`POST /api/credit-supply/suppliers/:id/share-payout`): platform
  credit lands in their wallet inside it; an external transfer needs the
  reference the seller can look up. Below the posted minimum it waits unless
  `force` is set.
- **Lots** — every key with draw-down, sold-for, share, expiry and health; the
  usual suspend / reactivate / reject with reasons; history per lot. Lots the
  operator enters by hand may still be outright purchases at the buy rates,
  paid with *Pay & activate*; sellers never see that deal.
- Suppliers are never paid per period from Settlement: `IssueSettlement`
  refuses a vendor statement for a `supplier:*` counterparty.

### How contributed credits are used and costed

- Priority `channel_priority` (default 10) beats our own accounts, so the
  router drains contributed credits first; within the tier the usual weights
  apply. The channel is offered to every customer pricing group except
  promotional pool groups.
- Draw-down is at the vendor's list price on every consume log; the request's
  revenue (the quota the customer was charged) accrues on the lot in the same
  statement. Exhaustion or expiry retires the lot and disables the channel.
- **Cost basis.** A share is a fraction of *revenue*, not a multiple of list
  price, and `ChannelCostRatio` is a multiple of list. Activation writes the
  **share itself** as the channel's ratio: `share × list` overstates the true
  cost by exactly the customer discount and never understates it — the
  conservative direction — and is far closer than the alternative of costing
  the channel at full list, which would show every share request as 0% margin.
  What is actually owed is exact on the lot (`UnpaidShareUSD`); do not add the
  two together.

## Audit trail and attestations

Every lot carries an append-only history (`credit_lot_events`): created,
edited, each operator transition with its reason, and automatic retirement.
`GET /api/credit-supply/lots/:id/events` reads it; the supplier portal shows
the public part of it.

Two attestations are recorded on the lot itself:

- **Supplier attestation** at submission (`attestation_version`, `attested_at`,
  `attested_by`). When an operator enters a lot on the supplier's behalf, the
  operator is recorded as the attesting party.
- **Operator confirmation** at approval (`approved_by`, `approved_at`). The
  server refuses `pending → active` without `transfer_rights_confirmed: true`;
  the screen cannot skip the question.

Rejecting or suspending requires a `reason`, stored as `status_reason` and
shown to the supplier. Free-text fields (notes, payout terms, reasons) refuse
anything that looks like a vendor API key — keys belong on the channel.

## Superseded: the credit-contribution module

PRs #56/#59 (`credit_contribution.go`, `/api/credit-contribution/*`, the Wallet
"supplier offer" card and the `/ops` review screen) implemented the same supply
side against promotional-pool inventory. They were consolidated into the
credit supply so there is one supplier record, one lot ledger and one payable.
What carried over: the no-credential application step, per-lot audit events,
versioned attestations, reasons on every decision, and the secret-marker guard.

## Relationship to promotional credit pools

`model/credit_pool.go` (PR #53) is the **customer** side: tenants receive
promotional grants and eligible requests are routed through a pool's channel
group and funded by reservations. It is a separate product and is untouched by
the credit supply.

The two meet at the channel. A supplier-backed channel can be placed in a
promotional pool's routing group like any other channel; its cost is then
already correct because activation wrote the acquisition rate into
`ChannelCostRatio`, which the pool's reservations read. The pool's own
`contributed` inventory source and `accrued_payable_quota` are superseded by
supplier settlement here and should not be used to record what we owe a
supplier — one payable, one place.

## Compliance note

Vendor terms commonly restrict transferring or reselling promotional credits.
The pool records who supplied each lot and under what terms so the operator can
evidence provenance; it does not and cannot establish that a supplier had the
right to sell. Confirm that before approving a lot.

## Withdrawn: buying credits outright

Until 2026-09-22 an operator could also **buy** a seller's credits at a posted
rate: one payment, made before a single request went through the key. It is
gone, and `ErrCreditLotBuyOutWithdrawn` refuses it by name so an old caller is
told what happened rather than quietly getting a different deal.

The reason is the risk sat entirely on our side. We paid cash against a face
value only the seller could see, on a key they could revoke the next minute,
for credits that might expire before they were drawn. Nothing about it was ever
offered to a seller either: the submission path always forced a revenue share.
Production had zero bought lots when it was removed, so nothing was migrated.

`credit_lots.deal_type` and the columns that served the purchase
(`paid_usd`, `paid_at`, `paid_by`, `payout_reference`, `payout_quote_multiplier`)
are left on the table, unread. Dropping a column earns nothing and costs a
migration.
