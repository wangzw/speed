/**
 * The retry action every auth-ui error surface offers.
 *
 * Three failures are retryable from the screen itself -- the discovery
 * read, accepting an invitation and the social callback's exchange. Each
 * used to render its own outlined button, which let the three drift into
 * separate faces; they now share this one action, rendered through the
 * platform's single action-row mapping (`ActionButton emphasis="primary"`)
 * so the retry is the same control wherever it appears.
 */

import type { ReactNode } from 'react'
import { ActionButton, ActionRow } from '@speed/ui-kit'

export type RetryActionProps = {
  /** The retry label, already translated by the calling surface. */
  readonly label: ReactNode
  /** Runs the retried operation. */
  readonly onClick: () => void
}

/** The one retry action: a primary button in the surface's action row. */
export function RetryAction({ label, onClick }: RetryActionProps) {
  return (
    <ActionRow>
      <ActionButton type="button" emphasis="primary" onClick={onClick}>
        {label}
      </ActionButton>
    </ActionRow>
  )
}
