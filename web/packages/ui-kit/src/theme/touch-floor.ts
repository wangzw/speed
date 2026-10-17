/**
 * The touch-target floor at the theme layer.
 *
 * The layout design draws the pointer line at what the pointing device can
 * do, not at how wide the viewport is: on a coarse pointer (`pointer:
 * coarse` -- a finger, at any viewport width, a touchscreen laptop and a
 * keyboarded tablet included) every clickable target must be at least
 * 44x44, while a precise pointer keeps the stock control sizes untouched.
 *
 * Applying that floor component by component left it to whoever edited a
 * surface last: a navigation item reached 44 while the checkbox beside it
 * stayed at 42 and the primary button at 33, with no design reason behind
 * the difference. The floor therefore lives here -- one guarded rule per
 * base MUI component -- so every consuming package inherits it and no
 * surface can miss it. `touchTargetSx` remains for the controls the theme
 * cannot reach (a bare anchor, a clickable host element that is not one of
 * MUI's controls).
 *
 * Every rule is nested under `POINTER_COARSE_QUERY`, so a fine pointer
 * applies none of them: the rendered styles are unchanged.
 *
 * Which slot carries the clickable box was read from MUI 9's own source,
 * not assumed:
 *
 * - `Button`, `IconButton`, `Checkbox`, `Radio` and `ListItemButton`
 *   root in `ButtonBase` (the checkbox's clickable element is its root
 *   span, not the inner `input`), so `MuiButtonBase.styleOverrides.root`
 *   is the catch-all; the named entries below repeat the same floor so
 *   each control's coverage is legible in the theme rather than
 *   inherited implicitly.
 * - `Switch`'s clickable button also roots in `ButtonBase`, but it is
 *   absolutely positioned inside a `MuiSwitch-root` span whose
 *   `overflow: hidden` clips it at the stock 38px. The root grows to the
 *   floor and the rule pins the track's stock height while centering it,
 *   so the hit area grows without thickening the track.
 * - `InputBase`'s root is the visual field and its `input` slot is the
 *   element that carries the typing caret and the click, so both take the
 *   floor (a `Select` renders its trigger on the same `input` slot).
 * - `Link` renders an inline `<a>`: `min-height` has no effect on a
 *   non-replaced inline box, so the coarse rule also makes it an
 *   `inline-flex` box that centers its text.
 * - A clickable `Chip` renders on `ButtonBase` and inherits the catch-all,
 *   but its root is `MuiChip`; the explicit rule ties the guarantee to the
 *   chip's own `MuiChip-clickable` class, so a status chip keeps its
 *   compact size and the coverage survives a change of the underlying
 *   component.
 */

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

/**
 * The theme's component overrides that carry the touch-target floor. The
 * factory spreads this into the MUI theme options; the ordering inside a
 * theme's component map does not matter, each entry stands on its own.
 */
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
          // The switch button is absolutely positioned inside this root
          // and the root clips it (`overflow: hidden`), so the root has
          // to grow with it or the extra 6px of hit area is cut off.
          // Centering the track and pinning its stock height keeps the
          // switch's own visual exactly where it was, with the larger
          // target grown around it.
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
  MuiLink: {
    styleOverrides: {
      root: {
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
