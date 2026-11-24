import { createInstance } from 'i18next'
import { describe, expect, it, vi } from 'vitest'
import { createI18n, type MissingKeyDetails } from './index'
import {
  PLATFORM_ERRORS_NAMESPACE,
  assertPlatformErrorBundleKeySet,
  platformErrorResources,
  registerPlatformErrors,
} from './platform-errors'
import type { ResourceBundle } from './register'
import { MemoryStorage } from '../test-utils/memory-storage'
import { registerWelcome } from '../test-utils/welcome'
import bundleZh from './platform-errors/locales/zh-CN.json' with { type: 'json' }
import bundleEn from './platform-errors/locales/en-US.json' with { type: 'json' }

type Bundle = { readonly errors: Record<string, string> }
const zhBundle = bundleZh as unknown as Bundle
const enBundle = bundleEn as unknown as Bundle

function codeText(bundle: Bundle, code: string): string {
  const text = bundle.errors[code]
  if (text === undefined) {
    throw new Error(`the bundle has no entry for ${code}`)
  }
  return text
}

function createPlatformI18n(
  options: { readonly onMissingKey?: (details: MissingKeyDetails) => void } = {},
) {
  const instance = createI18n({
    storage: new MemoryStorage(),
    navigatorLanguages: [],
    onMissingKey: options.onMissingKey,
    fallbackNamespaces: [PLATFORM_ERRORS_NAMESPACE],
  })
  registerWelcome(instance)
  registerPlatformErrors(instance)
  return instance
}

describe('registerPlatformErrors', () => {
  it('registers the bundle and answers a package lookup through the fallback', () => {
    const instance = createPlatformI18n()
    const expected = codeText(enBundle, 'authn.invalid_credentials')
    expect(instance.t(`platform-errors:errors.authn.invalid_credentials`)).toBe(expected)
    expect(instance.t('welcome:errors.authn.invalid_credentials')).toBe(expected)
  })

  it('resolves each language from its own bundle', () => {
    const instance = createPlatformI18n()
    const code = 'authn.invalid_credentials'
    expect(codeText(zhBundle, code)).not.toBe(codeText(enBundle, code))
    expect(instance.t(`platform-errors:errors.${code}`)).toBe(codeText(enBundle, code))
  })

  it('refuses a supported language the bundle does not ship, naming it', () => {
    const instance = createI18n({
      storage: new MemoryStorage(),
      navigatorLanguages: [],
      supportedLanguages: ['zh-CN', 'en-US', 'fr-FR'],
    })
    expect(() => registerPlatformErrors(instance)).toThrow(/fr-FR/)
  })

  it('refuses a bare i18next instance, naming createI18n', () => {
    expect(() => registerPlatformErrors(createInstance())).toThrow(/createI18n/)
  })

  it('refuses a second registration on the same instance', () => {
    const instance = createPlatformI18n()
    expect(() => registerPlatformErrors(instance)).toThrow(/already registered/)
  })
})

describe('platformErrorResources', () => {
  it('exports the shipped bundles keyed by language', () => {
    expect(Object.keys(platformErrorResources).sort()).toEqual(['en-US', 'zh-CN'])
    expect(platformErrorResources['en-US']).toEqual(enBundle)
    expect(platformErrorResources['zh-CN']).toEqual(zhBundle)
  })
})

