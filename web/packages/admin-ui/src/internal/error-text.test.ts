/**
 * The admin-ui error-text whitelist's pairing with the bundle.
 *
 * Every code the resolver whitelists must carry its own errors-section
 * leaf in both languages (a code added to the whitelist without its two
 * bundle keys -- or a bundle key without its whitelisted code -- fails
 * here), and a code outside the whitelist must resolve to the shared
 * unknown fallback so the bundle can never render a raw key.
 */

import { describe, expect, it } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { createErrorTextResolver, I18nextProvider } from '@speed/i18n'
import { adminUiResources } from '../resources.js'
import { createAdminUiI18n } from '../../test-utils/render.js'
import { ERROR_TEXT_CODES, useAdminUiErrorText } from './error-text.js'

/** The flat 'errors.*' dotted keys of one language bundle. */
function errorKeys(language: string): ReadonlySet<string> {
  const bundle = adminUiResources[language] as {
    readonly errors: Record<string, unknown>
  }
  const keys = new Set<string>()
  const walk = (value: Record<string, unknown>, prefix: string): void => {
    for (const [key, child] of Object.entries(value)) {
      const path = `${prefix}.${key}`
      if (child !== null && typeof child === 'object' && !Array.isArray(child)) {
        walk(child as Record<string, unknown>, path)
      } else {
        keys.add(path)
      }
    }
  }
  walk(bundle.errors, 'errors')
  return keys
}

