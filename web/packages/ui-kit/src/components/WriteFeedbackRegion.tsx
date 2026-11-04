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
 * own error-text resolver. A failure that stated a wait carries its
 * deadline, so the region counts the remaining seconds down itself: one
 * tick per second while the deadline is ahead, passed to `InlineError`
 * beside the wait the answer stated. The caller supplies no clock state.
 * The one spacing unit of top margin belongs to
 * a rendered banner, not to the always-mounted root: an empty region
 * keeps its live region mounted and takes no height, so a control
 * group's own rhythm is never widened by a slot with nothing to say.
 * The caller's `sx` applies after the margin and may override it.
 */

import { useEffect, useState } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import type { WriteOutcome } from '../write-feedback.js'
import { InlineError } from './InlineError.js'
import { InlineSuccess } from './InlineSuccess.js'

/**
 * The whole seconds left before `waitUntil`, ticking once a second while
 * the deadline is ahead. `undefined` while no deadline is armed; `0`
 * from the tick that crosses it, so a caller renders the wait-free
 * sentence rather than a stale zero-second one.
 */
function useLiveWaitSeconds(waitUntil: number | undefined): number | undefined {
  const [, setTick] = useState(0)
  useEffect(() => {
    if (waitUntil === undefined || Date.now() >= waitUntil) {
      return undefined
    }
    const timer = setInterval(() => {
      setTick((tick) => tick + 1)
      if (Date.now() >= waitUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [waitUntil])
  if (waitUntil === undefined) {
    return undefined
  }
  return Math.max(0, Math.ceil((waitUntil - Date.now()) / 1000))
}

export type WriteFeedbackRegionProps = {
  /** The scope's slot; `null` renders no visible feedback. */
  readonly outcome: WriteOutcome | null
  /** Resolves a failure code to its current-language text. The wait a
   * throttled answer stated rides along, for a resolver that names it. */
  readonly resolve: (code: string, retryAfterSeconds?: number) => string
  /** Styles for the region's root container; spacing adjustments. */
  readonly sx?: SxProps<Theme>
}

/** The result area of one feedback scope. */
export function WriteFeedbackRegion({
  outcome,
  resolve,
  sx,
}: WriteFeedbackRegionProps) {
  const successMessage = outcome?.kind === 'success' ? outcome.message : null
  const failureCode = outcome?.kind === 'failure' ? outcome.code : null
  const retryAfterSeconds =
    outcome?.kind === 'failure' ? outcome.retryAfterSeconds : undefined
  const waitUntil = outcome?.kind === 'failure' ? outcome.waitUntil : undefined
  const liveRetryAfterSeconds = useLiveWaitSeconds(waitUntil)
  return (
    <Box
      sx={[
        ...(outcome === null ? [] : [{ marginTop: 1 }]),
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      <InlineSuccess message={successMessage} />
      <InlineError
        code={failureCode}
        resolve={resolve}
        retryAfterSeconds={retryAfterSeconds}
        liveRetryAfterSeconds={liveRetryAfterSeconds}
      />
    </Box>
  )
}
