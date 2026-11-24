/** The clip recipe for elements that stay in the accessibility tree while taking no painted space. */

/**
 * Usable wherever MUI accepts styles. The clip technique, never
 * `display: none`: a hidden live region must stay live and a hidden input
 * focusable.
 */
export const visuallyHiddenSx = {
  clip: 'rect(0 0 0 0)',
  clipPath: 'inset(50%)',
  // '1px', not 1: MUI's sizing transform reads a bare number at or below 1
  // as a fraction, so `height: 1` renders as 100% and the box is only
  // invisible thanks to the clip -- not the 1px box this recipe is for.
  height: '1px',
  width: '1px',
  overflow: 'hidden',
  position: 'absolute',
  bottom: 0,
  left: 0,
  whiteSpace: 'nowrap',
  margin: 0,
} as const
