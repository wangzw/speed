/** The write-feedback scope: one slot for one control group's write result. */

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  errorCodeOf,
  retryAfterSecondsOf,
} from './components/InlineError.js'
import { useWriteFeedbackPageCoordination } from './write-feedback-page.js'

/**
 * One slot's worth of feedback: a success message or a failure code. A
 * failure keeps the stated wait in whole seconds and its deadline (epoch
 * milliseconds), so the region can count the remaining seconds down.
 */
export type WriteOutcome =
  | { readonly kind: 'success'; readonly message: string }
  | {
      readonly kind: 'failure'
      readonly code: string
      readonly retryAfterSeconds?: number
      readonly waitUntil?: number
    }

function failureOutcome(error: unknown): WriteOutcome {
  const retryAfterSeconds = retryAfterSecondsOf(error)
  if (retryAfterSeconds === undefined) {
    return { kind: 'failure', code: errorCodeOf(error) }
  }
  return {
    kind: 'failure',
    code: errorCodeOf(error),
    retryAfterSeconds,
    waitUntil: Date.now() + retryAfterSeconds * 1000,
  }
}

/** The write-feedback scope: the slot, its in-flight count and its setters. */
export interface WriteFeedback {
  /** The scope's slot; `null` is empty. */
  readonly outcome: WriteOutcome | null
  /** True until every attempt in flight in this scope has settled. */
  readonly pending: boolean
  /**
   * Runs one attempt, settling the slot with its string, its `null` (the
   * step-up gate took the write over, so the slot is not touched) or its
   * throw. Never rejects.
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
  const page = useWriteFeedbackPageCoordination()
  // The owner differs for every hook instance, so two scopes in one component
  // (or in StrictMode's doubled render) never clear each other.
  const ownerRef = useRef<symbol | null>(null)
  if (ownerRef.current === null) {
    ownerRef.current = Symbol('write-feedback-scope')
  }
  const owner = ownerRef.current

  const reset = useCallback(() => {
    setOutcome(null)
  }, [])

  // A settlement after the scope left the page must not clear the new page's result.
  const mountedRef = useRef(false)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    if (page === null) {
      return undefined
    }
    return page.register(owner, reset)
  }, [page, owner, reset])

  const clearSiblings = useCallback(() => {
    if (mountedRef.current) {
      page?.clearOthers(owner)
    }
  }, [page, owner])

  const succeed = useCallback(
    (message: string) => {
      clearSiblings()
      setOutcome({ kind: 'success', message })
    },
    [clearSiblings],
  )

  const fail = useCallback(
    (error: unknown) => {
      clearSiblings()
      setOutcome(failureOutcome(error))
    },
    [clearSiblings],
  )

  const run = useCallback(
    async (attempt: () => Promise<string | null>): Promise<void> => {
      clearSiblings()
      setOutcome(null)
      setInFlight((count) => count + 1)
      try {
        const message = await attempt()
        if (message !== null) {
          clearSiblings()
          setOutcome({ kind: 'success', message })
        }
      } catch (error) {
        clearSiblings()
        setOutcome(failureOutcome(error))
      } finally {
        setInFlight((count) => count - 1)
      }
    },
    [clearSiblings],
  )

  return { outcome, pending: inFlight > 0, run, reset, succeed, fail }
}
