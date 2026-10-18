/**
 * SelectorRow: the row that pairs a page's addressing selector with the
 * control that re-reads what the selector addresses.
 *
 * A page whose body is addressed by a selector (a tenant, or the sessions
 * page's account) puts its refresh immediately to the selector's right,
 * so the control sits next to the thing it re-reads rather than in the
 * page header. The row does not wrap: on a narrow viewport the selector
 * narrows and the trailing control keeps its size. The selector's width
 * cap is the width it had on its own; on a wide page the row therefore
 * looks like the selector followed by the button, not a control pinned to
 * the far edge.
 *
 * The trailing control is aligned with the selector's input rather than
 * with its whole field: a selector with helper text is taller than its
 * input, and centring in the whole field would drop the button below the
 * input's centre line.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'

/** The height of MUI's small outlined input, which the row centres against. */
const SMALL_INPUT_HEIGHT = 40

export interface SelectorRowProps {
  /** The selector, rendered at the row's start. */
  readonly children: ReactNode
  /** The control to the selector's right (the refresh button). */
  readonly trailing?: ReactNode
  /** The selector's width cap in the row; defaults to 240px. */
  readonly selectorMaxWidth?: number
}

/** The selector-and-refresh row (see the file header). */
export function SelectorRow({
  children,
  trailing,
  selectorMaxWidth = 240,
}: SelectorRowProps) {
  return (
    <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
      <Box
        sx={{
          flex: '1 1 auto',
          minWidth: 0,
          maxWidth: selectorMaxWidth,
          '& > *': { width: '100%' },
        }}
      >
        {children}
      </Box>
      {trailing !== undefined && (
        <Box
          sx={{
            flexShrink: 0,
            display: 'flex',
            alignItems: 'center',
            minHeight: SMALL_INPUT_HEIGHT,
          }}
        >
          {trailing}
        </Box>
      )}
    </Box>
  )
}
