/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

import { readFileSync, readdirSync } from 'node:fs'
import { dirname, extname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const sourceRoot = join(webRoot, 'src')
const sourceExtensions = new Set(['.ts', '.tsx'])
const nodeTestImport = /\b(?:from\s+|import\s*)['"]node:test['"]/
const violations = []

function scan(directory) {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)

    if (entry.isDirectory()) {
      scan(path)
    } else if (
      entry.isFile() &&
      !entry.name.endsWith('.d.ts') &&
      sourceExtensions.has(extname(entry.name)) &&
      nodeTestImport.test(readFileSync(path, 'utf8'))
    ) {
      violations.push(relative(webRoot, path))
    }
  }
}

scan(sourceRoot)

if (violations.length > 0) {
  console.error(
    'Tests run under Bun and must import their test APIs from bun:test:'
  )
  for (const path of violations) console.error(`- ${path}`)
  process.exitCode = 1
} else {
  console.log('test runner: all test APIs use bun:test')
}
