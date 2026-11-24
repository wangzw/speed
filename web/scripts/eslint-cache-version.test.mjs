/**
 * Pins the local ESLint plugin's `meta.version`: it hashes the rule sources
 * and the workspace lockfile, so a rule or dependency change invalidates the
 * per-file lint cache, and it reaches both the platform and demo configs.
 */
import { ESLint } from 'eslint'
import {
  appendFileSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { afterAll, describe, expect, it } from 'vitest'
import { eslintCacheVersion } from './eslint-cache-version.mjs'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')
const rulesDir = join(webRoot, 'eslint-rules')
const lockfilePath = join(webRoot, 'pnpm-lock.yaml')

const tempDirs = []

function makeTempDir(label) {
  const dir = mkdtempSync(join(tmpdir(), `speed-eslint-cache-${label}-`))
  tempDirs.push(dir)
  return dir
}

function copyFixture() {
  const dir = makeTempDir('fixture')
  const rules = join(dir, 'eslint-rules')
  cpSync(rulesDir, rules, { recursive: true })
  const lockfile = join(dir, 'pnpm-lock.yaml')
  copyFileSync(lockfilePath, lockfile)
  return { dir, rules, lockfile }
}

function findSpeedPlugin(config) {
  const entry = config.find((block) => block.plugins?.speed)
  return entry?.plugins.speed
}

const QUIET_RULE = `export default {
  meta: { type: 'problem', schema: [] },
  create() {
    return {}
  },
}
`

const REPORTING_RULE = `export default {
  meta: { type: 'problem', schema: [] },
  create(context) {
    return {
      Program(node) {
        context.report({ node, message: 'the rule changed' })
      },
    }
  },
}
`

afterAll(() => {
  for (const dir of tempDirs) {
    rmSync(dir, { recursive: true, force: true })
  }
})

describe('eslintCacheVersion', () => {
  it('is a hex sha256 of the rule sources and the lockfile', () => {
    const { rules, lockfile } = copyFixture()
    expect(eslintCacheVersion(rules, lockfile)).toMatch(/^[0-9a-f]{64}$/)
  })

  it('returns the same digest for the same input', () => {
    const { rules, lockfile } = copyFixture()
    expect(eslintCacheVersion(rules, lockfile)).toBe(
      eslintCacheVersion(rules, lockfile),
    )
  })

  it('changes when a non-test rule file changes', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    appendFileSync(join(rules, 'no-literal-text.js'), '\n// changed\n')
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('changes when a non-test file with any extension is added', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    writeFileSync(join(rules, 'exemptions.json'), '{}\n')
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('changes when a file in a rule subdirectory is added', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    mkdirSync(join(rules, 'helpers'))
    writeFileSync(join(rules, 'helpers', 'shared.js'), 'export const x = 1\n')
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('changes when a rule file is renamed', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    renameSync(
      join(rules, 'no-literal-text.js'),
      join(rules, 'no-literal-text-renamed.js'),
    )
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('changes when the lockfile changes', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    appendFileSync(lockfile, '\n# probe\n')
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('ignores a test file change', () => {
    const { rules, lockfile } = copyFixture()
    const before = eslintCacheVersion(rules, lockfile)
    appendFileSync(join(rules, 'no-literal-text.test.mjs'), '\n// changed\n')
    expect(eslintCacheVersion(rules, lockfile)).toBe(before)
  })

  it('changes when a helper whose name only contains .test. changes', () => {
    const { rules, lockfile } = copyFixture()
    const helper = join(rules, 'x.test.helpers.js')
    writeFileSync(helper, 'export const x = 1\n')
    const before = eslintCacheVersion(rules, lockfile)
    appendFileSync(helper, '\n// changed\n')
    expect(eslintCacheVersion(rules, lockfile)).not.toBe(before)
  })

  it('refuses a symbolic link among the rule sources, naming it', () => {
    const { dir, rules, lockfile } = copyFixture()
    const target = join(dir, 'shared-rule.js')
    writeFileSync(target, 'export const shared = 1\n')
    symlinkSync(target, join(rules, 'linked-rule.js'))
    expect(() => eslintCacheVersion(rules, lockfile)).toThrow(/linked-rule\.js/)
  })

  it('keeps a separator inside a file from merging two files into one', () => {
    const split = makeTempDir('split')
    const splitRules = join(split, 'eslint-rules')
    mkdirSync(splitRules)
    writeFileSync(join(splitRules, 'a'), 'b')
    writeFileSync(join(splitRules, 'c'), 'd')
    writeFileSync(join(split, 'pnpm-lock.yaml'), 'lockfileVersion: 9\n')

    const joined = makeTempDir('joined')
    const joinedRules = join(joined, 'eslint-rules')
    mkdirSync(joinedRules)
    writeFileSync(join(joinedRules, 'a'), 'b\0c\0d')
    writeFileSync(join(joined, 'pnpm-lock.yaml'), 'lockfileVersion: 9\n')

    expect(
      eslintCacheVersion(joinedRules, join(joined, 'pnpm-lock.yaml')),
    ).not.toBe(eslintCacheVersion(splitRules, join(split, 'pnpm-lock.yaml')))
  })

  it('keeps a file boundary from merging two files into one', () => {
    const twoFiles = makeTempDir('two-files')
    const twoRules = join(twoFiles, 'eslint-rules')
    mkdirSync(twoRules)
    writeFileSync(join(twoRules, 'ab'), 'c')
    writeFileSync(join(twoFiles, 'pnpm-lock.yaml'), 'lockfileVersion: 9\n')

    const oneFile = makeTempDir('one-file')
    const oneRules = join(oneFile, 'eslint-rules')
    mkdirSync(oneRules)
    writeFileSync(join(oneRules, 'a'), 'bc')
    writeFileSync(join(oneFile, 'pnpm-lock.yaml'), 'lockfileVersion: 9\n')

    expect(
      eslintCacheVersion(twoRules, join(twoFiles, 'pnpm-lock.yaml')),
    ).not.toBe(eslintCacheVersion(oneRules, join(oneFile, 'pnpm-lock.yaml')))
  })
})

describe('the local plugin meta', () => {
  it('carries the computed version in the platform config', async () => {
    const config = (await import('../eslint.config.mjs')).default
    const speed = findSpeedPlugin(config)
    expect(speed?.meta).toEqual({
      name: 'speed-local',
      version: eslintCacheVersion(rulesDir, lockfilePath),
    })
  })

  it('carries the same version in the demo host config', async () => {
    const config = (
      await import('../../starters/demo/web/eslint.config.mjs')
    ).default
    expect(findSpeedPlugin(config)?.meta).toEqual({
      name: 'speed-local',
      version: eslintCacheVersion(rulesDir, lockfilePath),
    })
  })
})

describe('the warm lint cache', () => {
  it('reports again after a rule change, with the cache kept in place', async () => {
    const dir = makeTempDir('warm')
    const rules = join(dir, 'eslint-rules')
    mkdirSync(rules)
    const lockfile = join(dir, 'pnpm-lock.yaml')
    writeFileSync(lockfile, 'lockfileVersion: 9\n')
    const ruleFile = join(rules, 'probe.mjs')
    writeFileSync(ruleFile, QUIET_RULE)
    const cacheLocation = join(dir, 'eslintcache')
    mkdirSync(join(dir, 'src'))
    const targetFile = join(dir, 'src', 'sample.js')
    writeFileSync(targetFile, 'const answer = 42;\n')

    async function lint(load) {
      const { default: rule } = await import(
        `${pathToFileURL(ruleFile).href}?load=${load}`
      )
      const plugin = {
        meta: { name: 'speed-local', version: eslintCacheVersion(rules, lockfile) },
        rules: { probe: rule },
      }
      const eslint = new ESLint({
        cwd: dir,
        overrideConfigFile: true,
        overrideConfig: [
          {
            files: ['**/*.js'],
            plugins: { speed: plugin },
            rules: { 'speed/probe': 'error' },
          },
        ],
        cache: true,
        cacheStrategy: 'content',
        cacheLocation,
      })
      return eslint.lintFiles([targetFile])
    }

    const [cold] = await lint('first')
    expect(cold.messages).toEqual([])
    expect(existsSync(cacheLocation)).toBe(true)

    writeFileSync(ruleFile, REPORTING_RULE)
    const [warm] = await lint('second')
    expect(warm.messages.map((message) => message.message)).toContain(
      'the rule changed',
    )
  })
})
