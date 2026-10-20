/**
 * AppThemeProvider: composes the MUI theme runtime for an app.
 *
 * It owns five concerns:
 *
 * 1. Theme assembly -- createAppTheme(projectTokens, tenantOverrides,
 *    mode) merges the token layers; the provider memoizes the result so
 *    the merged tree and the locale-free theme are built once per
 *    (override set, mode) (hosts that rebuild override objects every
 *    render should memoize them, or pass module-level constants).
 * 2. Preset shelf -- when the host passes `presets`, the provider holds
 *    the active preset id in local state and folds that preset's token
 *    diff under the project layer, so the assembly order is defaults ->
 *    preset -> project -> tenant. useAppThemePreset exposes the active id
 *    and a switch; without presets no layer is applied and the control
 *    reports 'default'.
 * 3. MUI locale linkage -- the provider merges the MUI locale matching
 *    the active i18n language onto the base theme (createTheme's second
 *    argument), and subscribes to 'languageChanged' so MUI built-in
 *    texts (table pagination labels, date picker strings, ...) follow
 *    every language switch. The locale merge lands after the token
 *    mapping, so MUI component defaults never overwrite theme tokens.
 * 4. Color mode -- the provider holds the active AppThemeMode in local
 *    state (starting at defaultMode, 'light') and exposes it, with a
 *    setter and a toggle, through useAppThemeMode. A mode change
 *    re-assembles the theme; every child under the provider re-renders
 *    with the new palette. The default is 'light', so a host that never
 *    reads the hook sees exactly the previous behavior.
 * 5. Global reset -- CssBaseline renders the theme-aware baseline
 *    (background color from theme.palette.background.default, no body
 *    margin) once, under the same theme context as the children.
 *
 * The provider deliberately does NOT render I18nextProvider: namespace
 * registration and the provider tree belong to the host bootstrap, and
 * this component neither registers nor owns any namespace. It takes the
 * app's i18n instance as a prop and reads only its language events.
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
import type { AppThemeMode } from './createAppTheme.js'

/**
 * The MUI localization for the active language, or the en-US MUI locale
 * when the language has no entry in the MUI locale table. muiLocaleFor
 * deliberately throws on unknown tags for direct callers who want a loud
 * failure at theme-assembly time; a render path must never throw, because
 * createI18n's supported set is host-chosen and can include languages
 * beyond the two MUI mappings this package ships. MUI's own built-in
 * texts (table pagination labels, tooltips, ...) then fall back to
 * English -- a documented default -- while the app's translations keep
 * rendering in the active language.
 */
function muiLocaleForOrDefault(language: string): Localization {
  try {
    return muiLocaleFor(language)
  } catch {
    return enUS
  }
}

/**
 * The color-mode surface useAppThemeMode exposes: the active mode, a
 * setter and a toggle. Backed by the provider's own state.
 */
export interface AppThemeModeControl {
  /** The active color mode. */
  readonly mode: AppThemeMode
  /** Switch to an explicit mode. */
  readonly setMode: (mode: AppThemeMode) => void
  /** Flip between 'light' and 'dark'. */
  readonly toggleMode: () => void
}

/** The provider's mode control; null until an AppThemeProvider is mounted. */
const AppThemeModeContext = createContext<AppThemeModeControl | null>(null)

/**
 * The active color mode and its controls, read from the nearest
 * AppThemeProvider. Throws when no provider is mounted above -- the same
 * loud failure a missing host tree gets elsewhere, rather than a silent
 * no-op toggle.
 */
export function useAppThemeMode(): AppThemeModeControl {
  const control = useContext(AppThemeModeContext)
  if (control === null) {
    throw new Error(
      'useAppThemeMode must be called within an AppThemeProvider',
    )
  }
  return control
}

/**
 * The preset surface useAppThemePreset exposes: the active preset id and
 * a setter. Backed by the provider's own state and the `presets` shelf it
 * was given.
 */
export interface AppThemePresetControl {
  /**
   * The active preset id. 'default' when the provider was given no
   * presets (nothing is layered in that case).
   */
  readonly preset: string
  /** Switch to the preset with this id. An id outside the shelf is ignored. */
  readonly setPreset: (id: string) => void
}

/** The provider's preset control; null until an AppThemeProvider is mounted. */
const AppThemePresetContext = createContext<AppThemePresetControl | null>(null)

