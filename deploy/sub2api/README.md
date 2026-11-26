# Sub2API in UnifyAPI

Run the pinned Sub2API stack with its own Postgres and Redis. Put the required
`SUB2API_ADMIN_EMAIL`, `SUB2API_ADMIN_PASSWORD`, `SUB2API_DB_PASSWORD`,
`SUB2API_JWT_SECRET` and `SUB2API_TOTP_KEY` values in a restricted `.env`.
Set `SUB2API_UPSTREAM_HOSTS` to the approved upstream domains and run
`docker compose up -d`. Keep the management port private. For operator access
use an SSH/SSM tunnel or a separate, access-controlled HTTPS origin. Configure
provider OAuth clients and redirect URIs in the Sub2API deployment as required.

Set the following on the UnifyAPI console:

- `UNIFYAPI_SUB2API_ADMIN_URL`: the management origin. Root operators see the
  Sub2API Accounts button in Channels and sign in to Sub2API to manage OAuth,
  accounts, group assignments, scheduling, and usage.
- `UNIFYAPI_SUB2API_GATEWAY_URL`: the private Sub2API gateway origin reachable
  from the console. Keep it on a private network or use HTTPS. Set
  `SUB2API_TRUSTED_PROXIES` to the console's address on that network so Sub2API
  can enforce client IP rules. Configure UnifyAPI's `TRUSTED_PROXIES` for its own
  ingress. Ingress must support streaming and WebSocket upgrades for this path.

Use the existing Sub2API channel type and the gateway's model Key to offer
standard model calls through UnifyAPI's usual routing and billing. Publish only
models with verified prices. Model discovery uses the models visible to that
Sub2API Key.

For the upstream protocol and task APIs, set the SDK's base URL to
`https://<unifyapi-host>/sub2api/v1` for OpenAI-compatible requests, or
`https://<unifyapi-host>/sub2api` for clients that add the version themselves.
Use a Sub2API model Key. Requests on this namespace use that Key's Sub2API
group, quota and billing. They don't draw from the UnifyAPI balance. Native
paths include OpenAI Responses, Claude Messages, Gemini v1beta, streaming,
realtime, async/batch images, video, voice and search as permitted by the
upstream platform and group. The administration API is on the private
management origin.

Existing channel settings can map model names and adapt request parameters.
They cannot register these routes or expose the OAuth and account management
screens. Both additions are opt-in and can be disabled by removing their
environment variables and restarting the console.
