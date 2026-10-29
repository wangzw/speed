/**
 * The theme-layer letter case for text buttons: a link-shaped action reads as
 * the sentence it is.
 *
 * Material's `button` typography asks for capitals, which suits a filled or
 * outlined button and does not suit a text button: "Create an account" and
 * "Forgot your password?" are actions styled as links, and capitals turn them
 * into buttons the eye has to reclassify. The override is scoped to the `text`
 * variant only; contained and outlined buttons keep the stock transform, and
 * the decorative `overline` role keeps its capitals.
 */

import type { ThemeOptions } from '@mui/material/styles'

export const letterCaseComponents: ThemeOptions['components'] = {
  MuiButton: {
    styleOverrides: {
      // The variant's own class carries the exception, so the rule outranks the
      // typography the root inherits without turning the transform off for
      // every button.
      root: { '&.MuiButton-text': { textTransform: 'none' } },
    },
  },
}
