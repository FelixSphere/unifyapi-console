/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

declare module 'bun:test' {
  export const afterAll: typeof import('node:test').after
  export const afterEach: typeof import('node:test').afterEach
  export const describe: typeof import('node:test').describe
  export const it: typeof import('node:test').it
  export const test: typeof import('node:test').test
}
