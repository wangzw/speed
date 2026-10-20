/**
 * The reduced-motion layer at the theme layer.
 *
 * `motion.reducedMotion = 'system'` makes every MUI transition helper
 * (Collapse, Fade, Grow, Slide, Zoom and the menu/drawer transitions)
 * honour the operating system's preference. Skeleton paints its pulse and
 * wave as keyframes rather than transitions, so that setting does not
 * reach it. The root rule below states the same gate on the component
 * itself: under the reduced-motion media feature the animation stops.
 *
 * MUI 9.5 already reads `theme.motion` for those two variants; the
 * override keeps the requirement explicit in the theme and stops any root
 * animation MUI attaches outside that path.
 */

import type { ThemeOptions } from '@mui/material/styles'

/**
 * The media feature MUI's `reducedMotion: 'system'` gates its own
 * transition and Skeleton animation styles on.
 */
export const REDUCED_MOTION_QUERY = '@media (prefers-reduced-motion: reduce)'

/** The theme's reduced-motion overrides, merged by component slot. */
export const reducedMotionComponents: ThemeOptions['components'] = {
  MuiSkeleton: {
    styleOverrides: {
      root: {
        [REDUCED_MOTION_QUERY]: { animation: 'none' },
      },
    },
  },
}
