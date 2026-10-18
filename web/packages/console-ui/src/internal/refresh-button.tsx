/**
 * RefreshButton: the one refresh control the console pages carry.
 *
 * The control re-reads the page's own data: outlined, small, with the
 * visible text "Refresh" and the same busy and disabled rules everywhere.
 * Only its position differs -- a page whose body is addressed by a
 * selector renders it beside that selector, every other page keeps it in
 * the page header.
 *
 * It is disabled only while it cannot do its job (a scope the page is
 * addressed by is still empty). While the read is in flight it stays
 * enabled and focusable and reports `aria-busy`, with the spinner in
 * place of an icon: MUI's `loading` would make the button native
 * disabled, and a disabled button drops keyboard focus to the document
 * body -- an operator who pressed Enter would lose their place until the
 * read settles, and the read would not put it back.
 *
 * A disabled refresh explains itself: the tooltip repeats the page's
 * existing "choose a tenant/account first" text (the caller passes that
 * same translation), and `aria-describedby` points at the one copy the
 * page already renders. MUI's Tooltip listens on the child element, and
 * a native disabled button receives no pointer events, so the disabled
 * face is wrapped in an inline span. An enabled control carries neither
 * the tooltip nor the description.
 */

import { useId } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Tooltip from '@mui/material/Tooltip'
import { useConsoleUiTranslation } from './translation.js'

export interface RefreshButtonProps {
  /** Whether the read it re-reads is in flight. */
  readonly busy: boolean
  /**
   * Whether the control cannot act: the scope the page is addressed by is
   * still empty. This is the only state that disables it.
   */
  readonly disabled?: boolean
  /** Starts the read. Not called while busy. */
  readonly onClick: () => void
  /** The id of the page's "choose a scope first" text; applied when disabled. */
  readonly describedBy?: string
  /** That same text, shown in the disabled control's tooltip. */
  readonly disabledReason?: string
}

/** The console's single refresh control (see the file header). */
export function RefreshButton({
  busy,
  disabled = false,
  onClick,
  describedBy,
  disabledReason,
}: RefreshButtonProps) {
  const { t } = useConsoleUiTranslation()
  // The spinner is named by the button's own text: the button carries the
  // id while it is busy, and the progressbar is labelled by it (the same
  // wiring MUI's own loading indicator uses).
  const loadingId = useId()
  const button = (
    <Button
      type="button"
      variant="outlined"
      size="small"
      id={busy ? loadingId : undefined}
      disabled={disabled}
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
      {t('common.refresh')}
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
