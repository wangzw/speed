/** The 44px floor for an interactive target the theme's own control overrides cannot reach. */

import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from './internal/pointer.js'

/**
 * Usable wherever MUI accepts styles. On a coarse pointer it raises the
 * minimum width and height to 44px; on a precise pointer it adds no rule.
 */
export const touchTargetSx = {
  [POINTER_COARSE_QUERY]: {
    minWidth: TOUCH_TARGET_MIN_PX,
    minHeight: TOUCH_TARGET_MIN_PX,
  },
} as const
