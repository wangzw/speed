/**
 * The surface system at the theme layer.
 *
 * The token tree names four semantic surface roles -- page, container,
 * overlay, header -- and leaves every field optional; an omitted field
 * means "inherit the slot the theme already reads" (see
 * @speed/tokens/surface). This module resolves that contract once, maps
 * the result onto `theme.surface` through MUI's documented module
 * augmentation, and emits the `components` overrides that paint the
 * surfaces.
 *
 * Two rules shape the code:
 *
 * 1. The style is the token value. There is no `surfaceStyle` switch and
 *    no theme-factory parameter: a project changes a surface by
 *    overriding a token, and the same value reaches `useTheme()` and
 *    every host component through `theme.surface`.
 * 2. Component overrides read from `theme`, never from a closed-over
 *    token object. `styleOverrides` callbacks receive the built theme,
 *    so the emitted CSS and `theme.surface` can never disagree, and a
 *    future CSS-variable route has a single read site.
 *
 * `MuiPaper.root` is deliberately untouched. MUI's dark mode paints an
 * elevation overlay onto Paper through `backgroundImage`; clearing it
 * (or marking it `none`) would flatten Dialog and Menu in dark mode. The
 * overrides below therefore target the concrete slots that own a
 * surface.
 */

import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'
import type { SpeedTokens, SurfaceRoleTokens } from '@speed/tokens'

/**
 * A surface role after inheritance has been resolved: every field the
 * theme paints with is concrete. `elevation` and `backdropFilter` stay
 * optional because their absent value is meaningful -- the first keeps
 * each MUI component's own elevation, the second means "no filter".
 */
export interface SurfaceThemeRole {
  /**
   * The resolved fill color with the role's opacity applied (an inheriting
   * slot when the token omits the fill).
   */
  readonly fill: string
  /** The resolved border color; surfaces only draw it at `borderWidth > 0`. */
  readonly borderColor: string
  /**
   * The border width in px. 0 when the token declares none: a surface has
   * no border until a project asks for one, except `container`, whose
   * token default is 1.
   */
  readonly borderWidth: number
  /** The elevation to paint with, or undefined to keep the component's own. */
  readonly elevation?: number
  /** The backdrop filter to apply, or undefined for none. */
  readonly backdropFilter?: string
}

/** The resolved surface roles, read by hosts and by the component overrides. */
export interface SurfaceTheme {
  readonly page: SurfaceThemeRole
  readonly container: SurfaceThemeRole
  readonly overlay: SurfaceThemeRole
  readonly header: SurfaceThemeRole
}

/**
 * MUI's documented extension point for theme values: module augmentation
 * puts `surface` on the built theme (required -- the factory always fills
 * it) and on the options (optional -- a host composing its own
 * `createTheme` without the factory keeps MUI's shape).
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

/** The palette slots the surface contract inherits from. */
export interface SurfacePaletteSlots {
  readonly background: {
    readonly default: string
    readonly paper: string
  }
  readonly divider: string
}

/**
 * Apply a role's alpha to its resolved fill. `#RRGGBB` becomes the same
 * color as `rgba(...)`; any other CSS color is returned unchanged, the
 * same hex-only rule the factory applies when deriving colors. A missing
 * or opaque alpha leaves the hex value untouched.
 */
function withOpacity(fill: string, opacity: number | undefined): string {
  if (opacity === undefined || opacity >= 1 || !/^#[0-9a-fA-F]{6}$/.test(fill)) {
    return fill
  }
  const value = Number.parseInt(fill.slice(1), 16)
  const red = (value >> 16) & 0xff
  const green = (value >> 8) & 0xff
  const blue = value & 0xff
  return `rgba(${red}, ${green}, ${blue}, ${opacity})`
}

