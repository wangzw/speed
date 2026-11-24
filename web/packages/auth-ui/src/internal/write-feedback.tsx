/** The channel feedback scope of the sign-in family. */

import { useCallback, useState } from 'react'
import Alert from '@mui/material/Alert'
import {
  useWriteFeedback,
  WriteFeedbackRegion as SharedWriteFeedbackRegion,
} from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useAuthUiErrorText } from './error-text.js'
import { useAuthUiTranslation } from './translation.js'

export interface ChannelFeedback {
  readonly outcome: WriteOutcome | null
  readonly localFailure: string | null
  readonly reset: () => void
  readonly fail: (code: string, retryAfterMs?: number) => void
  readonly failLocal: (key: string) => void
  readonly clearLocal: () => void
}

export function useChannelFeedback(): ChannelFeedback {
  const {
    outcome,
    reset: resetSlot,
    fail: failSlot,
  } = useWriteFeedback()
  const [localFailure, setLocalFailure] = useState<string | null>(null)

  const reset = useCallback(() => {
    setLocalFailure(null)
    resetSlot()
  }, [resetSlot])

  const fail = useCallback(
    (code: string, retryAfterMs?: number) => {
      setLocalFailure(null)
      failSlot(
        retryAfterMs === undefined ? { code } : { code, retryAfterMs },
      )
    },
    [failSlot],
  )

  const failLocal = useCallback(
    (key: string) => {
      resetSlot()
      setLocalFailure(key)
    },
    [resetSlot],
  )

  const clearLocal = useCallback(() => {
    setLocalFailure(null)
  }, [])

  return { outcome, localFailure, reset, fail, failLocal, clearLocal }
}

export interface ChannelFeedbackRegionProps {
  readonly feedback: ChannelFeedback
}

export function ChannelFeedbackRegion({
  feedback,
}: ChannelFeedbackRegionProps) {
  const resolve = useAuthUiErrorText()
  const { t } = useAuthUiTranslation()
  return (
    <>
      {/* display: contents keeps the hidden success region out of the flex
          flow and lifts a failure banner in as a plain sibling; a box would
          add the shared gap around nothing. */}
      <SharedWriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={resolve}
        sx={{ display: 'contents' }}
      />
      {feedback.localFailure === null ? null : (
        <Alert severity="error" role="alert" sx={{ width: '100%' }}>
          {t(feedback.localFailure)}
        </Alert>
      )}
    </>
  )
}
