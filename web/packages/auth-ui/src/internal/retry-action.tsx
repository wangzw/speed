/** The retry action every auth-ui error surface offers. */

import type { ReactNode } from 'react'
import { ActionButton, ActionRow } from '@speed/ui-kit'

export type RetryActionProps = {
  readonly label: ReactNode
  readonly onClick: () => void
}

export function RetryAction({ label, onClick }: RetryActionProps) {
  return (
    <ActionRow>
      <ActionButton type="button" emphasis="primary" onClick={onClick}>
        {label}
      </ActionButton>
    </ActionRow>
  )
}
