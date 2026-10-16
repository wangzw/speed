/**
 * The console-ui error-text whitelist's pairing with the bundle.
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
import { consoleUiResources } from '../resources.js'
import { createConsoleUiI18n } from '../../test-utils/render.js'
import { ERROR_TEXT_CODES, useConsoleUiErrorText } from './error-text.js'

/** The flat 'errors.*' dotted keys of one language bundle. */
function errorKeys(language: string): ReadonlySet<string> {
  const bundle = consoleUiResources[language] as {
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

describe('console-ui error text', () => {
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
      // The bare-HTTP family's single parameterized leaf: the resolver maps
      // the whole client.http.<status> family onto it (see the http-family
      // test below), so it has no single whitelisted code.
      'errors.client.http',
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

  it('renders each bare-HTTP status distinctly, carrying its status code', () => {
    const i18n = createConsoleUiI18n('en-US')
    const { result } = renderHook(() => useConsoleUiErrorText(), {
      wrapper: ({ children }: { readonly children: ReactNode }) =>
        createElement(I18nextProvider, { i18n }, children),
    })
    const notAllowed = result.current('client.http.405')
    const unauthorized = result.current('client.http.401')
    const serverError = result.current('client.http.500')
    // Each status keeps its own number: no longer one generic line, and
    // never the raw code or the unknown fallback.
    expect(notAllowed).toContain('405')
    expect(unauthorized).toContain('401')
    expect(serverError).toContain('500')
    expect(new Set([notAllowed, unauthorized, serverError]).size).toBe(3)
    expect(notAllowed).not.toContain('client.http.405')
    expect(notAllowed).not.toBe(
      (consoleUiResources['en-US'] as { errors: { unknown: string } }).errors
        .unknown,
    )
  })
})
