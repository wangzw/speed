/** The divider rule at the theme layer, drawn solid in both color modes. */

import { alpha } from '@mui/material/styles'
import type { ThemeOptions } from '@mui/material/styles'

/** The dark rule's alpha over `text.primary`. */
const DARK_RULE_ALPHA = 0.16

export const dividerComponents: ThemeOptions['components'] = {
  MuiDivider: {
    styleOverrides: {
      root: ({ theme, ownerState }) => {
        const borderColor =
          theme.palette.mode === 'dark'
            ? alpha(theme.palette.text.primary, DARK_RULE_ALPHA)
            : theme.palette.divider
        if (Boolean(ownerState.children)) {
          return { borderColor }
        }
        if (ownerState.orientation === 'vertical') {
          return { borderColor, borderRightWidth: 1, borderBottomWidth: 0 }
        }
        return { borderColor, borderBottomWidth: 1, borderTopWidth: 0 }
      },
    },
  },
}
