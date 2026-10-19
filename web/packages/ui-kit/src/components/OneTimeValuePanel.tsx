/**
 * OneTimeValuePanel: the presentation of a value handed out once -- an
 * invitation's acceptance link, a token, a secret.
 *
 * The component holds no state: the caller owns the value's lifetime and
 * decides when the panel closes. It renders the caller's message above
 * the value, the value in the theme's monospace stack (unbroken values
 * break anywhere rather than overflowing), a copy control named by
 * `copyLabel`, and an explicit close control whose accessible name is
 * `closeAriaLabel` (the visible text `closeLabel` may be shorter).
 *
 * The whole panel is one polite live region (`role="status"`): it appears
 * at the moment the value is handed out, so a screen-reader user is told
 * the value exists without the panel stealing focus. The copy control
 * announces its own copy inside it.
 *
 * The panel deliberately carries no feedback-slot behaviour: closing it
 * is the caller's state change, and a failed write beside it belongs to
 * the caller's feedback scope.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { CopyButton } from './CopyButton.js'

export interface OneTimeValuePanelProps {
  /** The line describing what was handed out and to whom. */
  readonly message: ReactNode
  /** The one-time value itself (a link, a token, a secret). */
  readonly value: string
  /** The copy control's visible label and accessible name. */
  readonly copyLabel: string
  /** The close control's visible text. */
  readonly closeLabel: ReactNode
  /** The close control's accessible name. */
  readonly closeAriaLabel: string
  /** Fired by the close control; the caller drops the value. */
  readonly onClose: () => void
}

/** The presentation-only one-time-value panel (see the file header). */
export function OneTimeValuePanel({
  message,
  value,
  copyLabel,
  closeLabel,
  closeAriaLabel,
  onClose,
}: OneTimeValuePanelProps) {
  const { t } = useUiKitTranslation()
  return (
    <Box
      role="status"
      sx={{
        display: 'flex',
        alignItems: 'flex-start',
        columnGap: 2,
        mb: 2,
        p: 2,
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
      }}
    >
      <Box sx={{ flex: 1 }}>
        <Typography variant="body2">{message}</Typography>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'center',
            columnGap: 1,
            rowGap: 0.5,
          }}
        >
          <Typography
            variant="body2"
            sx={(theme) => ({
              wordBreak: 'break-all',
              fontFamily: theme.typography.fontFamilyMono,
            })}
          >
            {value}
          </Typography>
          <CopyButton
            value={value}
            label={copyLabel}
            visibleLabel={copyLabel}
            copiedLabel={t('oneTimeValuePanel.copied')}
          />
        </Box>
      </Box>
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={closeAriaLabel}
        onClick={onClose}
      >
        {closeLabel}
      </Button>
    </Box>
  )
}
