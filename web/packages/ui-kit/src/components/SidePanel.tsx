/**
 * The controlled modal side panel anchored to the right edge. The host owns
 * `open` and may refuse a close; while `busy` all three close paths are
 * inert. It stacks at `theme.zIndex.modal` so the AppBar does not cover it.
 */

import { forwardRef, useId, useRef } from 'react'
import type { ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import Drawer from '@mui/material/Drawer'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import { useTheme } from '@mui/material/styles'
import { useForkRef } from '@mui/material/utils'
import { CloseIcon } from '../internal/icons.js'
import { useUiKitTranslation } from '../internal/translation.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { useActivationGuard } from '../activation-guard.js'
import { useInertBackground } from '../inert-background.js'
import { ActionRow } from './ActionRow.js'

/** The panel's width tier: 36rem (medium) or 52rem (wide) from `sm` up. */
export type SidePanelSize = 'medium' | 'wide'

export type SidePanelProps = RootStyleProps & {
  /** Whether the panel is shown; controlled by the host. */
  readonly open: boolean
  /** The panel heading, rendered as an `h2` and used as the dialog's name. */
  readonly title: ReactNode
  /** Optional paragraph under the title; it becomes the dialog's description. */
  readonly description?: ReactNode
  /** Width tier; `'medium'` (36rem) unless `'wide'` (52rem) is passed. */
  readonly size?: SidePanelSize
  /** The panel's task is in flight; all three close paths are inert. */
  readonly busy?: boolean
  /** Accessible name of the close control; defaults to the namespace label. */
  readonly closeLabel?: string
  /** The fixed footer's actions, outside the scroll layer. */
  readonly actions?: ReactNode
  /** The panel body. */
  readonly children?: ReactNode
  /** Fired on every close request; the host decides whether to close. */
  readonly onClose: () => void
  /** Fired once when the exit transition ends; the content unmounts in the next commit. */
  readonly onExited: () => void
}

const SIZE_WIDTH: Record<SidePanelSize, string> = {
  medium: '36rem',
  wide: '52rem',
}

function SidePanelInner(
  {
    open,
    title,
    description,
    size = 'medium',
    busy = false,
    closeLabel,
    actions,
    children,
    onClose,
    onExited,
    sx,
    className,
  }: SidePanelProps,
  ref: Ref<HTMLDivElement>,
) {
  const { t } = useUiKitTranslation()
  const theme = useTheme()
  const titleId = useId()
  const descriptionId = useId()
  const hasDescription = description !== undefined && description !== null
  const rootRef = useRef<HTMLDivElement>(null)
  const drawerRef = useForkRef(ref, rootRef)
  useInertBackground(open, rootRef)

  const requestClose = () => {
    if (busy) {
      return
    }
    onClose()
  }
  // Never natively disabled: the focus trap would land the focus on the paper.
  const handleCloseClick = useActivationGuard(requestClose, { busy })

  return (
    <Drawer
      ref={drawerRef}
      className={className}
      open={open}
      onClose={requestClose}
      anchor="right"
      variant="temporary"
      sx={layerSx({ zIndex: theme.zIndex.modal }, sx)}
      slotProps={{
        paper: {
          'aria-labelledby': titleId,
          ...(hasDescription ? { 'aria-describedby': descriptionId } : {}),
          sx: {
            // The content box is the only scroll layer; the paper's own overflow would scroll the header.
            overflowY: 'hidden',
            width: { xs: '100%', sm: SIZE_WIDTH[size] },
          },
        },
        transition: { onExited },
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          height: '100%',
          minHeight: 0,
        }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 2,
            px: 3,
            pt: 2.5,
            pb: 2,
            borderBottom: 1,
            borderColor: 'divider',
          }}
        >
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography id={titleId} component="h2" variant="h6">
              {title}
            </Typography>
            {hasDescription ? (
              <Typography
                id={descriptionId}
                variant="body2"
                color="text.secondary"
                sx={{ mt: 0.5 }}
              >
                {description}
              </Typography>
            ) : null}
          </Box>
          <IconButton
            type="button"
            aria-label={closeLabel ?? t('sidePanel.closeLabel')}
            onClick={handleCloseClick}
            aria-disabled={busy || undefined}
          >
            <CloseIcon />
          </IconButton>
        </Box>
        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', px: 3, py: 2 }}>
          {children}
        </Box>
        {actions !== undefined && actions !== null && (
          <ActionRow
            sx={{
              flexShrink: 0,
              px: 3,
              py: 2,
              borderTop: 1,
              borderColor: 'divider',
            }}
          >
            {actions}
          </ActionRow>
        )}
      </Box>
    </Drawer>
  )
}

/**
 * `sx`, `className` and the ref reach the drawer's root; the host's styles
 * merge after the panel's stacking rule.
 */
export const SidePanel = forwardRef(SidePanelInner)
