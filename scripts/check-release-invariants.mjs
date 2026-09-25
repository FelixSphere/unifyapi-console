/**
 * Fails the build if the image-publishing workflows lose a guarantee that
 * production depends on.
 *
 * Two of these break silently -- no compiler error, no failing test, and the
 * workflow still goes green while doing the wrong thing:
 *
 *   1. `latest` must move only on a tag push. The console instance pulls with
 *      `--pull always` on every restart, so a manual build of an arbitrary
 *      branch that claimed `latest` would put unreleased code into production
 *      at the next reboot -- with nothing in Terraform changing to show it, and
 *      no release anyone could point to. This is the staging-first rule's
 *      back door.
 *
 *   2. Upstream's own publishing workflows push to `calciumion/new-api`, which
 *      is THEIR Docker Hub namespace. They are gated to upstream's repository
 *      so they never run here. Upstream ships ~4 commits/day, so a release
 *      merge can add a new publishing job -- and an unguarded one would push
 *      our build under their name.
 *
 * Text-based rather than YAML-parsed on purpose: `web/package.json` is on the
 * never-touch list (adding a dependency breaks the release build), so there is
 * no YAML parser available. The checks are written to fail closed -- if a
 * reformat moves a guard out of reach, this reports it rather than passing.
 *
 * Run: node scripts/check-release-invariants.mjs
 */
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const workflows = join(repoRoot, '.github', 'workflows')

const OUR_REPO = "github.repository == 'FelixSphere/unifyapi-console'"
const UPSTREAM_REPO = "github.repository == 'QuantumNous/new-api'"
const OUR_IMAGE = 'ghcr.io/felixsphere/unifyapi-console'
const UPSTREAM_IMAGE = 'calciumion/new-api'

const failures = []

function read(name) {
  return readFileSync(join(workflows, name), 'utf8')
}

function check(description, condition) {
  if (!condition) failures.push(description)
}

// --- 1. fork-image.yml: ours, gated, and never wearing upstream's name -------

const forkImage = read('fork-image.yml')

check(
  `fork-image.yml must gate its build job with ${OUR_REPO}`,
  forkImage.includes(`if: ${OUR_REPO}`),
)

check(
  `fork-image.yml must publish to ${OUR_IMAGE}`,
  forkImage.includes(OUR_IMAGE),
)

// Comment lines are excluded deliberately: fork-image.yml's own header
// explains why upstream's workflows push to that namespace and ours does not.
// Matching the explanation would make this check fire on its own documentation.
const forkImageCode = forkImage
  .split('\n')
  .filter((line) => !line.trim().startsWith('#'))
  .join('\n')

check(
  `fork-image.yml must never push to upstream's ${UPSTREAM_IMAGE}`,
  !forkImageCode.includes(UPSTREAM_IMAGE),
)

// --- 2. `latest` moves only on a tag push ------------------------------------

// The trigger has to stay tag-only, because the release flag is derived from
// `github.event_name == 'push'`. If a branch push ever triggered this workflow,
// that same expression would silently start marking branch builds as releases.
const triggerBlock = forkImage.slice(
  forkImage.indexOf('\non:'),
  forkImage.indexOf('\nenv:'),
)
check(
  'fork-image.yml must trigger on tag pushes only -- a branch push would be ' +
    'read as a release by the `event_name == push` test',
  /push:\s*\n\s*tags:/.test(triggerBlock) && !/push:\s*\n\s*branches:/.test(triggerBlock),
)

// Every line that emits a `latest` image tag must sit under a release guard.
// Looking back a few lines is enough: these are short shell blocks, and a guard
// that has drifted further than this is exactly the ambiguity worth failing on.
const lines = forkImage.split('\n')
const RELEASE_GUARD = /if \[ "\$\{?release\}?" = true \]|if \[ "\$\{?RELEASE\}?" = true \]/i
const LATEST_EMITTER = /:latest|merge_tags\+=\(latest\)/

lines.forEach((line, index) => {
  if (!LATEST_EMITTER.test(line)) return
  if (line.trim().startsWith('#')) return
  // `runs-on: ubuntu-latest` is not an image tag.
  if (/runs-on:|ubuntu-latest|ubuntu-24/.test(line)) return

  const window = lines.slice(Math.max(0, index - 4), index).join('\n')
  check(
    `fork-image.yml:${index + 1} moves a \`latest\` tag without a release ` +
      `guard above it -- a workflow_dispatch build would claim it: ${line.trim()}`,
    RELEASE_GUARD.test(window),
  )
})

// --- 3. upstream's publishing workflows stay gated to upstream ---------------

for (const name of ['docker-build.yml', 'docker-image-branch.yml']) {
  const source = read(name)

  check(
    `${name} must keep at least one ${UPSTREAM_REPO} guard`,
    source.includes(UPSTREAM_REPO),
  )

  // Parse just far enough to enumerate jobs: a two-space key under `jobs:`.
  const jobsAt = source.indexOf('\njobs:')
  if (jobsAt === -1) {
    failures.push(`${name} has no jobs: block -- cannot verify its guards`)
    continue
  }

  const body = source.slice(jobsAt)
  const jobLines = body.split('\n')
  const jobs = new Map()
  let current = null

  for (const line of jobLines) {
    const header = /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(line)
    if (header) {
      current = header[1]
      jobs.set(current, [])
      continue
    }
    if (current) jobs.get(current).push(line)
  }

  check(`${name} must declare at least one job`, jobs.size > 0)

  const guarded = new Set()
  const needsOf = new Map()

  for (const [job, block] of jobs) {
    const text = block.join('\n')
    if (text.includes(UPSTREAM_REPO)) guarded.add(job)

    const needs = /needs:\s*\[([^\]]*)\]/.exec(text) ?? /needs:\s*([A-Za-z0-9_-]+)\s*$/m.exec(text)
    needsOf.set(
      job,
      needs ? needs[1].split(',').map((n) => n.trim()).filter(Boolean) : [],
    )
  }

  // A job is safe if it is guarded, or if every job it needs is safe -- a
  // skipped dependency skips it too.
  const safe = new Map()
  function isSafe(job, seen = new Set()) {
    if (safe.has(job)) return safe.get(job)
    if (seen.has(job)) return false
    seen.add(job)
    const deps = needsOf.get(job) ?? []
    const result =
      guarded.has(job) || (deps.length > 0 && deps.every((d) => isSafe(d, seen)))
    safe.set(job, result)
    return result
  }

  for (const job of jobs.keys()) {
    check(
      `${name}: job \`${job}\` would run in this fork and publish under ` +
        `upstream's name -- add \`if: ${UPSTREAM_REPO}\` or make it need a guarded job`,
      isSafe(job),
    )
  }
}

// --- report ------------------------------------------------------------------

if (failures.length > 0) {
  console.error('release-invariants: FAILED\n')
  for (const failure of failures) console.error(`  - ${failure}`)
  console.error(
    '\nThese guard what reaches production. If one genuinely needs to change,' +
      '\nsay why in the PR -- do not weaken the check to get a green build.',
  )
  process.exit(1)
}

console.log(
  'release-invariants: OK (latest is tag-only, upstream publish jobs stay gated)',
)
