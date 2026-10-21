/**
 * Surface tokens: the semantic layers the interface paints its chrome on.
 *
 * A role names where a piece of chrome sits -- page, container, overlay,
 * header, and the standing navigation column -- and carries only the fields
 * a project may sharpen: fill, fill opacity, border color (per mode), border
 * width, elevation and backdrop filter. Every field is optional on purpose.
 * An omitted field means "inherit the slot the theme already reads", never a
 * color of its own:
 *
 * - page    -> color.background.default
 * - container, overlay, nav -> color.background.paper
 * - any border -> color.divider
 * - header  -> the neutral ramp (the theme takes 50 in light mode and 900
 *              in dark mode)
 *
 * `nav` is optional as a role too: absent, the standing navigation column
 * follows `container` exactly, so a style that says nothing about it is
 * unchanged.
 *
 * This section therefore carries no surface color literals, and an
 * override that only changes color.background.paper keeps every surface
 * byte-identical. Resolving the inherited slot is the ui-kit theme
 * adapter's decision; this package states the contract, it does not
 * apply it. Border radius is not repeated here -- surfaces reuse
 * shape.borderRadius.
 */

/** The fields a surface role may set; an omitted field inherits its slot (see the module comment). */
export interface SurfaceRoleTokens {
  readonly fill?: string
  /**
   * The fill's alpha, 0-1; 1 is opaque. The theme adapter applies it to a
   * resolved `#RRGGBB` fill in either color mode, so a translucent surface
   * is one token value rather than one fill per mode.
   */
  readonly opacity?: number
  readonly borderColor?: string
  /**
   * The border color in dark mode, when one color cannot read on both
   * pages (a white rim reads on the dark canvas but disappears on a light
   * one). Absent, `borderColor` serves both modes. The resolved border
   * falls back to `color.divider` when neither is given.
   */
  readonly borderColorDark?: string
  readonly borderWidth?: number
  readonly elevation?: number
  readonly backdropFilter?: string
}

/** The four semantic surface roles, plus the standing navigation column. */
export interface SurfaceTokens {
  readonly page: SurfaceRoleTokens
  readonly container: SurfaceRoleTokens
  readonly overlay: SurfaceRoleTokens
  readonly header: SurfaceRoleTokens
  /**
   * The standing navigation column. It is its own role so a style can
   * tune the sidebar's density apart from the content cards; absent, it
   * follows `container` exactly.
   */
  readonly nav?: SurfaceRoleTokens
}

export const surfaceTokens: SurfaceTokens = {
  page: {},
  container: { borderWidth: 1 },
  overlay: {},
  header: {},
}
