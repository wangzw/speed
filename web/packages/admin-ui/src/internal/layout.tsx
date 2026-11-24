/** The console page-layout primitives: the detail page's main/side split. */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'

const SIDE_COLUMN_WIDTH = '16rem'

export interface DetailLayoutProps {
  readonly main: ReactNode
  readonly side: ReactNode
}

/** A detail page's main + fixed-width side skeleton, stacked below `md`. */
export function DetailLayout({ main, side }: DetailLayoutProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: { xs: 'column', md: 'row' },
        gap: 3,
        alignItems: 'flex-start',
      }}
    >
      <Box sx={{ flex: 1, minWidth: 0 }}>{main}</Box>
      <Box
        component="aside"
        sx={{
          width: { xs: '100%', md: SIDE_COLUMN_WIDTH },
          flexShrink: 0,
        }}
      >
        {side}
      </Box>
    </Box>
  )
}

export interface DefinitionRowProps {
  readonly label: string
  readonly value: ReactNode
}

/** One label/value row of a detail page's definition list. */
export function DefinitionRow({ label, value }: DefinitionRowProps) {
  return (
    <Box
      sx={{
        display: 'flex',
        columnGap: 2,
        rowGap: 0.5,
        flexWrap: 'wrap',
        paddingY: 1,
        borderBottom: 1,
        borderColor: 'divider',
      }}
    >
      <Typography
        component="dt"
        variant="body2"
        color="text.secondary"
        sx={{ minWidth: '8ch' }}
      >
        {label}
      </Typography>
      <Typography component="dd" variant="body2" sx={{ margin: 0, wordBreak: 'break-all' }}>
        {value}
      </Typography>
    </Box>
  )
}
