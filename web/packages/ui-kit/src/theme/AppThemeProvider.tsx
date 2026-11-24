/**
 * Composes the MUI theme runtime: theme assembly, preset shelf, MUI locale
 * linkage, color mode and CssBaseline. It deliberately does not render
 * `I18nextProvider` or register a namespace; both belong to host bootstrap.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import type { ReactNode } from 'react'
import { createTheme, ThemeProvider } from '@mui/material/styles'
import CssBaseline from '@mui/material/CssBaseline'
import { enUS } from '@mui/material/locale'
import type { Localization } from '@mui/material/locale'
import { muiLocaleFor } from '@speed/i18n/mui-locale'
import type { I18nInstance } from '@speed/i18n'
import { deepMerge } from '@speed/tokens'
import type { TokensOverride } from '@speed/tokens'
import type { ThemePreset } from '@speed/theme-presets'
import { createAppTheme } from './createAppTheme.js'
import type { AppThemeMode, AppThemeModePreference } from './createAppTheme.js'
import { useSystemColorMode } from './system-color-mode.js'

/**
 * The MUI localization for the active language, or the en-US MUI locale when
 * the language has no entry: `muiLocaleFor` throws for direct callers, and a
 * render path must never throw.
 */
function muiLocaleForOrDefault(language: string): Localization {
  try {
    return muiLocaleFor(language)
  } catch {
    return enUS
  }
}

/** Decoded config or storage can hand a foreign value, so both entry points check at runtime. */
function isAppThemeModePreference(
  value: unknown,
): value is AppThemeModePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

/** The color-mode surface `useAppThemeMode` exposes. */
export interface AppThemeModeControl {
  /** The resolved color mode, ready to pass to createAppTheme. */
  readonly mode: AppThemeMode
  /** The active preference; 'system' follows the device. */
  readonly preference: AppThemeModePreference
  /** Switch the preference. A value outside the three-value set is ignored. */
  readonly setPreference: (preference: AppThemeModePreference) => void
}

const AppThemeModeContext = createContext<AppThemeModeControl | null>(null)

/** The active color mode and its controls. Throws when no `AppThemeProvider` is mounted above. */
export function useAppThemeMode(): AppThemeModeControl {
  const control = useContext(AppThemeModeContext)
  if (control === null) {
    throw new Error(
      'useAppThemeMode must be called within an AppThemeProvider',
    )
  }
  return control
}

/** The preset surface `useAppThemePreset` exposes. */
export interface AppThemePresetControl {
  /** The active preset id; 'default' when the provider was given no presets. */
  readonly preset: string
  /** Switch to the preset with this id. An id outside the shelf is ignored. */
  readonly setPreset: (id: string) => void
}

const AppThemePresetContext = createContext<AppThemePresetControl | null>(null)

/** The active theme preset and its switch. Throws when no `AppThemeProvider` is mounted above. */
export function useAppThemePreset(): AppThemePresetControl {
  const control = useContext(AppThemePresetContext)
  if (control === null) {
    throw new Error(
      'useAppThemePreset must be called within an AppThemeProvider',
    )
  }
  return control
}

export interface AppThemeProviderProps {
  /** The app's i18n instance (createI18n result). MUI built-in texts follow its language. */
  readonly i18n: I18nInstance
  /** Project-layer token overrides, applied over the preset shelf and the speed defaults. */
  readonly projectTokens?: TokensOverride
  /** Tenant-layer token overrides, applied over the project layer. */
  readonly tenantOverrides?: TokensOverride
  /** The presets the provider may switch between; omitted or empty layers nothing. */
  readonly presets?: readonly ThemePreset[]
  /** The preset id to start at; an id absent from `presets` falls back to the first entry. */
  readonly defaultPreset?: string
  /**
   * The color-mode preference the tree starts in; defaults to 'light', and
   * a value outside the three-value set falls back to it.
   */
  readonly defaultMode?: AppThemeModePreference
  /** The app subtree that renders under the merged theme. */
  readonly children?: ReactNode
}

