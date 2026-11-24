import { describe, expect, it } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { createErrorTextResolver, I18nextProvider } from '@speed/i18n'
import { adminUiResources } from '../resources.js'
import { createAdminUiI18n } from '../../test-utils/render.js'
import { ERROR_TEXT_CODES, useAdminUiErrorText } from './error-text.js'

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
    expect(forbidden).toBe(bundle.errors.client.httpForbidden)
    expect(unauthorized).toBe(bundle.errors.client.httpUnauthorized)
    expect(forbidden).not.toContain('403')
    expect(unauthorized).not.toContain('401')
    expect(new Set([forbidden, unauthorized]).size).toBe(2)
    expect(notAllowed).toContain('405')
    expect(serverError).toContain('500')
    expect(notAllowed).not.toContain('client.http.405')
    expect(notAllowed).not.toBe(bundle.errors.unknown)
  })

  it('renders a throttled answer with no stated wait as a complete sentence', () => {
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
      'tenant.conflict',
      'org.creation_in_progress',
      'org.identifier_unavailable',
      'org.organization_limit',
      'org.rate_limited',
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
      'authn.invitation_target_invalid',
      'authn.invitation_expiry_too_long',
      'authn.invitation_member',
      'authn.invitation_pending',
      'authn.invitation_terminal',
      'authn.account_last_tenant_admin',
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
