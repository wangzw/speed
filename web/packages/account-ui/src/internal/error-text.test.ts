/**
 * Pins the error-text whitelist to the shipped bundles in both directions,
 * and pins the codes that must stay verbatim copies of the auth-ui texts.
 */

import { describe, expect, it } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import {
  SESSION_LIFECYCLE_ERROR_CODES,
  createI18n,
  I18nextProvider,
  registerNamespace,
} from '@speed/i18n'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '../resources.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
// Test data only: JSON is no code dependency, so the same-tier rule holds.
import authUiZhCN from '../../../auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import authUiEnUS from '../../../auth-ui/src/locales/en-US.json' with { type: 'json' }
// The shipped source of the last-login-way sentence, pinned as test data.
import platformErrorsZhCN from '../../../i18n/src/platform-errors/locales/zh-CN.json' with { type: 'json' }
import platformErrorsEnUS from '../../../i18n/src/platform-errors/locales/en-US.json' with { type: 'json' }
import { ERROR_TEXT_CODES, useAccountUiErrorText } from './error-text.js'

/** The whole client.http.<status> family shares this one parameterized key. */
const HTTP_FAMILY_LEAF = 'client.http'

type Bundle = { errors: unknown }

function errorLeafKeys(bundle: Bundle): string[] {
  const leaves: string[] = []
  for (const [source, codes] of Object.entries(
    bundle.errors as unknown as Record<string, Record<string, string>>,
  )) {
    if (source === 'unknown') {
      continue
    }
    for (const code of Object.keys(codes)) {
      leaves.push(`${source}.${code}`)
    }
  }
  return leaves.sort()
}

function bundleText(bundle: Bundle, code: string): string {
  const [source, leaf] = code.split('.')
  const section = (
    bundle.errors as unknown as Record<string, Record<string, string>>
  )[source!]
  return section?.[leaf!] ?? ''
}

function platformText(bundle: Bundle, code: string): string {
  return (bundle.errors as unknown as Record<string, string>)[code] ?? ''
}

function newInstance(): ReturnType<typeof createI18n> {
  const i18n = createI18n({
    storage: null,
    urlParameterName: null,
    searchParams: null,
    navigatorLanguages: [],
    defaultLanguage: 'zh-CN',
  })
  registerNamespace(i18n, ACCOUNT_UI_NAMESPACE, accountUiResources)
  return i18n
}

/** The codes whose account-ui text must copy the auth-ui text verbatim. */
const VERBATIM_CODES: readonly string[] = [
  'authn.bad_request',
  'authn.unsupported_region',
  'authn.login_failed',
  'authn.flow_invalid',
  'authn.link_required',
  'authn.method_disabled',
  'authn.account_unavailable',
  'authn.conflict',
  'authn.throttled',
  'authn.unavailable',
  'authn.human_verification_required',
]

