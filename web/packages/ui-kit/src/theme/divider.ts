/**
 * The divider rule at the theme layer.
 *
 * MUI's Divider root zeroes every border edge and then raises a single one
 * with the CSS `thin` keyword, leaving the background transparent. A theme
 * that colours the root but leaves the width to that keyword leaves the
 * rule's visibility at the engine's discretion. The override spells the
 * visible edge out from `theme.palette.divider`: a horizontal divider
 * paints a 1px solid bottom rule, a vertical divider a 1px solid right
 * rule, in both color modes. The edge the orientation does not use is
 * zeroed, so a divider never carries a second faint line.
 *
 * A divider with children draws its rule through `::before`/`::after`
 * (MUI's with-children variant zeroes the root border), so that variant
 * receives the colour alone and keeps MUI's own geometry.
 */

import type { ThemeOptions } from '@mui/material/styles'

export const dividerComponents: ThemeOptions['components'] = {
  MuiDivider: {
    styleOverrides: {
      root: ({ theme, ownerState }) => {
        const borderColor = theme.palette.divider
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
