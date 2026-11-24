/** Named z-index slots, matching the MUI theme zIndex keys. */
export type ZIndexKey =
  | 'mobileStepper'
  | 'fab'
  | 'speedDial'
  | 'appBar'
  | 'drawer'
  | 'modal'
  | 'snackbar'
  | 'tooltip'

/** Stack order per named z-index slot. */
export type ZIndexValues = { readonly [Key in ZIndexKey]: number }

/** The z-index section of the speed token tree (SpeedTokens["zIndex"]). */
export interface ZIndexTokens {
  readonly values: ZIndexValues
}

export const zIndexTokens: ZIndexTokens = {
  values: {
    mobileStepper: 1000,
    fab: 1050,
    speedDial: 1050,
    appBar: 1100,
    drawer: 1200,
    modal: 1300,
    snackbar: 1400,
    tooltip: 1500,
  },
}
