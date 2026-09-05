# Credit supply

The credit supply is the **supply side** of the console. Third parties who hold
vendor credits (OpenAI, Anthropic, Google, OpenRouter, ...) sell us the right to
consume them; we route customer traffic through their key and pay them for what
was consumed. Operators manage it in **System Settings → Billing → Credit
Pool**; suppliers see their own slice in the **Supplier portal**.

Nothing in the pool moves money. Payouts are recorded as vendor settlements and
executed outside the system.

## Vocabulary

| Term | Meaning |
|---|---|
| **Supplier** | A counterparty we buy credits from. Settled under `supplier:<code>`. |
| **Lot** | One tranche of credits from one supplier for one vendor, bound to one channel. |
| **Face value** | What the lot is worth at the vendor's **official list price**. This is the denomination the vendor's own balance decrements in, so it is what we draw down. |
| **Acquisition rate** | What we pay per $1 of face value consumed, in (0, 1]. 0.45 means we buy at 45 cents on the dollar. |
| **Payable** | `consumed face value × acquisition rate`. What we owe the supplier. |

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
- `POST /lots/:id/transition` `{ "to": "active|suspended|rejected" }`
- `GET /lots/:id/usage?days=30` — daily draw-down.

Supplier portal (authenticated user linked to a supplier), under
`/api/supplier`:

- `GET /me` — profile, lots, totals.
- `POST /lots` — submit a lot with an upstream key; creates a disabled channel
  tagged `supplier:<code>` and a `pending` lot.
- `GET /usage?days=30`, `GET /statements`.

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
