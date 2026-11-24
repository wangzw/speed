/**
 * The shared success banner, the positive twin of `InlineError`. Always
 * mounted as one polite live region, clipped while `message` is null, so the
 * text fills a region the screen reader already knows.
 */

import { forwardRef } from 'react'
import Alert from '@mui/material/Alert'
import { visuallyHiddenSx } from '../visually-hidden.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'

export interface InlineSuccessProps extends RootStyleProps {
  /** The confirmation text, or null for nothing to confirm (renders silent). */
  readonly message: string | null
}

/** The whole-attempt success banner, one role="status" live region. */
export const InlineSuccess = forwardRef<HTMLDivElement, InlineSuccessProps>(
  function InlineSuccess({ message, sx, className }, ref) {
    return (
      <Alert
        ref={ref}
        className={className}
        severity="success"
        role="status"
        sx={layerSx(
          message === null ? visuallyHiddenSx : { width: '100%' },
          sx,
        )}
      >
        {message ?? ''}
      </Alert>
    )
  },
)
