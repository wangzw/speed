/**
 * Public entry of @speed/tokens. The runtime surface is defaultTokens and
 * deepMerge; every other export is a type.
 */

export { defaultTokens } from './defaultTokens.js'
export { deepMerge } from './merge.js'

export type { BreakpointKey, BreakpointTokens, BreakpointValues } from './breakpoints.js'
export type {
  CanvasGradientTokens,
  CanvasTokens,
  ColorTokens,
  NeutralScale,
  NeutralTone,
  SemanticColor,
  SemanticPalette,
  SemanticRole,
} from './color.js'
export type { ElevationSlot, ShadowTokens } from './shadows.js'
export type { ShapeTokens } from './shape.js'
export type { SpacingTokens } from './spacing.js'
export type { SurfaceRoleTokens, SurfaceTokens } from './surface.js'
export type { DeepPartial, SpeedTokens, TokensOverride } from './types.js'
export type {
  FontSizeScale,
  FontSizeStep,
  FontWeightKey,
  FontWeightScale,
  TypographyTokens,
} from './typography.js'
export type { ZIndexKey, ZIndexTokens, ZIndexValues } from './z-index.js'
