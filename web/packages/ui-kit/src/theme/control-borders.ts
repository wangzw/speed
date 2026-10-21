/**
 * The resting control boundary at the theme layer.
 *
 * MUI paints an outlined input's resting border as 23% of the foreground
 * -- rgba(0, 0, 0, 0.23) in light mode, rgba(255, 255, 255, 0.23) in dark
 * mode. On this theme's surfaces that lands around 1.9-2.2:1, below the
 * 3:1 WCAG 1.4.11 asks for the boundary that identifies a control. The
 * resting border therefore reads `palette.text.secondary`, which clears
 * 3:1 on every surface role in both modes; hover already moves to
 * `text.primary` and focus to `primary.main`, so only the resting state
 * needs the lift.
 *
 * The rule reads the built theme inside the callback, so a host that
 * overrides the text colors moves the border with them.
 */

import type { ThemeOptions } from '@mui/material/styles'

export const controlBorderComponents: ThemeOptions['components'] = {
  MuiOutlinedInput: {
    styleOverrides: {
      notchedOutline: ({ theme }) => ({
        borderColor: theme.palette.text.secondary,
      }),
    },
  },
}
