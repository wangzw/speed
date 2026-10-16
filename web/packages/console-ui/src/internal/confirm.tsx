/**
 * The console's confirmation binding for destructive actions.
 *
 * A destructive control does not mutate on its first click: it asks the
 * page to hold a description of what it will do, the page renders that
 * description through ui-kit's danger ConfirmDialog, and the mutation
 * fires only on the confirming click. This module is the small shared
 * spine of that pattern -- the pending-description state and the dialog
 * bound to it -- so every page holds one `usePendingConfirm()` beside its
 * mutations and renders one `PendingConfirmDialog`, instead of re-wiring
 * the controlled dialog per action.
 *
 * The dialog closes as soon as it confirms (the mutation runs and the
 * page's own error face reports a refusal); a page that wanted to keep
 * the dialog busy across the mutation would wire ui-kit's ConfirmDialog
 * directly, as PermissionsPage's role removal does. The texts come from
 * the caller, so they resolve through the console-ui namespace and name
 * the concrete object.
 *
 * The guard's WEIGHT is the caller's: a `doubleConfirm` request is the
 * heavier two-step arm (the first click only re-labels the button), set
 * for a truly irreversible action -- a delete that takes other records
 * with it -- and left off for a recoverable one, so a reversible action
 * never carries a heavier guard than an irreversible one.
 */

import { useState } from 'react'
import { ConfirmDialog } from '@speed/ui-kit'

/** One destructive action awaiting confirmation. */
export interface PendingConfirm {
  /** The dialog heading. */
  readonly title: string
  /** What exactly will happen. */
  readonly message: string
  /** The confirming button's label. */
  readonly confirmLabel: string
  /**
   * The heavier two-step guard: the first confirming click only arms the
   * button ("click again to confirm") and a deliberate second click fires.
   * Set for a truly irreversible action (a delete that takes other records
   * with it); leave it off for a recoverable one.
   */
  readonly doubleConfirm?: boolean
  /** Fires on the confirming click (the second click under doubleConfirm). */
  readonly run: () => void
}

/** The pending-confirmation state a page holds beside its mutations. */
export interface PendingConfirmState {
  /** The held action, or null when none is open. */
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
  /** The page's confirmation state. */
  readonly state: PendingConfirmState
  /** The cancel button's label (the page's `common.cancel`). */
  readonly cancelLabel: string
}

/**
 * Renders the confirmation the state holds, if any: a danger dialog that
 * runs the held action and closes on the confirming click, and closes
 * unchanged on cancel, Escape or the backdrop.
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
