/**
 * The shared control that re-reads a page's own data. It is never natively
 * disabled: busy and unavailable states stay focusable and swallow
 * activation. Only `disabled` carries the empty-scope explanation.
 */

import { forwardRef, useId } from 'react'
import type { Ref } from 'react'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Tooltip from '@mui/material/Tooltip'
import { useUiKitTranslation } from '../internal/translation.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { useActivationGuard } from '../activation-guard.js'

export interface RefreshButtonProps extends RootStyleProps {
  /** Whether the read it re-reads is in flight. */
  readonly busy: boolean
  /** The surface's scope is still empty; carries the "choose a scope first" explanation. */
  readonly disabled?: boolean
  /** A write is in flight: unavailable, without an explanation, since re-reading would race it. */
  readonly writePending?: boolean
  /** Starts the read. Not called while busy or unavailable. */
  readonly onClick: () => void
  /** The id of the surface's "choose a scope first" text; applied when disabled. */
  readonly describedBy?: string
  /** That same text, shown in the disabled control's tooltip. */
  readonly disabledReason?: string
  /** The accessible name when the re-read object is more specific; must begin with the visible text. */
  readonly ariaLabel?: string
  /** The visible text; defaults to the ui-kit `refreshButton.label`. */
  readonly label?: string
}

function RefreshButtonInner(
  {
    busy,
    disabled = false,
    writePending = false,
    onClick,
    describedBy,
    disabledReason,
    ariaLabel,
    label,
    sx,
    className,
  }: RefreshButtonProps,
  ref: Ref<HTMLButtonElement>,
) {
  const { t } = useUiKitTranslation()
  // The spinner is labelled by the button's own text, so the button carries an id while busy.
  const loadingId = useId()
  const text = label ?? t('refreshButton.label')
  const unavailable = disabled || writePending
  // Activation is swallowed without turning the button natively disabled.
  const handleClick = useActivationGuard(onClick, { busy, unavailable })
  const button = (
    <Button
      ref={ref}
      className={className}
      sx={sx}
      type="button"
      variant="outlined"
      size="small"
      id={busy ? loadingId : undefined}
      aria-label={ariaLabel}
      aria-disabled={unavailable || undefined}
      aria-busy={busy || undefined}
      aria-describedby={disabled ? describedBy : undefined}
      startIcon={
        busy ? (
          <CircularProgress
            aria-labelledby={loadingId}
            color="inherit"
            size={16}
          />
        ) : undefined
      }
      onClick={handleClick}
    >
      {text}
    </Button>
  )
  // Every state returns the wrapper, so the button reconciles in place and
  // never loses focus when its availability changes. An empty title never opens.
  return (
    <Tooltip
      title={disabled && disabledReason !== undefined ? disabledReason : ''}
      describeChild
    >
      {button}
    </Tooltip>
  )
}

/** `sx`, `className` and the ref apply to the button itself; the tooltip attaches to it directly. */
export const RefreshButton = forwardRef(RefreshButtonInner)
