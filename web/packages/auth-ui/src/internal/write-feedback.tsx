/**
 * The channel feedback scope of the sign-in family.
 *
 * Every channel component -- the current form on the sign-in screen, the
 * social block, the enterprise entry and each passkey entry -- owns one
 * scope: one `useWriteFeedback` instance plus the shared
 * `WriteFeedbackRegion` that renders its slot, bound here to the auth-ui
 * namespace's error-text resolver. A channel's scope is its own, so one
 * channel's failure never clears another's banner, and leaving a channel
 * (switching tabs) unmounts its scope with it.
 *
 * A stepwise sign-in answer is not one promise, so call sites use only the
 * slot's `reset` and `fail` (never `run` or `succeed`): every answer begins
 * with `reset`, which drops the previous attempt's banner, and a refusal
 * settles the slot with its code. Success is the flow's own completion
 * (the host callback), so no success message travels through the slot.
 *
 * A failure the channel words itself is the scope's other source: the
 * passkey ceremony's own failures, and a server refusal the channel gives
 * its own text instead of the shared code text. Its text is a bundle key,
 * never an API code, so it does not enter the slot. The scope holds it
 * beside the slot and renders it through the same region. The two failure
 * sources are mutually exclusive -- raising either clears the other -- so a
 * channel component never shows two whole-attempt failure banners at once
 * (ui-kit's write-feedback rule).
 */

import { useCallback, useState } from 'react'
import Alert from '@mui/material/Alert'
import {
  useWriteFeedback,
  WriteFeedbackRegion as SharedWriteFeedbackRegion,
} from '@speed/ui-kit'
import type { WriteOutcome } from '@speed/ui-kit'
import { useAuthUiErrorText } from './error-text.js'
import { useAuthUiTranslation } from './translation.js'

/** One channel's feedback scope: the write slot and its client-side source. */
export interface ChannelFeedback {
  /** The scope's slot; `null` is empty. */
  readonly outcome: WriteOutcome | null
  /** The bundle key of the channel's own failure text, or `null`. */
  readonly localFailure: string | null
  /** A new attempt: clears every failure source of the scope. */
  readonly reset: () => void
  /** Settles the slot with a failure code; clears the client-side source. */
  readonly fail: (code: string) => void
  /** Raises the channel's own failure text (a bundle key); clears the slot. */
  readonly failLocal: (key: string) => void
  /** Clears the channel's own failure text only (a retry begins in the same scope). */
  readonly clearLocal: () => void
}

/** The channel feedback scope's state and operations. */
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
    (code: string) => {
      setLocalFailure(null)
      // The slot classifies a thrown value through errorCodeOf; the code is
      // already the family's judgement, so it rides as an object carrying it.
      failSlot({ code })
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
  /**
   * A resolver to use instead of the namespace's own, for a channel whose
   * current step words a code more specifically (the sign-in flow's
   * username step).
   */
  readonly resolve?: (code: string) => string
}

/**
 * The scope rendered under its channel's controls: the shared region's
 * slot (success live region, failure alert) and, when it holds one, the
 * client-side failure banner. At most one of the two failure sources can
 * be non-empty, so the region never shows two failure banners.
 */
export function ChannelFeedbackRegion({
  feedback,
  resolve,
}: ChannelFeedbackRegionProps) {
  const fallback = useAuthUiErrorText()
  const { t } = useAuthUiTranslation()
  return (
    <>
      {/* The channel surfaces stack in a flex flow whose own gap spaces
          their rows. A region that always renders a box would add a second
          gap and the shared margin around nothing, pushing the submit row
          away from the fields; `display: contents` keeps the hidden
          success live region out of the layout and lifts a failure banner
          into the flow as a plain sibling. */}
      <SharedWriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={resolve ?? fallback}
        sx={{ display: 'contents' }}
      />
      {feedback.localFailure === null ? null : (
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t(feedback.localFailure)}
        </Alert>
      )}
    </>
  )
}
