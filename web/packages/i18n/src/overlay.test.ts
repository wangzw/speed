import { describe, expect, it } from 'vitest'
import { createI18n } from './create.js'
import { overlayResources } from './overlay.js'
import { registerNamespace, type ResourceBundle } from './register.js'
import { MemoryStorage } from '../test-utils/memory-storage'

const base: Readonly<Record<string, ResourceBundle>> = {
  'en-US': {
    errors: { unknown: 'Unknown failure', other: 'Other failure' },
    page: { title: 'Page' },
  },
}

describe('overlayResources', () => {
  it('replaces an existing leaf and keeps every other leaf', () => {
    const merged = overlayResources(base, {
      'en-US': { errors: { unknown: 'Overridden failure' } },
    })
    expect(merged['en-US']).toEqual({
      errors: { unknown: 'Overridden failure', other: 'Other failure' },
      page: { title: 'Page' },
    })
  })

  it('leaves the base and the overlay untouched', () => {
    const overlay: Readonly<Record<string, ResourceBundle>> = {
      'en-US': { errors: { unknown: 'Overridden failure' } },
    }
    const merged = overlayResources(base, overlay)
    expect(base).toEqual({
      'en-US': {
        errors: { unknown: 'Unknown failure', other: 'Other failure' },
        page: { title: 'Page' },
      },
    })
    expect(overlay).toEqual({
      'en-US': { errors: { unknown: 'Overridden failure' } },
    })
    const replaced = merged['en-US'] as Record<string, unknown>
    replaced['extra'] = 'mutated'
    expect(base['en-US']).not.toHaveProperty('extra')
  })

  it('refuses a leaf the base does not carry', () => {
    expect(() =>
      overlayResources(base, {
        'en-US': { errors: { unknown: 'Overridden failure', missing: 'New' } },
      }),
    ).toThrow(/no such leaf/)
  })

  it('refuses a subtree the base does not carry', () => {
    expect(() =>
      overlayResources(base, {
        'en-US': { added: { leaf: 'New' } },
      }),
    ).toThrow(/no such subtree/)
  })

  it('refuses replacing a subtree with a leaf', () => {
    expect(() =>
      overlayResources(base, {
        'en-US': { errors: 'Collapsed' },
      }),
    ).toThrow(/no such leaf/)
  })

  it('takes a whole new language as supplied', () => {
    const merged = overlayResources(base, {
      'fr-FR': { errors: { unknown: 'Echec inconnu', other: 'Autre echec' } },
    })
    expect(Object.keys(merged).sort()).toEqual(['en-US', 'fr-FR'])
    expect(merged['fr-FR']).toEqual({
      errors: { unknown: 'Echec inconnu', other: 'Autre echec' },
    })
  })

  it('lets a supplied language register when its key set matches', () => {
    const instance = createI18n({
      storage: new MemoryStorage(),
      navigatorLanguages: [],
      supportedLanguages: ['en-US', 'fr-FR'],
    })
    const merged = overlayResources(base, {
      'fr-FR': {
        errors: { unknown: 'Echec inconnu', other: 'Autre echec' },
        page: { title: 'Page' },
      },
    })
    registerNamespace(instance, 'overlay', merged)
    expect(instance.t('overlay:page.title', { lng: 'fr-FR' })).toBe('Page')
    expect(instance.t('overlay:errors.unknown', { lng: 'en-US' })).toBe(
      'Unknown failure',
    )
  })

  it('leaves a supplied language to registration when it misses a key', () => {
    const instance = createI18n({
      storage: new MemoryStorage(),
      navigatorLanguages: [],
      supportedLanguages: ['en-US', 'fr-FR'],
    })
    const merged = overlayResources(base, {
      'fr-FR': { errors: { unknown: 'Echec inconnu' } },
    })
    expect(() => registerNamespace(instance, 'overlay', merged)).toThrow(
      /same key set/,
    )
  })
})
