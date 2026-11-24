import { describe, expect, it, vi } from 'vitest'
import { languageDisplayName } from './language-name.js'

function displayName(tag: string, uiLanguage: string): string | undefined {
  return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag)
}

describe('languageDisplayName', () => {
  it('names the tag by its base language subtag when the supported set has no variants', () => {
    for (const [tag, uiLanguage] of [
      ['en-US', 'zh-CN'],
      ['zh-CN', 'en-US'],
      ['fr-FR', 'en-US'],
    ] as const) {
      const base = new Intl.Locale(tag).language
      expect(languageDisplayName(tag, uiLanguage)).toBe(
        displayName(base, uiLanguage),
      )
    }
  })

  it('reads en-US as the base language name, not the region variant name', () => {
    for (const uiLanguage of ['en-US', 'zh-CN'] as const) {
      const baseName = displayName('en', uiLanguage)
      expect(languageDisplayName('en-US', uiLanguage, ['zh-CN', 'en-US'])).toBe(
        baseName,
      )
      expect(baseName).not.toBe(displayName('en-US', uiLanguage))
      expect(languageDisplayName('en-US', uiLanguage, [])).toBe(baseName)
      expect(languageDisplayName('en-US', uiLanguage)).toBe(baseName)
    }
  })

  it('keeps regional variants distinct when the supported set carries two of them', () => {
    const uiLanguage = 'en-US'
    const supported = ['en-US', 'en-GB'] as const
    const usName = displayName('en-US', uiLanguage)
    const gbName = displayName('en-GB', uiLanguage)
    expect(languageDisplayName('en-US', uiLanguage, supported)).toBe(usName)
    expect(languageDisplayName('en-GB', uiLanguage, supported)).toBe(gbName)
    expect(usName).not.toBe(gbName)
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
