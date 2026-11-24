import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SpeedAppDefinition } from '@speed/product-shell/bootstrap'
import { themePresets } from '@speed/theme-presets'
import { DemoApp } from './app.js'
import { COLOR_MODE_STORAGE_KEY } from './chrome.js'
import { bootstrapDemoApp, demoThemeOptions } from './main.js'

const captured = vi.hoisted(() => ({ definition: undefined as unknown }))

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

  it('starts at system when nothing is stored', () => {
    installStorageDouble()
    expect(demoThemeOptions().defaultMode).toBe('system')
  })

  it('hands a stored preference through unchanged', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'light')
    expect(demoThemeOptions().defaultMode).toBe('light')

    entries.set(COLOR_MODE_STORAGE_KEY, 'dark')
    expect(demoThemeOptions().defaultMode).toBe('dark')

    entries.set(COLOR_MODE_STORAGE_KEY, 'system')
    expect(demoThemeOptions().defaultMode).toBe('system')
  })

  it('keeps the platform palette as the brand placeholder', () => {
    installStorageDouble()
    expect(demoThemeOptions().projectTokens).toEqual({})
  })

  it('hands the assembly the shipped preset shelf and the resolved boot preset', () => {
    installStorageDouble()
    const options = demoThemeOptions('classic')
    expect(options.presets).toBe(themePresets)
    expect(options.defaultPreset).toBe('classic')
  })

  it('wires the deployment shell layout and preset into the app it assembles', async () => {
    installStorageDouble()
    await bootstrapDemoApp(document.createElement('div'))

    const definition = captured.definition as SpeedAppDefinition
    expect(definition.theme?.presets).toBe(themePresets)
    expect(definition.theme?.defaultPreset).toBe('glass')

    const view = definition.view as ReactElement<{ shellLayout?: string }>
    expect(view.type).toBe(DemoApp)
    expect(view.props.shellLayout).toBe('sidebar')
  })
})
