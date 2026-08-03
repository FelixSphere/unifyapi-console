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
-- On the licence: the prominent AGPLv3 s.7(b) attribution and the required link
-- to the upstream project are carried by the always-mounted footer bar on every
-- route (web/src/brand/upstream-attribution.tsx), which is what satisfies that
-- obligation. This page therefore does not need to repeat it at length, and the
-- long attribution block that used to live here has been cut down to the single
-- closing line below.
--
-- That line stays. It carries the s.7(c) statement of changes and the s.13
-- source offer, and both are obligations of running this as a hosted service.
-- Do not remove it -- see BRANDING.md.
INSERT INTO options (key, value) VALUES
  ('About', '<h2>UnifyAPI</h2>'
    || '<p>One API key for 300+ AI models across every major provider. One endpoint, one bill, and no per-provider contracts.</p>'
    || '<h3>What you get</h3>'
    || '<ul>'
    || '<li><strong>One endpoint.</strong> UnifyAPI speaks the OpenAI API, so existing code works by changing the base URL and the key.</li>'
    || '<li><strong>One bill.</strong> Pooled capacity across providers instead of separate contracts and idle commitments.</li>'
    || '<li><strong>Automatic failover.</strong> A single model name keeps working through an upstream provider incident.</li>'
    || '<li><strong>Smart routing.</strong> Requests go to the cheapest model that still clears your quality bar.</li>'
    || '<li><strong>One dashboard.</strong> Keys, spend, latency and usage for your whole team in one place.</li>'
    || '</ul>'
    || '<h3>Get started</h3>'
    || '<p>Read the <a href="https://www.unifyapi.ai/docs">documentation</a>, or create a key and make your first call in under a minute.</p>'
    || '<p><a href="https://www.unifyapi.ai">unifyapi.ai</a></p>'
    || '<hr />'
    || '<p><small>Built on <a href="https://github.com/QuantumNous/new-api" target="_blank" rel="noopener noreferrer">New API</a> '
    || '(<a href="https://github.com/QuantumNous/new-api/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">AGPL-3.0</a>), '
    || 'modified by FelixSphere: branding, typography, light-only theme, and a multi-tenant account model. '
    || 'Source: <a href="https://github.com/FelixSphere/unifyapi-console" target="_blank" rel="noopener noreferrer">unifyapi-console</a>.</small></p>')
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
