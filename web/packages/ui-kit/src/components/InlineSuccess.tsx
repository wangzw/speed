/**
 * InlineSuccess: the shared success banner, the positive twin of InlineError.
 *
 * A landed write that changed something server-side is worth confirming --
 * a failure renders in InlineError, and a save/register/revoke that silently
 * succeeds leaves the operator unsure whether the click was even registered.
 * This banner is the symmetric affordance: the surface family resolves the
 * confirmation's text (its own namespace) and passes it here, and a page that
 * has nothing to confirm passes null.
 *
 * The banner is always mounted as one polite live region (role="status"):
 * a live region announces only content changes that follow its own
 * insertion, so a region born together with its text is unreliable -- the
 * region therefore stands in the tree for the whole settled phase, visually
 * silent and clipped (never display:none, which would not be live) while the
 * message is null, and the message fills a region the screen reader already
 * knows when a write lands. This is the same mount-empty-then-fill recipe the
 * account surfaces use for their save notices.
 *
 * `WriteFeedbackRegion` renders this banner inside its root container and
 * owns the region's spacing; a host renders the region, never the banner
 * on its own.
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
