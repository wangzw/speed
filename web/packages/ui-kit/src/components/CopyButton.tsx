/**
 * CopyButton: the clipboard control for a value that must be handed over
 * exactly.
 *
 * The control writes `value` to the clipboard and announces the copy
 * through a polite live region, so a value clipped on screen (an
 * identifier past the scroll edge, a one-time token) stays reachable by
 * keyboard and touch. It is silent rather than failing when the Clipboard
 * API is unavailable (an insecure context has no `navigator.clipboard`),
 * and a refused write leaves the control silent too: copying is a
 * convenience, never a page-blocking action.
 *
 * Two faces share the behaviour. Without `visibleLabel` it is the
 * icon-sized control the DataTable renders beside a `cellFullValue`
 * (copy icon, then the copied icon for a short feedback window). With
 * `visibleLabel` it is a small outlined text button, for a hand-off
 * panel whose reader should see the action named.
 *
 * `label` is the accessible name and `copiedLabel` is the announcement
 * text; both default to the ui-kit namespace's `copyButton.*` strings
 * interpolated with the value. A caller that already owns wording for its
 * own surface passes its own.
 *
 * `pinned` refuses to shrink the control. The DataTable sets it inside a
 * cell capped by `contentMaxWidth`, where the cap must leave the control
 * its full width; without the cap the prior flex behaviour stays
 * byte-identical.
 */

import { useEffect, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import { useUiKitTranslation } from '../internal/translation.js'
import { CopiedIcon, CopyIcon } from '../internal/icons.js'
import { visuallyHiddenSx } from '../visually-hidden.js'

/** How long the copy control keeps its copied state and announcement. */
const COPY_FEEDBACK_MS = 2000

export interface CopyButtonProps {
  /** The complete value written to the clipboard. */
  readonly value: string
  /**
   * The control's accessible name; defaults to the ui-kit
   * `copyButton.copyLabel` string interpolated with the value.
   */
  readonly label?: string
  /**
   * The post-copy announcement; defaults to the ui-kit
   * `copyButton.copiedLabel` string interpolated with the value.
   */
  readonly copiedLabel?: string
  /**
   * Renders a small outlined text button carrying this visible label
   * instead of the icon control. The accessible name still comes from
   * `label` (or the default).
   */
  readonly visibleLabel?: string
  /**
   * Whether the control refuses to shrink (a cell capped by
   * `contentMaxWidth`). Without it the control keeps the prior flex
   * behaviour.
   */
  readonly pinned?: boolean
}

/** The clipboard control (see the file header). */
export function CopyButton({
  value,
  label,
  copiedLabel,
  visibleLabel,
  pinned = false,
}: CopyButtonProps) {
  const { t } = useUiKitTranslation()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return undefined
    const timer = window.setTimeout(() => setCopied(false), COPY_FEEDBACK_MS)
    return () => window.clearTimeout(timer)
  }, [copied])

  const copy = (): void => {
    const clipboard: Clipboard | undefined = navigator.clipboard
    if (clipboard === undefined) return
    void clipboard.writeText(value).then(
      () => setCopied(true),
      () => undefined,
    )
  }

  const accessibleName = label ?? t('copyButton.copyLabel', { value })
  const announcement = copiedLabel ?? t('copyButton.copiedLabel', { value })
  const shrinkSx = pinned ? { flexShrink: 0 } : undefined

  return (
    <>
      {visibleLabel === undefined ? (
        <IconButton
          type="button"
          size="small"
          aria-label={accessibleName}
          onClick={copy}
          sx={shrinkSx}
        >
          {copied ? <CopiedIcon /> : <CopyIcon />}
        </IconButton>
      ) : (
        <Button
          type="button"
          size="small"
          variant="outlined"
          aria-label={accessibleName}
          onClick={copy}
          sx={shrinkSx}
        >
          {visibleLabel}
        </Button>
      )}
      <Box component="span" role="status" sx={visuallyHiddenSx}>
        {copied ? announcement : ''}
      </Box>
    </>
  )
}
