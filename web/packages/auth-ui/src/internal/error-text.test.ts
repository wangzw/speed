import { describe, expect, it } from 'vitest'
import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { AUTH_UI_NAMESPACE, authUiResources } from '../resources.js'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { ERROR_TEXT_CODES, useAuthUiErrorText } from './error-text.js'

type Bundle = typeof zhCN

const HTTP_FAMILY_LEAF = 'client.http'

/** The code leaf keys of the errors section, e.g. 'authn.invalid_email'. */
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

function newInstance(): ReturnType<typeof createI18n> {
  const i18n = createI18n({
    storage: null,
    urlParameterName: null,
    searchParams: null,
    navigatorLanguages: [],
    defaultLanguage: 'zh-CN',
  })
  registerNamespace(i18n, AUTH_UI_NAMESPACE, authUiResources)
  return i18n
}

describe('error-text whitelist', () => {
  it('keep the whitelist and the bundle errors leaves identical, in both languages', () => {
    const expected = [...ERROR_TEXT_CODES, HTTP_FAMILY_LEAF].sort()
    expect(errorLeafKeys(zhCN)).toEqual(expected)
    expect(errorLeafKeys(enUS)).toEqual(expected)
  })

  it('resolve every whitelisted code to its own text in both languages', async () => {
    const i18n = newInstance()
    for (const code of ERROR_TEXT_CODES) {
      const zh = i18n.t(`errors.${code}`, { ns: AUTH_UI_NAMESPACE })
      expect(zh, code).not.toBe(zhCN.errors.unknown)
      expect(zh, code).not.toBe(`errors.${code}`)
      expect(zh, code).toBe(bundleText(zhCN, code))
    }
    await i18n.changeLanguage('en-US')
    for (const code of ERROR_TEXT_CODES) {
      const en = i18n.t(`errors.${code}`, { ns: AUTH_UI_NAMESPACE })
      expect(en, code).not.toBe(enUS.errors.unknown)
      expect(en, code).not.toBe(`errors.${code}`)
      expect(en, code).toBe(bundleText(enUS, code))
    }
  })

  it('renders each bare-HTTP status distinctly, carrying its status code', () => {
    const i18n = newInstance()
    const { result } = renderHook(() => useAuthUiErrorText(), {
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
  })

  it('cover the login-flow problem set the flow endpoints answer with', () => {
    expect(ERROR_TEXT_CODES).toEqual(
      expect.arrayContaining([
        'authn.login_failed',
        'authn.flow_invalid',
        'authn.unsupported_region',
        'authn.method_disabled',
      ]),
    )
  })
})
