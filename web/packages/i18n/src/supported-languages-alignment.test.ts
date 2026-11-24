import { readdirSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { DEFAULT_SUPPORTED_LANGUAGES } from './languages'

const REPO_ROOT = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
  '..',
)
const BACKEND_ROOT = join(REPO_ROOT, 'pkg')

function catalogDirs(root: string): string[] {
  const found: string[] = []
  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (!entry.isDirectory()) {
        continue
      }
      if (entry.name === 'internal' || entry.name === 'node_modules') {
        continue
      }
      const path = join(dir, entry.name)
      if (entry.name === 'locales') {
        found.push(path)
        continue
      }
      walk(path)
    }
  }
  walk(root)
  return found.sort()
}

function shippedLocaleIds(dir: string): string[] {
  return readdirSync(dir)
    .filter((name) => name.endsWith('.json'))
    .map((name) => name.slice(0, -'.json'.length))
}

const CATALOG_DIRS = catalogDirs(BACKEND_ROOT)

describe('the web supported-language set against the platform catalogs', () => {
  it('discovers the modules that ship locale catalogs', () => {
    expect(CATALOG_DIRS.length).toBeGreaterThan(0)
  })

  it.each(CATALOG_DIRS)('%s ships exactly the web supported set', (dir) => {
    const shipped = shippedLocaleIds(dir)
    expect(
      [...shipped].sort(),
      `${relative(REPO_ROOT, dir)} ships [${shipped.join(', ')}] but ` +
        `DEFAULT_SUPPORTED_LANGUAGES ` +
        `(web/packages/i18n/src/languages.ts) is ` +
        `[${DEFAULT_SUPPORTED_LANGUAGES.join(', ')}]`,
    ).toEqual([...DEFAULT_SUPPORTED_LANGUAGES].sort())
  })
})
