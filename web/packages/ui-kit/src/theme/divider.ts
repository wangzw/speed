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
 *
 * The colour is mode-dependent. Light mode paints `palette.divider`
 * directly. In dark mode the divider token (neutral 700) sits one step
 * above the paper (neutral 800): drawn, but indistinguishable at 1px.
 * The dark rule is therefore an ink wash -- `text.primary` at a low
 * alpha -- which separates from the paper while staying translucent, so
 * a preset that tints its surfaces (glass) shows through instead of
 * being flattened by an opaque line. Reading the palette keeps a host
 * override of `text.primary` flowing into the rule.
 */

import { alpha } from '@mui/material/styles'
import type { ThemeOptions } from '@mui/material/styles'

/**
 * The dark rule's alpha over `text.primary`. Low enough to read as a
 * rule rather than a bar, high enough to clear the dark paper.
 */
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
