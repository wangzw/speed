/**
 * The clipboard control: writes its value and announces the copy. Silent,
 * never failing, when the Clipboard API is unavailable or the write is
 * refused.
 */

import { forwardRef, useEffect, useState } from 'react'
import type { Ref } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import IconButton from '@mui/material/IconButton'
import { useUiKitTranslation } from '../internal/translation.js'
import { CopiedIcon, CopyIcon } from '../internal/icons.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { visuallyHiddenSx } from '../visually-hidden.js'

/** How long the copy control keeps its copied state and announcement. */
const COPY_FEEDBACK_MS = 2000

export interface CopyButtonProps extends RootStyleProps {
  /** The complete value written to the clipboard. */
  readonly value: string
  /** The accessible name; defaults to the ui-kit `copyButton.copyLabel` string. */
  readonly label?: string
  /** The post-copy announcement; defaults to the ui-kit `copyButton.copiedLabel` string. */
  readonly copiedLabel?: string
  /** Renders a small outlined text button instead of the icon control. */
  readonly visibleLabel?: string
  /** Refuses to shrink inside a cell capped by `contentMaxWidth`. */
  readonly pinned?: boolean
}

function CopyButtonInner(
  {
    value,
    label,
    copiedLabel,
    visibleLabel,
    pinned = false,
    sx,
    className,
  }: CopyButtonProps,
  ref: Ref<HTMLButtonElement>,
) {
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
  // The pinned rule stays first, so a host rule wins an overlap.
  const controlSx =
    sx === undefined ? shrinkSx : layerSx(shrinkSx ?? {}, sx)

  return (
    <>
      {visibleLabel === undefined ? (
        <IconButton
          ref={ref}
          className={className}
          type="button"
          size="small"
          aria-label={accessibleName}
          onClick={copy}
          sx={controlSx}
        >
          {copied ? <CopiedIcon /> : <CopyIcon />}
        </IconButton>
      ) : (
        <Button
          ref={ref}
          className={className}
          type="button"
          size="small"
          variant="outlined"
          aria-label={accessibleName}
          onClick={copy}
          sx={controlSx}
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

/**
 * The clipboard control. `sx`, `className` and the ref apply to the control
 * itself, not to the hidden live region beside it.
 */
export const CopyButton = forwardRef(CopyButtonInner)