describe('registerPlatformErrors with host-supplied languages', () => {
  const frResources: Readonly<Record<string, ResourceBundle>> = {
    'fr-FR': {
      errors: Object.fromEntries(
        Object.keys(enBundle.errors).map((code) => [code, `fr:${code}`]),
      ),
    },
  }

  function createWideI18n() {
    return createI18n({
      storage: new MemoryStorage(),
      navigatorLanguages: [],
      supportedLanguages: ['zh-CN', 'en-US', 'fr-FR'],
      fallbackNamespaces: [PLATFORM_ERRORS_NAMESPACE],
    })
  }

  it('registers a whole language the package does not carry', () => {
    const instance = createWideI18n()
    registerPlatformErrors(instance, frResources)
    expect(
      instance.t('platform-errors:errors.authn.invalid_credentials', {
        lng: 'fr-FR',
      }),
    ).toBe('fr:authn.invalid_credentials')
    expect(
      instance.t('platform-errors:errors.authn.invalid_credentials', {
        lng: 'en-US',
      }),
    ).toBe(codeText(enBundle, 'authn.invalid_credentials'))
  })

  it('refuses a supplied language whose key set differs from the package', () => {
    const instance = createWideI18n()
    expect(() =>
      registerPlatformErrors(instance, {
        'fr-FR': { errors: { 'authn.invalid_credentials': 'fr:invalid' } },
      }),
    ).toThrow(/packaged key set/)
  })

  it('refuses supplying a language the package already carries', () => {
    const instance = createWideI18n()
    expect(() =>
      registerPlatformErrors(instance, {
        'en-US': { errors: { 'authn.invalid_credentials': 'us:invalid' } },
      }),
    ).toThrow(/already ships/)
  })

  it('refuses a supplied language outside the supported set', () => {
    const instance = createWideI18n()
    expect(() =>
      registerPlatformErrors(instance, {
        ...frResources,
        'de-DE': frResources['fr-FR']!,
      }),
    ).toThrow(/not among the instance's supported languages/)
  })

  function createFrenchOnlyI18n() {
    return createI18n({
      storage: new MemoryStorage(),
      navigatorLanguages: [],
      supportedLanguages: ['fr-FR'],
      defaultLanguage: 'fr-FR',
    })
  }

  it('registers a supplied language even when the set omits the packaged pair', () => {
    const instance = createFrenchOnlyI18n()
    registerPlatformErrors(instance, frResources)
    expect(instance.t('platform-errors:errors.authn.invalid_credentials')).toBe(
      'fr:authn.invalid_credentials',
    )
  })

  it('compares a supplied language to the packaged bundle, not only to registered peers', () => {
    const instance = createFrenchOnlyI18n()
    expect(() =>
      registerPlatformErrors(instance, {
        'fr-FR': { errors: { 'authn.invalid_credentials': 'fr:invalid' } },
      }),
    ).toThrow(/must carry the packaged key set/)
  })
})

describe('the bundle contents', () => {
  it('carry the same key set in both languages', () => {
    expect(Object.keys(enBundle.errors).length).toBeGreaterThan(0)
    expect(Object.keys(zhBundle.errors).sort()).toEqual(
      Object.keys(enBundle.errors).sort(),
    )
  })

  it('never carries a backend content entry', () => {
    const onMissingKey = vi.fn()
    const instance = createPlatformI18n({ onMissingKey })
    const contentIds = [
      'org.invitation.subject',
      'authn.sms.verification_code',
      'notification.contact.verify_code.email.subject',
    ]
    for (const id of contentIds) {
      expect(enBundle.errors).not.toHaveProperty(id)
      expect(zhBundle.errors).not.toHaveProperty(id)
      expect(instance.t(`welcome:errors.${id}`)).toBe(`errors.${id}`)
    }
    expect(onMissingKey).toHaveBeenCalledTimes(contentIds.length)
  })

  it('carries i18next placeholders, with no Go template syntax left over', () => {
    for (const [code, text] of Object.entries(enBundle.errors)) {
      expect(text.length, code).toBeGreaterThan(0)
      expect(text, code).not.toContain('{{.')
      for (const placeholder of text.matchAll(/\{\{([^}]*)\}\}/g)) {
        expect(placeholder[1], code).toMatch(/^[A-Za-z_]\w*$/)
      }
    }
  })

  it('names the ratelimit refusal without a wait only a Retry-After can supply', () => {
    expect(codeText(enBundle, 'ratelimit.throttled')).not.toMatch(/\{\{.*\}\}/)
    expect(codeText(zhBundle, 'ratelimit.throttled')).not.toMatch(/\{\{.*\}\}/)
  })

  it('accepts the packaged bundle and refuses one missing a code', () => {
    const en = platformErrorResources['en-US']!
    expect(() => assertPlatformErrorBundleKeySet('en-US', en)).not.toThrow()
    const partial: ResourceBundle = { errors: { authn: { invalid_credentials: 'x' } } }
    expect(() => assertPlatformErrorBundleKeySet('fr-FR', partial)).toThrow(
      /packaged key set/,
    )
  })

  it('refuses a bundle carrying a code the packaged one lacks', () => {
    const en = platformErrorResources['en-US'] as {
      readonly errors: Readonly<Record<string, string>>
    }
    const withExtra = {
      errors: { ...en.errors, invented_code: 'x' },
    }
    expect(() => assertPlatformErrorBundleKeySet('en-US', withExtra)).toThrow(
      /not in the packaged bundle/,
    )
  })
})
