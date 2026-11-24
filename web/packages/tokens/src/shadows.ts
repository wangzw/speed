/** The MUI theme.shadows indices a named elevation slot maps to. */
export type ElevationSlot = 1 | 2 | 4 | 8 | 16 | 24

/** A complete CSS box-shadow value per elevation slot. */
export type ShadowTokens = { readonly [Elevation in ElevationSlot]: string }

export const shadowTokens: ShadowTokens = {
  1: '0 1px 2px 0 rgba(15, 23, 42, 0.05)',
  2: '0 1px 3px 0 rgba(15, 23, 42, 0.10), 0 1px 2px -1px rgba(15, 23, 42, 0.10)',
  4: '0 4px 6px -1px rgba(15, 23, 42, 0.10), 0 2px 4px -2px rgba(15, 23, 42, 0.10)',
  8: '0 10px 15px -3px rgba(15, 23, 42, 0.10), 0 4px 6px -4px rgba(15, 23, 42, 0.10)',
  16: '0 20px 25px -5px rgba(15, 23, 42, 0.10), 0 8px 10px -6px rgba(15, 23, 42, 0.10)',
  24: '0 25px 50px -12px rgba(15, 23, 42, 0.25)',
}
