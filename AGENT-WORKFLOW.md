# Agent development workflow

How to make changes to this fork without one agent's work quietly breaking
another's. Read this before starting; it is short on purpose.

The problem this solves: this is a fork of a fast-moving upstream (~4 commits a
day), with our own branding, licence obligations, and tenancy model layered on
top. Those three things are invisible to an agent that is only looking at the
task in front of it, and all three break silently — no compiler error, no failing
test, just a page that renders wrong or a licence term that quietly disappears.

## 1. Know the baseline before you start

The fork carries the small compatibility and formatting fixes needed to make
every CI gate green on top of the pinned upstream tag. Record the baseline
before you change anything so a regression is attributable:

```bash
scripts/baseline.sh > /tmp/baseline.txt   # run before your first edit
# ... do the work ...
scripts/baseline.sh > /tmp/after.txt
diff /tmp/baseline.txt /tmp/after.txt      # this diff is your blast radius
```

Red is never an accepted baseline. `fork-ci.yml` blocks on typecheck, build,
copyright, formatting and the full frontend test suite.

## 1b. Where this repo lives

This repo is `console/` inside the `unifyai/` workspace:

```
unifyai/
├── web/       marketing site   — proprietary — FelixSphere/unifyapi
└── console/   this repo        — AGPL-3.0      — FelixSphere/unifyapi-console
```

Two git repos on purpose, not an oversight: this one is a public AGPL fork whose
source must stay available, `web/` is proprietary, and keeping them separate is
what makes taking an upstream release a plain `git merge <tag>` instead of a
subtree merge. See `../README.md`.

Workspace-level helpers: `../scripts/dev.sh`, `../scripts/verify.sh`,
`../scripts/status.sh`.

## 2. Isolation: one agent, one worktree

Never let two agents edit the same checkout. Use a worktree per task:

```bash
git worktree add ../../uc-<task> -b feat/<task>   # outside the workspace
cd ../../uc-<task>
```

Worktrees share the object store but have separate working trees and separate
`web/node_modules` state, so a half-finished `bun install` in one cannot break
the other. Remove it when done: `git worktree remove ../../uc-<task>`.

Two agents must not both run the console on port 3001 or the marketing site on
3000. Pick a port per agent and say which one you used.

## 2b. Turn off format-on-save (one-time, per machine)

This repo formats with `oxfmt` (single quotes, no semicolons). VS Code has no
oxfmt integration, so format-on-save falls back to its built-in TS formatter or
Prettier and rewrites files to double quotes and semicolons.

That is not cosmetic. Upstream ships ~4 commits a day, so a reformat turns a
3-line diff into a 200-line conflict in a file upstream is actively editing. It
has already silently damaged `footer.tsx` once and took the licence-required
attribution key with it.

`.vscode/` is gitignored by upstream, so this cannot be committed. Create
`.vscode/settings.json` locally with:

```json
{
  "editor.formatOnSave": false,
  "files.readonlyInclude": { "web/src/i18n/locales/*.json": true }
}
```

Format deliberately instead: `cd web && bun run format`. And if a diff you did
not intend suddenly spans the whole file, run
`git show HEAD:<file> | sed "s/'/\"/g; s/;$//" > /tmp/a` and diff the normalised
versions to find your real change.

## 3. What is ours vs upstream's

Our code lives in files upstream does not have. **Prefer adding a file over
editing one.** Every edit to an upstream file is a future merge conflict.

Ours, edit freely:

```
model/tenant.go, model/tenant_ops.go, model/tenant_test.go
controller/tenant.go
web/src/brand/**            web/src/features/ops/**
web/src/styles/unifyapi.css
web/public/fonts/**         web/public/logo.png
web/scripts/check-brand-invariants.mjs
scripts/**  BRANDING.md  AGENT-WORKFLOW.md  seed-branding.sql
.github/workflows/fork-ci.yml
```

Upstream files we deliberately touch — **keep the delta minimal, every line is a
merge cost.** All are marked `UNIFYAPI-BRAND`, so `grep -rn UNIFYAPI-BRAND` is
the authoritative inventory:

```
web/src/main.tsx                       (css entry, defaultTheme='light')
web/index.html                         (title, meta, theme-color)
web/src/components/layout/components/authenticated-layout.tsx
web/src/components/layout/components/public-layout.tsx
web/src/components/layout/components/footer.tsx   (one word: export)
web/src/features/errors/general-error.tsx         (feedback URL)
model/user.go  model/main.go  router/api-router.go
```

**Never touch:**

