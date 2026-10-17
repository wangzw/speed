/**
 * SidePanel: the controlled modal side panel that hosts a secondary task
 * inside a page -- a list row's detail, a create form wider than a
 * dialog.
 *
 * Everything is props-driven: `open` shows it, `onClose` reports every
 * close request (the close button, Escape, a backdrop click) and
 * `onExited` reports the end of the exit transition. The component never
 * flips `open` itself, so the host may refuse a close (unsaved content,
 * a confirmation first) simply by calling onClose and doing nothing.
 * While `busy` is true all three close paths are inert: the close button
 * is disabled and Escape and backdrop clicks do not call onClose.
 *
 * MUI's temporary Drawer supplies the dialog semantics on its paper:
 * `role="dialog"`, `aria-modal` and `tabIndex={-1}`. This component adds
 * only the accessible name (`aria-labelledby` at the title, an `h2`
 * because the page header owns the `h1`) and, when a description is
 * given, `aria-describedby`. Focus behaviour is MUI's: opening focuses
 * the paper, Tab loops inside the panel, and closing restores focus to
 * the element that opened it; a host whose opener has left the document
 * places focus itself in `onExited`.
 *
 * The panel stacks at `theme.zIndex.modal`: a temporary Drawer's root
 * defaults to `zIndex.drawer`, which sits under the AppShell AppBar
 * (`zIndex.drawer + 1`) and would put the panel's header behind the top
 * bar. Dialogs use the same `zIndex.modal` and, mounting later, sit
 * above an open panel.
 *
 * Layout: the paper fills the viewport height, its header stays fixed
 * and the content area is the panel's only scrolling layer. The width is
 * the size tier's fixed width from the viewport's `sm` up and the full
 * viewport width below it -- the panel is a viewport-anchored overlay,
 * not a box in the content flow.
 */

import { useId } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Drawer from '@mui/material/Drawer'
import IconButton from '@mui/material/IconButton'
import Typography from '@mui/material/Typography'
import { useTheme } from '@mui/material/styles'
import { CloseIcon } from '../internal/icons.js'
import { useUiKitTranslation } from '../internal/translation.js'

/** The panel's width tier: 36rem (medium) or 52rem (wide) from `sm` up. */
export type SidePanelSize = 'medium' | 'wide'

export type SidePanelProps = {
  /** Whether the panel is shown; controlled by the host. */
  readonly open: boolean
  /** The panel heading, rendered as an `h2` and used as the dialog's name. */
  readonly title: ReactNode
  /**
   * Optional supporting paragraph under the title. Given, it becomes the
   * dialog's accessible description.
   */
  readonly description?: ReactNode
  /** Width tier; `'medium'` (36rem) unless `'wide'` (52rem) is passed. */
  readonly size?: SidePanelSize
  /**
   * Busy state of the task inside the panel. While true the close button
   * is disabled and Escape and backdrop clicks do not call onClose.
   */
  readonly busy?: boolean
  /** Accessible name of the close control; defaults to the namespace label. */
  readonly closeLabel?: string
  /** The panel body. */
  readonly children?: ReactNode
  /**
   * Fired on every close request -- the close button, Escape or a
   * backdrop click -- never on open. The host decides whether to close.
   */
  readonly onClose: () => void
  /** Fired once after the exit transition, when the content has unmounted. */
  readonly onExited: () => void
}

const SIZE_WIDTH: Record<SidePanelSize, string> = {
  medium: '36rem',
  wide: '52rem',
}

/**
 * The controlled modal side panel: an `h2`-named dialog anchored to the
 * right edge, with a fixed header, a single scrolling content area and
 * close paths the host can refuse.
 */
export function SidePanel({
  open,
  title,
  description,
  size = 'medium',
  busy = false,
  closeLabel,
  children,
  onClose,
  onExited,
}: SidePanelProps) {
  const { t } = useUiKitTranslation()
  const theme = useTheme()
  const titleId = useId()
  const descriptionId = useId()
  const hasDescription = description !== undefined && description !== null

  const requestClose = () => {
    if (busy) {
      return
    }
    onClose()
  }

  return (
    <Drawer
      open={open}
      onClose={requestClose}
      anchor="right"
      variant="temporary"
      sx={{ zIndex: theme.zIndex.modal }}
      slotProps={{
        paper: {
          'aria-labelledby': titleId,
          ...(hasDescription ? { 'aria-describedby': descriptionId } : {}),
          sx: {
            // The content box below is the panel's only scroll layer; the
            // paper's own overflow would scroll the fixed header too.
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
            onClick={requestClose}
            disabled={busy}
          >
            <CloseIcon />
          </IconButton>
        </Box>
        <Box sx={{ flex: 1, minHeight: 0, overflowY: 'auto', px: 3, py: 2 }}>
          {children}
        </Box>
      </Box>
    </Drawer>
  )
}
