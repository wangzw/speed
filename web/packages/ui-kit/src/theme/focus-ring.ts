/** The static 2px focus ring at the theme layer. */

import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'

/** The 2px primary outline drawn for keyboard-visible focus. */
function focusRingStyle({ theme }: { theme: Theme }): CSSObject {
  return {
    '&.Mui-focusVisible': {
      outline: `2px solid ${theme.palette.primary.main}`,
      outlineOffset: 2,
    },
  }
}

export const focusRingComponents: ThemeOptions['components'] = {
  MuiButtonBase: { styleOverrides: { root: focusRingStyle } },
  MuiLink: { styleOverrides: { root: focusRingStyle } },
}