```
LICENSE  NOTICE  THIRD-PARTY-LICENSES.md
web/src/i18n/locales/*.json          <- see §4
web/scripts/add-copyright.mjs
web/scripts/format-with-protected-headers.mjs
web/src/styles/index.css  theme.css  theme-presets.css
web/package.json  web/bun.lock       <- adding a dep breaks the release build
```

Adding a dependency touches `package.json` **and** the 322 KB machine-generated
`bun.lock`, and both `Dockerfile` and `makefile` run `bun install
--frozen-lockfile` — so a conflicted lockfile fails the *release* build, not just
CI. Vendor the asset instead, as `web/public/fonts/` does.

## 4. The three things that break silently

**Licence attribution.** `NOTICE` §7 requires the attribution notice and a
visible link to `https://github.com/QuantumNous/new-api` in a prominent
about/legal/footer location. It is carried by `UpstreamAttribution`, mounted in
`authenticated-layout.tsx` and `public-layout.tsx`. Upstream's own `<Footer />`
mounts on exactly one page and is bypassed once `HomePageContent` is set, which
is why we mount our own. Do not remove, hide, conditionally render, or fade it.

The i18n key is deliberately obfuscated twice — built at runtime as
`['footer', 'new' + 'api', 'projectAttributionSuffix'].join('.')` and stored
unicode-escaped in all seven locale files. **A brand-rename grep will not find
it, but a JSON prettifier makes it findable and a careless rename then destroys
a licence-required string.** Never run a bulk rename over
`web/src/i18n/locales/`.

`scripts/check-brand-invariants.mjs` guards all of this. Run it before every
commit; `fork-ci.yml` blocks on it.

**Never run write-mode `bun run copyright`** over files we wrote — it stamps a
QuantumNous copyright onto FelixSphere code, which is a false attribution and
cuts against §7's "must not misrepresent the origin".

**About/Footer rendering.** `features/about/index.tsx` branches on
`isLikelyHtml()`. Any HTML tag routes content to `showMainContainer={false}` with
`htmlVariant='isolated'` — no container padding, so it renders under the fixed
header, and no prose typography. Keep `About` as tag-free **markdown**. This has
already been broken once.

**Tenancy.** `users.tenant_id == 0` means "no tenant", and that user is its own
billing entity behaving exactly as upstream. `TestUntenantedUserKeepsItsOwnBalance`
pins that. Staff (role >= 10) are deliberately tenantless — see `IsStaffRole` —
so our own team never appears in the customer list. The relay hot path still
bills `users.quota` through the per-user Redis cache; a per-user cache diverges
across members of one tenant, so that cache must move to the billing entity
before tenant balances are authoritative in the request path.

## 4b. You do not deploy this

**Only the UnifyAI CI/CD agent releases to production.** Getting your change
merged to `main` is where your task ends.

Nothing here deploys on merge, and that is deliberate: a release is a tag, an
image build, and an SSM image swap on a single live instance. Do not run that
chain because your change is ready. Finish, then **tell the CI/CD agent** what
is waiting — the commit, what it changes, and anything the release needs in a
particular order (a seed to run, an SSM parameter, a `variables.tf` bump).

The exception is a genuine hotfix: a live outage, a security issue, data being
lost. Then ship it, mark it `HOTFIX`, and tell the CI/CD agent straight after.
Wanting your feature live is not a hotfix.

The full procedure lives in `unifyai/.claude/agents/cicd.md`. Two things there
that matter even if you never run it: the instance is arm64, and
`GET /api/status` reports the upstream tag, so it lies about which release is
running.

## 5. Before you hand off

```bash
scripts/verify.sh        # brand invariants, go vet, go test, typecheck, build
```

Anything you could not finish, say so explicitly, and say what is left. A partial
change reported as done is worse than an unfinished one, because the next agent
builds on it.

Commit messages: Conventional Commits, and explain *why* — the next agent has
none of your context.

## 6. Merging upstream

```bash
git fetch upstream tag v1.0.0-rc.NN --no-tags   # never fetch all tags
git switch -c merge/upstream-v1.0.0-rc.NN
git merge v1.0.0-rc.NN
```

`remote.upstream.tagOpt=--no-tags` is set for a reason: pushing fetched upstream
tags would fire four upstream release pipelines inside our fork. Actions is
disabled repo-wide; re-enable selectively and never re-enable `pr-check.yml`,
which runs `pull_request_target` with `close-pr: true` and will auto-close our
own PRs.

Expected conflicts are only the binaries (`--ours`) and the `UNIFYAPI-BRAND`
lines. After merging, the five smoke checks in `BRANDING.md` matter more than the
test suite — especially that dashboard charts still render light, which is the
canary for `main.tsx` losing `defaultTheme='light'`.
