/**
 * The unavailable appearance at the theme layer: the disabled face for a
 * control that carries `aria-disabled="true"` and stays focusable.
 */

import { switchClasses } from '@mui/material/Switch'
import { listItemButtonClasses } from '@mui/material/ListItemButton'
import { darken, lighten } from '@mui/material/styles'
import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'

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

const SWITCH_COLOR_CLASS_PREFIX = switchClasses.colorPrimary.slice(
  0,
  -'Primary'.length,
)

function capitalize(value: string): string {
  return `${value.charAt(0).toUpperCase()}${value.slice(1)}`
}

// `pointer-events` stays: hover is needed to show an unavailable reason in a tooltip.
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

function buttonOutlinedStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    [`&${UNAVAILABLE}`]: {
      border: `1px solid ${theme.palette.action.disabledBackground}`,
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
    },
  }
}

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

function listItemButtonStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    [`&${UNAVAILABLE}`]: {
      cursor: 'default',
      color: theme.palette.action.disabled,
      '&:hover': { backgroundColor: 'transparent' },
      '&:active': { backgroundColor: 'transparent' },
      [`&.${listItemButtonClasses.selected}`]: {
        color: theme.palette.action.disabled,
        backgroundColor: 'transparent',
      },
      [`&.${listItemButtonClasses.selected}:hover`]: {
        backgroundColor: 'transparent',
      },
    },
  }
}

// MUI 9.5's `checkboxClasses` has no `input` key, so the input is selected by element.
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

// The attribute sits on the inner input; the thumb is its following sibling, the track a sibling of the base.
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

export const unavailableComponents: ThemeOptions['components'] = {
  MuiButton: {
    styleOverrides: {
      root: buttonRootStyle,
      contained: buttonContainedStyle,
      outlined: buttonOutlinedStyle,
    },
  },
  MuiIconButton: { styleOverrides: { root: iconButtonStyle } },
  MuiListItemButton: { styleOverrides: { root: listItemButtonStyle } },
  MuiCheckbox: { styleOverrides: { root: checkboxStyle } },
  MuiSwitch: { styleOverrides: { root: switchStyle } },
}
