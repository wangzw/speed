/**
 * WriteFeedbackRegion: one feedback scope's slot rendered as its result
 * area.
 *
 * A control group reports every write through one `useWriteFeedback`
 * slot and renders this region directly under the controls. The region
 * owns the two renderings and nothing else: the success message renders
 * through `InlineSuccess` (always mounted as the polite live region the
 * message fills) and the failure code through `InlineError` (an alert
 * mounted only for a failure). The slot holds at most one outcome, so the
 * two never stand beside each other.
 *
 * The region carries no copy of its own. The success message is host
 * text -- the string the host translated when it settled the attempt --
 * and a failure code resolves through `resolve`, the calling package's
 * own error-text resolver. The root container carries one spacing unit
 * of top margin, shared by the success and failure parts; the caller's
 * `sx` applies after it and may override it.
 */

import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import type { WriteOutcome } from '../write-feedback.js'
import { InlineError } from './InlineError.js'
import { InlineSuccess } from './InlineSuccess.js'

export type WriteFeedbackRegionProps = {
  /** The scope's slot; `null` renders no visible feedback. */
  readonly outcome: WriteOutcome | null
  /** Resolves a failure code to its current-language text. */
  readonly resolve: (code: string) => string
  /** Styles for the region's root container; spacing adjustments. */
  readonly sx?: SxProps<Theme>
}

/** The result area of one feedback scope. */
export function WriteFeedbackRegion({
  outcome,
  resolve,
  sx,
}: WriteFeedbackRegionProps) {
  const successMessage =
    outcome !== null && outcome.kind === 'success' ? outcome.message : null
  const failureCode =
    outcome !== null && outcome.kind === 'failure' ? outcome.code : null
  return (
    <Box
      sx={[
        { marginTop: 1 },
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      <InlineSuccess message={successMessage} />
      <InlineError code={failureCode} resolve={resolve} />
    </Box>
  )
}
