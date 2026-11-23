/**
 * RefreshButton: the shared control that re-reads a page's own data.
 *
 * The control re-reads the surface's own data: outlined, small, with a
 * visible text label and the same busy and disabled rules everywhere.
 * Only its position differs -- a surface whose body is addressed by a
 * selector renders it beside that selector, every other surface keeps it
 * in the page header.
 *
 * It is unavailable while it cannot do its job: the scope the surface is
 * addressed by is still empty, or a write in the surface's scopes is in
 * flight. The two unavailable reasons are kept apart, because they explain
 * different things. An empty scope explains itself: the tooltip repeats
 * the surface's existing "choose a tenant/account first" text (the
 * caller passes that same translation), and `aria-describedby` points at
 * the one copy the surface already renders. A write in flight does not:
 * the operator already chose the scope, and a control claiming otherwise
 * would be wrong. Only `disabled` applies the description and the
 * tooltip; `writePending` renders the control unavailable without either.
 *
 * While the read itself is in flight the control stays focusable and
 * reports `aria-busy`, with the spinner in place of an icon. None of these
 * states renders the control natively disabled: a natively disabled button
 * stops holding focus, browsers differ in when and how they move it, and
 * an operator who pressed Enter would lose their place until the read
 * settles, with the read putting it nowhere. Every such state keeps the
 * control focusable, carries `aria-disabled` where appropriate, and
 * swallows activation through a guard, so a second activation issues no
 * second request and queues none.
 *
 * Because the control is never natively disabled, the tooltip attaches to
 * the button directly instead of a wrapper span. An enabled control
 * carries neither the tooltip nor the description.
 *
 * A surface whose refresh re-reads a named object passes `ariaLabel`:
 * the visible text stays the shared label, while the accessible name
 * names what is re-read. The caller's label must begin with that visible
 * text, so a speech-input user can still say what is on screen; callers
 * that re-read nothing more specific than the surface omit it.
 *
 * `label` is the visible text. Callers pass their own package's existing
 * translation so the control keeps their wording; the ui-kit namespace's
 * `refreshButton.label` is the default.
 *
 * The console renders this control; the package keeps no copy of its own.
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
  /**
   * Whether the scope the surface is addressed by is still empty. This
   * state carries the "choose a scope first" description and tooltip.
   */
  readonly disabled?: boolean
  /**
   * Whether a write in the surface's scopes is in flight. It renders the
   * control unavailable, without the empty-scope description: the scope is
   * chosen, and re-reading mid-write would race the write.
   */
  readonly writePending?: boolean
  /** Starts the read. Not called while busy or unavailable. */
  readonly onClick: () => void
  /** The id of the surface's "choose a scope first" text; applied when disabled. */
  readonly describedBy?: string
  /** That same text, shown in the disabled control's tooltip. */
  readonly disabledReason?: string
  /**
   * The accessible name, when the re-read object is more specific than
   * the surface. Must begin with the visible text.
   */
  readonly ariaLabel?: string
  /**
   * The visible button text. Callers pass their package's existing
   * translation; the default is the ui-kit namespace's
   * `refreshButton.label`.
   */
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
  // The spinner is named by the button's own text: the button carries the
  // id while it is busy, and the progressbar is labelled by it (the same
  // wiring MUI's own loading indicator uses).
  const loadingId = useId()
  const text = label ?? t('refreshButton.label')
  const unavailable = disabled || writePending
  // Activation is swallowed while the read is in flight or the control is
  // unavailable, without turning the button natively disabled.
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
  // Every state returns the tooltip wrapper: an enabled control or a
  // write-pending one passes an empty title, which MUI never opens, while
  // the button stays a child of the same wrapper in every state and React
  // reconciles it in place. Returning the bare button in some states and a
  // wrapped one in others changes the root element's type, unmounts the
  // button, and drops a focused control's focus exactly when its
  // availability changes.
  return (
    <Tooltip
      title={disabled && disabledReason !== undefined ? disabledReason : ''}
      describeChild
    >
      {button}
    </Tooltip>
  )
}

/**
 * The shared refresh control (see the file header), rendered by the
 * console's pages. `sx`, `className` and the ref apply to the button
 * itself in both of its renderings; the tooltip attaches to the button
 * directly.
 */
export const RefreshButton = forwardRef(RefreshButtonInner)
