/**
 * The touch-target recipe: the one style object for interactive controls
 * that must reach a finger-sized hit area on a touch device.
 *
 * The layout design draws this line at the pointing device, not at the
 * viewport: on a coarse pointer (`pointer: coarse` -- a finger, at any
 * viewport width, a touchscreen laptop and a keyboarded tablet included)
 * an interactive target must be at least 44x44, while a precise pointer
 * keeps the stock control size untouched. The recipe is a plain sx object
 * gated on the media feature, so applying it adds the floor on touch and
 * nothing at all on a mouse.
 *
 * The theme's own component overrides (src/theme/touch-floor.ts) carry
 * this floor for MUI's base controls -- Button, IconButton, Checkbox,
 * Radio, Switch, InputBase, Link, ListItemButton and a clickable Chip --
 * so a control built from one of those needs no per-control application.
 * This recipe remains for a target the theme cannot reach: a bare DOM
 * element a host makes interactive (a clickable Box or span), a
 * third-party widget outside MUI's classes, or a custom control that does
 * not root in ButtonBase. The media feature and the 44 number are owned
 * by src/internal/pointer.ts, so the floor the recipe applies cannot
 * drift from the theme's. It is deliberately additive: a control that
 * omits it renders exactly as before.
 */

import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from './internal/pointer.js'

/**
 * The recipe object, usable wherever MUI accepts styles: the `sx` prop of
 * a Button, IconButton or ListItemButton (an array with a control's own
 * styles), or the argument of a `styled()` call. On a coarse pointer it
 * raises the control's minimum width and height to the 44px floor; on a
 * precise pointer it contributes no rule.
 */
export const touchTargetSx = {
  [POINTER_COARSE_QUERY]: {
    minWidth: TOUCH_TARGET_MIN_PX,
    minHeight: TOUCH_TARGET_MIN_PX,
  },
} as const
