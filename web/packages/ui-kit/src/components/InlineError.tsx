/**
 * The shared failure banner for whole-attempt failures, namespace-agnostic:
 * the text comes from `resolve`, `errorCodeOf` classifies the throw and
 * `httpStatusOf` reads a status structurally.
 */

import { forwardRef } from 'react'
import type { ReactNode } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { visuallyHiddenSx } from '../visually-hidden.js'

/** Collapses an arbitrary thrown value to the code InlineError renders. */
export function errorCodeOf(error: unknown): string {
  if (typeof error !== 'object' || error === null) {
    return UNKNOWN_FAILURE_CODE
  }
  let code: unknown
  try {
    code = (error as { code?: unknown }).code
  } catch {
    // A throwing getter or revoked proxy is still a failure with no readable code.
    return UNKNOWN_FAILURE_CODE
  }
  return typeof code === 'string' && code.length > 0
    ? code
    : UNKNOWN_FAILURE_CODE
}

/**
 * The collapse target of a non-ApiError failure. @speed/api-client never
 * emits it, so a resolver renders its unknown fallback.
 */
const UNKNOWN_FAILURE_CODE = 'client.unknown'

/**
 * The wait an error states before retrying, in whole seconds: the
 * api-client's `retryAfterMs` rounded up, or `undefined`. Read defensively,
 * so a throwing getter or revoked proxy reads as no wait.
 */
export function retryAfterSecondsOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  let value: unknown
  try {
    value = (error as { retryAfterMs?: unknown }).retryAfterMs
  } catch {
    return undefined
  }
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return undefined
  }
  return Math.max(1, Math.ceil(value / 1000))
}

/**
 * The HTTP status an error carries, read structurally: an integer `status`
 * in 100-599, else a `client.http.<n>` code. Returns `null` otherwise; a
 * valid numeric status wins over a code when both are present.
 */
export function httpStatusOf(error: unknown): number | null {
  if (typeof error !== 'object' || error === null) {
    return null
  }
  let status: unknown
  try {
    status = (error as { status?: unknown }).status
  } catch {
    return null
  }
  if (
    typeof status === 'number' &&
    Number.isInteger(status) &&
    status >= 100 &&
    status <= 599
  ) {
    return status
  }
  let code: unknown
  try {
    code = (error as { code?: unknown }).code
  } catch {
    return null
  }
  if (typeof code !== 'string') {
    return null
  }
  const match = /^client\.http\.(\d+)$/.exec(code)
  return match === null ? null : Number(match[1])
}

export interface InlineErrorProps extends RootStyleProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /** Resolves a code to its current-language text; the caller binds its own namespace. */
  readonly resolve: (code: string, retryAfterSeconds?: number) => string
  /**
   * The stated wait in whole seconds. This is the fixed sentence assistive
   * technology reads, so the announcement does not change while it runs down.
   */
  readonly retryAfterSeconds?: number
  /**
   * The remaining seconds the host counts down. The visible sentence follows
   * it; at zero it resolves without a wait, dropping only the number.
   */
  readonly liveRetryAfterSeconds?: number
  /** Content rendered inside the alert, below the sentence, so it is announced with it. */
  readonly detail?: ReactNode
}

/**
 * The failure banner, one `role="alert"` per error; with no error nothing
 * renders. A stated wait renders two sentences: the clipped one is stable in
 * the accessibility tree, the painted `aria-hidden` one counts down.
 */
export const InlineError = forwardRef<HTMLDivElement, InlineErrorProps>(
  function InlineError(
    {
      code,
      resolve,
      retryAfterSeconds,
      liveRetryAfterSeconds,
      detail,
      sx,
      className,
    },
    ref,
  ) {
    if (code === null) {
      return null
    }
    const liveSeconds = liveRetryAfterSeconds ?? retryAfterSeconds
    return (
      <Alert
        ref={ref}
        className={className}
        severity="error"
        role="alert"
        sx={layerSx({ width: '100%' }, sx)}
      >
        {retryAfterSeconds === undefined ? (
          resolve(code)
        ) : (
          <>
            <Box component="span" sx={visuallyHiddenSx}>
              {resolve(code, retryAfterSeconds)}
            </Box>
            <Box component="span" aria-hidden="true">
              {liveSeconds === 0 ? resolve(code) : resolve(code, liveSeconds)}
            </Box>
          </>
        )}
        {detail === undefined ? null : (
          <Box component="div" sx={{ mt: 0.5 }}>
            {detail}
          </Box>
        )}
      </Alert>
    )
  },
)
