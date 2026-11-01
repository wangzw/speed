/**
 * The error-text whitelist contract, pinned in both directions against
 * the shipped bundles: ERROR_TEXT_CODES (the reachable-subset whitelist
 * of the signed-in account surface) and the errors-section leaves of
 * each language file must name exactly the same codes -- errors.unknown
 * is the one errors-section key with no code, the fallback itself. A
 * code added to the whitelist without its two bundle keys, or a bundle
 * key added without its whitelist code, fails the equality pin here; key
 * parity between the two language files is enforced earlier, by
 * registerNamespace, so a key missing from one language fails
 * registration before any component can ship it.
 *
 * The render walk adds the value half: every whitelisted code must
 * resolve through the registered namespace to its own dedicated text in
 * both languages -- never the errors.unknown fallback and never a raw
 * key. Component-level render coverage (the role="alert" banner for a
 * given code) lives in the component suites beside each failure path;
 * this file pins the whole-list pairing that the banner depends on.
 *
 * The wording policy of error-text.ts adds a third pin: the codes whose
 * failure context is identical to the sign-in surface's must stay
 * verbatim copies of the auth-ui texts for the same codes, in both
 * languages, so the same server answer reads the same on the account
 * page and the sign-in surface. The pin must live in this package's own
 * suite: another package's whitelist sharing some of these codes covers
 * them only as a side effect of its own whitelist, never as this
 * package's protection -- that package can narrow its whitelist for its
 * own reasons and silently drop the coverage. The shared codes are
 * pinned here, against the auth-ui bundles imported below as test data.
 * The account-only codes (insufficient_user_authentication,
 * impersonation_restricted, forbidden, login_identifier_required,
 * username_immutable, unsupported_media_type) carry account-authored texts
 * and carry no copy claim to pin.
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
// The same-tier rule keeps this package from importing auth-ui's catalog
// at runtime; the verbatim-copy pin below imports the source bundles
// here, as test data only -- JSON data is no code dependency, so this
// crosses no package boundary the rule draws.
import authUiZhCN from '../../../auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import authUiEnUS from '../../../auth-ui/src/locales/en-US.json' with { type: 'json' }
// The platform catalog is the shipped source of the last-login-way sentence.
// account-ui carries its own bundle, so the copy is pinned here to the source
// text as test data, the same way the auth-ui copies above are.
import platformErrorsZhCN from '../../../i18n/src/platform-errors/locales/zh-CN.json' with { type: 'json' }
import platformErrorsEnUS from '../../../i18n/src/platform-errors/locales/en-US.json' with { type: 'json' }
import { ERROR_TEXT_CODES, useAccountUiErrorText } from './error-text.js'

/**
 * The one errors-section leaf with no single whitelisted code: the
 * bare-HTTP family (client.http.<status>) resolves onto it, the whole
 * status family sharing one parameterized key.
 */
const HTTP_FAMILY_LEAF = 'client.http'

/** A language bundle's errors section, structural so both packages'
 * bundles (account-ui's own and the auth-ui bundles imported as test
 * data) satisfy the helpers below. */
type Bundle = { errors: unknown }

/** The code leaf keys of the errors section, e.g. 'authn.bad_request'. */
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

/** The errors-section value a whitelisted code must resolve to. */
function bundleText(bundle: Bundle, code: string): string {
  const [source, leaf] = code.split('.')
  const section = (
    bundle.errors as unknown as Record<string, Record<string, string>>
  )[source!]
  return section?.[leaf!] ?? ''
}

/** The flat, dotted-key catalog value of the platform error bundle. */
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

/**
 * The whitelisted codes whose account-ui text is a verbatim copy of the
 * auth-ui text for the same code -- the codes error-text.ts's wording
 * policy names, restricted to the ones the auth-ui bundle actually
 * carries. The account-only codes and the client-transport family are
 * outside the claim: no auth-ui leaf exists for them.
 */
const VERBATIM_CODES: readonly string[] = [
  // authn: the problem codes both surfaces render for the same server
  // answer -- the account page and the sign-in surface read them alike.
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
      // Dedicated text, never the unknown fallback and never a raw key:
      // fallbackLng is false, so a missing key would render as the key
      // itself -- asserted against both failure shapes.
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
    // A non-2xx with no readable envelope arrives as client.http.<status>.
    // It is outside the fixed whitelist, so without the parameterized
    // family it would collapse onto errors.unknown; the family leaf keeps
    // the status visible and keeps different statuses apart.
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
    // The address and quota layers refuse a throttled request with
    // ratelimit.throttled and a Retry-After; the sentence names how long
    // the caller must wait rather than only restating the refusal.
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
    // A session that dies mid-flight answers through the refresh leg with
    // one of these codes; the account surface must render its own sentence
    // for each, never the unknown fallback. The family is composed from
    // @speed/i18n rather than re-listed, so this pin also keeps the set
    // from being trimmed.
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
    // The account page refuses to remove the last way to sign in; the answer
    // is authn.last_login_method, and its text is the one the platform error
    // catalog already ships. The copy in this package's bundle must be that
    // sentence verbatim, in both languages, so the same server answer reads
    // the same wherever the catalog is rendered.
    const code = 'authn.last_login_method'
    expect(bundleText(zhCN, code)).not.toBe('')
    expect(bundleText(enUS, code)).not.toBe('')
    expect(bundleText(zhCN, code)).toBe(platformText(platformErrorsZhCN, code))
    expect(bundleText(enUS, code)).toBe(platformText(platformErrorsEnUS, code))
  })

  it('name both a username and an email address in the missing-login-identifier text', () => {
    // The wording is pinned through the bundle's own labels: either kind is a
    // login name, and the proving channel does not decide that -- the text
    // therefore carries no verified qualifier, and it must read the same for
    // the set-password and remove-last-login-name refusals that share the code.
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
      // The auth-ui leaf is the copy's source: a missing or empty one
      // fails here rather than passing a vacuous equality, in either
      // language.
      const authZh = bundleText(authUiZhCN, code)
      const authEn = bundleText(authUiEnUS, code)
      expect(authZh, code).not.toBe('')
      expect(authEn, code).not.toBe('')
      expect(bundleText(zhCN, code), code).toBe(authZh)
      expect(bundleText(enUS, code), code).toBe(authEn)
    }
  })

  it('keep every verbatim-listed code whitelisted', () => {
    // The pin list must not drift into dead copy of its own: a code the
    // whitelist no longer draws is a code the pin should stop claiming,
    // not one it quietly stops enforcing -- the wording policy of
    // error-text.ts names the same set, and the pairing test above
    // already tied each whitelisted code to its two bundle leaves.
    for (const code of VERBATIM_CODES) {
      expect(
        (ERROR_TEXT_CODES as readonly string[]).includes(code),
        `${code} is pinned as verbatim but not whitelisted`,
      ).toBe(true)
    }
  })
})
