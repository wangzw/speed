/**
 * AppThemeProvider contract: provides the merged-token theme through MUI
 * context, merges the MUI locale of the active i18n language onto it,
 * follows 'languageChanged' (MUI built-in texts switch with the app
 * language), rebuilds when an override layer changes identity, and
 * renders the theme-aware baseline alongside the children.
 */

import { act, render } from '@testing-library/react'
import { useEffect } from 'react'
import { createTheme, ThemeProvider, useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { enUS, zhCN } from '@mui/material/locale'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n, I18nextProvider, switchLanguage } from '@speed/i18n'
import { defaultTokens } from '@speed/tokens'
import type { TokensOverride } from '@speed/tokens'
import type { ThemePreset } from '@speed/theme-presets'
import {
  AppThemeProvider,
  useAppThemeMode,
  useAppThemePreset,
  type AppThemeModeControl,
  type AppThemePresetControl,
} from './AppThemeProvider.js'
import type { AppThemeModePreference } from './createAppTheme.js'
import {
  createUiKitI18n,
  renderWithProviders,
  type RenderWithProvidersOptions,
} from '../../test-utils/render.js'

afterEach(() => {
  vi.unstubAllGlobals()
})

/** Read the theme MUI context hands the tree at render time. */
function ThemeProbe({ onTheme }: { onTheme: (theme: Theme) => void }) {
  onTheme(useTheme())
  return null
}

/** Capture the provider's color-mode control at render time. */
function ModeProbe({
  onControl,
}: {
  onControl: (control: AppThemeModeControl) => void
}) {
  onControl(useAppThemeMode())
  return null
}

/** Install a matchMedia stub for the system color-mode query. */
function stubSystemColorMode(matches: boolean): {
  readonly changeMatches: (next: boolean) => void
} {
  let current = matches
  const listeners = new Set<() => void>()
  const list = {
    get matches(): boolean {
      return current
    },
    addEventListener: (_type: string, listener: () => void): void => {
      listeners.add(listener)
    },
    removeEventListener: (_type: string, listener: () => void): void => {
      listeners.delete(listener)
    },
  }
  vi.stubGlobal('matchMedia', vi.fn(() => list))
  return {
    changeMatches(next: boolean): void {
      current = next
      for (const listener of listeners) {
        listener()
      }
    },
  }
}

/** Capture the provider's preset control at render time. */
function PresetProbe({
  onControl,
}: {
  onControl: (control: AppThemePresetControl) => void
}) {
  onControl(useAppThemePreset())
  return null
}

/** Two addressable presets with a field the built theme exposes directly. */
const SHARP_PRESET: ThemePreset = {
  id: 'sharp',
  tokens: { shape: { borderRadius: 2 } },
}
const ROUND_PRESET: ThemePreset = {
  id: 'round',
  tokens: { shape: { borderRadius: 18 } },
}

function paginationLabel(theme: Theme | undefined): string | undefined {
  return theme?.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage as
    | string
    | undefined
}

