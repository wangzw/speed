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
 * One shared object serves every control that gates this way -- the
 * ui-kit's own components, and each consuming package's icon buttons and
 * compact action controls through the public export. A single object is
 * what lets a change to the floor (the 44 number, or the media feature it
 * keys on, both owned by src/internal/pointer.ts) land in every gated
 * control at once instead of in one of a dozen hand-copied property sets.
 * It is deliberately additive: a control that omits it renders exactly as
 * before.
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
