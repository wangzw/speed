/** The control boundary at the theme layer: the resting border lifted to 3:1. */

import type { ThemeOptions } from '@mui/material/styles'

export const controlBorderComponents: ThemeOptions['components'] = {
  MuiOutlinedInput: {
    styleOverrides: {
      notchedOutline: ({ theme }) => ({
        borderColor: theme.palette.text.secondary,
      }),
    },
  },
  MuiButton: {
    styleOverrides: {
      // MUI 9.5 composes no `outlinedPrimary` slot, so an override keyed that way never lands.
      outlined: () => ({
        borderColor: 'currentColor',
      }),
    },
  },
}
