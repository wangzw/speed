/**
 * WriteFeedbackRegion: the account surface's binding of ui-kit's feedback
 * region to the account-ui error-text resolver.
 */

import type { SxProps, Theme } from '@mui/material/styles'
import { WriteFeedbackRegion as SharedWriteFeedbackRegion } from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useAccountUiErrorText } from './error-text.js'

export interface WriteFeedbackRegionProps {
  readonly outcome: WriteOutcome | null
  readonly sx?: SxProps<Theme>
}

export function WriteFeedbackRegion({ outcome, sx }: WriteFeedbackRegionProps) {
  const resolve = useAccountUiErrorText()
  return (
    <SharedWriteFeedbackRegion outcome={outcome} resolve={resolve} sx={sx} />
  )
}
