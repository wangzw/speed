/** The console's confirmation binding for destructive actions. */

import { useState } from 'react'
import { ConfirmDialog } from '@speed/ui-kit'

/** One destructive action awaiting confirmation. */
export interface PendingConfirm {
  readonly title: string
  readonly message: string
  readonly confirmLabel: string
  /** The heavier two-step guard: the first confirming click only arms the button. */
  readonly doubleConfirm?: boolean
  /** Fires on the confirming click (the second click under doubleConfirm). */
  readonly run: () => void
}

/** The pending-confirmation state a page holds beside its mutations. */
export interface PendingConfirmState {
  readonly pending: PendingConfirm | null
  /** Holds a destructive action and opens the dialog. */
  ask: (request: PendingConfirm) => void
  /** Drops the held action and closes the dialog. */
  cancel: () => void
}

/** Builds the pending-confirmation state for one page. */
export function usePendingConfirm(): PendingConfirmState {
  const [pending, setPending] = useState<PendingConfirm | null>(null)
  return {
    pending,
    ask: (request) => setPending(request),
    cancel: () => setPending(null),
  }
}

export interface PendingConfirmDialogProps {
  readonly state: PendingConfirmState
  /** The cancel button's label (the page's `common.cancel`). */
  readonly cancelLabel: string
}

/**
 * Renders the held confirmation, if any: it runs the action and closes on
 * the confirming click, and closes unchanged on cancel.
 */
export function PendingConfirmDialog({
  state,
  cancelLabel,
}: PendingConfirmDialogProps) {
  const { pending } = state
  return (
    <ConfirmDialog
      open={pending !== null}
      variant="danger"
      doubleConfirm={pending?.doubleConfirm ?? false}
      title={pending?.title}
      message={pending?.message}
      confirmLabel={pending?.confirmLabel}
      cancelLabel={cancelLabel}
      onConfirm={() => {
        pending?.run()
        state.cancel()
      }}
      onCancel={state.cancel}
    />
  )
}
