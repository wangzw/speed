/**
 * features.test.ts -- the freshness and type guarantees of the generated
 * `features.ts` artifact.
 *
 * The committed file is a generated artifact (`web/scripts/feature-names.mjs`
 * writes it from `contracts/features.json`, which the demo host's `gen
 * --features` writes from the binary's declarations). Regenerating it into a
 * temporary file and comparing bytes is what makes a hand edit, or a backend
 * rename that the frontend never picked up, fail here instead of shipping.
 *
 * The `@ts-expect-error` assertion is the compile-time half: a misspelled
 * feature name must not be assignable to `FeatureName`. If the type ever
 * widened to `string`, the directive would become unused and the package's
 * typecheck would fail (TS2578), which is why the assertion lives in a file
 * `tsc -p tsconfig.json` compiles.
 */

import { execFileSync } from 'node:child_process'
import {
  mkdtempSync,
  readFileSync,
  rmSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterAll, describe, expect, it } from 'vitest'
import { FEATURE_NAMES } from './features.js'
import type { FeatureName } from './features.js'

const sourceDir = dirname(fileURLToPath(import.meta.url))
const packageDir = dirname(sourceDir)
const repoRoot = resolve(packageDir, '../../..')
const contractPath = join(repoRoot, 'contracts/features.json')
const generatorPath = join(repoRoot, 'web/scripts/feature-names.mjs')
const committedPath = join(sourceDir, 'features.ts')

/** A declared name, and a misspelled one the compiler must refuse. */
const declared: FeatureName = 'authn.mfa'
// @ts-expect-error 'objectstore.objectz' is not a declared feature name
const misspelled: FeatureName = 'objectstore.objectz'

const scratch = mkdtempSync(join(tmpdir(), 'speed-feature-names-'))

afterAll(() => {
  rmSync(scratch, { recursive: true, force: true })
})

describe('the feature vocabulary', () => {
  it('regenerates src/features.ts byte for byte from contracts/features.json', () => {
    const outputPath = join(scratch, 'features.ts')
    execFileSync(process.execPath, [generatorPath, contractPath, outputPath], {
      cwd: repoRoot,
      stdio: 'pipe',
    })
    const generated = readFileSync(outputPath)
    const committed = readFileSync(committedPath)
    expect(
      generated.equals(committed),
      'src/features.ts is stale: run the demo host\'s `pnpm api:gen` (contracts/features.json changed) and commit the regenerated file',
    ).toBe(true)
  })

  it('lists every name once, ascending', () => {
    const sorted = [...FEATURE_NAMES].sort()
    expect(FEATURE_NAMES).toEqual(sorted)
    expect(new Set(FEATURE_NAMES).size).toBe(FEATURE_NAMES.length)
  })

  it('types a declared name but not a misspelled one', () => {
    expect(declared).toBe('authn.mfa')
    expect(FEATURE_NAMES).toContain(declared)
    // The value is reached only so the assertion above is not dead code;
    // the compile-time claim is the @ts-expect-error directive.
    expect(misspelled).toBe('objectstore.objectz')
  })
})