/**
 * Resolve the optional surface tokens against the theme's own slots:
 * page -> background.default, container/overlay -> background.paper,
 * every border -> divider, header -> neutral 50 in light mode and
 * neutral 900 in dark mode. Because the resolved values come from the
 * theme palette, a project that only overrides `background.paper` moves
 * the container and overlay surfaces with it.
 *
 * `mode` decides the header tone alone; every other slot already carries
 * its mode-specific value in `palette`. A role's `opacity` is applied to
 * the resolved fill in both modes, so one token value makes a surface
 * translucent on either page; the alpha is derived per mode from the
 * already-resolved fill rather than from a second color.
 */
export function resolveSurfaceTheme(
  tokens: SpeedTokens,
  palette: SurfacePaletteSlots,
  mode: 'light' | 'dark',
): SurfaceTheme {
  const resolve = (
    role: SurfaceRoleTokens,
    fill: string,
  ): SurfaceThemeRole => ({
    fill: withOpacity(role.fill ?? fill, role.opacity),
    borderColor: role.borderColor ?? palette.divider,
    borderWidth: role.borderWidth ?? 0,
    ...(role.elevation === undefined ? {} : { elevation: role.elevation }),
    ...(role.backdropFilter === undefined
      ? {}
      : { backdropFilter: role.backdropFilter }),
  })
  return {
    page: resolve(tokens.surface.page, palette.background.default),
    container: resolve(tokens.surface.container, palette.background.paper),
    overlay: resolve(tokens.surface.overlay, palette.background.paper),
    header: resolve(
      tokens.surface.header,
      tokens.color.neutral[mode === 'dark' ? 900 : 50],
    ),
  }
}

/**
 * The CSS a surface role paints. `borderStyle` is explicit because a
 * border color alone draws nothing; a zero width keeps the declaration
 * inert without removing any border the component's own styles set.
 */
function roleStyle(role: SurfaceThemeRole, theme: Theme): CSSObject {
  return {
    backgroundColor: role.fill,
    borderColor: role.borderColor,
    borderStyle: 'solid',
    borderWidth: role.borderWidth,
    ...(role.backdropFilter === undefined
      ? {}
      : { backdropFilter: role.backdropFilter }),
    ...(role.elevation === undefined
      ? {}
      : { boxShadow: theme.shadows[role.elevation] }),
  }
}

/** The overlay paper style, shared by the floating surfaces below. */
function overlayStyle(theme: Theme): CSSObject {
  return roleStyle(theme.surface.overlay, theme)
}

/**
 * The theme's surface overrides.
 *
 * - Card: the container surface on `variant="outlined"`, expressed as a
 *   theme variant (the documented theme-components pattern). Card has a
 *   single `root` slot -- a `styleOverrides.outlined` key would never be
 *   resolved -- so the variant is the only attachment that reaches the
 *   component. `borderRadius` is not set: Paper's own root style reads
 *   `theme.shape.borderRadius`, which is where the radius belongs.
 * - AppBar: the container surface with a bottom divider only. The bar is
 *   the shell's top edge, so the four-sided border `roleStyle` emits
 *   would draw three edges at the viewport boundary; the fill and the
 *   divider still read from `theme.surface.container`, so a project
 *   override reaches the bar without the host re-declaring a color.
 * - Dialog / Menu / Popover / Drawer paper and Tooltip: the overlay
 *   surface. A permanent or persistent drawer is a persistent navigation
 *   panel rather than a floating layer, so it takes the container
 *   surface; only the temporary drawer floats.
 * - TableCell head: the header surface, as one role rather than an
 *   ad-hoc `action.hover` (a hover-state color would make the header and
 *   a hovered row identical).
 * - TableContainer: no surface. The surrounding SectionCard owns the
 *   single surface; painting one here too would stack two surfaces at
 *   the same level, which the design explicitly rejects.
 */
