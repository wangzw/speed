/**
 * SelectorRow: pairs a page's addressing selector with the control that
 * re-reads it, immediately to the selector's right. The trailing control
 * aligns with the selector's input, not its whole field.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'

const SMALL_INPUT_HEIGHT = 40

export interface SelectorRowProps {
  readonly children: ReactNode
  /** The control to the selector's right (the refresh button). */
  readonly trailing?: ReactNode
  /** The selector's width cap in the row; defaults to 240px. */
  readonly selectorMaxWidth?: number
}

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
