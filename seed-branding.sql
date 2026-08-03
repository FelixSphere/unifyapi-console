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

-- About. Replaces upstream's EmptyAboutState, so it must preserve everything
-- that page carried: the upstream link, the AGPLv3 s.7(b) notice verbatim, the
-- licence link, the One API provenance chain, and our s.7(c) change statement.
INSERT INTO options (key, value) VALUES
  ('About', '<h2>About UnifyAPI</h2>'
    || '<p>UnifyAPI gives you one API key and one bill across 300+ AI models from every major provider, with automatic failover and smart routing.</p>'
    || '<h3>Open source attribution</h3>'
    || '<p>UnifyAPI Console is a <strong>modified version of New API</strong>, an open source AI model gateway: '
    || '<a href="https://github.com/QuantumNous/new-api" target="_blank" rel="noopener noreferrer">https://github.com/QuantumNous/new-api</a></p>'
    || '<p>Frontend design and development by New API contributors.</p>'
    || '<p>New API is in turn based on One API, Copyright (c) 2023 JustSong.</p>'
    || '<h3>Licence</h3>'
    || '<p>This software is distributed under the GNU Affero General Public License v3.0: '
    || '<a href="https://github.com/QuantumNous/new-api/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">AGPL-3.0</a>.</p>'
    || '<p><strong>Statement of changes (AGPLv3 s.7(c)):</strong> modified by FelixSphere. '
    || 'Changes cover visual rebranding (colour system, typography, logo, page titles), '
    || 'light-only theme enforcement, always-mounted upstream attribution, and a multi-tenant account model.</p>'
    || '<p>Complete corresponding source for this modified version: '
    || '<a href="https://github.com/FelixSphere/unifyapi-console" target="_blank" rel="noopener noreferrer">https://github.com/FelixSphere/unifyapi-console</a></p>')
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
