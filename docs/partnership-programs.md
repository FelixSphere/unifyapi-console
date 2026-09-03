# Partnership programs

Partnership programs are reusable, country-independent registration offers.
Operators manage them in **System Settings → Billing → Partnership Programs**.

## Resource ownership

- A user group is an independent resource managed by Group Pricing. Its group
  multiplier and model pricing remain the only pricing source of truth.
- A partnership program stores only the identifier of an existing group. It
  does not copy a multiplier or model price.
- An enabled program prevents its referenced group from being removed. Disable
  the program or move it to another existing group first.

## Registration and connection

Send participants `/sign-up?partnership=<code>`. A newly provisioned account is
assigned the program's group. While grant capacity remains, registration also
atomically records an enrollment and applies the configured starting quota.
After the cap, registration still succeeds with the group and zero program
grant; normal top-up and usage billing apply from then on.

Connecting an existing account records a zero-grant enrollment and returns
`connected_existing`. It never silently changes that account's current group.
Use the existing explicit group-management flow if a group change is desired.

## API

- `GET /api/partnership/:code` — public registration-link validation.
- `POST /api/partnership/:code/connect` — connect the authenticated existing
  user without changing their group or granting registration credit.
- `GET /api/partnership/`, `POST /api/partnership/`,
  `PUT /api/partnership/:id` — root-only management endpoints.

Partnership programs do not modify Stripe checkout, wallet/top-up behavior,
request billing, or relay accounting.
