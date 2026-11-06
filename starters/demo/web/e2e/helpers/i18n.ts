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
 * The browser is pinned to en-US by the Playwright config, so a journey that
 * asserts the starting interface reads the en-US bundle. A journey that
 * switches the interface's language (the personalization journey chooses
 * zh-CN) names that language explicitly: the product's copy after the switch
 * comes from the zh-CN bundle, and asserting it against en-US would be both
 * wrong and a hardcoded literal. A journey that asserts a translated string
 * names the bundle, the dot path and optionally the language; `{{name}}`
 * placeholders are filled from the call's variables.
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
  | 'adminUi'
  | 'orgCreateUi'
  | 'reviewNotice'
  | 'invitemail'
  | 'authzPermissions'

/** The languages the deployment ships bundles for. */
export type CopyLocale = 'en-US' | 'zh-CN'

/** The bundle directory; the language's file name is appended to it. */
const BUNDLES: Record<CopyBundle, string> = {
  demo: '../../src/locales',
  authUi: '../../../../../web/packages/auth-ui/src/locales',
  accountUi: '../../../../../web/packages/account-ui/src/locales',
  tenancyUi: '../../../../../web/packages/tenancy-ui/src/locales',
  productShell: '../../../../../web/packages/product-shell/src/locales',
  layoutKit: '../../../../../web/packages/layout-kit/src/locales',
  notificationUi: '../../../../../web/packages/notification-ui/src/locales',
  uiKit: '../../../../../web/packages/ui-kit/src/locales',
  adminUi: '../../../../../web/packages/admin-ui/src/locales',
  orgCreateUi: '../../../../../web/packages/org-create-ui/src/locales',
  // The review assembly's notification catalog. The delivery pipeline renders
  // an in-app message from "<type_key>.<channel>.<part>" in the module's own
  // catalog, and the review build is where a type exists, so its text is a
  // shipped product string like any other. Its keys carry dots as literal
  // characters; `copyKey` reads them that way.
  reviewNotice: '../../../server/review_notification_locales',
  // The invitation notice the backend renders and the review mail carrier
  // writes to the process log. Its keys carry dots as literal characters; read
  // them through `copyKey`.
  invitemail: '../../../../../pkg/authn/invitemail/locales',
  // The governed permission catalog's own descriptions, rendered by the
  // authorization surface in the request's language. The keys are the
  // permission codes with their separators turned into dashes; read them
  // through `copyKey`.
  authzPermissions: '../../../../../pkg/authz/policy/locales',
}

const loaded = new Map<string, unknown>()

/** Read and cache one bundle in one language. */
function bundle(name: CopyBundle, locale: CopyLocale): unknown {
  const cacheKey = `${name}\u0000${locale}`
  const cached = loaded.get(cacheKey)
  if (cached !== undefined) return cached
  const path = fileURLToPath(
    new URL(`${BUNDLES[name]}/${locale}.json`, import.meta.url),
  )
  let parsed: unknown
  try {
    parsed = JSON.parse(readFileSync(path, 'utf8'))
  } catch (error) {
    throw new Error(
      `the ${name} ${locale} locale bundle at ${path} could not be read: ${String(error)}`,
    )
  }
  loaded.set(cacheKey, parsed)
  return parsed
}

/** Resolve one dot path to its string, failing on a miss or a non-string. */
function lookup(name: CopyBundle, path: string, locale: CopyLocale): string {
  let current: unknown = bundle(name, locale)
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
  locale: CopyLocale = 'en-US',
): string {
  const template = lookup(name, path, locale)
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

/**
 * The expected copy for one bundle key read literally.
 *
 * A catalog whose keys are themselves whole strings -- the review fixture's
 * notification text is keyed `<type_key>.<channel>.<part>` -- cannot go through
 * `copy`'s dot-path lookup, which reads those dots as nesting. This reads the
 * top-level entry under the exact key. A missing key or a non-string value is
 * a harness error, the same as `copy`. `variables` fill the entry's `{{name}}`
 * placeholders; a placeholder with no value is a harness error, like `copy`'s.
 */
export function copyKey(
  name: CopyBundle,
  key: string,
  locale: CopyLocale = 'en-US',
  variables: Readonly<Record<string, string | number>> = {},
): string {
  const catalog = bundle(name, locale)
  if (typeof catalog !== 'object' || catalog === null || Array.isArray(catalog)) {
    throw new Error(`the ${name} locale bundle is not an object`)
  }
  const value = (catalog as Record<string, unknown>)[key]
  if (typeof value !== 'string') {
    throw new Error(`the ${name} locale bundle has no string at the key ${key}`)
  }
  return value.replace(/\{\{(\w+)\}\}/g, (match: string, variable: string) => {
    const replacement = variables[variable]
    if (replacement === undefined) {
      throw new Error(
        `the ${name} locale key ${key} needs a value for ${match}`,
      )
    }
    return String(replacement)
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
  locale: CopyLocale = 'en-US',
): RegExp {
  const template = lookup(name, path, locale)
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
