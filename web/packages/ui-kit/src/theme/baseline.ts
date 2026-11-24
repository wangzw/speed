/** The page-level baseline at the theme layer: page fill, text glow and canvas gradient. */

import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'
import type { SpeedTokens } from '@speed/tokens'

/**
 * The theme's page baseline overrides. An absent optional token emits no
 * declaration, so rendering is unchanged until a project asks for one.
 */
export function baselineComponents(
  tokens: SpeedTokens,
  mode: 'light' | 'dark',
): ThemeOptions['components'] {
  const textGlow = tokens.typography.textGlow
  const canvas = tokens.color.background.canvas?.[mode]
  return {
    MuiCssBaseline: {
      styleOverrides: (theme: Theme): CSSObject => ({
        body: {
          backgroundColor: theme.surface.page.fill,
          ...(textGlow === undefined ? {} : { textShadow: textGlow }),
          ...(canvas === undefined
            ? {}
            : {
                backgroundImage: `linear-gradient(160deg, ${canvas.from}, ${canvas.to})`,
                backgroundAttachment: 'fixed',
              }),
        },
      }),
    },
  }
}
