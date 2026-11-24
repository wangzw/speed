import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  THEME_PRESET_STORAGE_KEY,
  initialThemePreset,
  persistThemePreset,
  readStoredThemePreset,
} from './chrome.js'

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
