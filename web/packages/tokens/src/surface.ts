/** Surface tokens: the semantic layers the interface paints its chrome on. */

/** The fields a surface role may set; an omitted field inherits the theme's slot. */
export interface SurfaceRoleTokens {
  /** Surface fill color. */
  readonly fill?: string
  /** Fill alpha, 0-1; 1 is opaque. Applies to a resolved `#RRGGBB` fill in either mode. */
  readonly opacity?: number
  /** Border color. */
  readonly borderColor?: string
  /** Border color in dark mode; absent, `borderColor` serves both modes. */
  readonly borderColorDark?: string
  /** Border width in pixels. */
  readonly borderWidth?: number
  /** MUI elevation slot. */
  readonly elevation?: number
  /** CSS backdrop-filter value. */
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