export const surfaceComponents: ThemeOptions['components'] = {
  MuiCard: {
    variants: [
      {
        props: { variant: 'outlined' },
        style: ({ theme }) => roleStyle(theme.surface.container, theme),
      },
    ],
  },
  MuiAppBar: {
    styleOverrides: {
      root: ({ theme }) => ({
        backgroundColor: theme.surface.container.fill,
        borderBottomColor: theme.surface.container.borderColor,
        borderBottomStyle: 'solid',
        borderBottomWidth: theme.surface.container.borderWidth,
        ...(theme.surface.container.backdropFilter === undefined
          ? {}
          : { backdropFilter: theme.surface.container.backdropFilter }),
      }),
    },
  },
  MuiDialog: {
    styleOverrides: { paper: ({ theme }) => overlayStyle(theme) },
  },
  MuiMenu: {
    styleOverrides: { paper: ({ theme }) => overlayStyle(theme) },
  },
  MuiPopover: {
    styleOverrides: { paper: ({ theme }) => overlayStyle(theme) },
  },
  MuiDrawer: {
    styleOverrides: {
      // The paper slot receives the drawer's own owner state, so the
      // variant is read here rather than through a component-level
      // variant: the permanent/persistent panel is a standing navigation
      // surface, only the temporary drawer floats.
      paper: ({ theme, ownerState }) =>
        roleStyle(
          ownerState.variant === 'temporary'
            ? theme.surface.overlay
            : theme.surface.container,
          theme,
        ),
    },
  },
  MuiTooltip: {
    styleOverrides: { tooltip: ({ theme }) => overlayStyle(theme) },
  },
  MuiTableCell: {
    styleOverrides: {
      head: ({ theme }) => ({
        backgroundColor: theme.surface.header.fill,
        color: theme.palette.text.secondary,
        ...(theme.surface.header.backdropFilter === undefined
          ? {}
          : { backdropFilter: theme.surface.header.backdropFilter }),
      }),
    },
  },
}

/** A plain object (not an array, not a class instance) worth merging. */
function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const proto = Object.getPrototypeOf(value) as unknown
  return proto === Object.prototype || proto === null
}

/** A style contribution as a callback; a static object is wrapped. */
function asCallback(value: unknown): (props: unknown) => unknown {
  return typeof value === 'function'
    ? (value as (props: unknown) => unknown)
    : () => value
}

/**
 * Merge two values at the same path. Objects merge key by key, variant
 * arrays concatenate, and style callbacks at the same slot compose --
 * MUI accepts an array of interpolations from one callback, so both
 * contributions still apply. A style slot accepts either a static object
 * or a theme callback, so the two forms compose in either order rather
 * than the callback replacing the object (the touch floor is static, the
 * focus ring reads the theme). Any other scalar wins, matching MUI's own
 * "later source overrides" reading.
 */
function mergeDeep(base: unknown, next: unknown): unknown {
  if (next === undefined) {
    return base
  }
  if (base === undefined) {
    return next
  }
  if (isPlainObject(base) && isPlainObject(next)) {
    const result: Record<string, unknown> = { ...base }
    for (const [key, value] of Object.entries(next)) {
      result[key] = mergeDeep(result[key], value)
    }
    return result
  }
  if (Array.isArray(base) && Array.isArray(next)) {
    return [...base, ...next]
  }
  const baseIsCallback = typeof base === 'function'
  const nextIsCallback = typeof next === 'function'
  if (
    (baseIsCallback && (nextIsCallback || isPlainObject(next))) ||
    (nextIsCallback && isPlainObject(base))
  ) {
    const first = asCallback(base)
    const second = asCallback(next)
    return (props: unknown) => [first(props), second(props)]
  }
  return next
}

/**
 * Merge theme component maps BY SLOT. A shallow spread would drop the
 * earlier source's `styleOverrides` for the same `Mui*` key (the most
 * likely collision: two concerns painting the same component), so each
 * key and each slot is merged in turn. Later sources win per leaf, which
 * is the order the factory hands them in.
 */
export function mergeThemeComponents(
  ...sources: ReadonlyArray<ThemeOptions['components']>
): NonNullable<ThemeOptions['components']> {
  const merged: Record<string, unknown> = {}
  for (const source of sources) {
    if (source === undefined) {
      continue
    }
    for (const [name, entry] of Object.entries(source)) {
      merged[name] = mergeDeep(merged[name], entry)
    }
  }
  return merged as NonNullable<ThemeOptions['components']>
}
