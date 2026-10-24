/**
 * WriteFeedbackRegion: the account surface's binding of ui-kit's feedback
 * region.
 *
 * A section reports one control group's writes through one
 * `useWriteFeedback` slot and renders this region directly under the
 * controls. The region itself -- the success notice always mounted as the
 * polite live region, and the failure alert mounted only for a failure --
 * is ui-kit's shared `WriteFeedbackRegion`, whose slot holds at most one
 * outcome so a success and a failure never stand beside each other. This
 * module is the surface's binding of it to the account-ui namespace's
 * error-text resolver, so call sites pass the slot and nothing else: a
 * failure code resolves only through the account whitelist, and a code
 * outside it renders the shared `errors.unknown` fallback instead of a
 * raw key.
 */

import type { SxProps, Theme } from '@mui/material/styles'
import { WriteFeedbackRegion as SharedWriteFeedbackRegion } from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useAccountUiErrorText } from './error-text.js'

export interface WriteFeedbackRegionProps {
  /** The scope's slot; `null` renders no visible feedback. */
  readonly outcome: WriteOutcome | null
  /** Styles for the region's root container; spacing adjustments. */
  readonly sx?: SxProps<Theme>
}

/** The account surface's feedback region: one slot rendered with its resolver. */
export function WriteFeedbackRegion({ outcome, sx }: WriteFeedbackRegionProps) {
  const resolve = useAccountUiErrorText()
  return (
    <SharedWriteFeedbackRegion outcome={outcome} resolve={resolve} sx={sx} />
  )
}
