/**
 * The theme-preset half of the boot choice: src/chrome.tsx's device storage
 * helpers and the resolution main.tsx hands the assembly as the theme's
 * defaultPreset.
 *
 * The choice is a manual one, like the colour mode, so it must land on the
 * device and win over the deployment's default on the next boot. An
 * unusable stored value is ignored rather than propagated, so a value left
 * behind by an older build cannot pin the app to a preset the shelf no
 * longer carries; an unusable deployment value falls back to the default
 * preset for the same reason.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  THEME_PRESET_STORAGE_KEY,
  initialThemePreset,
  persistThemePreset,
  readStoredThemePreset,
} from './chrome.js'

/** A browser-storage stand-in: the jsdom environment's own localStorage is
 * unavailable (the Node runtime's experimental global shadows it), so the
 * suite installs one it can seed. */
function installStorageDouble(): Map<string, string> {
  const entries = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => {
      entries.set(key, value)
    },
    removeItem: (key: string) => {
      entries.delete(key)
    },
    clear: () => entries.clear(),
  })
  return entries
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('the theme preset choice', () => {
  it('round-trips a pick through the device storage', () => {
    const entries = installStorageDouble()
    persistThemePreset('glass')
    expect(entries.get(THEME_PRESET_STORAGE_KEY)).toBe('glass')
    expect(readStoredThemePreset()).toBe('glass')
  })

  it('prefers the stored choice over the deployment default', () => {
    const entries = installStorageDouble()
    entries.set(THEME_PRESET_STORAGE_KEY, 'glass')
    expect(initialThemePreset('classic')).toBe('glass')
  })

  it('uses the deployment default when the device stored nothing', () => {
    installStorageDouble()
    expect(initialThemePreset('classic')).toBe('classic')
  })

  it('starts on the default preset when neither names one', () => {
    installStorageDouble()
    expect(initialThemePreset()).toBe('default')
  })

  it('ignores a stored value that names no shipped preset', () => {
    const entries = installStorageDouble()
    // A value an older build, a hand-edited storage or another host left
    // behind: it must not win, and it must not stop the deployment default
    // from applying either.
    entries.set(THEME_PRESET_STORAGE_KEY, 'sepia')
    expect(readStoredThemePreset()).toBeNull()
    expect(initialThemePreset('modern')).toBe('modern')
    expect(initialThemePreset()).toBe('default')
  })

  it('ignores a deployment value that names no shipped preset', () => {
    installStorageDouble()
    expect(initialThemePreset('sepia')).toBe('default')
  })

  it('survives a storage that refuses reads and writes', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new DOMException('denied', 'SecurityError')
      },
      setItem: () => {
        throw new DOMException('denied', 'SecurityError')
      },
    })
    expect(readStoredThemePreset()).toBeNull()
    expect(() => persistThemePreset('glass')).not.toThrow()
    expect(initialThemePreset('classic')).toBe('classic')
  })
})
