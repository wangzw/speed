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

import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SpeedAppDefinition } from '@speed/product-shell/bootstrap'
import { themePresets } from '@speed/theme-presets'
import { DemoApp } from './app.js'
import { COLOR_MODE_STORAGE_KEY } from './chrome.js'
import { bootstrapDemoApp, demoThemeOptions } from './main.js'

/** The definition the assembly receives, captured from the mocked bootstrap. */
const captured = vi.hoisted(() => ({ definition: undefined as unknown }))

// The boot wiring is what this suite is about: the deployment config the
// real bootstrap reads is replaced with a fixed one, and the assembly call
// is recorded instead of mounting, so the view and theme options main.tsx
// hands it can be read directly. `applyDocumentMetadata` still runs against
// the stub i18n the mock returns.
vi.mock('./runtime-config.js', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./runtime-config.js')>()
  return {
    ...actual,
    loadRuntimeConfig: async () => ({
      shellLayout: 'sidebar' as const,
      themePreset: 'glass',
    }),
  }
})

vi.mock('@speed/product-shell/bootstrap', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@speed/product-shell/bootstrap')>()
  return {
    ...actual,
    bootstrapSpeedApp: (
      _container: Element,
      definition: SpeedAppDefinition,
    ) => {
      captured.definition = definition
      return {
        root: { unmount: () => undefined },
        i18n: { t: () => 'Demo', on: () => undefined },
        queryClient: {},
        session: {},
      } as unknown as ReturnType<typeof actual.bootstrapSpeedApp>
    },
  }
})

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

  it('hands the assembly the shipped preset shelf and the resolved boot preset', () => {
    installStorageDouble()
    const options = demoThemeOptions('classic')
    // The shelf is the shipped one, and the deployment's preset is only a
    // starting point: with no stored choice the boot preset is the one the
    // deployment named.
    expect(options.presets).toBe(themePresets)
    expect(options.defaultPreset).toBe('classic')
  })

  it('wires the deployment shell layout and preset into the app it assembles', async () => {
    installStorageDouble()
    await bootstrapDemoApp(document.createElement('div'))

    const definition = captured.definition as SpeedAppDefinition
    // The preset shelf and the boot preset reach the provider stack; dropping
    // either leaves a deployment's {"themePreset":"glass"} silently ignored.
    expect(definition.theme?.presets).toBe(themePresets)
    expect(definition.theme?.defaultPreset).toBe('glass')

    // The view is the routed app with the deployment's shell layout: the
    // shell form is a prop of DemoApp, and dropping it renders the top bar
    // for a {"shellLayout":"sidebar"} deployment.
    const view = definition.view as ReactElement<{ shellLayout?: string }>
    expect(view.type).toBe(DemoApp)
    expect(view.props.shellLayout).toBe('sidebar')
  })
})