describe('AppThemeProvider', () => {
  it('provides the merged-token theme and applies the active MUI locale', () => {
    let seen: Theme | undefined
    const { i18n } = renderWithProviders(<ThemeProbe onTheme={(t) => (seen = t)} />)
    expect(i18n.language).toBe('zh-CN')
    expect(seen?.palette.primary.main).toBe(defaultTokens.color.semantic.primary.main)
    expect(paginationLabel(seen)).toBe(
      zhCN.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage,
    )
  })

  it('re-merges the MUI locale when the language switches', async () => {
    let seen: Theme | undefined
    const { i18n } = renderWithProviders(<ThemeProbe onTheme={(t) => (seen = t)} />)
    const zhLabel = zhCN.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).toBe(zhLabel)
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(i18n.language).toBe('en-US')
    const enLabel = enUS.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).toBe(enLabel)
    expect(paginationLabel(seen)).not.toBe(zhLabel)
  })

  it('applies project and tenant override layers, tenant winning per key', () => {
    const project: TokensOverride = {
      color: {
        semantic: {
          primary: { main: '#111111' },
          secondary: { main: '#222222' },
        },
      },
    }
    const tenant: TokensOverride = {
      color: { semantic: { primary: { main: '#333333' } } },
    }
    let seen: Theme | undefined
    renderWithProviders(<ThemeProbe onTheme={(t) => (seen = t)} />, {
      projectTokens: project,
      tenantOverrides: tenant,
    })
    expect(seen?.palette.primary.main).toBe('#333333')
    expect(seen?.palette.secondary.main).toBe('#222222')
    // Untouched branches keep the defaults.
    expect(seen?.palette.error.main).toBe(defaultTokens.color.semantic.error.main)
  })

  it('keeps the kit palette, typography and shape under a components-only override layer', () => {
    let seen: Theme | undefined
    // The README's component-level customisation recipe: the second
    // createTheme argument is a diff options object, never a theme built
    // by createTheme() -- a full theme would re-apply MUI's defaults over
    // the token-mapped palette, typography and shape.
    const componentOverrides = {
      components: {
        MuiButton: {
          defaultProps: { disableElevation: true, size: 'small' as const },
        },
      },
    }
    renderWithProviders(
      <ThemeProvider theme={(outer) => createTheme(outer, componentOverrides)}>
        <ThemeProbe onTheme={(theme) => (seen = theme)} />
      </ThemeProvider>,
    )
    expect(seen?.palette.primary.main).toBe(
      defaultTokens.color.semantic.primary.main,
    )
    expect(seen?.typography.fontFamily).toBe(
      defaultTokens.typography.fontFamily.sans,
    )
    expect(seen?.shape.borderRadius).toBe(defaultTokens.shape.borderRadius)
    expect(seen?.components?.MuiButton?.defaultProps).toMatchObject({
      disableElevation: true,
      size: 'small',
    })
  })

  it('rebuilds the theme when an override layer changes identity', () => {
    const i18n = createUiKitI18n('zh-CN')
    const seen: Theme[] = []
    const first: TokensOverride = { color: { semantic: { primary: { main: '#AAAAAA' } } } }
    const tree = (projectTokens: TokensOverride | undefined) => (
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n} projectTokens={projectTokens}>
          <ThemeProbe onTheme={(t) => seen.push(t)} />
        </AppThemeProvider>
      </I18nextProvider>
    )
    const { rerender } = render(tree(first))
    expect(seen[0]?.palette.primary.main).toBe('#AAAAAA')
    const second: TokensOverride = { color: { semantic: { primary: { main: '#BBBBBB' } } } }
    rerender(tree(second))
    expect(seen[1]?.palette.primary.main).toBe('#BBBBBB')
  })

  it('stops following language changes after unmount', async () => {
    const i18n = createUiKitI18n('zh-CN')
    const { unmount } = renderWithProviders(<ThemeProbe onTheme={() => undefined} />, {
      i18n,
    })
    unmount()
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(i18n.language).toBe('en-US')
  })

  it('picks up a language a child switches to inside its own mount effect (P1-4 regression)', async () => {
    // AppThemeProvider snapshots i18n.language into state at first render
    // and registers its languageChanged listener in an effect. React runs
    // child effects before parent effects, so a child that switches
    // language inside its own mount effect emits the event before the
    // provider's listener exists -- the MUI built-in texts would stay on
    // the snapshot language. (Re)attaching the listener re-adopts the
    // instance's live language in the same effect, so a switch that fired
    // before the listener existed is recovered at attach time instead of
    // being lost.
    const i18n = createUiKitI18n('zh-CN')
    let seen: Theme | undefined
    function SwitchingChild() {
      useEffect(() => {
        void switchLanguage(i18n, 'en-US')
      }, [])
      return null
    }
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ThemeProbe onTheme={(t) => (seen = t)} />
          <SwitchingChild />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    await act(async () => {})
    expect(i18n.language).toBe('en-US')
    const enLabel = enUS.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).toBe(enLabel)
    const zhLabel = zhCN.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).not.toBe(zhLabel)
  })

  it('adopts the language of a swapped-in i18n instance without waiting for an event (P1-4 regression)', () => {
    // Re-subscribing the effect to a new i18n instance (the prop changed
    // identity) must also re-adopt the instance's live language: a new
    // instance never emits languageChanged for the language it was
    // created on, so the re-attach is the only point that can pick it
    // up.
    const first = createUiKitI18n('zh-CN')
    const second = createUiKitI18n('en-US')
    const seen: Theme[] = []
    const tree = (i18n: ReturnType<typeof createUiKitI18n>) => (
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ThemeProbe onTheme={(t) => seen.push(t)} />
        </AppThemeProvider>
      </I18nextProvider>
    )
    const { rerender } = render(tree(first))
    const zhLabel = zhCN.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen[0])).toBe(zhLabel)
    rerender(tree(second))
    expect(second.language).toBe('en-US')
    const enLabel = enUS.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen.at(-1))).toBe(enLabel)
    expect(paginationLabel(seen.at(-1))).not.toBe(zhLabel)
  })

  it('renders children inside the theme context', () => {
    const { getByText } = renderWithProviders(<div>child content</div>)
    expect(getByText('child content')).toBeInTheDocument()
  })

  it('starts in light mode and re-assembles the theme dark when the preference changes', () => {
    let seen: Theme | undefined
    let control: AppThemeModeControl | undefined
    renderWithProviders(
      <>
        <ThemeProbe onTheme={(t) => (seen = t)} />
        <ModeProbe onControl={(c) => (control = c)} />
      </>,
    )
    expect(control?.mode).toBe('light')
    expect(control?.preference).toBe('light')
    expect(seen?.palette.mode).toBe('light')
    const neutral = defaultTokens.color.neutral

    act(() => {
      control?.setPreference('dark')
    })
    expect(control?.mode).toBe('dark')
    expect(control?.preference).toBe('dark')
    expect(seen?.palette.mode).toBe('dark')
    expect(seen?.palette.background.default).toBe(neutral[900])

    act(() => {
      control?.setPreference('light')
    })
    expect(seen?.palette.mode).toBe('light')
  })

  it('starts dark when defaultMode is dark', () => {
    let seen: Theme | undefined
    const i18n = createUiKitI18n('zh-CN')
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n} defaultMode="dark">
          <ThemeProbe onTheme={(t) => (seen = t)} />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    expect(seen?.palette.mode).toBe('dark')
  })

  it('follows the system preference while defaultMode is system', () => {
    const media = stubSystemColorMode(false)
    let seen: Theme | undefined
    let control: AppThemeModeControl | undefined
    renderWithProviders(
      <>
        <ThemeProbe onTheme={(t) => (seen = t)} />
        <ModeProbe onControl={(c) => (control = c)} />
      </>,
      { defaultMode: 'system' },
    )
    expect(control?.preference).toBe('system')
    expect(control?.mode).toBe('light')
    expect(seen?.palette.mode).toBe('light')

    act(() => {
      media.changeMatches(true)
    })
    expect(control?.mode).toBe('dark')
    expect(seen?.palette.mode).toBe('dark')
    expect(seen?.palette.background.default).toBe(
      defaultTokens.color.neutral[900],
    )
  })

  it('resolves system to light without matchMedia and without throwing', () => {
    vi.stubGlobal('matchMedia', undefined)
    let seen: Theme | undefined
    let control: AppThemeModeControl | undefined
    expect(() =>
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(t) => (seen = t)} />
          <ModeProbe onControl={(c) => (control = c)} />
        </>,
        { defaultMode: 'system' },
      ),
    ).not.toThrow()
    expect(control?.preference).toBe('system')
    expect(control?.mode).toBe('light')
    expect(seen?.palette.mode).toBe('light')
  })

  it('keeps an explicit mode when the system preference changes', () => {
    const media = stubSystemColorMode(true)
    let seen: Theme | undefined
    let control: AppThemeModeControl | undefined
    renderWithProviders(
      <>
        <ThemeProbe onTheme={(t) => (seen = t)} />
        <ModeProbe onControl={(c) => (control = c)} />
      </>,
      { defaultMode: 'dark' },
    )
    expect(control?.preference).toBe('dark')
    expect(control?.mode).toBe('dark')
    expect(seen?.palette.mode).toBe('dark')

    act(() => {
      media.changeMatches(false)
    })
    expect(control?.mode).toBe('dark')
    expect(seen?.palette.mode).toBe('dark')
  })

  it('starts at the defaultMode preference and follows the system after setPreference', () => {
    const media = stubSystemColorMode(true)
    let seen: Theme | undefined
    let control: AppThemeModeControl | undefined
    renderWithProviders(
      <>
        <ThemeProbe onTheme={(t) => (seen = t)} />
        <ModeProbe onControl={(c) => (control = c)} />
      </>,
      { defaultMode: 'light' },
    )
    expect(control?.preference).toBe('light')
    expect(control?.mode).toBe('light')
    expect(seen?.palette.mode).toBe('light')

    act(() => {
      control?.setPreference('system')
    })
    expect(control?.preference).toBe('system')
    expect(control?.mode).toBe('dark')
    expect(seen?.palette.mode).toBe('dark')

    act(() => {
      media.changeMatches(false)
    })
    expect(control?.mode).toBe('light')
    expect(seen?.palette.mode).toBe('light')
  })

  it('ignores a setPreference value outside the three-value set', () => {
    let control: AppThemeModeControl | undefined
    renderWithProviders(
      <>
        <ThemeProbe onTheme={() => undefined} />
        <ModeProbe onControl={(c) => (control = c)} />
      </>,
    )
    act(() => {
      control?.setPreference('sepia' as AppThemeModePreference)
    })
    expect(control?.preference).toBe('light')
    expect(control?.mode).toBe('light')
  })

  it('falls back to light for an unrecognised defaultMode', () => {
    let control: AppThemeModeControl | undefined
    renderWithProviders(<ModeProbe onControl={(c) => (control = c)} />, {
      defaultMode: 'sepia' as AppThemeModePreference,
    })
    expect(control?.preference).toBe('light')
    expect(control?.mode).toBe('light')
  })

  it('throws when the mode hook is read without a provider above', () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      expect(() => render(<ModeProbe onControl={() => undefined} />)).toThrow(
        /AppThemeProvider/,
      )
    } finally {
      consoleError.mockRestore()
    }
  })

  describe('theme presets', () => {
    it('layers the active preset under the project and tenant layers', () => {
      const preset: ThemePreset = {
        id: 'branded',
        tokens: { color: { semantic: { primary: { main: '#111111' } } } },
      }
      const project: TokensOverride = {
        color: { semantic: { primary: { main: '#222222' } } },
      }
      const tenant: TokensOverride = {
        color: { semantic: { primary: { main: '#333333' } } },
      }
      const primaryWith = (
        options: RenderWithProvidersOptions,
      ): string | undefined => {
        let seen: Theme | undefined
        renderWithProviders(<ThemeProbe onTheme={(theme) => (seen = theme)} />, options)
        return seen?.palette.primary.main
      }
      expect(
        primaryWith({ presets: [preset], defaultPreset: 'branded' }),
      ).toBe('#111111')
      expect(
        primaryWith({
          presets: [preset],
          defaultPreset: 'branded',
          projectTokens: project,
        }),
      ).toBe('#222222')
      expect(
        primaryWith({
          presets: [preset],
          defaultPreset: 'branded',
          projectTokens: project,
          tenantOverrides: tenant,
        }),
      ).toBe('#333333')
    })

    it('re-assembles the theme when the active preset changes', () => {
      let seen: Theme | undefined
      let control: AppThemePresetControl | undefined
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(theme) => (seen = theme)} />
          <PresetProbe onControl={(next) => (control = next)} />
        </>,
        { presets: [SHARP_PRESET, ROUND_PRESET], defaultPreset: 'sharp' },
      )
      expect(control?.preset).toBe('sharp')
      expect(seen?.shape.borderRadius).toBe(2)

      act(() => {
        control?.setPreset('round')
      })
      expect(control?.preset).toBe('round')
      expect(seen?.shape.borderRadius).toBe(18)
    })

    it('keeps the active preset and the color mode independent', () => {
      // Two dimensions, one theme: a mode change must not reset the
      // selected preset, and a preset change must not reset the mode. Each
      // dimension is flipped twice, with the other observed after every
      // step, so a provider that bound them (resetting the selection on a
      // mode change, or rebuilding the theme without the mode) fails on
      // the first assertion after the crossing.
      let seen: Theme | undefined
      let presetControl: AppThemePresetControl | undefined
      let modeControl: AppThemeModeControl | undefined
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(theme) => (seen = theme)} />
          <PresetProbe onControl={(next) => (presetControl = next)} />
          <ModeProbe onControl={(next) => (modeControl = next)} />
        </>,
        { presets: [SHARP_PRESET, ROUND_PRESET], defaultPreset: 'sharp' },
      )
      expect(presetControl?.preset).toBe('sharp')
      expect(modeControl?.mode).toBe('light')
      expect(seen?.palette.mode).toBe('light')
      expect(seen?.shape.borderRadius).toBe(2)

      act(() => {
        modeControl?.setPreference('dark')
      })
      expect(modeControl?.mode).toBe('dark')
      expect(modeControl?.preference).toBe('dark')
      expect(seen?.palette.mode).toBe('dark')
      expect(presetControl?.preset).toBe('sharp')
      expect(seen?.shape.borderRadius).toBe(2)

      act(() => {
        presetControl?.setPreset('round')
      })
      expect(presetControl?.preset).toBe('round')
      expect(seen?.shape.borderRadius).toBe(18)
      expect(seen?.palette.mode).toBe('dark')

      act(() => {
        modeControl?.setPreference('light')
      })
      expect(modeControl?.mode).toBe('light')
      expect(modeControl?.preference).toBe('light')
      expect(seen?.palette.mode).toBe('light')
      expect(presetControl?.preset).toBe('round')
      expect(seen?.shape.borderRadius).toBe(18)
    })

    it('ignores a setPreset id outside the shelf', () => {
      let seen: Theme | undefined
      let control: AppThemePresetControl | undefined
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(theme) => (seen = theme)} />
          <PresetProbe onControl={(next) => (control = next)} />
        </>,
        { presets: [SHARP_PRESET], defaultPreset: 'sharp' },
      )
      act(() => {
        control?.setPreset('missing')
      })
      expect(control?.preset).toBe('sharp')
      expect(seen?.shape.borderRadius).toBe(2)
    })

    it('layers nothing and reports the default when the shelf is empty', () => {
      let seen: Theme | undefined
      let control: AppThemePresetControl | undefined
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(theme) => (seen = theme)} />
          <PresetProbe onControl={(next) => (control = next)} />
        </>,
        { presets: [] },
      )
      expect(control?.preset).toBe('default')
      expect(seen?.shape.borderRadius).toBe(defaultTokens.shape.borderRadius)
    })

    it('falls back to the first entry when defaultPreset names no preset', () => {
      let seen: Theme | undefined
      let control: AppThemePresetControl | undefined
      renderWithProviders(
        <>
          <ThemeProbe onTheme={(theme) => (seen = theme)} />
          <PresetProbe onControl={(next) => (control = next)} />
        </>,
        { presets: [SHARP_PRESET, ROUND_PRESET], defaultPreset: 'missing' },
      )
      expect(control?.preset).toBe('sharp')
      expect(seen?.shape.borderRadius).toBe(2)
    })

    it('throws when the preset hook is read without a provider above', () => {
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
      try {
        expect(() => render(<PresetProbe onControl={() => undefined} />)).toThrow(
          /AppThemeProvider/,
        )
      } finally {
        consoleError.mockRestore()
      }
    })
  })

  it('renders with a language outside the MUI locale table, using the en-US MUI locale fallback', () => {
    // createI18n's supported set is host-chosen and may include languages
    // the MUI locale table does not map; rendering must never throw for
    // one of them.
    const i18n = createI18n({
      supportedLanguages: ['zh-CN', 'en-US', 'fr-FR'],
      defaultLanguage: 'fr-FR',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
    })
    let seen: Theme | undefined
    expect(() =>
      render(
        <I18nextProvider i18n={i18n}>
          <AppThemeProvider i18n={i18n}>
            <ThemeProbe onTheme={(t) => (seen = t)} />
          </AppThemeProvider>
        </I18nextProvider>,
      ),
    ).not.toThrow()
    expect(i18n.language).toBe('fr-FR')
    expect(paginationLabel(seen)).toBe(
      enUS.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage,
    )
  })

  it('re-merges with the fallback MUI locale when switching into a language outside the table', async () => {
    const i18n = createI18n({
      supportedLanguages: ['zh-CN', 'en-US', 'fr-FR'],
      defaultLanguage: 'zh-CN',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
    })
    let seen: Theme | undefined
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <ThemeProbe onTheme={(t) => (seen = t)} />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    const zhLabel = zhCN.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).toBe(zhLabel)
    await act(async () => {
      await switchLanguage(i18n, 'fr-FR')
    })
    expect(i18n.language).toBe('fr-FR')
    const enLabel = enUS.components?.MuiTablePagination?.defaultProps?.labelRowsPerPage
    expect(paginationLabel(seen)).toBe(enLabel)
    expect(paginationLabel(seen)).not.toBe(zhLabel)
  })
})
