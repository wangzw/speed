/**
 * The theme factory: speed tokens into an MUI v9 theme.
 *
 * Layering is the token tree's own discipline applied three deep:
 *
 *   defaultTokens  <-  projectTokens  <-  tenantOverrides
 *
 * Each layer is a diff against the one below it, merged through the
 * tokens package's deepMerge (copy-on-write: untouched branches stay
 * shared with the defaults by identity, no input is ever mutated, hostile
 * "__proto__" override keys land as inert own properties). The defaults
 * themselves are deep-frozen at assembly in @speed/tokens, so a write
 * through any shared branch of the merged tree throws in strict mode
 * rather than polluting the tree every tenant merge starts from. The
 * factory then maps the merged tree onto MUI's createTheme surface:
 *
 * - palette roles: in light mode each semantic role's
 *   main/light/dark/contrastText maps key for key (dark mode remaps them,
 *   see the color-mode paragraph); the neutral ramp aliases the MUI grey
 *   scale (steps 50-900
 *   by number; A100/A200/A400/A700 alias the same-number steps, matching
 *   MUI's own grey aliasing). Tone 950 has no MUI slot and stays a token
 *   only. text/background/divider map directly.
 * - spacing: the token unit IS the MUI spacing unit (probe-verified
 *   against MUI 9.5.0: a numeric spacing option sets the base unit, so
 *   theme.spacing(n) === n x unit).
 * - shape.borderRadius maps directly; breakpoints.values and
 *   zIndex.values map directly (the slot names are MUI's own).
 * - typography: fontFamily.sans becomes the theme font family, and
 *   fontFamily.mono becomes `theme.typography.fontFamilyMono` (module
 *   augmentation, see the declaration below); the named
 *   size steps feed the variant role table, converted to rem on a 16px
 *   base (MUI's default htmlFontSize); fontWeight.regular/medium/bold
 *   fill fontWeightRegular/Medium/Bold (light has no token and keeps
 *   MUI's 300). Role table (all documented in the README):
 *   h1-h6 <- 5xl..lg, tight line height, semibold;
 *   subtitle1/2 <- md/sm, normal line height, medium;
 *   body1/2 <- md/sm, normal line height, regular;
 *   button <- sm, medium; caption <- xs, regular;
 *   overline <- xs, semibold, wide letter spacing.
 * - shadows: MUI themes carry 25 elevations. The six token slots are
 *   floored onto the ramp -- elevation i uses the nearest token slot at
 *   or below i (monotonic, never exceeding the design scale) -- with
 *   'none' at 0. MUI 9 accepts any array length, so the 25-entry ramp is
 *   deliberate: consumers can index theme.shadows like any stock MUI
 *   theme.
 * - components: the clickable-target floor, the surface mapping, the
 *   focus ring, the control boundary, the divider rule, the reduced-motion
 *   gate, the unavailable appearance and the page baseline, merged by
 *   component slot (see touch-floor.ts, surface.ts, focus-ring.ts,
 *   control-borders.ts, divider.ts, reduced-motion.ts, unavailable.ts and
 *   baseline.ts). Every base control
 *   carries a `minWidth`/`minHeight` of 44 nested under
 *   `@media (pointer: coarse)`, so the floor reaches every consuming
 *   package with no per-surface gating; a fine pointer emits none of it.
 *   The surface overrides paint
 *   the container, overlay and header roles and read their values from
 *   `theme.surface`, never from the tokens directly; the baseline paints
 *   the page role on the body, together with the typography glow and the
 *   optional canvas gradient. Every button-like
 *   control and Link carries a 2px `palette.primary.main` outline in its
 *   `focusVisible` state.
 * - motion: `motion.reducedMotion = 'system'` makes every MUI transition
 *   helper (Collapse, Fade, Grow, Slide, Zoom and the menu/drawer
 *   transitions) honour the operating system's reduced-motion
 *   preference. Skeleton paints keyframes rather than transitions, so it
 *   also carries an explicit root rule under the same media feature.
 * - surface: the resolved surface roles (module augmentation, see
 *   surface.ts) ride on the theme, so `useTheme()` and host components
 *   read exactly the values the component overrides paint with. The
 *   omitted token fields resolve through the inheritance contract: page
 *   -> background.default, container/overlay -> background.paper,
 *   borders -> divider, header -> neutral 50 (light) / neutral 800
 *   (dark).
 *
 * The returned theme is locale-free on purpose: AppThemeProvider merges
 * the MUI locale matching the active language on top at render time, and
 * hosts composing their own theme do the same via
 * createTheme(appTheme.theme, muiLocaleFor(i18n.language)).
 *
 * The color mode is the third argument (default 'light'). It sets
 * palette.mode and, for 'dark', swaps background/text/divider onto the
 * neutral ramp's dark end -- the token tree carries one set of surface
 * colors (a light palette), while the neutral ramp is a full scale, so a
 * dark surface is the same tokens read from the other end rather than a
 * second hardcoded palette. The semantic roles follow the same discipline:
 * each role's main tone is chosen for light surfaces and its light tone for
 * dark ones, so the dark mode maps main to the role's light tone and flips
 * contrastText to the dark end of the neutral ramp (neutral 900). MUI's
 * filled controls paint their hover background from the role's dark slot
 * (Button, filled Chip and Fab all read palette[role].dark), so keeping the
 * token's darkest tone there would drop the neutral-900 label below 4.5:1.
 * The dark mode therefore maps dark to the light tone mixed toward white:
 * a hover step that stays readable on the same label. The default is
 * 'light', so every existing caller that omits the argument renders
 * byte-for-byte as before.
 *
 * MUI 9 notes verified against the installed 9.5.0 at implementation
 * time: css variables are off by default (theme.vars is absent), so no
 * cssVariables setup is needed for a plain palette theme; createTheme
 * partial options merge with the defaults (keys left unmapped keep MUI's
 * defaults); the typography option surface is TypographyVariantsOptions
 * with 13 variants (no inherit).
 */

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
 * MUI's typography surface carries no monospace entry, while the token
 * tree does (`typography.fontFamily.mono`). The augmentation adds it
 * under the theme rather than exposing a sibling helper: a consumer
 * reads `theme.typography.fontFamilyMono` exactly like
 * `theme.typography.fontFamily`, including from `sx` callbacks, and a
 * host override of the token reaches it with no second wiring.
 *
 * `fontFamilyMono` is required on the built theme (the factory always
 * sets it) and optional on the theme options (a host composing its own
 * `createTheme` without the factory keeps MUI's shape).
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
 * The surface roles ride on the theme too (see surface.ts for how they are
 * resolved). The augmentation sits here rather than in surface.ts because
 * a consumer compiling against this package's emitted declarations loads
 * only the modules the entry's `.d.ts` graph reaches: surface.ts is
 * internal, and an augmentation declared there is invisible to such a
 * consumer, leaving `theme.surface` an unknown property.
 *
 * `surface` is required on the built theme (the factory always fills it)
 * and optional on the options (a host composing its own `createTheme`
 * without the factory keeps MUI's shape).
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

/** Typography rem base; MUI's default htmlFontSize is 16, keep aligned. */
const REM_BASE = 16

/** The token elevation slots in ascending order (their numeric key order). */
const ELEVATION_SLOTS = [1, 2, 4, 8, 16, 24] as const

/**
 * The two color modes a speed theme ships. The token tree defines the
 * surface colors for one mode (a light palette); the dark mode reads the
 * neutral ramp from its dark end for background/text/divider, so a mode
 * change needs no second palette.
 */
export type AppThemeMode = 'light' | 'dark'

/**
 * The color-mode preference a host may hold: an explicit resolved mode,
 * or 'system' to follow the device. Only the provider resolves 'system';
 * createAppTheme itself accepts the resolved AppThemeMode alone.
 */
export type AppThemeModePreference = AppThemeMode | 'system'

/** What the factory hands back: the merged tokens and the theme built from them. */
export interface AppTheme {
  /**
   * The merged token tree (defaults <- project <- tenant layers). Each
   * untouched branch is shared by identity with defaultTokens, which is
   * deep-frozen at assembly in @speed/tokens: a write through a shared
   * branch throws in strict mode and can never reach the default tree.
   * Branches an override layer rebuilt are plain objects owned by this
   * result alone -- a write there is local and cannot reach defaultTokens
   * or another result. Override through the factory's own arguments.
   */
  readonly tokens: SpeedTokens
  /**
   * The MUI theme assembled from the merged tokens. Locale-free: compose
   * with createTheme(appTheme.theme, muiLocaleFor(language)) -- or let
   * AppThemeProvider do it -- for MUI built-in texts to follow the
   * active language.
   */
  readonly theme: Theme
}

/**
 * Build the theme for merged speed tokens.
 *
 * `projectTokens` and `tenantOverrides` are the two override layers on
 * top of the built-in defaults, applied in that order (each diff-only).
 * Both are optional; a layer that is undefined is skipped entirely.
 * `mode` selects the color mode (default 'light'); see the module doc
 * comment for what the dark mode derives.
 */
export function createAppTheme(
  projectTokens?: TokensOverride,
  tenantOverrides?: TokensOverride,
  mode: AppThemeMode = 'light',
): AppTheme {
  // deepMerge skips undefined overrides by contract; drop empty layers
  // before the call so an omitted layer never reaches the type signature.
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
 * How far a dark-mode role's hover fill moves from its light tone toward
 * white. MUI paints the hover background of contained controls (Button,
 * filled Chip, Fab) from `palette[role].dark`, and in dark mode that fill
 * carries the neutral-900 label. Mixing toward white raises the fill's
 * luminance monotonically, so the label keeps 4.5:1 or better while the
 * hover still steps visibly away from `main` (the same light tone).
 */
const DARK_HOVER_WHITE_MIX = 0.2

/**
 * Mix a #RRGGBB color toward white by `weight` (0..1). A value outside
 * that shape is returned unchanged: tokens are hex by contract, and a host
 * overriding with another CSS color keeps it rather than getting "NaN".
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
 * A semantic role's palette entries for a mode. The light mode maps the
 * role key for key onto its tokens. The dark mode reads the same tonal
 * family from its other end: `main` becomes the role's light tone (the
 * tone meant for dark surfaces), `light` keeps that tone, and
 * `contrastText` becomes the dark end of the neutral ramp (neutral 900).
 * MUI's hover surfaces read `dark`; under that label the token's darkest
 * tone is unreadable, so the dark slot is the light tone mixed toward
 * white (see DARK_HOVER_WHITE_MIX) -- a lighter hover fill that keeps the
 * label readable.
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
  // A-tone aliases repeat the same-number step, mirroring MUI's own grey
  // scale where A100 === 100, A200 === 200, A400 === 400, A700 === 700.
  // Tone 950 is not part of MUI's grey and stays token-only.
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
 * MUI's theme.shadows is a 25-entry ramp indexed by elevation. The token
 * set only defines six slots; floor every other index onto the nearest
 * token slot at or below it so elevation never exceeds the design scale.
 *
 * MUI 9 types the ramp as a fixed 25-tuple; the loop below provably
 * produces 25 entries ('none' plus one push per elevation 1..24), which
 * the cast asserts on the runtime guarantee, not on faith.
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
 * The palette surface colors for a mode. The light mode maps the token
 * tree's own text/background/divider values directly; the dark mode reads
 * the same neutral ramp from its dark end (surfaces dark, text light),
 * because the token tree carries a single light palette while the ramp is
 * a full scale. The semantic roles follow the same direction (see
 * roleOptions).
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
