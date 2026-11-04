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
 * An outlined button's resting border has the same problem. In the
 * installed MUI (9.5.0) the `outlined` slot declares
 * `border-color: var(--variant-outlinedBorder, currentColor)`, and the
 * per-color `variants` entries (matched through the root's `colorPrimary`
 * class) fill that variable with `alpha(palette[color].main, 0.5)`. At
 * 50% alpha the boundary measures roughly 2.0-2.7:1 against the card
 * surface on every preset except glass. The component's
 * `overridesResolver` resolves `root`, the variant slot itself
 * (`outlined`) and the size/flag slots -- it does not compose an
 * `outlinedPrimary` slot in this version, so an override keyed that way
 * never reaches the emitted CSS.
 *
 * The override therefore writes `borderColor: 'currentColor'` on the
 * `outlined` slot. CurrentColor is the button's own text colour, which
 * the theme already picks for contrast on the surface the button sits
 * on, so the boundary keeps each role's accent -- primary, secondary,
 * error, inherit -- instead of forcing primary onto every role, and it
 * is a keyword rather than a hand-written palette value. The declaration
 * lands in the same emitted rule as MUI's own `border-color` and comes
 * after it, so it wins over the translucent variant variable and the
 * boundary stays at full opacity.
 *
 * The input rule reads the built theme inside the callback, so a host
 * that overrides `text.secondary` moves the input border with it.
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
  MuiButton: {
    styleOverrides: {
      outlined: () => ({
        borderColor: 'currentColor',
      }),
    },
  },
}
