/**
 * The focus-ring layer at the theme layer.
 *
 * Keyboard focus must stay visible on every interactive control. MUI's
 * default feedback is a pointer-centred ripple, which is an animation
 * rather than a steady outline and is suppressed under reduced motion;
 * the ring below is a static 2px outline in `palette.primary.main`,
 * offset 2px from the control's own box, so focus remains locatable with
 * no motion at all.
 *
 * The rule reads the `focusVisible` state class MUI sets only when focus
 * should be visible (`.Mui-focusVisible`) -- a pointer click that focuses
 * a button deliberately leaves the ring off. The color is read from the
 * built theme inside the callback, so a host that overrides `palette
 * .primary` moves the ring with it.
 *
 * `MuiButtonBase.root` is the catch-all: Button, IconButton, Checkbox,
 * Radio, Switch, MenuItem, ListItemButton, a clickable Chip and
 * TableSortLabel all root in ButtonBase, so one rule reaches every one of
 * them. `MuiLink` renders its own `<a>` and does not root in ButtonBase,
 * so it repeats the same rule. Both slots already carry the touch-target
 * floor; `mergeThemeComponents` composes that static style object with
 * this theme callback per slot rather than letting one replace the other.
 *
 * The undocumented `theme.focusVisible` option is deliberately not used:
 * its shape is internal to MUI, and this ring is a conscious size and
 * color choice rather than a restatement of MUI's own default.
 */

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

/** The theme's focus-ring overrides, merged by component slot. */
export const focusRingComponents: ThemeOptions['components'] = {
  MuiButtonBase: { styleOverrides: { root: focusRingStyle } },
  MuiLink: { styleOverrides: { root: focusRingStyle } },
}