describe('admin-ui error text', () => {
  for (const language of ['zh-CN', 'en-US']) {
    it(`pairs every whitelisted code with an errors leaf in ${language}`, () => {
      const keys = errorKeys(language)
      for (const code of ERROR_TEXT_CODES) {
        expect(keys, `missing errors.${code} in ${language}`).toContain(
          `errors.${code}`,
        )
      }
      expect(keys).toContain('errors.unknown')
    })
  }

  it('carries no errors leaf without a whitelisted code, apart from the fallback', () => {
    const whitelisted = new Set<string>([
      ...ERROR_TEXT_CODES.map((code) => `errors.${code}`),
      'errors.unknown',
      // The bare-HTTP family's parameterized leaf and the two refusal leaves
      // the resolver maps a client.http.<status> code onto (see the http-family
      // test below), so none of them has a single whitelisted code.
      'errors.client.http',
      'errors.client.httpForbidden',
      'errors.client.httpUnauthorized',
    ])
    for (const key of errorKeys('en-US')) {
      expect(whitelisted, `${key} has no whitelisted code`).toContain(key)
    }
  })

  it('resolves a whitelisted code to its own leaf and anything else to unknown', () => {
    const texts: Record<string, string> = {
      'errors.tenant.conflict': 'CONFLICT',
      'errors.unknown': 'UNKNOWN',
    }
    const resolve = createErrorTextResolver(
      (key) => texts[key] ?? `RAW:${key}`,
      new Set<string>(['tenant.conflict']),
    )
    expect(resolve('tenant.conflict')).toBe('CONFLICT')
    expect(resolve('client.whatever')).toBe('UNKNOWN')
  })

  it('renders a bare 403 and 401 as the actionable copy, other statuses with their code', () => {
    const i18n = createAdminUiI18n('en-US')
    const { result } = renderHook(() => useAdminUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    const forbidden = result.current('client.http.403')
    const unauthorized = result.current('client.http.401')
    const notAllowed = result.current('client.http.405')
    const serverError = result.current('client.http.500')
    const bundle = adminUiResources['en-US'] as {
      errors: {
        unknown: string
        client: { httpForbidden: string; httpUnauthorized: string }
      }
    }
    // A write refused with no readable envelope tells the operator what to
    // do: the permission copy for a 403, the ended-session copy for a 401 --
    // neither restates the status.
    expect(forbidden).toBe(bundle.errors.client.httpForbidden)
    expect(unauthorized).toBe(bundle.errors.client.httpUnauthorized)
    expect(forbidden).not.toContain('403')
    expect(unauthorized).not.toContain('401')
    expect(new Set([forbidden, unauthorized]).size).toBe(2)
    // Every other status keeps its own parameterized line, never the raw
    // code or the unknown fallback.
    expect(notAllowed).toContain('405')
    expect(serverError).toContain('500')
    expect(notAllowed).not.toContain('client.http.405')
    expect(notAllowed).not.toBe(bundle.errors.unknown)
  })

  it('renders a throttled answer with no stated wait as a complete sentence', () => {
    // A refusal that stated no Retry-After reaches the resolver without a
    // wait, so it resolves through the code's own errors leaf. That leaf
    // must read as a sentence: the seconds form lives in throttle.retryAfter
    // and is used only when a wait is known, so a placeholder left in the
    // leaf would render literally (the bundle escapes nothing by default)
    // instead of naming a time.
    for (const language of ['zh-CN', 'en-US']) {
      const i18n = createAdminUiI18n(language)
      const { result } = renderHook(() => useAdminUiErrorText(), {
        wrapper: ({ children }: { readonly children: ReactNode }) =>
          createElement(I18nextProvider, { i18n }, children),
      })
      const text = result.current('ratelimit.throttled')
      const bundle = adminUiResources[language] as {
        errors: { unknown: string; ratelimit: { throttled: string } }
      }
      expect(text, language).toBe(bundle.errors.ratelimit.throttled)
      expect(text, language).not.toMatch(/\{\{.*\}\}/)
      expect(text, language).not.toBe(bundle.errors.unknown)
    }
  })

  it('names the wait a throttled ratelimit answer stated', () => {
    // The step-up flow's start and answers can be refused by the ratelimit
    // layer with a Retry-After; the sentence names how long the operator
    // must wait instead of collapsing onto the unknown fallback.
    const i18n = createAdminUiI18n('en-US')
    const { result } = renderHook(() => useAdminUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    const bundle = adminUiResources['en-US'] as {
      throttle: { retryAfter: string }
      errors: { unknown: string }
    }
    expect(result.current('ratelimit.throttled', 45)).toBe(
      bundle.throttle.retryAfter.replace('{{seconds}}', '45'),
    )
    expect(result.current('ratelimit.throttled', 45)).toContain('45')
    expect(result.current('ratelimit.throttled', 45)).not.toBe(
      bundle.errors.unknown,
    )
  })

  it('renders the protected settings refusal with its own copy', () => {
    // The tenant settings write seam refuses a protected item with a 403
    // settings.protected. Without its own leaf the organization settings page
    // falls back to the generic failure copy, which says nothing about the
    // deployment having protected the item.
    for (const language of ['zh-CN', 'en-US']) {
      const i18n = createAdminUiI18n(language)
      const { result } = renderHook(() => useAdminUiErrorText(), {
        wrapper: ({ children }: { readonly children: ReactNode }) =>
          createElement(I18nextProvider, { i18n }, children),
      })
      const bundle = adminUiResources[language] as {
        errors: { unknown: string; settings: { protected: string } }
      }
      const text = result.current('settings.protected')
      expect(text, language).toBe(bundle.errors.settings.protected)
      expect(text, language).not.toBe(bundle.errors.unknown)
      expect(text, language).not.toContain('settings.protected')
    }
  })

  it('resolves every module refusal a write can be answered with to its own text', () => {
    // The api-client derives a module code from a refusal's RFC 9457
    // problem `type` (urn:speed:<module>:<code>), so a console write the
    // server refuses arrives as one of these codes; each must render its
    // own leaf rather than collapse into the generic fallback the surface
    // showed while no module code reached it.
    const i18n = createAdminUiI18n('en-US')
    const { result } = renderHook(() => useAdminUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    const unknown = (
      adminUiResources['en-US'] as { errors: { unknown: string } }
    ).errors.unknown
    for (const code of [
      'tenant.bad_request',
      'authz.bad_request',
      'authz.conflict',
      'authz.forbidden',
      'authz.protected',
      'authn.bad_request',
      'authn.unsupported_region',
      'authn.conflict',
      'authn.email_taken',
      'authn.login_identifier_required',
      'authn.username_immutable',
      'authn.forbidden',
      'authn.impersonation_restricted',
      'authn.factor_not_seasoned',
      'authn.human_verification_required',
      'audit.bad_request',
      'settings.bad_request',
      'sms.bad_request',
      'sms.conflict',
      'sms.config_template',
    ]) {
      const text = result.current(code)
      expect(text, code).not.toBe(unknown)
      expect(text, code).not.toContain(code)
    }
  })
})
