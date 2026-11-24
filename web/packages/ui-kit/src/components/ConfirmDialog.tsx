/**
 * The controlled confirmation modal, with a danger variant and a two-step
 * `doubleConfirm`. The armed flag and its lockout are reset both by an
 * effect on `open` and synchronously in the exit handlers.
 */

import { forwardRef, useCallback, useEffect, useId, useRef, useState } from 'react'
import type { ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import Dialog from '@mui/material/Dialog'
import { useForkRef } from '@mui/material/utils'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import { useUiKitTranslation } from '../internal/translation.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { useInertBackground } from '../inert-background.js'
import { visuallyHiddenSx } from '../visually-hidden.js'
import { ActionButton, ActionRow } from './ActionRow.js'

/** The confirm button's role: `'danger'` paints it in the error role. */
export type ConfirmDialogVariant = 'default' | 'danger'

export interface ConfirmDialogProps extends RootStyleProps {
  /** Whether the dialog is shown; controlled by the host. */
  readonly open: boolean
  /** Dialog heading. Defaults to the namespace's generic confirm title. */
  readonly title?: ReactNode
  /** What exactly will happen. Defaults to the generic caution message. */
  readonly message?: ReactNode
  /** 'default' or 'danger' (danger paints the confirm button in the error role). */
  readonly variant?: ConfirmDialogVariant
  /**
   * Danger two-step guard: the first confirm click re-labels the button and
   * only the second fires `onConfirm`; the arming click holds the button
   * inert for `CONFIRM_ARM_LOCKOUT_MS`. Meaningless on the default variant.
   */
  readonly doubleConfirm?: boolean
  /** Overrides the built-in confirm label (the "again" label in the second step). */
  readonly confirmLabel?: ReactNode
  /** Overrides the built-in cancel label. */
  readonly cancelLabel?: ReactNode
  /** Busy state of the confirmation; both exits are inert while true. */
  readonly confirmLoading?: boolean
  /**
   * Makes the confirm button unavailable while the host's precondition is
   * unmet; it stays focusable and swallows activation.
   */
  readonly confirmDisabled?: boolean
  /** The translated reason shown for the confirm button while `confirmDisabled` holds. */
  readonly confirmDisabledReason?: string
  /** Extra content below the message; it is not part of `aria-describedby`. */
  readonly children?: ReactNode
  /** Fired when the user confirms (the second click under doubleConfirm). */
  readonly onConfirm: () => void
  /** Fired on cancel, Escape or backdrop click -- never on confirm. */
  readonly onCancel: () => void
}

/**
 * How long the confirm button stays inert after the arming click. It
 * outlasts the platform double-click threshold (~500ms), so a true double
 * click's second event cannot skip the step.
 */
export const CONFIRM_ARM_LOCKOUT_MS = 600

function ConfirmDialogInner(
  {
    open,
    title,
    message,
    variant = 'default',
    doubleConfirm = false,
    confirmLabel,
    cancelLabel,
    confirmLoading = false,
    confirmDisabled = false,
    confirmDisabledReason,
    children,
    onConfirm,
    onCancel,
    sx,
    className,
  }: ConfirmDialogProps,
  ref: Ref<HTMLDivElement>,
) {
  const { t } = useUiKitTranslation()
  const titleId = useId()
  const messageId = useId()
  const [armed, setArmed] = useState(false)
  // Its own state, not folded into `armed`: the lockout auto-clears, `armed` waits for a click.
  const [armLockout, setArmLockout] = useState(false)
  // Held so every reset path can cancel a pending re-enable.
  const armLockoutTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const dialogRef = useForkRef(ref, rootRef)
  useInertBackground(open, rootRef)

  // Every reset of `armed` calls this, so the lockout cannot outlive its arm.
  const endArmLockout = useCallback(() => {
    if (armLockoutTimer.current !== null) {
      clearTimeout(armLockoutTimer.current)
      armLockoutTimer.current = null
    }
    setArmLockout(false)
  }, [])

  // Backstop for the ordinary close; the exit handlers cover the batched reopen.
  useEffect(() => {
    if (!open) {
      setArmed(false)
      endArmLockout()
    }
  }, [open, endArmLockout])

  useEffect(() => {
    return () => {
      if (armLockoutTimer.current !== null) {
        clearTimeout(armLockoutTimer.current)
        armLockoutTimer.current = null
      }
    }
  }, [])

  const busy = confirmLoading
  const handleRequestClose = () => {
    if (busy) {
      return
    }
    // Clear before calling out, so a dialog the host reopens in the same batch starts unarmed.
    setArmed(false)
    endArmLockout()
    onCancel()
  }
  const handleConfirmClick = () => {
    if (busy || armLockout) {
      return
    }
    if (variant === 'danger' && doubleConfirm && !armed) {
      setArmed(true)
      // The lockout guard above means no arming can happen while one is pending.
      setArmLockout(true)
      armLockoutTimer.current = setTimeout(() => {
        armLockoutTimer.current = null
        setArmLockout(false)
      }, CONFIRM_ARM_LOCKOUT_MS)
      return
    }
    // Reset synchronously, in the same tick as the call that is about to fire.
    setArmed(false)
    endArmLockout()
    onConfirm()
  }

  return (
    <Dialog
      ref={dialogRef}
      className={className}
      sx={sx}
      open={open}
      onClose={handleRequestClose}
      aria-labelledby={titleId}
      aria-describedby={messageId}
      maxWidth="xs"
      fullWidth
    >
      <DialogTitle id={titleId}>{title ?? t('confirmDialog.title')}</DialogTitle>
      <DialogContent>
        <DialogContentText id={messageId}>
          {message ?? t('confirmDialog.message')}
        </DialogContentText>
        {children}
        {/* Mounted empty so arming is announced; a region born with its text would be silent. */}
        {variant === 'danger' && doubleConfirm ? (
          <Box component="p" role="status" sx={visuallyHiddenSx}>
            {armed ? t('confirmDialog.confirmAgainLabel') : ''}
          </Box>
        ) : null}
      </DialogContent>
      <ActionRow sx={{ p: 1, flex: '0 0 auto' }}>
        <ActionButton onClick={handleRequestClose} unavailable={busy}>
          {cancelLabel ?? t('confirmDialog.cancelLabel')}
        </ActionButton>
        <ActionButton
          onClick={handleConfirmClick}
          // The lockout covers the confirm button alone; Cancel stays live.
          busy={busy}
          unavailable={armLockout || confirmDisabled}
          unavailableReason={
            confirmDisabled ? confirmDisabledReason : undefined
          }
          emphasis="primary"
          tone={variant === 'danger' ? 'danger' : 'default'}
        >
          {variant === 'danger' && doubleConfirm && armed
            ? t('confirmDialog.confirmAgainLabel')
            : (confirmLabel ?? t('confirmDialog.confirmLabel'))}
        </ActionButton>
      </ActionRow>
    </Dialog>
  )
}

/**
 * The confirmation modal. `sx`, `className` and the ref reach the dialog's
 * root element.
 */
export const ConfirmDialog = forwardRef(ConfirmDialogInner)