/**
 * Wraps an app's tree with the speed theme: MUI `ThemeProvider` under the
 * merged-token theme, locale-aware, plus the theme-aware `CssBaseline`.
 */
export function AppThemeProvider({
  i18n,
  projectTokens,
  tenantOverrides,
  presets,
  defaultPreset,
  defaultMode = 'light',
  children,
}: AppThemeProviderProps) {
  // The default is 'light', so a host that never reads the hook renders as before.
  const [preference, setPreferenceState] = useState<AppThemeModePreference>(
    isAppThemeModePreference(defaultMode) ? defaultMode : 'light',
  )
  const setPreference = useCallback((next: AppThemeModePreference) => {
    if (isAppThemeModePreference(next)) {
      setPreferenceState(next)
    }
  }, [])
  // 'system' follows the device while selected; an explicit preference subscribes to nothing.
  const systemMode = useSystemColorMode(preference === 'system')
  const mode: AppThemeMode =
    preference === 'system' ? systemMode : preference
  const modeControl = useMemo<AppThemeModeControl>(
    () => ({ mode, preference, setPreference }),
    [mode, preference, setPreference],
  )

  // null means no user choice yet, so resolution keeps consulting the live `defaultPreset` prop.
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null)
  const presetById = useMemo(
    () => new Map((presets ?? []).map((preset) => [preset.id, preset])),
    [presets],
  )
  // A deployment config typo must not white-screen the tree, so an unknown defaultPreset falls back.
  const activePreset = useMemo(() => {
    if (presets === undefined || presets.length === 0) {
      return undefined
    }
    if (selectedPreset !== null) {
      const selected = presetById.get(selectedPreset)
      if (selected !== undefined) {
        return selected
      }
    }
    if (defaultPreset !== undefined) {
      const fallback = presetById.get(defaultPreset)
      if (fallback !== undefined) {
        return fallback
      }
    }
    return presets[0]
  }, [presets, presetById, selectedPreset, defaultPreset])
  const preset = activePreset?.id ?? 'default'
  const setPreset = useCallback(
    (id: string) => {
      if (presetById.has(id)) {
        setSelectedPreset(id)
      }
    },
    [presetById],
  )
  const presetControl = useMemo<AppThemePresetControl>(
    () => ({ preset, setPreset }),
    [preset, setPreset],
  )

  const base = useMemo(() => {
    // Assembly order: defaults -> preset -> project -> tenant, so the preset folds under the project layer.
    const projectLayer =
      activePreset === undefined
        ? projectTokens
        : projectTokens === undefined
          ? activePreset.tokens
          : deepMerge(activePreset.tokens, projectTokens)
    return createAppTheme(projectLayer, tenantOverrides, mode)
  }, [activePreset, projectTokens, tenantOverrides, mode])
  // Re-adopt the instance's live language whenever the listener attaches: a
  // child's mount-effect switch fires before this effect, and a fresh instance
  // never emits for the language it was created on.
  const [language, setLanguage] = useState(i18n.language)
  useEffect(() => {
    setLanguage(i18n.language)
    const onLanguageChanged = (next: string) => setLanguage(next)
    i18n.on('languageChanged', onLanguageChanged)
    return () => {
      i18n.off('languageChanged', onLanguageChanged)
    }
  }, [i18n])
  const theme = useMemo(
    () => createTheme(base.theme, muiLocaleForOrDefault(language)),
    [base, language],
  )

  return (
    <AppThemePresetContext.Provider value={presetControl}>
      <AppThemeModeContext.Provider value={modeControl}>
        <ThemeProvider theme={theme}>
          <CssBaseline />
          {children}
        </ThemeProvider>
      </AppThemeModeContext.Provider>
    </AppThemePresetContext.Provider>
  )
}
