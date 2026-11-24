/** Text buttons keep their sentence case; contained and outlined buttons keep MUI's capitals. */

import type { ThemeOptions } from '@mui/material/styles'

export const letterCaseComponents: ThemeOptions['components'] = {
  MuiButton: {
    styleOverrides: {
      // The variant class outranks the inherited typography without touching every button.
      root: { '&.MuiButton-text': { textTransform: 'none' } },
    },
  },
}
