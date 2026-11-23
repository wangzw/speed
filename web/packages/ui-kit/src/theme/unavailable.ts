/**
 * The unavailable appearance at the theme layer.
 *
 * A control that must stay focusable while it cannot run carries
 * `aria-disabled="true"` instead of MUI's native `disabled` prop, because
 * a natively disabled control stops being focusable: per the HTML spec it
 * no longer holds focus, and browsers differ in when and how they move
 * focus off it (inside a modal's focus trap it lands on the trap surface).
 * MUI paints its disabled face from the `.Mui-disabled` state class, which
 * such a control never gets, so without this layer an unavailable control
 * would look exactly like an enabled one. The overrides below give it the
 * same face, keyed on the attribute the control really carries:
 *
 * - `MuiButton` and `MuiIconButton` carry the attribute on their root
 *   element; `MuiSwitch` and `MuiCheckbox` carry it on the inner `input`,
 *   so those two are selected through `:has()` (and, for the switch thumb,
 *   the input's following-sibling relation).
 * - Every declaration mirrors MUI 9.5.0's own disabled rules read from the
 *   installed package: the disabled foreground and background from
 *   `palette.action`, the outlined boundary, a flat contained fill with no
 *   elevation, the switch thumb's disabled tones (the grey pair unchecked,
 *   the lightened/darkened role tone checked) and the switch track's
 *   disabled opacity. The surfaces MUI paints on `:hover` and `:active`
 *   are reset because the control still receives pointer events.
 * - `pointer-events` is deliberately not set to `none`: the control keeps
 *   hover so an unavailable reason can be shown in a tooltip. Only the
 *   clickable feedback is removed (`cursor: default`, no hover fill).
 * - The focus ring is untouched: its rule keys on `.Mui-focusVisible`,
 *   which a keyboard-focused unavailable control still gets.
 *
 * A host that recolors `palette.action` or the semantic roles moves these
 * values with it, because every declaration is read from the built theme
 * inside the callback.
 */

import { switchClasses } from '@mui/material/Switch'
import { darken, lighten } from '@mui/material/styles'
import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'

/** The state attribute an unavailable control carries. */
const UNAVAILABLE = '[aria-disabled="true"]'

/** The role colours MUI's switch disabled rules distinguish when checked. */
const SWITCH_COLORS = [
  'primary',
  'secondary',
  'error',
  'warning',
  'info',
  'success',
] as const

/**
 * The class-name prefix MUI composes per switch colour, derived from the
 * one documented key rather than repeated as a literal.
 */
const SWITCH_COLOR_CLASS_PREFIX = switchClasses.colorPrimary.slice(
  0,
  -'Primary'.length,
)

function capitalize(value: string): string {
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`
}

/** The disabled face of a Button root: foreground, cursor, no hover fill. */
function buttonRootStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    [`&${UNAVAILABLE}`]: {
      cursor: 'default',
      color: theme.palette.action.disabled,
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
    },
  }
}

/** The disabled face of a contained Button: flat fill, no elevation. */
function buttonContainedStyle({ theme }: { theme: Theme }): CSSObject {
  const { action } = theme.palette
  return {
    [`&${UNAVAILABLE}`]: {
      color: action.disabled,
      backgroundColor: action.disabledBackground,
      boxShadow: theme.shadows[0],
      '&:hover': {
        backgroundColor: action.disabledBackground,
        boxShadow: theme.shadows[0],
      },
      '&:active': {
        backgroundColor: action.disabledBackground,
        boxShadow: theme.shadows[0],
      },
    },
  }
}

/** The disabled face of an outlined Button: the disabled boundary. */
function buttonOutlinedStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    [`&${UNAVAILABLE}`]: {
      border: `1px solid ${theme.palette.action.disabledBackground}`,
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
    },
  }
}

/** The disabled face of an IconButton: foreground only, no hover disc. */
function iconButtonStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    [`&${UNAVAILABLE}`]: {
      cursor: 'default',
      color: theme.palette.action.disabled,
      backgroundColor: 'transparent',
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
    },
  }
}

/**
 * The disabled face of a Checkbox. The attribute sits on the inner
 * `input`, while the face is the root's `color` (the icon paints in
 * `currentColor`), so the root is reached with `:has()`. The input is
 * selected by its element rather than a class: MUI 9.5.0's
 * `checkboxClasses` exposes no `input` key.
 */
function checkboxStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    '&:has(input[aria-disabled="true"])': {
      cursor: 'default',
      color: theme.palette.action.disabled,
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
    },
  }
}

/**
 * The disabled face of a Switch. The attribute sits on the inner `input`
 * inside the switch base; the thumb is that input's following sibling and
 * the track is a sibling of the switch base, so the thumb is reached by
 * the sibling selector and the track by `:has()` on the root. Every
 * checked tone MUI distinguishes gets its own rule, because the role
 * colour is a state class on the switch base.
 */
function switchStyle({ theme }: { theme: Theme }): CSSObject {
  const { palette } = theme
  const uncheckedThumb =
    palette.mode === 'light' ? palette.grey[100] : palette.grey[600]
  const trackOpacity = palette.mode === 'light' ? 0.12 : 0.2
  const style: CSSObject = {
    [`&:has(.${switchClasses.input}${UNAVAILABLE})`]: {
      cursor: 'default',
      [`& .${switchClasses.switchBase}`]: { cursor: 'default' },
      [`& .${switchClasses.switchBase}:hover`]: {
        backgroundColor: 'transparent',
      },
      [`& .${switchClasses.track}`]: { opacity: trackOpacity },
    },
    [`& .${switchClasses.input}${UNAVAILABLE} ~ .${switchClasses.thumb}`]: {
      color: uncheckedThumb,
    },
  }
  for (const color of SWITCH_COLORS) {
    const main = palette[color].main
    const checkedThumb =
      palette.mode === 'light' ? lighten(main, 0.62) : darken(main, 0.55)
    const colorClass = `${SWITCH_COLOR_CLASS_PREFIX}${capitalize(color)}`
    style[
      `& .${colorClass}.${switchClasses.checked} .${switchClasses.input}${UNAVAILABLE} ~ .${switchClasses.thumb}`
    ] = { color: checkedThumb }
  }
  return style
}

/** The theme's unavailable overrides, merged by component slot. */
export const unavailableComponents: ThemeOptions['components'] = {
  MuiButton: {
    styleOverrides: {
      root: buttonRootStyle,
      contained: buttonContainedStyle,
      outlined: buttonOutlinedStyle,
    },
  },
  MuiIconButton: { styleOverrides: { root: iconButtonStyle } },
  MuiCheckbox: { styleOverrides: { root: checkboxStyle } },
  MuiSwitch: { styleOverrides: { root: switchStyle } },
}
