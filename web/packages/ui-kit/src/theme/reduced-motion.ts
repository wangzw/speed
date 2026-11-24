/** The reduced-motion layer: Skeleton's keyframe animations stop under the system preference. */

import type { ThemeOptions } from '@mui/material/styles'

/** The media feature MUI's `reducedMotion: 'system'` gates its animations on. */
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