describe('error-text whitelist', () => {
  it('keep the whitelist and the bundle errors leaves identical, in both languages', () => {
    const expected = [...ERROR_TEXT_CODES, HTTP_FAMILY_LEAF].sort()
    expect(errorLeafKeys(zhCN)).toEqual(expected)
    expect(errorLeafKeys(enUS)).toEqual(expected)
  })

  it('resolve every whitelisted code to its own text in both languages', async () => {
    const i18n = newInstance()
    for (const code of ERROR_TEXT_CODES) {
      // fallbackLng is false, so a missing key renders as the key itself.
      const zh = i18n.t(`errors.${code}`, { ns: ACCOUNT_UI_NAMESPACE })
      expect(zh, code).not.toBe(zhCN.errors.unknown)
      expect(zh, code).not.toBe(`errors.${code}`)
      expect(zh, code).toBe(bundleText(zhCN, code))
    }
    await i18n.changeLanguage('en-US')
    for (const code of ERROR_TEXT_CODES) {
      const en = i18n.t(`errors.${code}`, { ns: ACCOUNT_UI_NAMESPACE })
      expect(en, code).not.toBe(enUS.errors.unknown)
      expect(en, code).not.toBe(`errors.${code}`)
      expect(en, code).toBe(bundleText(enUS, code))
    }
  })

  it('renders each bare-HTTP status distinctly, carrying its status code', () => {
    // Without the parameterized family these would collapse onto errors.unknown.
    const i18n = newInstance()
    const { result } = renderHook(() => useAccountUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    const notAllowed = result.current('client.http.405')
    const unauthorized = result.current('client.http.401')
    const serverError = result.current('client.http.500')
    expect(notAllowed).toContain('405')
    expect(unauthorized).toContain('401')
    expect(serverError).toContain('500')
    expect(new Set([notAllowed, unauthorized, serverError]).size).toBe(3)
    expect(notAllowed).not.toBe(zhCN.errors.unknown)
    expect(notAllowed).not.toContain('client.http')
  })

  it('names the wait a throttled ratelimit answer stated', () => {
    const i18n = newInstance()
    const { result } = renderHook(() => useAccountUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    expect(result.current('ratelimit.throttled', 45)).toBe(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '45'),
    )
    expect(result.current('ratelimit.throttled', 45)).toContain('45')
    expect(result.current('ratelimit.throttled', 45)).not.toBe(
      zhCN.errors.unknown,
    )
  })

  it('compose the shared session-lifecycle family and resolve each code to its own text', async () => {
    // Composed from @speed/i18n rather than re-listed, so this pin keeps the
    // set from being trimmed.
    const whitelist = ERROR_TEXT_CODES as readonly string[]
    for (const code of SESSION_LIFECYCLE_ERROR_CODES) {
      expect(whitelist, `${code} must be whitelisted`).toContain(code)
    }

    const i18n = newInstance()
    for (const code of SESSION_LIFECYCLE_ERROR_CODES) {
      const zh = i18n.t(`errors.${code}`, { ns: ACCOUNT_UI_NAMESPACE })
      expect(zh, code).not.toBe(zhCN.errors.unknown)
      expect(zh, code).not.toBe(`errors.${code}`)
      expect(zh, code).toBe(bundleText(zhCN, code))
      expect(zh, code).not.toBe('')
    }
    await i18n.changeLanguage('en-US')
    for (const code of SESSION_LIFECYCLE_ERROR_CODES) {
      const en = i18n.t(`errors.${code}`, { ns: ACCOUNT_UI_NAMESPACE })
      expect(en, code).not.toBe(enUS.errors.unknown)
      expect(en, code).not.toBe(`errors.${code}`)
      expect(en, code).toBe(bundleText(enUS, code))
      expect(en, code).not.toBe('')
    }
  })

  it('renders the shipped platform sentence for the last-login-way refusal', () => {
    const code = 'authn.last_login_method'
    expect(bundleText(zhCN, code)).not.toBe('')
    expect(bundleText(enUS, code)).not.toBe('')
    expect(bundleText(zhCN, code)).toBe(platformText(platformErrorsZhCN, code))
    expect(bundleText(enUS, code)).toBe(platformText(platformErrorsEnUS, code))
  })

  it('name both a username and an email address in the missing-login-identifier text', () => {
    // Either kind is a login name, and proof does not decide that, so the text
    // carries no verified qualifier.
    const zh = zhCN.errors.authn.login_identifier_required
    const en = enUS.errors.authn.login_identifier_required
    expect(zh).toContain(zhCN.identifiers.add.value.labelUsername)
    expect(zh).toContain(zhCN.identifiers.add.value.labelEmail)
    expect(zh).not.toContain(zhCN.identifiers.verified)
    expect(en).toContain('username')
    expect(en).toContain('email address')
    expect(en).not.toContain('verified')
  })

  it('keep every policy-named shared-answer text a verbatim copy of the auth-ui text for the same code', () => {
    for (const code of VERBATIM_CODES) {
      // A missing or empty source fails rather than passing a vacuous equality.
      const authZh = bundleText(authUiZhCN, code)
      const authEn = bundleText(authUiEnUS, code)
      expect(authZh, code).not.toBe('')
      expect(authEn, code).not.toBe('')
      expect(bundleText(zhCN, code), code).toBe(authZh)
      expect(bundleText(enUS, code), code).toBe(authEn)
    }
  })

  it('keep every verbatim-listed code whitelisted', () => {
    for (const code of VERBATIM_CODES) {
      expect(
        (ERROR_TEXT_CODES as readonly string[]).includes(code),
        `${code} is pinned as verbatim but not whitelisted`,
      ).toBe(true)
    }
  })
})