/**
 * The active theme preset and its switch, read from the nearest
 * AppThemeProvider. Throws when no provider is mounted above -- the same
 * loud failure a missing host tree gets elsewhere, rather than a silent
 * no-op switch.
 */
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
  /**
   * The presets the provider may switch between, applied as one layer
   * between the defaults and the project tokens. Omitted or empty layers
   * nothing.
   */
  readonly presets?: readonly ThemePreset[]
  /**
   * The preset id to start at. An id absent from `presets` falls back to
   * the first entry rather than throwing: a deployment config typo must
   * not white-screen the tree.
   */
  readonly defaultPreset?: string
  /** The color mode the tree starts in; defaults to 'light'. */
  readonly defaultMode?: AppThemeMode
  readonly children?: ReactNode
}

/**
 * Wrap an app's tree with the speed theme: MUI ThemeProvider under the
 * theme built from the merged tokens, locale-aware (language switches
 * re-merge the MUI locale), plus the theme-aware CssBaseline.
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
  // The active color mode is the provider's own state, promoted through
  // useAppThemeMode so any descendant can flip it. It starts at
  // defaultMode ('light'), so a host that never reads the hook renders
  // exactly as before.
  const [mode, setModeState] = useState<AppThemeMode>(defaultMode)
  const setMode = useCallback((next: AppThemeMode) => {
    setModeState(next)
  }, [])
  const toggleMode = useCallback(() => {
    setModeState((current) => (current === 'light' ? 'dark' : 'light'))
  }, [])
  const modeControl = useMemo<AppThemeModeControl>(
    () => ({ mode, setMode, toggleMode }),
    [mode, setMode, toggleMode],
  )

  // The preset shelf is the provider's own state too, but it is a selection
  // over the host's `presets` list: null means "no user choice yet", so the
  // resolution below can keep consulting the live `defaultPreset` prop.
  const [selectedPreset, setSelectedPreset] = useState<string | null>(null)
  const presetById = useMemo(
    () => new Map((presets ?? []).map((preset) => [preset.id, preset])),
    [presets],
  )
  // Resolve the active preset every render:
  // - no shelf -> none (the control reports 'default' and nothing is layered)
  // - a user selection still on the shelf -> it wins
  // - otherwise defaultPreset when it names an entry, else the first entry.
  // An unknown defaultPreset falls back rather than throwing: a deployment
  // config typo must not white-screen the tree.
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
      // An unknown id is ignored: the shelf defines the addressable set.
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
    // Assembly order: defaults -> preset -> project -> tenant.
    // createAppTheme owns the defaults and the two named layers, so the
    // preset is folded into the project layer here: a project's brand still
    // beats a preset, and tenant overrides still beat both.
    const projectLayer =
      activePreset === undefined
        ? projectTokens
        : projectTokens === undefined
          ? activePreset.tokens
          : deepMerge(activePreset.tokens, projectTokens)
    return createAppTheme(projectLayer, tenantOverrides, mode)
  }, [activePreset, projectTokens, tenantOverrides, mode])
  // The active MUI locale language, kept in step with the i18n instance.
  // The initial useState(i18n.language) snapshot cannot be the whole story:
  // the subscription below attaches in an effect, and React runs child
  // effects before parent effects -- a child that switches language inside
  // its own mount effect emits 'languageChanged' before this listener
  // exists, and swapping in a fresh i18n instance never emits an event for
  // the language it was created on. Both are recovered by re-adopting the
  // instance's LIVE language every time the listener (re)attaches: an
  // event that fired too early to be heard is reflected in i18n.language
  // by the time this effect runs, and an event heard here updates state as
  // usual.
  const [language, setLanguage] = useState(i18n.language)
  useEffect(() => {
    setLanguage(i18n.language)
    const onLanguageChanged = (next: string) => setLanguage(next)
    i18n.on('languageChanged', onLanguageChanged)
    return () => {
      i18n.off('languageChanged', onLanguageChanged)
    }
  }, [i18n])
  // MUI's built-in texts follow the active language. A language outside
  // the MUI locale table must never throw mid-render: createI18n supports
  // host-chosen canonical sets that can exceed the MUI mappings, so such a
  // language falls back to the en-US MUI locale (see muiLocaleForOrDefault)
  // instead of white-screening the tree.
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
