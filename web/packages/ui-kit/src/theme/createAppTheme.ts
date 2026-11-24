/** The theme factory: speed tokens, merged in layers, into an MUI v9 theme. */

import { createTheme } from '@mui/material/styles'
import type {
  PaletteColorOptions,
  PaletteOptions,
  Theme,
  ThemeOptions,
} from '@mui/material/styles'
import {
  deepMerge,
  defaultTokens,
  type ColorTokens,
  type NeutralScale,
  type SemanticRole,
  type ShadowTokens,
  type SpeedTokens,
  type TokensOverride,
} from '@speed/tokens'
import { baselineComponents } from './baseline.js'
import { controlBorderComponents } from './control-borders.js'
import { dividerComponents } from './divider.js'
import { focusRingComponents } from './focus-ring.js'
import { reducedMotionComponents } from './reduced-motion.js'
import { touchFloorComponents } from './touch-floor.js'
import { unavailableComponents } from './unavailable.js'
import { letterCaseComponents } from './letter-case.js'
import {
  mergeThemeComponents,
  resolveSurfaceTheme,
  surfaceComponents,
} from './surface.js'
import type { SurfacePaletteSlots, SurfaceTheme } from './surface.js'

/**
 * MUI's typography surface has no monospace entry. Required on the built
 * theme (the factory always fills it), optional on the options so a host
 * composing its own `createTheme` keeps MUI's shape.
 */
declare module '@mui/material/styles' {
  interface TypographyVariants {
    /** The token tree's monospace font stack (`typography.fontFamily.mono`). */
    fontFamilyMono: string
  }
  interface TypographyVariantsOptions {
    /** The monospace stack; the factory fills it from the merged tokens. */
    fontFamilyMono?: string
  }
}

/**
 * The surface roles ride on the theme too. The augmentation must sit here,
 * not in `surface.ts`: a consumer loading only the entry's `.d.ts` graph
 * never reaches an internal module, so `theme.surface` would be unknown.
 */
declare module '@mui/material/styles' {
  interface Theme {
    /** The resolved surface roles (see `resolveSurfaceTheme`). */
    surface: SurfaceTheme
  }
  interface ThemeOptions {
    /** The resolved surface roles; the factory fills them from the tokens. */
    surface?: SurfaceTheme
  }
}

/** Typography rem base, aligned with MUI's default htmlFontSize. */
const REM_BASE = 16

/** The token elevation slots in ascending order (their numeric key order). */
const ELEVATION_SLOTS = [1, 2, 4, 8, 16, 24] as const

/**
 * The two color modes a speed theme ships. Dark mode reads the neutral ramp
 * from its dark end, so it needs no second palette.
 */
export type AppThemeMode = 'light' | 'dark'

/**
 * The color-mode preference a host may hold. Only the provider resolves
 * 'system'; `createAppTheme` accepts the resolved `AppThemeMode` alone.
 */
export type AppThemeModePreference = AppThemeMode | 'system'

/** What the factory hands back: the merged tokens and the theme built from them. */
export interface AppTheme {
  /**
   * The merged token tree. Untouched branches are shared by identity with
   * the frozen `defaultTokens` (a write throws in strict mode); rebuilt
   * branches belong to this result alone.
   */
  readonly tokens: SpeedTokens
  /**
   * The locale-free MUI theme. Compose it with
   * `createTheme(appTheme.theme, muiLocaleFor(language))` -- or let
   * `AppThemeProvider` do it -- for MUI built-in texts to follow the language.
   */
  readonly theme: Theme
}

/**
 * Builds the theme for merged speed tokens. `projectTokens` and
 * `tenantOverrides` are diff-only override layers applied in that order; an
 * undefined layer is skipped. `mode` defaults to 'light'.
 */
