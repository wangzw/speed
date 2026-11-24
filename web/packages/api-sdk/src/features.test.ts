import { execFileSync } from 'node:child_process'
import {
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
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
      "src/features.ts is stale: update contracts/features.json first (the demo host's `gen --features` writes it), then run `pnpm -C web run api:gen` and commit the regenerated file",
    ).toBe(true)
  })

  it('lists every name once, ascending', () => {
    const sorted = [...FEATURE_NAMES].sort()
    expect(FEATURE_NAMES).toEqual(sorted)
    expect(new Set(FEATURE_NAMES).size).toBe(FEATURE_NAMES.length)
  })

  it('carries exactly the platform-owned names', () => {
    const vocabulary = JSON.parse(readFileSync(contractPath, 'utf8')) as {
      features: { name: string; owner: string }[]
    }
    const platformOwned = vocabulary.features
      .filter((entry) => entry.owner === 'platform')
      .map((entry) => entry.name)
    const applicationOwned = vocabulary.features
      .filter((entry) => entry.owner === 'application')
      .map((entry) => entry.name)
    expect(FEATURE_NAMES).toEqual(platformOwned)
    const committed = readFileSync(committedPath, 'utf8')
    for (const name of applicationOwned) {
      expect(FEATURE_NAMES).not.toContain(name)
      expect(committed).not.toContain(`'${name}'`)
    }
  })

  it('writes only the platform names from a mixed vocabulary', () => {
    const vocabularyPath = join(scratch, 'mixed-features.json')
    const platformPath = join(scratch, 'mixed-platform.ts')
    const applicationPath = join(scratch, 'mixed-application.ts')
    writeFileSync(
      vocabularyPath,
      JSON.stringify({
        features: [
          { name: 'app.route', module: 'app', owner: 'application', conditional: false },
          { name: 'platform.route', module: 'platform', owner: 'platform', conditional: false },
        ],
      }),
    )
    execFileSync(
      process.execPath,
      [generatorPath, vocabularyPath, platformPath, applicationPath],
      { cwd: repoRoot, stdio: 'pipe' },
    )
    const platform = readFileSync(platformPath, 'utf8')
    const application = readFileSync(applicationPath, 'utf8')
    expect(platform).toContain("'platform.route'")
    expect(platform).not.toContain("'app.route'")
    expect(application).toContain("'app.route'")
    expect(application).not.toContain("'platform.route'")
  })

  it('types a declared name but not a misspelled one', () => {
    expect(declared).toBe('authn.mfa')
    expect(FEATURE_NAMES).toContain(declared)
    expect(misspelled).toBe('objectstore.objectz')
  })
})
