/** The spacing section of the speed token tree (SpeedTokens["spacing"]). */
export interface SpacingTokens {
  /** Base spacing unit in pixels; layout gaps are multiples of it. */
  readonly unit: number
}

export const spacingTokens: SpacingTokens = {
  unit: 8,
}
