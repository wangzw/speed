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
  | 'demoNotice'
  | 'invitemail'
  | 'ownershipNotice'
  | 'authzPermissions'

export type CopyLocale = 'en-US' | 'zh-CN'

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
  demoNotice: '../../../server/demo_notification_locales',
  invitemail: '../../../../../pkg/authn/invitemail/locales',
  ownershipNotice: '../../../../../pkg/tenant/ownership/locales',
  authzPermissions: '../../../../../pkg/authz/policy/locales',
}

const loaded = new Map<string, unknown>()

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

// For a catalog whose keys are whole strings (dots are literal): read the top-level entry, not a dot-path.
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

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// Each placeholder is replaced by its caller's pattern; every other character is escaped.
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