export function createAppTheme(
  projectTokens?: TokensOverride,
  tenantOverrides?: TokensOverride,
  mode: AppThemeMode = 'light',
): AppTheme {
  // Drop empty layers so an omitted layer never reaches the type signature.
  const layers = [projectTokens, tenantOverrides].filter(
    (layer): layer is TokensOverride => layer !== undefined,
  )
  const tokens = deepMerge(defaultTokens, ...layers)
  return { tokens, theme: createTheme(tokensToThemeOptions(tokens, mode)) }
}

function rem(px: number): string {
  return `${px / REM_BASE}rem`
}

/**
 * How far a dark-mode role's hover fill moves toward white. MUI reads
 * `palette[role].dark` for that fill, which carries the neutral-900 label:
 * mixing toward white keeps the label at 4.5:1 or better.
 */
const DARK_HOVER_WHITE_MIX = 0.2

/**
 * Mixes a `#RRGGBB` color toward white by `weight` (0..1); any other CSS
 * color is returned unchanged.
 */
function mixWithWhite(hex: string, weight: number): string {
  if (!/^#[0-9a-fA-F]{6}$/.test(hex)) {
    return hex
  }
  const value = Number.parseInt(hex.slice(1), 16)
  const channel = (offset: number): number => {
    const base = (value >> offset) & 0xff
    return Math.round(base + (255 - base) * weight)
  }
  const mixed = (channel(16) << 16) | (channel(8) << 8) | channel(0)
  return `#${mixed.toString(16).padStart(6, '0').toUpperCase()}`
}

/**
 * A semantic role's palette entries for a mode. Light maps the tokens key
 * for key. Dark takes the role's light tone as `main` and `light`, neutral
 * 900 as `contrastText`, and mixes `dark` toward white for a readable hover.
 */
function roleOptions(
  color: ColorTokens['semantic'][SemanticRole],
  mode: AppThemeMode,
  neutral: NeutralScale,
): PaletteColorOptions {
  if (mode === 'dark') {
    return {
      main: color.light,
      light: color.light,
      dark: mixWithWhite(color.light, DARK_HOVER_WHITE_MIX),
      contrastText: neutral[900],
    }
  }
  return {
    main: color.main,
    light: color.light,
    dark: color.dark,
    contrastText: color.contrastText,
  }
}

function neutralAsGrey(neutral: NeutralScale): PaletteOptions['grey'] {
  // A-tones repeat the same-number step, mirroring MUI's own grey scale.
  return {
    50: neutral[50],
    100: neutral[100],
    200: neutral[200],
    300: neutral[300],
    400: neutral[400],
    500: neutral[500],
    600: neutral[600],
    700: neutral[700],
    800: neutral[800],
    900: neutral[900],
    A100: neutral[100],
    A200: neutral[200],
    A400: neutral[400],
    A700: neutral[700],
  }
}

/**
 * Builds MUI's 25-entry shadow ramp from the six token slots: each elevation
 * uses the nearest slot at or below it, so it never exceeds the design
 * scale. The loop produces exactly 25 entries ('none' plus 1..24).
 */
function shadowRamp(shadows: ShadowTokens): ThemeOptions['shadows'] {
  const ramp: string[] = ['none']
  let slotIndex = 0
  for (let elevation = 1; elevation <= 24; elevation += 1) {
    const next = ELEVATION_SLOTS[slotIndex + 1]
    if (next !== undefined && next <= elevation) {
      slotIndex += 1
    }
    ramp.push(shadows[ELEVATION_SLOTS[slotIndex] as (typeof ELEVATION_SLOTS)[number]])
  }
  return ramp as unknown as ThemeOptions['shadows']
}

/**
 * The palette surface colors for a mode: the token values directly in light
 * mode, the neutral ramp's dark end in dark mode.
 */
