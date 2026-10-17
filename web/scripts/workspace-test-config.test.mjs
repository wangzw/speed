/**
 * Structural guard: every workspace package that runs tests takes its
 * Vitest options from the shared preset (web/vitest.shared.ts).
 *
 * A package that hand-writes its test block can silently run on Vitest's
 * 5s default, which the parallel workspace run (`pnpm -r test`) exceeds
 * on a loaded machine -- the failure lands on the clock, not on an
 * assertion. This suite makes that omission red at the source: each
 * package with a `test` script must import `sharedTestOptions` from the
 * preset, must not pin its own `testTimeout`, and its exported config
 * must carry the preset's timeout and coverage options.
 *
 * The demo host (starters/demo/web) is out of scope: it is a copyable
 * application whose repository-internal test wiring is dropped on copy
 * out, and it carries its own setup instead of consuming web/'s.
 */
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { describe, expect, it } from 'vitest'
import { sharedCoverage } from '../vitest.shared'

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

/** Every workspace package directory: packages/* plus test-utils. */
const packageDirs = [
  ...readdirSync(join(webRoot, 'packages'), { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => join(webRoot, 'packages', entry.name)),
  join(webRoot, 'test-utils'),
]

/** A package is in scope when its manifest declares a `test` script. */
const testPackages = packageDirs
  .map((dir) => {
    const manifest = JSON.parse(
      readFileSync(join(dir, 'package.json'), 'utf8'),
    )
    return { dir, name: manifest.name, test: manifest.scripts?.test }
  })
  .filter((pkg) => typeof pkg.test === 'string')

/** Comments are prose, not configuration; drop them before matching. */
function stripComments(source) {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/.*$/gm, '')
}

describe('workspace test configuration', () => {
  it('finds the packages that run tests', () => {
    expect(testPackages.length).toBeGreaterThan(0)
  })

  for (const pkg of testPackages) {
    it(`${pkg.name} takes its test options from the shared preset`, async () => {
      const configPath = join(pkg.dir, 'vitest.config.ts')
      const source = stripComments(readFileSync(configPath, 'utf8'))

      // The spread is what makes the preset the source of the options;
      // a hand-written block (or a locally pinned timeout) is exactly the
      // omission this guard exists to catch. The import carries the .ts
      // extension because Vite's native config loader warns on an
      // extensionless one and plans to reject it.
      expect(source).toMatch(
        /from '\.\.\/(?:\.\.\/)?vitest\.shared\.ts'/,
      )
      expect(source).toContain('sharedTestOptions(')
      expect(source).not.toMatch(/testTimeout\s*:/)

      const config = (await import(pathToFileURL(configPath).href)).default

      expect(config.test?.testTimeout).toBe(20_000)
      expect(config.test?.coverage).toMatchObject({
        provider: sharedCoverage.provider,
        include: sharedCoverage.include,
        reporter: sharedCoverage.reporter,
      })
      // test-utils deliberately drops the floor (its src is driven by the
      // consuming packages' suites); any package that keeps one keeps the
      // shared one.
      if (config.test?.coverage?.thresholds !== undefined) {
        expect(config.test.coverage.thresholds).toEqual(
          sharedCoverage.thresholds,
        )
      }
    })
  }
})
