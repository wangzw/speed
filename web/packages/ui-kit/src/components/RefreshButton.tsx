/**
 * RefreshButton: the shared control that re-reads a page's own data.
 *
 * The control re-reads the surface's own data: outlined, small, with a
 * visible text label and the same busy and disabled rules everywhere.
 * Only its position differs -- a surface whose body is addressed by a
 * selector renders it beside that selector, every other surface keeps it
 * in the page header.
 *
 * It is disabled while it cannot do its job: the scope the surface is
 * addressed by is still empty, or a write in the surface's scopes is in
 * flight. The two disabling reasons are kept apart, because they explain
 * different things. An empty scope explains itself: the tooltip repeats
 * the surface's existing "choose a tenant/account first" text (the
 * caller passes that same translation), and `aria-describedby` points at
 * the one copy the surface already renders. A write in flight does not:
 * the operator already chose the scope, and a control claiming otherwise
 * would be wrong. Only `disabled` applies the description and the
 * tooltip; `writePending` renders the control natively disabled without
 * either.
 *
 * While the read itself is in flight the control stays enabled and
 * focusable and reports `aria-busy`, with the spinner in place of an
 * icon: MUI's `loading` would make the button native disabled, and a
 * disabled button drops keyboard focus to the document body -- an
 * operator who pressed Enter would lose their place until the read
 * settles, and the read would not put it back. The click handler is
 * guarded, so a second activation while the read is in flight issues no
 * second request and queues none.
 *
 * MUI's Tooltip listens on the child element, and a native disabled
 * button receives no pointer events, so the empty-scope face is wrapped
 * in an inline span. An enabled control carries neither the tooltip nor
 * the description.
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

import { useId } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Tooltip from '@mui/material/Tooltip'
import { useUiKitTranslation } from '../internal/translation.js'

export interface RefreshButtonProps {
  /** Whether the read it re-reads is in flight. */
  readonly busy: boolean
  /**
   * Whether the scope the surface is addressed by is still empty. This
   * state carries the "choose a scope first" description and tooltip.
   */
  readonly disabled?: boolean
  /**
   * Whether a write in the surface's scopes is in flight. It renders the
   * control natively disabled, without the empty-scope description: the
   * scope is chosen, and re-reading mid-write would race the write.
   */
  readonly writePending?: boolean
  /** Starts the read. Not called while busy. */
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

/**
 * The shared refresh control (see the file header), rendered by the
 * console's pages.
 */
export function RefreshButton({
  busy,
  disabled = false,
  writePending = false,
  onClick,
  describedBy,
  disabledReason,
  ariaLabel,
  label,
}: RefreshButtonProps) {
  const { t } = useUiKitTranslation()
  // The spinner is named by the button's own text: the button carries the
  // id while it is busy, and the progressbar is labelled by it (the same
  // wiring MUI's own loading indicator uses).
  const loadingId = useId()
  const text = label ?? t('refreshButton.label')
  const button = (
    <Button
      type="button"
      variant="outlined"
      size="small"
      id={busy ? loadingId : undefined}
      aria-label={ariaLabel}
      disabled={disabled || writePending}
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
      onClick={() => {
        if (!busy) {
          onClick()
        }
      }}
    >
      {text}
    </Button>
  )
  if (!disabled || disabledReason === undefined) {
    return button
  }
  return (
    <Tooltip title={disabledReason}>
      <Box component="span" sx={{ display: 'inline-flex' }}>
        {button}
      </Box>
    </Tooltip>
  )
}