function surfacePalette(
  tokens: SpeedTokens,
  mode: AppThemeMode,
): SurfacePaletteSlots & Pick<PaletteOptions, 'text' | 'background' | 'divider'> {
  if (mode === 'light') {
    return {
      text: {
        primary: tokens.color.text.primary,
        secondary: tokens.color.text.secondary,
        disabled: tokens.color.text.disabled,
      },
      background: {
        default: tokens.color.background.default,
        paper: tokens.color.background.paper,
      },
      divider: tokens.color.divider,
    }
  }
  const neutral = tokens.color.neutral
  return {
    text: {
      primary: neutral[50],
      secondary: neutral[300],
      disabled: neutral[500],
    },
    background: {
      default: neutral[900],
      paper: neutral[800],
    },
    divider: neutral[700],
  }
}

/** Map merged speed tokens onto the MUI theme options surface. */
function tokensToThemeOptions(
  tokens: SpeedTokens,
  mode: AppThemeMode,
): ThemeOptions {
  const t = tokens.typography
  const heading = (px: number) => ({
    fontSize: rem(px),
    lineHeight: t.lineHeight.tight,
    fontWeight: t.fontWeight.semibold,
    letterSpacing: t.letterSpacing.normal,
  })
  const bodyText = (px: number, weight: number, letterSpacing?: string) => ({
    fontSize: rem(px),
    lineHeight: t.lineHeight.normal,
    fontWeight: weight,
    letterSpacing: letterSpacing ?? t.letterSpacing.normal,
  })
  const surfaceSlots = surfacePalette(tokens, mode)
  const palette: PaletteOptions = {
    mode,
    primary: roleOptions(tokens.color.semantic.primary, mode, tokens.color.neutral),
    secondary: roleOptions(tokens.color.semantic.secondary, mode, tokens.color.neutral),
    error: roleOptions(tokens.color.semantic.error, mode, tokens.color.neutral),
    warning: roleOptions(tokens.color.semantic.warning, mode, tokens.color.neutral),
    info: roleOptions(tokens.color.semantic.info, mode, tokens.color.neutral),
    success: roleOptions(tokens.color.semantic.success, mode, tokens.color.neutral),
    grey: neutralAsGrey(tokens.color.neutral),
    ...surfaceSlots,
  }
  return {
    palette,
    surface: resolveSurfaceTheme(tokens, surfaceSlots, mode),
    typography: {
      fontFamily: t.fontFamily.sans,
      fontFamilyMono: t.fontFamily.mono,
      fontWeightRegular: t.fontWeight.regular,
      fontWeightMedium: t.fontWeight.medium,
      fontWeightBold: t.fontWeight.bold,
      h1: heading(t.fontSize['5xl']),
      h2: heading(t.fontSize['4xl']),
      h3: heading(t.fontSize['3xl']),
      h4: heading(t.fontSize['2xl']),
      h5: heading(t.fontSize.xl),
      h6: heading(t.fontSize.lg),
      subtitle1: bodyText(t.fontSize.md, t.fontWeight.medium),
      subtitle2: bodyText(t.fontSize.sm, t.fontWeight.medium),
      body1: bodyText(t.fontSize.md, t.fontWeight.regular),
      body2: bodyText(t.fontSize.sm, t.fontWeight.regular),
      button: bodyText(t.fontSize.sm, t.fontWeight.medium),
      caption: bodyText(t.fontSize.xs, t.fontWeight.regular),
      overline: bodyText(t.fontSize.xs, t.fontWeight.semibold, t.letterSpacing.wide),
    },
    spacing: tokens.spacing.unit,
    shape: { borderRadius: tokens.shape.borderRadius },
    breakpoints: { values: { ...tokens.breakpoints.values } },
    zIndex: { ...tokens.zIndex.values },
    motion: { reducedMotion: 'system' },
    shadows: shadowRamp(tokens.shadows),
    components: mergeThemeComponents(
      touchFloorComponents,
      letterCaseComponents,
      surfaceComponents,
      focusRingComponents,
      controlBorderComponents,
      dividerComponents,
      reducedMotionComponents,
      unavailableComponents,
      baselineComponents(tokens, mode),
    ),
  }
}
