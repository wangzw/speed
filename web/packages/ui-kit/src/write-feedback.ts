/**
 * The write-feedback scope: one slot for one control group's write result.
 *
 * A feedback scope is a `useWriteFeedback` instance plus the
 * `WriteFeedbackRegion` that renders its slot. The slot holds at most one
 * outcome -- a success message or a failure code -- so a success and a
 * failure can never stand beside each other, and the outcome of the
 * attempt that settles last is the one the slot shows.
 *
 * `run` is the standard attempt: it clears the slot, counts the attempt as
 * in flight, awaits it, and settles the slot from what it returned or
 * threw. An attempt that resolves a string confirms with that string; an
 * attempt that resolves `null` does not touch the slot and is how a write
 * refused for insufficient step-up hands over to the elevation gate (the
 * replay after a successful challenge is a new `run`); the slot is empty
 * from that attempt's own reset, and a result later filled by an
 * overlapping attempt or by `succeed` / `fail` stays. An attempt that
 * throws lands as a failure, classified through `errorCodeOf`. A returned
 * string -- including the empty string -- is stored verbatim and renders a
 * banner with no text, so callers must pass non-empty copy. `run` never
 * throws.
 *
 * `succeed`, `fail` and `reset` serve flows that are not one promise (a
 * sign-in step settling through a sequence of answers). The success
 * message is the string the caller translated when it called `succeed`;
 * the slot stores that string, so a later language switch does not rewrite
 * it. Failure codes are stored as codes and resolved to text at render
 * time, so they follow the active language.
 *
 * The state lives in the component that calls the hook -- the feedback
 * scope belongs to that host component, like the rest of its props and
 * state. The callbacks keep their identity for the component's lifetime.
 */

import { useCallback, useState } from 'react'
import {
  errorCodeOf,
  retryAfterSecondsOf,
} from './components/InlineError.js'

/** One slot's worth of feedback: a success message or a failure code. A
 * failure carries the wait a throttled answer stated, when it stated one,
 * so the sentence the slot renders can name how long to wait. */
export type WriteOutcome =
  | { readonly kind: 'success'; readonly message: string }
  | {
      readonly kind: 'failure'
      readonly code: string
      readonly retryAfterSeconds?: number
    }

/** The write-feedback scope: the slot, its in-flight count and its setters. */
export interface WriteFeedback {
  /** The scope's slot; `null` is empty. */
  readonly outcome: WriteOutcome | null
  /** True until every attempt in flight in this scope has settled. */
  readonly pending: boolean
  /**
   * Runs one attempt: clears the slot, counts the attempt in flight,
   * awaits `attempt`, and settles the slot with its string, its `null`
   * (the slot is not touched -- the step-up gate took the write over)
   * or its throw. Never rejects.
   */
  readonly run: (attempt: () => Promise<string | null>) => Promise<void>
  /** Clears the slot without cancelling attempts in flight. */
  readonly reset: () => void
  /** Replaces the slot with a success message (already translated). */
  readonly succeed: (message: string) => void
  /** Replaces the slot with the failure's code, classified by `errorCodeOf`. */
  readonly fail: (error: unknown) => void
}

/** One feedback scope's slot and the operations that settle it. */
export function useWriteFeedback(): WriteFeedback {
  const [outcome, setOutcome] = useState<WriteOutcome | null>(null)
  const [inFlight, setInFlight] = useState(0)

  const reset = useCallback(() => {
    setOutcome(null)
  }, [])

  const succeed = useCallback((message: string) => {
    setOutcome({ kind: 'success', message })
  }, [])

  const fail = useCallback((error: unknown) => {
    setOutcome({
      kind: 'failure',
      code: errorCodeOf(error),
      retryAfterSeconds: retryAfterSecondsOf(error),
    })
  }, [])

  const run = useCallback(
    async (attempt: () => Promise<string | null>): Promise<void> => {
      setOutcome(null)
      setInFlight((count) => count + 1)
      try {
        const message = await attempt()
        if (message !== null) {
          setOutcome({ kind: 'success', message })
        }
      } catch (error) {
        setOutcome({
          kind: 'failure',
          code: errorCodeOf(error),
          retryAfterSeconds: retryAfterSecondsOf(error),
        })
      } finally {
        setInFlight((count) => count - 1)
      }
    },
    [],
  )

  return { outcome, pending: inFlight > 0, run, reset, succeed, fail }
}
