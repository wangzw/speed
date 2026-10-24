/**
 * Expected copy, read from the shipped locale bundles.
 *
 * A journey asserts on what the product renders, so its expected text has to
 * be the product's own text: the same bundle the app loads, not a copy of a
 * sentence in the test. The bundles are read from their package sources (the
 * demo host's own and the platform packages') instead of being imported as
 * JSON so the harness does not depend on a bundler's JSON handling; the lookup
 * still fails loudly when a key moves, which is what catches drift.
 *
 * The browser is pinned to en-US by the Playwright config, so this module reads
 * that bundle. A journey that asserts a translated string names the bundle and
 * the dot path; `{{name}}` placeholders are filled from the call's variables.
 */
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export type CopyBundle =
  | 'demo'
  | 'authUi'
  | 'accountUi'
  | 'tenancyUi'
  | 'productShell'
  | 'layoutKit'
  | 'notificationUi'
  | 'uiKit'
  | 'consoleUi'

const BUNDLES: Record<CopyBundle, URL> = {
  demo: new URL('../../src/locales/en-US.json', import.meta.url),
  authUi: new URL(
    '../../../../../web/packages/auth-ui/src/locales/en-US.json',
    import.meta.url,
  ),
  accountUi: new URL(
    '../../../../../web/packages/account-ui/src/locales/en-US.json',
    import.meta.url,
  ),
  tenancyUi: new URL(
    '../../../../../web/packages/tenancy-ui/src/locales/en-US.json',
    import.meta.url,
  ),
  productShell: new URL(
    '../../../../../web/packages/product-shell/src/locales/en-US.json',
    import.meta.url,
  ),
  layoutKit: new URL(
    '../../../../../web/packages/layout-kit/src/locales/en-US.json',
    import.meta.url,
  ),
  notificationUi: new URL(
    '../../../../../web/packages/notification-ui/src/locales/en-US.json',
    import.meta.url,
  ),
  uiKit: new URL(
    '../../../../../web/packages/ui-kit/src/locales/en-US.json',
    import.meta.url,
  ),
  consoleUi: new URL(
    '../../../../../web/packages/console-ui/src/locales/en-US.json',
    import.meta.url,
  ),
}

const loaded = new Map<CopyBundle, unknown>()

/** Read and cache one bundle. */
function bundle(name: CopyBundle): unknown {
  const cached = loaded.get(name)
  if (cached !== undefined) return cached
  const path = fileURLToPath(BUNDLES[name])
  let parsed: unknown
  try {
    parsed = JSON.parse(readFileSync(path, 'utf8'))
  } catch (error) {
    throw new Error(
      `the ${name} locale bundle at ${path} could not be read: ${String(error)}`,
    )
  }
  loaded.set(name, parsed)
  return parsed
}

/** Resolve one dot path to its string, failing on a miss or a non-string. */
function lookup(name: CopyBundle, path: string): string {
  let current: unknown = bundle(name)
  for (const segment of path.split('.')) {
    if (typeof current !== 'object' || current === null || Array.isArray(current)) {
      throw new Error(
        `the ${name} locale bundle has no string at ${path}: ${segment} is not under an object`,
      )
    }
    current = (current as Record<string, unknown>)[segment]
    if (current === undefined) {
      throw new Error(`the ${name} locale bundle has no key ${path}`)
    }
  }
  if (typeof current !== 'string') {
    throw new Error(`the ${name} locale bundle key ${path} is not a string`)
  }
  return current
}

/**
 * The expected copy for one bundle key, with its `{{name}}` placeholders
 * filled. A placeholder with no value is a harness error rather than an empty
 * substitution: a test that expected `Verification is complete for .` would
 * pass for the wrong reason.
 */
export function copy(
  name: CopyBundle,
  path: string,
  variables: Readonly<Record<string, string | number>> = {},
): string {
  const template = lookup(name, path)
  return template.replace(/\{\{(\w+)\}\}/g, (match: string, key: string) => {
    const value = variables[key]
    if (value === undefined) {
      throw new Error(
        `the ${name} locale key ${path} needs a value for ${match}`,
      )
    }
    return String(value)
  })
}

/** Escape the characters a regular expression reads as syntax. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * A matcher for one bundle key whose `{{name}}` placeholders are filled by
 * patterns rather than fixed values.
 *
 * A sentence the product renders with a number the reader cannot know in
 * advance -- `You can request another code in {{seconds}} seconds.` -- still
 * has to be asserted against the bundle's own wording. Each placeholder is
 * replaced by the regular expression its caller supplies; every other
 * character is escaped, so the answer matches the product's sentence with only
 * the variable part left open. A placeholder with no pattern is a harness
 * error, like `copy`'s missing value.
 */
export function copyPattern(
  name: CopyBundle,
  path: string,
  patterns: Readonly<Record<string, string>>,
): RegExp {
  const template = lookup(name, path)
  const source = template
    .split(/(\{\{\w+\}\})/)
    .map((part) => {
      const placeholder = /^\{\{(\w+)\}\}$/.exec(part)
      if (placeholder === null) return escapeRegExp(part)
      const pattern = patterns[placeholder[1] ?? '']
      if (pattern === undefined) {
        throw new Error(
          `the ${name} locale key ${path} needs a pattern for ${part}`,
        )
      }
      return pattern
    })
    .join('')
  return new RegExp(`^${source}$`)
}
