# Partnership programs

Partnership programs are reusable, country-independent registration offers.
Operators manage them in **System Settings → Billing → Partnership Programs**.

## Resource ownership

- A user group is an independent resource managed by Group Pricing. Its group
  multiplier and model pricing remain the only pricing source of truth.
- A partnership program has one default customer group and may link additional
  customer groups. It does not copy a multiplier or model price.
- Each linked customer group is one company and invoice owner. Each gets a
  dedicated registration code while sharing the Program grant, cap, and active
  schedule.
- An enabled program prevents its active customer groups from being removed
  from Group Pricing. Remove the customer association, disable it, or move it
  to another existing group first.
- Removing a non-default customer group archives only the Program association
  and disables its registration link. Existing users, tenant balances,
  enrollments, usage, and invoice history remain unchanged. Change the default
  customer group through the Program settings instead of removing it.

## Registration and connection

Send participants `/sign-up?partnership=<code>`. A newly provisioned account is
assigned the program's group. While grant capacity remains, registration also
atomically records an enrollment and applies the configured starting quota.
After the cap, registration still succeeds with the group and zero program
grant; normal top-up and usage billing apply from then on.

The public `grant_available` field and signup banner are informational only.
They describe capacity at lookup time and never reserve a grant. The registration
transaction is authoritative, so concurrent users competing for the final slot
receive at most one grant.

Partnership signup does not stack the ordinary affiliate invitee/inviter rewards
on top of the Program grant. An inviter relationship may still be retained for
attribution, but the capped Program grant is the only signup credit on this path.

Connecting an existing account records a zero-grant enrollment and returns
`connected_existing`. It never silently changes that account's current group.
Use the existing explicit group-management flow if a group change is desired.

## API

- `GET /api/partnership/:code` — public registration-link validation.
- `POST /api/partnership/:code/connect` — connect the authenticated existing
  user without changing their group or granting registration credit.
- `GET /api/partnership/`, `POST /api/partnership/`,
  `PUT /api/partnership/:id` — root-only management endpoints.
- `POST /api/partnership-programs/:id/customers`,
  `PUT /api/partnership-programs/:id/customers/:customerId`, and
  `DELETE /api/partnership-programs/:id/customers/:customerId` — root-only
  customer-group association management.

Partnership programs do not modify Stripe checkout, wallet/top-up behavior,
request billing, or relay accounting.
