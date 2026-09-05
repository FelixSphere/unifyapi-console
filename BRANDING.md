# UnifyAPI fork — branding, licence obligations, and upstream merge runbook

This fork of [QuantumNous/new-api](https://github.com/QuantumNous/new-api) is the
logged-in console for **UnifyAPI** (`app.unifyapi.ai`) and its OpenAI-compatible
relay (`api.unifyapi.ai`).

- **Upstream base:** `v1.0.0-rc.23` (commit `0ab02020`)
- **Fork:** <https://github.com/FelixSphere/unifyapi-console> (public)
- **Marketing site:** separate repo, `FelixSphere/unifyapi` → `www.unifyapi.ai`

---

## AGPLv3 §7(c) statement of changes

> This is a modified version of New API. Modifications by FelixSphere: visual
> rebranding (colour system, typography, logo, page titles), light-only theme
> enforcement, always-mounted upstream attribution, and a multi-tenant account
> model. Complete corresponding source:
> <https://github.com/FelixSphere/unifyapi-console>

## Licence obligations — read before touching anything

Upstream is AGPL-3.0 **plus** a §7 rider in [`NOTICE`](NOTICE) lines 8–25. Two
things are non-negotiable:

1. The attribution notice ("Designed and developed by the project contributors")
   must stay visible in a prominent about/legal/footer location.
2. A visible link to `https://github.com/QuantumNous/new-api` must stay in that
   same prominent location.

Because we serve this over a network, AGPL §13 also entitles our users to the
Corresponding Source of our modified version. **That is why this fork is public** —
keeping it public is what discharges §13. Making it private would require a
process for handing source to any user who asks.

### Why upstream's own attribution is not enough

`<Footer />` mounts in exactly one place, `web/src/features/home/index.tsx`, and
`Home()` returns earlier whenever the `HomePageContent` option is set — which we
do set. `web/src/features/about/index.tsx` likewise only shows its attribution
when the `About` option is empty. A logged-in product configured normally would
therefore render **no attribution at all**.

So we mount it ourselves, unconditionally, in both shells. `web/src/brand/upstream-attribution.tsx`
renders upstream's own `ProjectAttribution` — imported, not re-implemented — so
the notice text and URL stay single-sourced in `footer.tsx` and any upstream
rewording propagates automatically.

**Known gap:** the `/setup` first-run wizard uses neither layout and shows no
attribution. It is a one-time operator bootstrap, not a user-facing surface, so
we have accepted it. If `/setup` ever becomes user-reachable, mount the bar there
too.

### Tripwires

- **Never run a bulk rename over `web/src/i18n/locales/`.** The attribution key
  is obfuscated twice: built at runtime as `['footer', 'new' + 'api',
  'projectAttributionSuffix'].join('.')`, and stored unicode-escaped as
  `footer.newapi.projectAttributionSuffix` at line 2042 of all seven locale
  files. A `grep` for `newapi` will not find it — but a JSON prettifier that
  normalises the escape makes it findable, and the next careless rename deletes
  a licence-required string.
- **Never run write-mode `bun run copyright` over our own files.** It stamps
  `Copyright (C) 2023-2026 QuantumNous … support@quantumnous.com`, which would be
  a false attribution on files FelixSphere wrote and cuts against §7's
  "must not misrepresent the origin". Our files carry a FelixSphere AGPL header;
  the script correctly classifies them as third-party and skips them.
- `node web/scripts/check-brand-invariants.mjs` enforces all of the above and
  runs as a hard gate in `fork-ci.yml`. Do not satisfy it by deleting the check.

---

## Our delta: 5 modified upstream files, 4 new paths

Kept deliberately tiny because upstream ships ~4 commits/day. Measured churn over
the 12 days after upstream's `web/` rewrite: `i18n/locales/en.json` 5 commits,
`styles/index.css` 3, `ci.yml` 2, `package.json`/`bun.lock` 1 each — and **zero**
for every file below.

Every one of our edits carries a `UNIFYAPI-BRAND` comment, so `grep -rn UNIFYAPI-BRAND`
finds the whole delta without consulting git history.

### Modified

| File | Change | Why there |
|---|---|---|
| `web/src/main.tsx` | import `./styles/unifyapi.css` instead of `./styles/index.css` | Our stylesheet re-imports `index.css` first, so upstream's stays **byte-untouched** and merges fast-forward forever. Appending an `@import` inside `index.css` would touch their highest-churn stylesheet, in the exact import block they last edited. |
| `web/src/main.tsx` | `<ThemeProvider defaultTheme='light'>` | Required, not cosmetic. CSS cannot reach the JS consumers of `resolvedTheme` — VChart (`lib/use-chart-theme.ts`, the three dashboard chart components) and Sonner. Without it, charts and toasts render dark on a paper-white page. |
| `…/layout/components/authenticated-layout.tsx` | mount `<UpstreamAttribution />`; `<AppHeader showConfigDrawer={false} />` | The config drawer is simultaneously the dark-mode switch and the theme-preset/font/radius picker. Disabling it pins the brand and guarantees `data-theme-preset` is never written to `<body>` — which matters because the preset bridge selector in `theme-presets.css` has specificity (0,4,0) and would out-rank our `:root`. |
| `…/layout/components/public-layout.tsx` | mount `<UpstreamAttribution />`; default `showThemeSwitch` to `false` | One line covers all nine early returns across `features/home` and `features/about`, plus `/pricing`, `/rankings`, and the legal pages. `public-header.tsx` defaults the switch on. |
| `…/layout/components/footer.tsx` | `export` `ProjectAttribution` | One word, so our bar reuses upstream's notice verbatim instead of duplicating it. |
| `web/index.html` | title, `meta[name=title]`, description, `theme-color: #faf9f6` | First-paint / no-JS / crawler surface only; `main.tsx` overwrites the title from `/api/status` once loaded. Line 5 (`href="/logo.png"`) is left alone — the asset swap covers it. |
| `VERSION` | `unifyapi-v0.1.0+upstream-v1.0.0-rc.23` | Upstream ships this file **empty** (populated only by their release workflow), so a plain `docker build` produces an empty version string. |

### New (ours; no conflict surface)

- `web/src/styles/unifyapi.css` — the entire visual rebrand.
- `web/src/assets/fonts/` — four vendored `latin` woff2 + three OFL-1.1 licences.
- `web/src/brand/upstream-attribution.tsx` — the always-mounted attribution.
- `web/scripts/check-brand-invariants.mjs` — the licence/brand guard.
- `.github/workflows/fork-ci.yml` — fork-owned CI.
- `BRANDING.md` — this file.
- `model/credit_supplier.go`, `model/credit_lot.go`, `model/credit_supply_consume.go`,
  `model/credit_supplier_portal.go`, `service/credit_supply.go`, `controller/credit_supply.go`,
  `controller/credit_supplier_portal.go`, `docs/credit-supply.md` —
  the supplier credit supply: third-party vendor credits routed through a channel,
  drawn down at list price, settled to the supplier at an acquisition rate that
  doubles as the channel's `ChannelCostRatio`.

### Not touched, on purpose

`LICENSE`, `NOTICE`, `THIRD-PARTY-LICENSES.md`, all 7 `web/src/i18n/locales/*.json`,
`web/scripts/add-copyright.mjs`, `web/scripts/format-with-protected-headers.mjs`,
`web/src/styles/{index,theme,theme-presets}.css`, `web/package.json`, `web/bun.lock`.

Also deliberately skipped, because the runtime options below make them dead code:
`web/src/lib/constants.ts` (`DEFAULT_SYSTEM_NAME`), `common/constants.go`
(`SystemName` — the hottest Go file in range), the `'New API'` literal fallbacks
in `footer.tsx`/`system-brand.tsx`, and the admin-form placeholders.

### Design notes

- **Fonts are vendored, not installed.** `bun add` would touch `package.json` and
  the 322 KB machine-generated `bun.lock`; both `Dockerfile` and `makefile` run
  `bun install --frozen-lockfile`, so a conflicted lockfile breaks the *release*
  build, not just CI.
- **Fonts live in `web/src/assets/fonts/`, not `web/public/fonts/`.** Rspack's
  css-loader resolves `url()` as a module and fails on root-relative `/fonts/…`.
  Relative paths from `src/` let the bundler emit them with content hashes and
  need no `rsbuild.config.ts` change (one fewer upstream file in our delta).
- **Palette uses the marketing site's exact hex** for the warm neutrals
  (`#faf9f6` `#ffffff` `#1c1b19` `#6b6862` `#e7e4dd`) and **Tailwind v4's exact
  OKLCH** for the accent ramp, because the marketing site styles those with the
  `indigo-600` / `emerald-600` utility classes — copying Tailwind's own values
  keeps them pixel-identical rather than approximately close.
- **Light-only** is enforced in two halves: `@custom-variant dark (&:is(.unifyapi-never-dark *))`
  makes the `dark:` utilities in ~81 files inert, and `defaultTheme='light'`
  handles the JS consumers. The palette block targets `:root, .dark` with
  identical values, so it is correct even if the `dark` class ever lands.
- **Not built on `[data-theme-preset='anthropic']`**, tempting as it is (warm
  cream + serif already): it is user-selectable, hard-codes clay `#d97757`, and
  its (0,4,0) bridge selector would out-rank anything we write cheaply.

---

## Runtime configuration (set in the DB, not in code)

These produce **zero** merge conflicts, so anything that is just text or a URL
belongs here rather than in code. `main.tsx` already plumbs `SystemName` →
`document.title` and `Logo` → favicon, cache-first from `localStorage['status']`
then refreshed from `GET /api/status`. There is **no environment variable** for
any of them — DB only, via the admin UI or `PUT /api/option/`.

**Set these on first boot or the console renders as "New API".** Verified: with
`SystemName` unset, the title flips from our static `<title>UnifyAPI</title>` back
to "New API" as soon as `/api/status` resolves.

| Option | Value |
|---|---|
| `SystemName` | `UnifyAPI` |
| `Logo` | `/logo.png` |
| `Footer` | UnifyAPI HTML, and a visible **Source code** link to this fork (discharges §13). Safe: the `footerHtml` branch still renders `<ProjectAttribution inline />`. |
| `About` | Must contain: "UnifyAPI Console is a modified version of **New API**" + link to `https://github.com/QuantumNous/new-api`; the §7(b) notice; the AGPL licence link; the §7(c) statement at the top of this file; and the chain from upstream's own about page — "Based on One API © 2023 JustSong". |
| `HomePageContent` | UnifyAPI console landing (safe: `PublicLayout` now carries attribution unconditionally). |

The split we hold to: **anything visible before `/api/status` resolves, or needed
at build time (fonts, colours, bundled assets, the static `<title>`) goes in
code. Anything that is text or a URL goes in the DB.**

### Also worth configuring

- Default language is Chinese out of the box; set it for an English product.
- Verification and password-reset emails are hardcoded Chinese in
  `controller/misc.go` — they need rewriting for an English product.

---

## Upstream merge runbook

### One-time local setup after cloning

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git config remote.upstream.tagOpt --no-tags        # see the warning below
git config merge.keepours.driver true
printf 'web/src/assets/fonts/** merge=keepours\n' >> .git/info/attributes
```

`.gitattributes` is an upstream file, so the merge driver is deliberately kept in
`.git/info/attributes` (untracked) rather than added to it.

### Never push upstream tags to this fork

`docker-build.yml`, `release.yml`, `electron-build.yml`, and
`sync-release-to-gitcode.yml` all trigger on `push: tags: '*'`. A
`git fetch upstream --tags && git push --tags` would fire four upstream release
pipelines inside our fork. Hence `tagOpt = --no-tags`; fetch a tag explicitly:

```bash
git fetch upstream tag v1.0.0-rc.24 --no-tags
```

### Upstream Actions are neutralised via repo state, not by editing files

Editing upstream's seven workflow files would add seven files to our delta and
conflict on every upstream CI change, so we neutralise them through GitHub repo
settings instead. `pr-check.yml` is the dangerous one: it runs
`peakoss/anti-slop` on `pull_request_target` with `close-pr: true`, which
auto-closes our own PRs.

**Current state: Actions are disabled repo-wide.**

```bash
gh api repos/FelixSphere/unifyapi-console/actions/permissions   # {"enabled": false}
```

This is deliberate and is the safe state until the first push. GitHub registers
workflow files lazily — they do not appear in the workflows API until something
triggers them — so they cannot be disabled individually before that first push.
A repo-wide disable is the only way to be safe in the window between forking and
pushing.

**Sequence to switch to selective enablement**, once you are ready for CI:

```bash
# 1. Push. Nothing runs, because Actions are still disabled repo-wide.
git push -u origin brand/unifyapi-rebrand

# 2. Workflows are now registered. Disable every upstream one by id.
gh api repos/FelixSphere/unifyapi-console/actions/workflows \
  --jq '.workflows[] | select(.path | test("fork-ci|dependabot") | not) | .id' \
  | while read -r id; do
      gh api -X PUT "repos/FelixSphere/unifyapi-console/actions/workflows/$id/disable"
    done

# 3. Re-enable Actions repo-wide. Only fork-ci.yml can now run.
gh api -X PUT repos/FelixSphere/unifyapi-console/actions/permissions -F enabled=true

# 4. Confirm. Everything except fork-ci.yml must read disabled_manually.
gh api repos/FelixSphere/unifyapi-console/actions/workflows \
  --jq '.workflows[] | "\(.state)\t\(.path)"'
```

Upstream's `ci.yml` is safe to leave enabled if you want it — it checks out
`github.event.pull_request.base.repo.full_name`, which resolves to this repo in a
fork PR. The four tag-triggered release workflows and `pr-check.yml` are not.

Re-verify after any change to Actions settings, since a repo-wide re-enable does
**not** re-disable individual workflows but a newly-appearing upstream workflow
file will arrive enabled.

### The merge itself

```bash
git switch main && git pull
git fetch upstream tag v1.0.0-rc.NN --no-tags
git switch -c merge/upstream-v1.0.0-rc.NN
git merge v1.0.0-rc.NN          # tag-to-tag, never upstream/main

# Expected conflict set is only:
#   web/src/main.tsx                    (import + provider lines) -> keep both
#   web/index.html                      -> keep ours
#   the three layout/footer edits        -> keep ours
#   web/src/assets/fonts/**              -> --ours (merge driver handles it)

cd web && bun install
node scripts/check-brand-invariants.mjs   # must pass before anything else
bun run typecheck && bun run build
```

Release tags: `unifyapi-v<n>+upstream-v1.0.0-rc.NN` — the suffix records the
exact upstream base, which is what you will want at 3am.

### Post-merge smoke checks

1. `node web/scripts/check-brand-invariants.mjs` passes.
2. Dashboard charts render **light** — the canary for `defaultTheme='light'`
   surviving the merge. If they go dark, `main.tsx` lost the prop.
3. `getComputedStyle(document.documentElement).getPropertyValue('--background')`
   is `#faf9f6`, and `document.fonts.check('16px "Instrument Serif"')` is true.
4. `document.body` has **no** `data-theme-preset` attribute.
5. Set `HomePageContent`, reload the landing page → attribution strip still
   visible. Set `About`, open `/about` → notice and upstream link still there.
6. Log in → attribution strip at the bottom of `/dashboard`, `/channels`,
   `/playground`.

### Gate baseline at v1.0.0-rc.23

Three of upstream's own scripts fail on a **clean** checkout of their release
tag, so `fork-ci.yml` runs them as advisory. Re-check at every bump and promote
them to hard gates once a tag lands green.

| Script | Clean-tree result at rc.23 |
|---|---|
| `bun run typecheck` | green |
| `bun run build` | green |
| `bun run copyright:check` | **fails** — `features/auth/lib/oauth-callback-mode.ts`, `features/channels/lib/channel-field-update.ts` (cosmetic blank line after the licence header) |
| `bun run format:check` | **fails** — `channel-mutate-drawer.tsx`, `channel-form.ts`, `api-key-group-cell.tsx` |
| `bun test` | **fails** — 9 fail / 6 errors (`node:test` vs `bun test` incompatibility) |

We deliberately do **not** "fix" these: the changes are cosmetic, carry no licence
significance, and each one would add an upstream file to our delta for nothing.

---

## Building

`main.go` embeds `web/dist`, so **the Go build fails unless the frontend is built
first**:

```bash
cd web && bun install --frozen-lockfile && bun run build && cd ..
GOWORK=off CGO_ENABLED=0 go build \
  -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" \
  -o unifyapi-console .
```

Requires `bun` (pinned to 1.3.14, matching upstream CI) and Go 1.26.
