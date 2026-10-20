/**
 * Surface tokens: the semantic layers the interface paints its chrome on.
 *
 * A role names where a piece of chrome sits -- page, container, overlay,
 * header -- and carries only the fields a project may sharpen: fill, fill
 * opacity, border color, border width, elevation and backdrop filter.
 * Every field is optional on purpose. An omitted field means "inherit the
 * slot the theme already reads", never a color of its own:
 *
 * - page    -> color.background.default
 * - container, overlay -> color.background.paper
 * - any border -> color.divider
 * - header  -> the neutral ramp (the theme takes 50 in light mode and 900
 *              in dark mode)
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
  readonly borderWidth?: number
  readonly elevation?: number
  readonly backdropFilter?: string
}

/** The four semantic surface roles: page, container, overlay and header. */
export interface SurfaceTokens {
  readonly page: SurfaceRoleTokens
  readonly container: SurfaceRoleTokens
  readonly overlay: SurfaceRoleTokens
  readonly header: SurfaceRoleTokens
}

export const surfaceTokens: SurfaceTokens = {
  page: {},
  container: { borderWidth: 1 },
  overlay: {},
  header: {},
}
