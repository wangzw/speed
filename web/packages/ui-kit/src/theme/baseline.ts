/**
 * The page-level baseline at the theme layer.
 *
 * `MuiCssBaseline` paints the document body, which is where three token
 * values land:
 *
 * - the page surface fill (`surface.page.fill`) becomes the body
 *   background color; it defaults to `palette.background.default`, so a
 *   token tree that sets no page fill renders exactly as before;
 * - `typography.textGlow` becomes the body text shadow;
 * - `color.background.canvas` becomes a fixed gradient on the body, using
 *   the stop pair of the active color mode.
 *
 * The page fill is read from the built theme through MUI's documented
 * `styleOverrides` callback, so the emitted CSS and `theme.surface` can
 * never disagree. The other two values have no theme slot of their own;
 * the callback closes over them, and both are plain CSS values the token
 * tree carries.
 *
 * Paint order matters when both are set: a background image paints above
 * the background color, so an opaque canvas gradient hides the page fill.
 * A preset that wants the fill to participate in the backdrop leaves alpha
 * in the canvas stops; a preset whose canvas is the page background sets
 * no page fill.
 */

import type { CSSObject, Theme, ThemeOptions } from '@mui/material/styles'
import type { SpeedTokens } from '@speed/tokens'

/**
 * The theme's page baseline overrides. Absent optional tokens emit no glow
 * or gradient declaration, and the page fill repeats MUI's own default
 * value, so rendering is unchanged until a project asks for one.
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
