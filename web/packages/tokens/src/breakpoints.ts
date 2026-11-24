/** Named breakpoint keys. */
export type BreakpointKey = 'xs' | 'sm' | 'md' | 'lg' | 'xl'

/** Width in pixels per named breakpoint. */
export type BreakpointValues = { readonly [Key in BreakpointKey]: number }

/** The breakpoints section of the speed token tree (SpeedTokens["breakpoints"]). */
export interface BreakpointTokens {
  readonly values: BreakpointValues
}

export const breakpointTokens: BreakpointTokens = {
  values: {
    xs: 0,
    sm: 600,
    md: 900,
    lg: 1200,
    xl: 1536,
  },
}
