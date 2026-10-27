/**
 * InlineError: the shared failure banner for whole-attempt failures.
 *
 * A failed attempt resolves to one error code and renders it through
 * this banner instead of laying out per-field error text (field errors
 * belong to the fields; whole-attempt failures belong to one alert).
 * The banner itself is namespace-agnostic: the text comes from the
 * `resolve` prop, so each surface family binds its own namespace's
 * resolver and the English fallback an API returns for log triage can
 * never reach a user.
 *
 * errorCodeOf is the companion classifier: an ApiError-shaped failure
 * keeps its code, and anything else -- a bug-shaped throw, an
 * un-normalized answer, a value whose `code` read throws (a throwing
 * getter, a revoked proxy) -- collapses to a code that no resolver
 * lists, so the family renders its unknown fallback. The pair is
 * fail-closed: an operation that throws at all always has a code to
 * show, and classifying it never throws.
 *
 * httpStatusOf is the status-reading companion: callers that decide a
 * branch from an HTTP status (a refusal, a missing record) read it
 * structurally instead of re-implementing the two shapes
 * (`error.status`, or the `client.http.<n>` code) at every call site.
 */

import { forwardRef } from 'react'
import type { ReactNode } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'

/** Collapses an arbitrary thrown value to the code InlineError renders. */
export function errorCodeOf(error: unknown): string {
  if (typeof error !== 'object' || error === null) {
    return UNKNOWN_FAILURE_CODE
  }
  let code: unknown
  try {
    code = (error as { code?: unknown }).code
  } catch {
    // A throwing getter or a revoked proxy is still a failure with no
    // readable code: classify it as unknown rather than let the read throw.
    return UNKNOWN_FAILURE_CODE
  }
  return typeof code === 'string' && code.length > 0
    ? code
    : UNKNOWN_FAILURE_CODE
}

/**
 * The collapse target of a failure that is not an ApiError-shaped
 * answer. Not a code @speed/api-client ever emits (its failures are
 * client.network / client.timeout / client.protocol / client.http.*),
 * so a resolver treats it as unknown and renders its fallback.
 */
const UNKNOWN_FAILURE_CODE = 'client.unknown'

/**
 * The HTTP status an error carries, read structurally: a numeric
 * `status` property holding a valid HTTP status (an integer in 100-599),
 * or else a `code` of the bare-HTTP shape `client.http.<n>`. Returns
 * `null` when neither names a status -- a transport failure (whose
 * `status` is 0), a bug-shaped throw, or a value whose reads throw (a
 * throwing getter, a revoked proxy).
 *
 * The helper is structural like its companion `errorCodeOf`: it imports
 * nothing from the transport and recognizes no envelope of its own. A
 * valid numeric `status` wins over a code when both are present, because
 * the answer's own field is the direct statement; the code is the
 * synthesized fallback a transport writes when the response carried no
 * readable envelope. An out-of-range number (0 on a transport failure)
 * names no status, so the code is still tried.
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
  /**
   * Resolves a code to its current-language text. The surface family
   * binds its own namespace's resolver; the banner never guesses a
   * namespace or falls back across languages.
   */
  readonly resolve: (code: string) => string
  /**
   * Content rendered inside the alert, below the resolved sentence. A
   * failure that names something -- the address a refusal collided on --
   * goes here rather than beside the banner, so the name is part of the
   * one region announced when the alert appears.
   */
  readonly detail?: ReactNode
}

/**
 * The whole-attempt failure banner, one role="alert" per error. With no
 * error nothing renders, so a host ref stays unattached.
 */
export const InlineError = forwardRef<HTMLDivElement, InlineErrorProps>(
  function InlineError({ code, resolve, detail, sx, className }, ref) {
    if (code === null) {
      return null
    }
    return (
      <Alert
        ref={ref}
        className={className}
        severity="error"
        role="alert"
        sx={layerSx({ width: '100%' }, sx)}
      >
        {resolve(code)}
        {detail === undefined ? null : (
          <Box component="div" sx={{ mt: 0.5 }}>
            {detail}
          </Box>
        )}
      </Alert>
    )
  },
)
