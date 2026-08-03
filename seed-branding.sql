-- UnifyAPI console branding seed.
--
-- These five options cannot be set by environment variable -- new-api reads them
-- from the `options` table only. Without them the console renders as "New API":
-- the static <title> in web/index.html is overwritten from GET /api/status as
-- soon as the SPA boots.
--
-- Checked in so a fresh environment is reproducible. Editing branding directly
-- in a production database and nowhere else is how this becomes undocumented
-- tribal knowledge.
--
-- Usage (SQLite and PostgreSQL share this ON CONFLICT syntax):
--   sqlite3  /data/one-api.db          < seed-branding.sql
--   psql "$SQL_DSN"                    -f seed-branding.sql
-- For MySQL, replace each `ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`
-- with `ON DUPLICATE KEY UPDATE value = VALUES(value)`.
--
-- IMPORTANT: options are cached in process memory at boot (InitOptionMap), so
-- restart the container after seeding, or seed before first start.
--
-- The `About` and `Footer` values carry licence obligations -- see BRANDING.md
-- before editing them. Do not remove the upstream link or the attribution notice.

INSERT INTO options (key, value) VALUES
  ('SystemName', 'UnifyAPI')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('Logo', '/logo.png')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Footer. The "Source code" link is what discharges AGPL s.13 for users of the
-- hosted service. Setting Footer takes the footerHtml branch of footer.tsx,
-- which still renders <ProjectAttribution inline /> -- so this does not weaken
-- the upstream attribution.
INSERT INTO options (key, value) VALUES
  ('Footer', '<div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-1">'
    || '<span>&copy; 2026 UnifyAPI</span>'
    || '<a href="https://www.unifyapi.ai" target="_blank" rel="noopener noreferrer">unifyapi.ai</a>'
    || '<a href="https://www.unifyapi.ai/privacy" target="_blank" rel="noopener noreferrer">Privacy</a>'
    || '<a href="https://www.unifyapi.ai/terms" target="_blank" rel="noopener noreferrer">Terms</a>'
    || '<a href="https://github.com/FelixSphere/unifyapi-console" target="_blank" rel="noopener noreferrer">Source code</a>'
    || '</div>')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- About: about UnifyAPI, the product.
--
-- MARKDOWN, NOT HTML, and that matters. features/about/index.tsx branches on
-- isLikelyHtml() (lib/content-format.ts): any HTML tag routes the content to
-- PublicLayout showMainContainer={false} + RichContent htmlVariant='isolated',
-- which has no container padding (so it renders under the fixed header) and does
-- not receive the prose typography. The markdown branch gets
-- `mx-auto max-w-6xl px-4 py-8` and prose styling. Keep this tag-free.
--
-- On the licence: the prominent AGPLv3 s.7(b) attribution and the required link
-- to the upstream project are carried by the always-mounted footer bar on every
-- route (web/src/brand/upstream-attribution.tsx), which is what satisfies that
-- obligation. This page therefore does not repeat it at length.
--
-- The closing line stays. It is the AGPLv3 s.7(c) marking of changes, which is
-- required of a modified version. The s.13 source offer is not duplicated here
-- because the `Footer` option carries a "Source code" link on every page --
-- if that Footer link is ever removed, this line must regain it. See BRANDING.md.
INSERT INTO options (key, value) VALUES
  ('About', '## UnifyAPI

One API key for 300+ AI models across every major provider. One endpoint, one bill, and no per-provider contracts.

### What you get

- **One endpoint.** UnifyAPI speaks the OpenAI API, so existing code works by changing the base URL and the key.
- **One bill.** Pooled capacity across providers instead of separate contracts and idle commitments.
- **Automatic failover.** A single model name keeps working through an upstream provider incident.
- **Smart routing.** Requests go to the cheapest model that still clears your quality bar.
- **One dashboard.** Keys, spend, latency and usage for your whole team in one place.

### Get started

Read the [documentation](https://www.unifyapi.ai/docs), or create a key and make your first call in under a minute.

---

Built on [New API](https://github.com/QuantumNous/new-api) ([AGPL-3.0](https://github.com/QuantumNous/new-api/blob/main/LICENSE)), modified by FelixSphere: branding, typography, light-only theme, and a multi-tenant account model.
')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Docs link. Upstream defaults this to https://docs.newapi.pro, which sends our
-- customers into another product's documentation. It drives the console's top-nav
-- "Docs" entry (web/src/hooks/use-top-nav-links.ts) and the landing hero CTA.
INSERT INTO options (key, value) VALUES
  ('general_setting.docs_link', 'https://www.unifyapi.ai/docs')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- HomePageContent replaces the whole landing page. Safe now that PublicLayout
-- carries the attribution unconditionally -- before that change, setting this
-- silently removed the only attribution in the product.
-- Left empty here so the stock landing page shows until marketing content is
-- ready; www.unifyapi.ai is the real landing page and app.unifyapi.ai users
-- arrive already deep-linked to /sign-in or /register.
INSERT INTO options (key, value) VALUES
  ('HomePageContent', '')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
