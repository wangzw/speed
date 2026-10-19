/**
 * language-name.ts behaviour: the UI-language name of a tag through
 * Intl.DisplayNames, and the raw-tag fallback when the engine cannot
 * answer. The expected values are computed through Intl itself, so the
 * test does not pin one ICU release's exact wording.
 */

import { describe, expect, it, vi } from 'vitest'
import { languageDisplayName } from './language-name.js'

describe('languageDisplayName', () => {
  it('names the tag in the UI language', () => {
    for (const [tag, uiLanguage] of [
      ['en-US', 'zh-CN'],
      ['zh-CN', 'en-US'],
      ['fr-FR', 'en-US'],
    ] as const) {
      const expected = new Intl.DisplayNames([uiLanguage], {
        type: 'language',
      }).of(tag)
      expect(languageDisplayName(tag, uiLanguage)).toBe(expected)
    }
  })

  it('falls back to the raw tag when the engine refuses the construction', () => {
    const broken = function (): never {
      throw new RangeError('test: Intl.DisplayNames unavailable')
    } as unknown as typeof Intl.DisplayNames
    const spy = vi.spyOn(Intl, 'DisplayNames').mockImplementation(broken)
    try {
      expect(languageDisplayName('en-US', 'en-US')).toBe('en-US')
    } finally {
      spy.mockRestore()
    }
  })

  it('falls back to the raw tag when the lookup answers undefined', () => {
    const nameless = function (): Intl.DisplayNames {
      return {
        of: () => undefined,
        resolvedOptions: () => ({
          locale: 'en-US',
          type: 'language',
          fallback: 'code',
        }),
      } as unknown as Intl.DisplayNames
    } as unknown as typeof Intl.DisplayNames
    const spy = vi.spyOn(Intl, 'DisplayNames').mockImplementation(nameless)
    try {
      expect(languageDisplayName('und', 'en-US')).toBe('und')
    } finally {
      spy.mockRestore()
    }
  })
})
