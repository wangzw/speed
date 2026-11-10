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
 * A hand-off that carries no value -- a notice that something was sent and
 * there is nothing to copy -- omits `value` (and `copyLabel`): the panel
 * then renders the message and the close control alone, with no empty value
 * line and no copy control.
 *
 * `details` carries further labelled values the same hand-off needs
 * beside the primary one -- a DNS record's name and the value to publish,
 * for example. Each renders under the primary value with its own label,
 * monospace value and copy control; the copy control's visible label and
 * accessible name are that detail's `copyLabel`.
 *
 * The whole panel is one polite live region (`role="status"`): it appears
 * at the moment the value is handed out, so a screen-reader user is told
 * the value exists without the panel stealing focus. The copy controls
 * announce their own copy inside it.
 *
 * The panel deliberately carries no feedback-slot behaviour: closing it
 * is the caller's state change, and a failed write beside it belongs to
 * the caller's feedback scope.
 */

import { forwardRef } from 'react'
import type { ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { CopyButton } from './CopyButton.js'

/** A labelled value handed out beside the panel's primary `value`. */
export interface OneTimeValuePanelDetail {
  /** The line naming what the value is. */
  readonly label: string
  /** The value itself. */
  readonly value: string
  /** The copy control's visible label and accessible name. */
  readonly copyLabel: string
}

export interface OneTimeValuePanelProps extends RootStyleProps {
  /** The line describing what was handed out and to whom. */
  readonly message: ReactNode
  /** The one-time value itself (a link, a token, a secret). Omitted when
   * nothing is handed out and there is nothing to copy. */
  readonly value?: string
  /** The copy control's visible label and accessible name. Required with
   * `value`, and unused without it. */
  readonly copyLabel?: string
  /** Further labelled values handed out beside `value`, each with its own copy control. */
  readonly details?: readonly OneTimeValuePanelDetail[]
  /** The close control's visible text. */
  readonly closeLabel: ReactNode
  /** The close control's accessible name. */
  readonly closeAriaLabel: string
  /** Fired by the close control; the caller drops the value. */
  readonly onClose: () => void
}

function OneTimeValuePanelInner(
  {
    message,
    value,
    copyLabel,
    details,
    closeLabel,
    closeAriaLabel,
    onClose,
    sx,
    className,
  }: OneTimeValuePanelProps,
  ref: Ref<HTMLDivElement>,
) {
  const { t } = useUiKitTranslation()
  return (
    <Box
      ref={ref}
      className={className}
      role="status"
      sx={layerSx(
        {
          display: 'flex',
          alignItems: 'flex-start',
          columnGap: 2,
          mb: 2,
          p: 2,
          border: 1,
          borderColor: 'divider',
          borderRadius: 1,
        },
        sx,
      )}
    >
      <Box sx={{ flex: 1 }}>
        <Typography variant="body2">{message}</Typography>
        {value !== undefined ? (
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
              label={copyLabel ?? ''}
              visibleLabel={copyLabel ?? ''}
              copiedLabel={t('oneTimeValuePanel.copied')}
            />
          </Box>
        ) : null}
        {details?.map((detail, index) => (
          <Box key={`${detail.label}-${index}`} sx={{ mt: 1 }}>
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block' }}
            >
              {detail.label}
            </Typography>
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
                {detail.value}
              </Typography>
              <CopyButton
                value={detail.value}
                label={detail.copyLabel}
                visibleLabel={detail.copyLabel}
                copiedLabel={t('oneTimeValuePanel.copied')}
              />
            </Box>
          </Box>
        ))}
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

/** The presentation-only one-time-value panel (see the file header). */
export const OneTimeValuePanel = forwardRef(OneTimeValuePanelInner)
