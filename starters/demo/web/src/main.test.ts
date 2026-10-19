/**
 * The boot theme's persisted half: src/main.tsx hands the assembly the
 * device's remembered colour mode (demoThemeOptions' defaultMode), so a reload
 * starts in the mode the user left.
 *
 * The suite's setup stubs matchMedia to match every query, so the
 * operating-system branch answers dark. A stored light choice must still win
 * -- that is what distinguishes reading the persisted value from following the
 * system -- and a stored dark choice must reach the assembly unchanged.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { COLOR_MODE_STORAGE_KEY } from './chrome.js'
import { demoThemeOptions } from './main.js'

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

describe('the boot theme', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('starts in a stored light choice over the system preference', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'light')
    expect(demoThemeOptions().defaultMode).toBe('light')
  })

  it('starts in the stored dark choice', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'dark')
    expect(demoThemeOptions().defaultMode).toBe('dark')
  })

  it('keeps the platform palette as the brand placeholder', () => {
    installStorageDouble()
    expect(demoThemeOptions().projectTokens).toEqual({})
  })
})
