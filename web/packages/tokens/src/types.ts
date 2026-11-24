import type { BreakpointTokens } from './breakpoints.js'
import type { ColorTokens } from './color.js'
import type { ShadowTokens } from './shadows.js'
import type { ShapeTokens } from './shape.js'
import type { SpacingTokens } from './spacing.js'
import type { SurfaceTokens } from './surface.js'
import type { TypographyTokens } from './typography.js'
import type { ZIndexTokens } from './z-index.js'

/** The complete token tree a theme is built from. */
export interface SpeedTokens {
  readonly color: ColorTokens
  readonly typography: TypographyTokens
  readonly spacing: SpacingTokens
  readonly shape: ShapeTokens
  readonly breakpoints: BreakpointTokens
  readonly zIndex: ZIndexTokens
  readonly shadows: ShadowTokens
  readonly surface: SurfaceTokens
}

/**
 * A recursive partial of an object type. Arrays, functions and optional object
 * fields are leaves: an override supplies the whole value or omits it.
 */
export type DeepPartial<T> = {
  [K in keyof T]?: T[K] extends (...args: never[]) => unknown
    ? T[K]
    : T[K] extends readonly unknown[]
      ? T[K]
      : T[K] extends object
        ? DeepPartial<T[K]>
        : T[K]
}

/** What a project overrides of the speed defaults: any partial token tree. */
export type TokensOverride = DeepPartial<SpeedTokens>
