/** The surface system at the theme layer: role resolution, `theme.surface` and the component overrides. */

import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'
import type { SpeedTokens, SurfaceRoleTokens } from '@speed/tokens'

/**
 * A surface role after inheritance: every field the theme paints with is
 * concrete. An absent `elevation` keeps the component's own; an absent
 * `backdropFilter` means no filter.
 */
export interface SurfaceThemeRole {
  /**
   * The resolved fill color with the role's opacity applied (an inheriting
   * slot when the token omits the fill).
   */
  readonly fill: string
  /** The resolved border color; surfaces only draw it at `borderWidth > 0`. */
  readonly borderColor: string
  /** The border width in px; 0 when the token declares none. */
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

/** The palette slots the surface contract inherits from. */
export interface SurfacePaletteSlots {
  readonly background: {
    readonly default: string
    readonly paper: string
  }
  readonly divider: string
}

/**
 * Applies the role's alpha to its fill. `#RRGGBB` becomes `rgba(...)`; any
 * other CSS color, a missing alpha or an opaque one is returned unchanged.
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
 * Resolves the optional surface tokens against the theme's own slots: page
 * from `background.default`, container and overlay from
 * `background.paper`, borders from `divider`, header from neutral 50 or
 * 900 by mode. A role's `opacity` applies to the resolved fill in both
 * modes.
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
    // One rim color cannot read on both a light and a dark surface.
    borderColor:
      (mode === 'dark' ? role.borderColorDark : undefined) ??
      role.borderColor ??
      palette.divider,
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

/** `borderStyle` is explicit: a border color alone draws nothing. */
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

function overlayStyle(theme: Theme): CSSObject {
  return roleStyle(theme.surface.overlay, theme)
}

/** The theme's surface overrides, one concrete component slot per surface role. */
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
      // Only the temporary drawer floats; permanent and persistent panels are navigation chrome.
      paper: ({ theme, ownerState }) => {
        if (ownerState.variant === 'temporary') {
          return overlayStyle(theme)
        }
        // A standing column runs edge to edge with no radius and no elevation.
        const role = theme.surface.container
        return {
          backgroundColor: role.fill,
          borderRightColor: role.borderColor,
          borderRightStyle: 'solid',
          borderRightWidth: role.borderWidth,
          borderRadius: 0,
          ...(role.backdropFilter === undefined
            ? {}
            : { backdropFilter: role.backdropFilter }),
        }
      },
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

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const proto = Object.getPrototypeOf(value) as unknown
  return proto === Object.prototype || proto === null
}

function asCallback(value: unknown): (props: unknown) => unknown {
  return typeof value === 'function'
    ? (value as (props: unknown) => unknown)
    : () => value
}

/**
 * Merges two values at the same path: objects key by key, variant arrays
 * concatenate, and style callbacks (or an object with a callback) compose,
 * because a callback replacing the other would drop one contribution.
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
 * Merges theme component maps by slot: a shallow spread would drop the
 * earlier source's `styleOverrides` for the same `Mui*` key. Later sources
 * win per leaf.
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
