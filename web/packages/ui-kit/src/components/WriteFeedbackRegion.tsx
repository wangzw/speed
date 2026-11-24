/**
 * One feedback scope's slot rendered as its result area: success through
 * `InlineSuccess`, failure through `InlineError`, with the stated wait
 * counted down here. The region carries no copy of its own.
 */

import { useEffect, useState } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import type { WriteOutcome } from '../write-feedback.js'
import { InlineError } from './InlineError.js'
import { InlineSuccess } from './InlineSuccess.js'

/**
 * The whole seconds left before `waitUntil`, ticking once a second.
 * `undefined` with no deadline; `0` once it is crossed.
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
  /** Resolves a failure code to its current-language text; the stated wait rides along. */
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
