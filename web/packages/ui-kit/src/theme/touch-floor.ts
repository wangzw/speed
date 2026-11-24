/** The 44px touch-target floor at the theme layer, gated on a coarse pointer. */

import { chipClasses } from '@mui/material/Chip'
import type { ThemeOptions } from '@mui/material/styles'
import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'

/** The 44x44 minimum, gated on a coarse pointer. A fresh object per slot. */
function coarseFloor(): Record<string, unknown> {
  return {
    [POINTER_COARSE_QUERY]: {
      minWidth: TOUCH_TARGET_MIN_PX,
      minHeight: TOUCH_TARGET_MIN_PX,
    },
  }
}

/** The theme's component overrides that carry the touch-target floor. */
export const touchFloorComponents: ThemeOptions['components'] = {
  MuiButtonBase: { styleOverrides: { root: coarseFloor() } },
  MuiButton: { styleOverrides: { root: coarseFloor() } },
  MuiIconButton: { styleOverrides: { root: coarseFloor() } },
  MuiCheckbox: { styleOverrides: { root: coarseFloor() } },
  MuiRadio: { styleOverrides: { root: coarseFloor() } },
  MuiSwitch: {
    styleOverrides: {
      root: {
        [POINTER_COARSE_QUERY]: {
          minWidth: TOUCH_TARGET_MIN_PX,
          minHeight: TOUCH_TARGET_MIN_PX,
          // The root clips its absolutely positioned button, so it must grow;
          // the pinned track height keeps the switch's visual unchanged.
          alignItems: 'center',
          '& .MuiSwitch-track': { height: 14 },
          '&.MuiSwitch-sizeSmall .MuiSwitch-track': { height: 10 },
        },
      },
    },
  },
  MuiListItemButton: { styleOverrides: { root: coarseFloor() } },
  MuiInputBase: {
    styleOverrides: { root: coarseFloor(), input: coarseFloor() },
  },
  // The trigger is its own `div`, not the InputBase input, so it needs the floor explicitly.
  MuiSelect: { styleOverrides: { select: coarseFloor() } },
  MuiLink: {
    styleOverrides: {
      root: {
        // `min-height` does not apply to an inline box, so the link becomes inline-flex.
        [POINTER_COARSE_QUERY]: {
          display: 'inline-flex',
          alignItems: 'center',
          minWidth: TOUCH_TARGET_MIN_PX,
          minHeight: TOUCH_TARGET_MIN_PX,
        },
      },
    },
  },
  MuiChip: {
    styleOverrides: {
      root: {
        [POINTER_COARSE_QUERY]: {
          [`&.${chipClasses.clickable}`]: {
            minWidth: TOUCH_TARGET_MIN_PX,
            minHeight: TOUCH_TARGET_MIN_PX,
          },
        },
      },
    },
  },
}
