/**
 * Pointer-capability media features.
 *
 * The layout design draws this responsive line at what the pointing
 * device can do, not at how wide the viewport is: a hover-dependent
 * affordance belongs to pointer devices, and a touch device's
 * interactive targets sit at or above the 44x44 floor a finger needs.
 * The two media features named there are `hover` and `pointer`; this
 * module is the single place the components spell them, so they gate on
 * the same strings.
 *
 * `hover: hover` together with `pointer: fine` means a device with a
 * hovering, precise pointer (a mouse or a trackpad); `pointer: coarse`
 * means a finger. A wide viewport can still be coarse -- a touchscreen
 * laptop or a tablet with a keyboard -- which is exactly why the gate is
 * a media feature and not a breakpoint.
 */

/**
 * A finger-driven device: the gate for the touch-target floor and for
 * the affordances that a touch device must show without a hover.
 */
export const POINTER_COARSE_QUERY = '@media (pointer: coarse)'

/**
 * A device with a hovering, precise pointer (mouse/trackpad): the gate
 * for enabling the affordances that depend on hover.
 */
export const POINTER_FINE_HOVER_QUERY = '@media (hover: hover) and (pointer: fine)'

/**
 * The minimum interactive-target edge on a touch device, in px (the
 * 44x44 finger-target floor the layout design states).
 */
export const TOUCH_TARGET_MIN_PX = 44
