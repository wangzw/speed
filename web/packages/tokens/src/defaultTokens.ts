import type { SpeedTokens } from './types.js'
import { breakpointTokens } from './breakpoints.js'
import { colorTokens } from './color.js'
import { shadowTokens } from './shadows.js'
import { shapeTokens } from './shape.js'
import { spacingTokens } from './spacing.js'
import { surfaceTokens } from './surface.js'
import { typographyTokens } from './typography.js'
import { zIndexTokens } from './z-index.js'

function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null) {
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child)
    }
    Object.freeze(value)
  }
  return value
}

/**
 * The default token tree every project starts from, deep-frozen at assembly.
 * Override through deepMerge, which never mutates its inputs.
 */
export const defaultTokens: SpeedTokens = deepFreeze({
  color: colorTokens,
  typography: typographyTokens,
  spacing: spacingTokens,
  shape: shapeTokens,
  breakpoints: breakpointTokens,
  zIndex: zIndexTokens,
  shadows: shadowTokens,
  surface: surfaceTokens,
})
