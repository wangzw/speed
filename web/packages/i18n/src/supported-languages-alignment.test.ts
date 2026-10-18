/**
 * Cross-layer pin: the web's supported-language set equals the language
 * catalogs the platform's Go modules ship.
 *
 * An account language preference is stored by the backend and drives
 * backend-rendered content (verification messages, notification emails,
 * invitations) in the chosen language; a locale no deployed catalog
 * carries is refused with a readable failure code (authn.invalid_locale).
 * The set the web offers and the catalog set the backend can render are
 * therefore one set, and this suite asserts that equality module by
 * module: a language added to or dropped from either side fails here,
 * naming both sides, instead of surfacing as a preference the server
 * refuses or content it cannot render.
 *
 * Both sides are read from the tree rather than declared here. The web
 * side is the exported DEFAULT_SUPPORTED_LANGUAGES (languages.ts); the
 * backend side is every locales/ directory under pkg/, one catalog file
 * per language it speaks -- so a module that starts shipping catalogs, or
 * a module that adds a language, is covered without editing this file.
 * Fixture catalogs nested under an internal/ directory are not part of the
 * shipped set and stay outside the sweep.
 *
 * The set is a build-time constant today; distributing the deployment's
 * optional languages through public configuration is the future
 * convergence path for it, and until that lands this pin is what keeps
 * the two layers moving together.
 */

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

/** Every locales/ directory under the backend module, fixtures excluded. */
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

/** The locale ids a catalog directory ships, from its file names. */
function shippedLocaleIds(dir: string): string[] {
  return readdirSync(dir)
    .filter((name) => name.endsWith('.json'))
    .map((name) => name.slice(0, -'.json'.length))
}

const CATALOG_DIRS = catalogDirs(BACKEND_ROOT)

describe('the web supported-language set against the platform catalogs', () => {
  it('discovers the modules that ship locale catalogs', () => {
    // A sweep that discovered nothing would pass vacuously.
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
